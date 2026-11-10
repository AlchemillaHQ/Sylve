// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/logger"
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
	if source != "" && source != "local" && source != "pam" && source != clusterModels.UserSourceOIDC {
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

	if user.Username == "" || len(user.Username) > 128 {
		return fmt.Errorf("invalid_username_length: %s", user.Username)
	}

	if utils.IsReservedUsername(user.Username) {
		return userValidationError("reserved_username")
	}
	if !utils.IsValidUsername(user.Username) {
		return fmt.Errorf("invalid_username_format: %s", user.Username)
	}

	if user.Password == "" || len(user.Password) < 8 || len(user.Password) > 72 {
		return fmt.Errorf("invalid_password_length")
	}

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
	shared, err := s.nameShared(user.Username)
	if err != nil {
		return userInternalError("username_check_failed", err)
	}
	if shared {
		return fmt.Errorf("username_already_exists: %s", user.Username)
	}

	hashed, err := s.passwordHasher.Hash(user.Password)
	if err != nil {
		return fmt.Errorf("failed_to_hash_password: %w", err)
	}
	if err := s.propose(context.Background(), "sylve_user", "create", clusterModels.SylveUser{
		Username: user.Username, Password: hashed, FullName: user.FullName, Email: user.Email,
		ConfirmGuestActions: user.ConfirmGuestActions,
	}); err != nil {
		return userDependencyError("cluster_write_failed", err)
	}
	// Another node may have shared the name first.
	if err := s.DB.Where("username = ? AND shared AND password = ?", user.Username, hashed).First(user).Error; err != nil {
		return fmt.Errorf("username_already_exists: %s", user.Username)
	}
	return nil
}

func (s *Service) SharedUsernames() ([]string, error) {
	var names []string
	if err := s.DB.Model(&clusterModels.SylveUser{}).Pluck("username", &names).Error; err != nil {
		return nil, userInternalError("shared_users_lookup_failed", err)
	}
	return names, nil
}

func (s *Service) nameShared(username string) (bool, error) {
	var count int64
	if err := s.DB.Model(&clusterModels.SylveUser{}).Where("username = ?", username).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// Local accounts from before the upgrade or a join are offered to the cluster; a name it already
// has stays this node's own. In a cluster the commands wait until every member knows them.
func (s *Service) ShareLocalUsers(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.shareLocalUsers(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("local_user_sharing_deferred")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// An account without a password hash couldn't be recognised as this node's own, so it stays here.
func (s *Service) shareLocalUsers(ctx context.Context) error {
	var own []models.User
	if err := s.DB.Where("source = ? AND NOT shared AND password != ''", "local").Find(&own).Error; err != nil {
		return fmt.Errorf("failed_to_list_local_users: %w", err)
	}
	for _, user := range own {
		if isSuperuser(user.Username, RealmSylve) {
			continue
		}
		shared, err := s.nameShared(user.Username)
		if err != nil {
			return fmt.Errorf("failed_to_check_shared_users: %w", err)
		}
		if shared {
			continue
		}
		if err := s.propose(ctx, "sylve_user", "create", clusterModels.SylveUser{
			Username: user.Username, Password: user.Password, FullName: user.FullName,
			Email: user.Email, Notes: user.Notes, Locked: user.Locked,
			DisablePassword: user.DisablePassword, ConfirmGuestActions: user.ConfirmGuestActions,
		}); err != nil {
			return fmt.Errorf("failed_to_share_%s: %w", user.Username, err)
		}
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

func (s *Service) ImportUser(username, password string, admin, confirmGuestActions bool) (*models.User, error) {
	return s.importPamUser(username, password, admin, confirmGuestActions)
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

func (s *Service) DeleteUser(userID uint, removeHome bool) error {
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

	if user.Source == clusterModels.UserSourceOIDC {
		principal := PrincipalOf(user)
		if err := s.propose(context.Background(), "oidc_user", "delete", clusterModels.OIDCUser{Realm: principal.Realm, Username: principal.Username}); err != nil {
			return userDependencyError("cluster_write_failed", err)
		}
		return nil
	}

	if user.Source == "pam" && !user.ExternallyManaged {
		if err := s.deleteManagedPamUser(user, removeHome); err != nil {
			return err
		}
	} else {
		if user.Source == "pam" && removeHome {
			return userConflictError("external_user_read_only")
		}
		if user.Shared {
			if err := s.propose(context.Background(), "sylve_user", "delete", clusterModels.SylveUser{Username: user.Username}); err != nil {
				return userDependencyError("cluster_write_failed", err)
			}
		} else {
			if err := s.revokeOwnGrants(user); err != nil {
				return err
			}
			if err := s.DB.Transaction(func(tx *gorm.DB) error {
				if err := clusterModels.DeleteUserRowTx(tx, user.ID); err != nil {
					return err
				}
				if user.Source == "local" {
					return clusterModels.RestoreSylveUserTx(tx, user.Username)
				}
				return nil
			}); err != nil {
				return userInternalError("user_delete_failed", err)
			}
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
	if user.Source == clusterModels.UserSourceOIDC {
		return s.editOIDCUser(user, opts)
	}
	if opts.Admin == nil {
		opts.Admin = &user.Admin
	}

	if user.Username == "admin" {
		if opts.Username != user.Username {
			return userConflictError("cannot_change_admin_username")
		}
		if !*opts.Admin {
			return userConflictError("cannot_demote_admin_user")
		}
		if opts.Locked {
			return userConflictError("cannot_lock_admin_user")
		}
	}

	if user.Source == "pam" {
		if user.ExternallyManaged {
			return s.editExternalPamUser(user, opts)
		}
		return s.editManagedPamUser(user, opts)
	}

	if opts.Username != user.Username {
		if opts.Username == "" || len(opts.Username) > 128 {
			return userValidationError("invalid_username_length")
		}
		if utils.IsReservedUsername(opts.Username) {
			return userValidationError("reserved_username")
		}
		if !utils.IsValidUsername(opts.Username) {
			return userValidationError("invalid_username_format")
		}
	}
	if opts.Email != "" && !utils.IsValidEmail(opts.Email) {
		return userValidationError("invalid_email_format")
	}
	if opts.Password != "" && (len(opts.Password) < 8 || len(opts.Password) > 72) {
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
		shared, err := s.nameShared(opts.Username)
		if err != nil {
			return userInternalError("username_check_failed", err)
		}
		if shared {
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

	if user.Shared {
		err = s.editSharedUser(user, opts, hashedPassword)
	} else {
		err = s.editLocalUser(user, opts, hashedPassword)
	}
	if err != nil {
		return err
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

// A realm user's name, profile and credentials belong to their provider; Sylve keeps the lock and
// preferences.
func (s *Service) editOIDCUser(user *models.User, opts EditUserOpts) error {
	if opts.Username != user.Username || opts.Password != "" {
		return userConflictError("external_user_read_only")
	}
	principal := PrincipalOf(user)
	var current clusterModels.OIDCUser
	if err := s.DB.Where("realm = ? AND username = ?", principal.Realm, principal.Username).First(&current).Error; err != nil {
		return userInternalError("user_lookup_failed", err)
	}
	current.Locked = opts.Locked
	if opts.ConfirmGuestActions != nil {
		current.ConfirmGuestActions = *opts.ConfirmGuestActions
	}
	if err := s.propose(context.Background(), "oidc_user", "update", current); err != nil {
		return userDependencyError("cluster_write_failed", err)
	}
	return nil
}

func (s *Service) editSharedUser(user *models.User, opts EditUserOpts, hashedPassword string) error {
	var current clusterModels.SylveUser
	if err := s.DB.Where("username = ?", user.Username).First(&current).Error; err != nil {
		return userInternalError("user_lookup_failed", err)
	}
	ctx := context.Background()
	if opts.Username != user.Username {
		if err := s.propose(ctx, "sylve_user", "rename", clusterModels.SylveUserRename{From: user.Username, To: opts.Username}); err != nil {
			return userDependencyError("cluster_write_failed", err)
		}
		current.Username = opts.Username
	}
	current.FullName = opts.FullName
	current.Email = opts.Email
	if opts.ConfirmGuestActions != nil {
		current.ConfirmGuestActions = *opts.ConfirmGuestActions
	}
	if hashedPassword != "" {
		current.Password = hashedPassword
	}
	if err := s.propose(ctx, "sylve_user", "update", current); err != nil {
		return userDependencyError("cluster_write_failed", err)
	}
	return nil
}

func (s *Service) editLocalUser(user *models.User, opts EditUserOpts, hashedPassword string) error {
	updates := map[string]any{
		"full_name": opts.FullName,
		"username":  opts.Username,
		"email":     opts.Email,
		"admin":     *opts.Admin,
	}
	if opts.ConfirmGuestActions != nil {
		updates["confirm_guest_actions"] = *opts.ConfirmGuestActions
	}
	if hashedPassword != "" {
		updates["password"] = hashedPassword
	}
	if opts.Username != user.Username {
		owns, err := s.ownsNameGrants(user)
		if err != nil {
			return userInternalError("user_grants_rename_failed", err)
		}
		realm := PrincipalOf(user).Realm
		if owns {
			if err := s.renameSubject(clusterModels.ACLSubjectUser, user.Username+"@"+realm, opts.Username+"@"+realm); err != nil {
				return userInternalError("user_grants_rename_failed", err)
			}
		}
	}
	revokeSessions := opts.Username != user.Username || *opts.Admin != user.Admin || opts.Password != ""
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
		if revokeSessions {
			if err := s.revokeUserTokens(tx, user.ID); err != nil {
				return err
			}
		}
		if opts.Username != user.Username {
			return clusterModels.RestoreSylveUserTx(tx, user.Username)
		}
		return nil
	}); err != nil {
		return userInternalError("user_update_failed", err)
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
