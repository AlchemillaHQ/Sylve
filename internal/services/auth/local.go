// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/system/samba"
	sambaUtils "github.com/alchemillahq/sylve/pkg/system/samba"
	"github.com/alchemillahq/sylve/pkg/utils"
)

// Re-export opts types so handlers can use auth.CreateUserOpts without importing the interface package.
type CreateUserOpts = serviceInterfaces.CreateUserOpts
type EditUserOpts = serviceInterfaces.EditUserOpts

func (s *Service) ListUsers() ([]models.User, error) {
	var users []models.User
	if err := s.DB.Preload("Groups").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_users: %w", err)
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
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return fmt.Errorf("failed_to_get_basic_settings: %w", err)
	}

	if user.Email != "" && !utils.IsValidEmail(user.Email) {
		return fmt.Errorf("invalid_email_format: %s", user.Email)
	}

	if user.Username == "" || len(user.Username) < 3 || len(user.Username) > 128 {
		return fmt.Errorf("invalid_username_length: %s", user.Username)
	}

	if !user.DisablePassword {
		if user.Password == "" || len(user.Password) < 8 || len(user.Password) > 128 {
			return fmt.Errorf("invalid_password_length: %s", user.Password)
		}
	}

	if !utils.IsValidUsername(user.Username) {
		return fmt.Errorf("invalid_username_format: %s", user.Username)
	}

	// Validate UID uniqueness if specified
	if user.UID > 0 {
		var count int64
		if err := s.DB.Model(&models.User{}).Where("uid = ?", user.UID).Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_uid_uniqueness: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("uid_already_in_use: %d", user.UID)
		}
	}

	var pwCopy string
	if user.Password != "" {
		pwCopy = user.Password
		hashed, err := utils.HashPassword(user.Password)
		if err != nil {
			return fmt.Errorf("failed_to_hash_password: %w", err)
		}
		user.Password = hashed
	}

	exists, err := system.UnixUserExists(user.Username)
	if err != nil {
		return fmt.Errorf("failed_to_check_unix_user: %w", err)
	}
	if exists {
		return fmt.Errorf("user_already_exists: %s", user.Username)
	}

	// Resolve primary group
	primaryGroupName := "sylve_g"
	if opts.NewPrimaryGroup {
		if err := system.CreateUnixGroup(user.Username); err != nil {
			return fmt.Errorf("failed_to_create_primary_group: %w", err)
		}
		primaryGroupName = user.Username
	} else if user.PrimaryGroupID != nil {
		var pg models.Group
		if err := s.DB.First(&pg, *user.PrimaryGroupID).Error; err != nil {
			return fmt.Errorf("primary_group_not_found: %d", *user.PrimaryGroupID)
		}
		primaryGroupName = pg.Name
	}

	// Apply defaults
	if user.Shell == "" {
		user.Shell = "/usr/sbin/nologin"
	}
	if user.HomeDirectory == "" {
		user.HomeDirectory = "/nonexistent"
	}
	if user.HomeDirPerms == 0 {
		user.HomeDirPerms = 493
	}

	createHome := user.HomeDirectory != "/nonexistent"

	if err := system.CreateUnixUserFull(system.UnixUserCreateOpts{
		Name:       user.Username,
		Shell:      user.Shell,
		Dir:        user.HomeDirectory,
		Group:      primaryGroupName,
		UID:        user.UID,
		CreateHome: createHome,
	}); err != nil {
		return fmt.Errorf("failed_to_create_unix_user: %w", err)
	}

	// chmod home directory if it was created
	if createHome {
		if err := os.Chmod(user.HomeDirectory, os.FileMode(user.HomeDirPerms)); err != nil {
			logger.L.Warn().Msgf("failed to chmod home directory %s: %v", user.HomeDirectory, err)
		}
	}

	// Record new primary group in DB if created
	if opts.NewPrimaryGroup {
		newGroup := models.Group{Name: user.Username}
		if err := s.DB.Create(&newGroup).Error; err != nil {
			logger.L.Warn().Msgf("failed to create primary group record: %v", err)
		} else {
			user.PrimaryGroupID = &newGroup.ID
		}
	}

	if slices.Contains(basicSettings.Services, models.SambaServer) {
		if err := samba.CreateSambaUser(user.Username, pwCopy); err != nil {
			return fmt.Errorf("failed_to_create_samba_user: %w", err)
		}
	}

	if err := s.DB.Create(user).Error; err != nil {
		return fmt.Errorf("failed_to_create_user: %w", err)
	}

	// Associate user with new primary group in the many-to-many table
	if opts.NewPrimaryGroup && user.PrimaryGroupID != nil {
		var pg models.Group
		if err := s.DB.First(&pg, *user.PrimaryGroupID).Error; err == nil {
			if err := s.DB.Model(&pg).Association("Users").Append(user); err != nil {
				logger.L.Warn().Msgf("failed to associate user with primary group: %v", err)
			}
		}
	}

	// Add to sylve_g (always, unless primary group is sylve_g which handles it via pw)
	var sylveGroup models.Group
	if err := s.DB.Where("name = ?", "sylve_g").First(&sylveGroup).Error; err == nil {
		if err := s.DB.Model(&sylveGroup).Association("Users").Append(user); err != nil {
			return fmt.Errorf("failed_to_add_user_to_sylve_g_group: %w", err)
		}
	}

	// Add aux groups
	for _, gid := range opts.AuxGroupIDs {
		var ag models.Group
		if err := s.DB.First(&ag, gid).Error; err != nil {
			logger.L.Warn().Msgf("auxiliary group %d not found, skipping", gid)
			continue
		}
		if err := system.AddUserToGroup(user.Username, ag.Name); err != nil {
			logger.L.Warn().Msgf("failed to add user to aux group %s: %v", ag.Name, err)
		}
		if err := s.DB.Model(&ag).Association("Users").Append(user); err != nil {
			logger.L.Warn().Msgf("failed to add aux group association for %s: %v", ag.Name, err)
		}
	}

	// Post-creation system actions
	if user.SSHPublicKey != "" && createHome {
		if err := system.WriteSSHAuthorizedKey(user.HomeDirectory, user.SSHPublicKey); err != nil {
			logger.L.Warn().Msgf("failed to write SSH authorized key: %v", err)
		}
	}

	if user.DoasEnabled {
		if err := system.AddDoasPerm(user.Username); err != nil {
			logger.L.Warn().Msgf("failed to add doas perm: %v", err)
		}
	}

	if user.Locked {
		if err := system.LockUnixUser(user.Username); err != nil {
			logger.L.Warn().Msgf("failed to lock user: %v", err)
		}
	}

	if user.DisablePassword {
		if err := system.DisableUnixUserPassword(user.Username); err != nil {
			logger.L.Warn().Msgf("failed to disable unix password: %v", err)
		}
	}

	return nil
}

func (s *Service) GetNextUID() (int, error) {
	return system.GetNextUnixUID()
}

func (s *Service) DeleteUser(userID uint) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("failed_to_get_user: %w", err)
	}

	if user.Username == "" {
		return fmt.Errorf("user_not_found: %d", userID)
	}

	if user.Username == "admin" {
		return fmt.Errorf("cannot_delete_admin_user")
	}

	if err := samba.DeleteSambaUser(user.Username); err != nil {
		return fmt.Errorf("failed_to_delete_samba_user: %w", err)
	}

	if err := system.DeleteUnixUser(user.Username, true); err != nil {
		return fmt.Errorf("failed_to_delete_unix_user: %w", err)
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&models.Token{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user_tokens: %w", err)
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&models.WebAuthnCredential{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user_passkeys: %w", err)
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&models.WebAuthnChallenge{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user_passkey_challenges: %w", err)
	}

	if err := s.DB.Delete(user).Error; err != nil {
		return fmt.Errorf("failed_to_delete_user: %w", err)
	}

	return nil
}

func (s *Service) EditUser(userID uint, opts EditUserOpts) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("failed_to_get_user: %w", err)
	}

	if user.Username == "admin" {
		if opts.Username != user.Username {
			return fmt.Errorf("cannot_change_admin_username")
		}
	}

	if !utils.IsValidUsername(opts.Username) {
		return fmt.Errorf("invalid_username_format: %s", opts.Username)
	}

	if user.Username != opts.Username {
		system.ChangeUsername(user.Username, opts.Username)
		user.Username = opts.Username
	}

	if opts.Password != "" {
		if len(opts.Password) < 8 || len(opts.Password) > 128 {
			return fmt.Errorf("invalid_password_length")
		}
		// Fix: hash the password before storing
		hashed, err := utils.HashPassword(opts.Password)
		if err != nil {
			return fmt.Errorf("failed_to_hash_password: %w", err)
		}
		user.Password = hashed

		err = sambaUtils.EditSambaUser(user.Username, opts.Password)
		if err != nil {
			logger.L.Error().Msgf("Failed to update Samba user '%s': %v", user.Username, err)
		}
	}

	if opts.Email != "" {
		if !utils.IsValidEmail(opts.Email) {
			return fmt.Errorf("invalid_email_format: %s", opts.Email)
		}
		user.Email = opts.Email
	}

	user.FullName = opts.FullName
	user.Admin = opts.Admin

	// UID
	if opts.UID > 0 && opts.UID != user.UID {
		var count int64
		if err := s.DB.Model(&models.User{}).Where("uid = ? AND id != ?", opts.UID, userID).Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_uid_uniqueness: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("uid_already_in_use: %d", opts.UID)
		}
		if err := system.ChangeUnixUserUID(user.Username, opts.UID); err != nil {
			return fmt.Errorf("failed_to_change_uid: %w", err)
		}
		user.UID = opts.UID
	}

	// Shell
	if opts.Shell != "" && opts.Shell != user.Shell {
		if err := system.SetUnixUserShell(user.Username, opts.Shell); err != nil {
			return fmt.Errorf("failed_to_set_shell: %w", err)
		}
		user.Shell = opts.Shell
	}

	// Home directory change
	if opts.HomeDirectory != "" && opts.HomeDirectory != user.HomeDirectory {
		createHome := opts.HomeDirectory != "/nonexistent"
		if err := system.ChangeUnixUserHomeDir(user.Username, opts.HomeDirectory, createHome); err != nil {
			return fmt.Errorf("failed_to_change_home_directory: %w", err)
		}
		user.HomeDirectory = opts.HomeDirectory
	}

	// Home directory permissions (only if home dir exists and is not /nonexistent)
	if opts.HomeDirPerms > 0 && opts.HomeDirPerms != user.HomeDirPerms {
		if user.HomeDirectory != "/nonexistent" && user.HomeDirectory != "" {
			if err := os.Chmod(user.HomeDirectory, os.FileMode(opts.HomeDirPerms)); err != nil {
				logger.L.Warn().Msgf("failed to chmod home directory %s: %v", user.HomeDirectory, err)
			}
		}
		user.HomeDirPerms = opts.HomeDirPerms
	}

	// SSH Public Key
	if opts.SSHPublicKey != user.SSHPublicKey {
		if opts.SSHPublicKey != "" && user.HomeDirectory != "/nonexistent" && user.HomeDirectory != "" {
			if err := system.WriteSSHAuthorizedKey(user.HomeDirectory, opts.SSHPublicKey); err != nil {
				logger.L.Warn().Msgf("failed to write SSH key: %v", err)
			}
		} else if opts.SSHPublicKey == "" && user.HomeDirectory != "/nonexistent" {
			if err := system.RemoveSSHAuthorizedKey(user.HomeDirectory); err != nil {
				logger.L.Warn().Msgf("failed to remove SSH key: %v", err)
			}
		}
		user.SSHPublicKey = opts.SSHPublicKey
	}

	// Disable password toggle
	if opts.DisablePassword != user.DisablePassword {
		if opts.DisablePassword {
			if err := system.DisableUnixUserPassword(user.Username); err != nil {
				logger.L.Warn().Msgf("failed to disable unix password: %v", err)
			}
		}
		user.DisablePassword = opts.DisablePassword
	}

	// Locked toggle
	if opts.Locked != user.Locked {
		if opts.Locked {
			if err := system.LockUnixUser(user.Username); err != nil {
				return fmt.Errorf("failed_to_lock_user: %w", err)
			}
		} else {
			if err := system.UnlockUnixUser(user.Username); err != nil {
				return fmt.Errorf("failed_to_unlock_user: %w", err)
			}
		}
		user.Locked = opts.Locked
	}

	// Doas toggle
	if opts.DoasEnabled != user.DoasEnabled {
		if opts.DoasEnabled {
			if err := system.AddDoasPerm(user.Username); err != nil {
				logger.L.Warn().Msgf("failed to add doas perm: %v", err)
			}
		} else {
			if err := system.RemoveDoasPerm(user.Username); err != nil {
				logger.L.Warn().Msgf("failed to remove doas perm: %v", err)
			}
		}
		user.DoasEnabled = opts.DoasEnabled
	}

	// Primary group change
	if opts.NewPrimaryGroup {
		// Create a new group named after the user and set it as primary
		groupName := user.Username
		if !system.UnixGroupExists(groupName) {
			if err := system.CreateUnixGroup(groupName); err != nil {
				return fmt.Errorf("failed_to_create_primary_group: %w", err)
			}
		}
		newGroup := models.Group{Name: groupName}
		if err := s.DB.Where("name = ?", groupName).FirstOrCreate(&newGroup).Error; err != nil {
			return fmt.Errorf("failed_to_create_primary_group_record: %w", err)
		}
		if err := system.ChangeUnixUserPrimaryGroup(user.Username, groupName); err != nil {
			return fmt.Errorf("failed_to_change_primary_group: %w", err)
		}
		user.PrimaryGroupID = &newGroup.ID
		// Associate the user with the new primary group in the many-to-many table
		if err := s.DB.Model(&newGroup).Association("Users").Append(user); err != nil {
			logger.L.Warn().Msgf("failed to associate user with new primary group: %v", err)
		}
	} else if opts.PrimaryGroupID != nil && (user.PrimaryGroupID == nil || *opts.PrimaryGroupID != *user.PrimaryGroupID) {
		var pg models.Group
		if err := s.DB.First(&pg, *opts.PrimaryGroupID).Error; err != nil {
			return fmt.Errorf("primary_group_not_found: %d", *opts.PrimaryGroupID)
		}
		if err := system.ChangeUnixUserPrimaryGroup(user.Username, pg.Name); err != nil {
			return fmt.Errorf("failed_to_change_primary_group: %w", err)
		}
		user.PrimaryGroupID = opts.PrimaryGroupID
	} else if opts.PrimaryGroupID == nil && !opts.NewPrimaryGroup && user.PrimaryGroupID != nil {
		// Clear primary group — revert to default (sylve_g)
		if err := system.ChangeUnixUserPrimaryGroup(user.Username, "sylve_g"); err != nil {
			logger.L.Warn().Msgf("failed to revert primary group to sylve_g: %v", err)
		}
		user.PrimaryGroupID = nil
	}

	// Sync auxiliary groups
	if opts.AuxGroupIDs != nil {
		// Build desired set (exclude primary group)
		desiredAux := make(map[uint]bool)
		for _, gid := range opts.AuxGroupIDs {
			if user.PrimaryGroupID == nil || gid != *user.PrimaryGroupID {
				desiredAux[gid] = true
			}
		}

		currentAux := make(map[uint]bool)
		for _, g := range user.Groups {
			if user.PrimaryGroupID != nil && g.ID == *user.PrimaryGroupID {
				continue
			}
			currentAux[g.ID] = true
		}

		// Remove from groups no longer desired
		for gid := range currentAux {
			if !desiredAux[gid] {
				var ag models.Group
				if err := s.DB.First(&ag, gid).Error; err != nil {
					continue
				}
				if err := system.RemoveUserFromGroup(user.Username, ag.Name); err != nil {
					logger.L.Warn().Msgf("failed to remove user from aux group %s: %v", ag.Name, err)
				}
				if err := s.DB.Model(&ag).Association("Users").Delete(user); err != nil {
					logger.L.Warn().Msgf("failed to remove aux group association for %s: %v", ag.Name, err)
				}
			}
		}

		// Add to new groups
		for gid := range desiredAux {
			if !currentAux[gid] {
				var ag models.Group
				if err := s.DB.First(&ag, gid).Error; err != nil {
					logger.L.Warn().Msgf("auxiliary group %d not found, skipping", gid)
					continue
				}
				if err := system.AddUserToGroup(user.Username, ag.Name); err != nil {
					logger.L.Warn().Msgf("failed to add user to aux group %s: %v", ag.Name, err)
				}
				if err := s.DB.Model(&ag).Association("Users").Append(user); err != nil {
					logger.L.Warn().Msgf("failed to add aux group association for %s: %v", ag.Name, err)
				}
			}
		}
	}

	// Chown home directory to user + primary group after all changes
	if user.HomeDirectory != "" && user.HomeDirectory != "/nonexistent" {
		primaryGroupName := "sylve_g"
		if user.PrimaryGroupID != nil {
			var pg models.Group
			if err := s.DB.First(&pg, *user.PrimaryGroupID).Error; err == nil {
				primaryGroupName = pg.Name
			}
		}
		if err := system.ChownHome(user.HomeDirectory, user.UID, primaryGroupName); err != nil {
			logger.L.Warn().Msgf("failed to chown home directory: %v", err)
		}
	}

	// Omit Groups so GORM does not re-sync the stale in-memory many2many slice
	// (associations are managed explicitly via the Association API above).
	if err := s.DB.Omit("Groups").Save(user).Error; err != nil {
		return fmt.Errorf("failed_to_edit_user: %w", err)
	}

	return nil
}

func (s *Service) UpdateLastUsageTime(userID uint) error {
	now := time.Now()

	// Try to update only if last_login_time < now - 30s
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
