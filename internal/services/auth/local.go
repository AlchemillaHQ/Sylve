// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

// Re-export opts types so handlers can use auth.CreateUserOpts without importing the interface package.
type CreateUserOpts = serviceInterfaces.CreateUserOpts
type EditUserOpts = serviceInterfaces.EditUserOpts

func (s *Service) findUsernameConflict(username string, excludeUserID uint) (*models.User, error) {
	query := s.DB.Where("username = ?", username)
	if excludeUserID > 0 {
		query = query.Where("id != ?", excludeUserID)
	}

	var existing models.User
	if err := query.First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed_to_check_username: %w", err)
	}

	return &existing, nil
}

func (s *Service) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	if err := s.DB.Where("username = ?", username).Preload("Groups").First(&user).Error; err != nil {
		return nil, fmt.Errorf("user_not_found: %s", username)
	}
	return &user, nil
}

func (s *Service) ListUsers() ([]models.User, error) {
	var users []models.User
	if err := s.DB.Preload("Groups").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_users: %w", err)
	}
	return users, nil
}

func (s *Service) ListUsersBySource(source string) ([]models.User, error) {
	if source != "" && source != "local" && source != "pam" {
		return nil, fmt.Errorf("invalid_user_source")
	}

	var users []models.User
	query := s.DB.Preload("Groups")
	if source != "" {
		query = query.Where("source = ?", source)
	}
	if err := query.Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_users_by_source: %w", err)
	}
	return users, nil
}

func (s *Service) GetUserByID(id uint) (*models.User, error) {
	var user models.User
	if err := s.DB.Preload("Groups").First(&user, id).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_user_by_id: %w", err)
	}
	return &user, nil
}

func (s *Service) CreateUser(user *models.User, opts CreateUserOpts) error {
	if user.Email != "" && !utils.IsValidEmail(user.Email) {
		return fmt.Errorf("invalid_email_format: %s", user.Email)
	}

	if user.Username == "" || len(user.Username) < 3 || len(user.Username) > 128 {
		return fmt.Errorf("invalid_username_length: %s", user.Username)
	}

	if !utils.IsValidUsername(user.Username) {
		return fmt.Errorf("invalid_username_format: %s", user.Username)
	}

	if user.Password == "" || len(user.Password) < 8 || len(user.Password) > 128 {
		return fmt.Errorf("invalid_password_length")
	}

	hashed, err := s.passwordHasher.Hash(user.Password)
	if err != nil {
		return fmt.Errorf("failed_to_hash_password: %w", err)
	}
	user.Password = hashed

	user.Source = "local"
	user.UID = 0
	user.Shell = ""
	user.HomeDirectory = ""
	user.HomeDirPerms = 0
	user.SSHPublicKey = ""
	user.DisablePassword = false
	user.Locked = false
	user.DoasEnabled = false
	user.PrimaryGroupID = nil

	existing, err := s.findUsernameConflict(user.Username, 0)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.Source == "pam" {
			return fmt.Errorf("a_pam_user_with_this_username_already_exists: %s", user.Username)
		}
		return fmt.Errorf("username_already_exists: %s", user.Username)
	}

	if err := s.DB.Create(user).Error; err != nil {
		return fmt.Errorf("failed_to_create_user: %w", err)
	}

	return nil
}

func (s *Service) GetNextUID() (int, error) {
	uid, err := system.GetNextUnixUID()
	if err != nil {
		return 0, userDependencyError("unix_user_discovery_failed", err)
	}
	return uid, nil
}

func (s *Service) CreatePamUser(user *models.User, opts CreateUserOpts) error {
	return s.createPamUser(user, opts)
}

func (s *Service) ImportUser(username, password string, admin bool) (*models.User, error) {
	return s.importPamUser(username, password, admin)
}

func (s *Service) ListImportableUnixUsers() ([]ImportableUnixUser, error) {
	return s.listImportablePamUsers()
}

func isProtectedSystemUser(username string) bool {
	switch username {
	case "root", "nobody":
		return true
	default:
		return false
	}
}

func (s *Service) revokeUserTokens(db *gorm.DB, userID uint) error {
	return db.Where("user_id = ?", userID).Delete(&models.Token{}).Error
}

func (s *Service) DeleteUser(userID uint) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return userNotFoundError("user_not_found")
		}
		return userInternalError("user_lookup_failed", err)
	}

	switch user.Username {
	case "admin":
		return userConflictError("cannot_delete_admin_user")
	case "root":
		return userConflictError("cannot_delete_root_user")
	}

	if user.Source == "pam" {
		if err := s.deletePamUser(user); err != nil {
			return err
		}
	} else {
		if err := s.deleteUserDatabaseState(user.ID); err != nil {
			return userInternalError("user_delete_failed", err)
		}
	}
	s.loginMu.Lock()
	delete(s.loginAttempts, user.Username)
	s.loginMu.Unlock()
	return nil
}

func (s *Service) EditUser(userID uint, opts EditUserOpts) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return userNotFoundError("user_not_found")
		}
		return userInternalError("user_lookup_failed", err)
	}

	if user.Username == "admin" {
		if opts.Username != user.Username {
			return userConflictError("cannot_change_admin_username")
		}
		if !opts.Admin {
			return userConflictError("cannot_demote_admin_user")
		}
		if opts.Locked {
			return userConflictError("cannot_lock_admin_user")
		}
	}

	if user.Source == "pam" {
		return s.editPamUser(user, opts)
	}

	if opts.Username != user.Username {
		if len(opts.Username) < 3 || len(opts.Username) > 128 {
			return userValidationError("invalid_username_length")
		}
		if !utils.IsValidUsername(opts.Username) {
			return userValidationError("invalid_username_format")
		}
	}
	if opts.Email != "" && !utils.IsValidEmail(opts.Email) {
		return userValidationError("invalid_email_format")
	}
	if opts.Password != "" && (len(opts.Password) < 8 || len(opts.Password) > 128) {
		return userValidationError("invalid_password_length")
	}

	if opts.Username != user.Username {
		existing, err := s.findUsernameConflict(opts.Username, userID)
		if err != nil {
			return userInternalError("username_check_failed", err)
		}
		if existing != nil {
			if existing.Source != user.Source {
				return userConflictError("user_source_conflict")
			}
			return userConflictError("username_already_exists")
		}
	}

	hashedPassword := ""
	if opts.Password != "" {
		hashedPassword, err = s.passwordHasher.Hash(opts.Password)
		if err != nil {
			return userInternalError("password_hash_failed", err)
		}
	}

	updates := map[string]any{
		"full_name": opts.FullName,
		"username":  opts.Username,
		"email":     opts.Email,
		"admin":     opts.Admin,
	}
	if hashedPassword != "" {
		updates["password"] = hashedPassword
	}
	revokeSessions := opts.Username != user.Username || opts.Admin != user.Admin || opts.Password != ""
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
		if revokeSessions {
			return s.revokeUserTokens(tx, user.ID)
		}
		return nil
	}); err != nil {
		return userInternalError("user_update_failed", err)
	}

	if opts.Password != "" {
		s.loginMu.Lock()
		delete(s.loginAttempts, user.Username)
		if opts.Username != user.Username {
			delete(s.loginAttempts, opts.Username)
		}
		s.loginMu.Unlock()
	}
	return nil
}

func (s *Service) UpdateLastUsageTime(userID uint) error {
	now := time.Now()

	// Try to update only if last_login_time < now - 30s.
	result := s.DB.
		Model(&models.User{}).
		Where("id = ? AND last_login_time < ?", userID, now.Add(-30*time.Second)).
		UpdateColumn("last_login_time", now)

	if result.Error != nil {
		return fmt.Errorf("failed_to_update_last_usage_time: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var count int64
		if err := s.DB.
			Model(&models.User{}).
			Where("id = ?", userID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_verify_user_existence: %w", err)
		}

		if count == 0 {
			return nil
		}
	}

	return nil
}
