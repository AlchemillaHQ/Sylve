// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import (
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func (s *Service) editExternalPamUser(user *models.User, opts EditUserOpts) error {
	if opts.Username != user.Username {
		return userConflictError("cannot_change_pam_username")
	}
	if opts.UID != user.UID || opts.Shell != user.Shell ||
		opts.HomeDirectory != user.HomeDirectory || opts.HomeDirPerms != user.HomeDirPerms ||
		opts.SSHPublicKey != user.SSHPublicKey || opts.DoasEnabled != user.DoasEnabled ||
		opts.NewPrimaryGroup {
		return userConflictError("external_user_read_only")
	}
	if (opts.PrimaryGroupID == nil) != (user.PrimaryGroupID == nil) ||
		(opts.PrimaryGroupID != nil && *opts.PrimaryGroupID != *user.PrimaryGroupID) {
		return userConflictError("external_user_read_only")
	}
	auxGroups := groupIDs(user.Groups)
	if user.PrimaryGroupID != nil {
		delete(auxGroups, *user.PrimaryGroupID)
	}
	requested := make(map[uint]struct{}, len(opts.AuxGroupIDs))
	for _, id := range opts.AuxGroupIDs {
		if user.PrimaryGroupID != nil && id == *user.PrimaryGroupID {
			continue
		}
		if _, exists := auxGroups[id]; !exists {
			return userConflictError("external_user_read_only")
		}
		requested[id] = struct{}{}
	}
	if len(requested) != len(auxGroups) {
		return userConflictError("external_user_read_only")
	}
	sambaAction, err := normalizeSambaAction(opts.SambaAction)
	if err != nil {
		return err
	}
	if sambaAction != SambaActionKeep {
		return userConflictError("external_user_read_only")
	}
	if opts.Email != "" && !utils.IsValidEmail(opts.Email) {
		return userValidationError("invalid_email_format")
	}
	if opts.Password != "" && (len(opts.Password) < 8 || len(opts.Password) > 72) {
		return userValidationError("invalid_password_length")
	}
	if user.DisablePassword && !opts.DisablePassword && opts.Password == "" {
		return userValidationError("password_required_to_enable")
	}

	updates := map[string]any{
		"full_name":        opts.FullName,
		"email":            opts.Email,
		"admin":            opts.Admin,
		"locked":           opts.Locked,
		"disable_password": opts.DisablePassword,
	}
	if opts.ConfirmGuestActions != nil {
		updates["confirm_guest_actions"] = *opts.ConfirmGuestActions
	}
	if opts.Password != "" {
		hashed, err := s.passwordHasher.Hash(opts.Password)
		if err != nil {
			return userInternalError("password_hash_failed", err)
		}
		updates["password"] = hashed
	}
	revokeSessions := opts.Password != "" || opts.Admin != user.Admin ||
		opts.Locked != user.Locked || opts.DisablePassword != user.DisablePassword
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(user).Updates(updates).Error; err != nil {
			return err
		}
		if revokeSessions {
			return s.revokeUserTokens(tx, user.ID)
		}
		return nil
	}); err != nil {
		return userInternalError("pam_user_update_failed", err)
	}
	if opts.Password != "" {
		s.loginMu.Lock()
		delete(s.loginAttempts, user.Username)
		s.loginMu.Unlock()
	}
	return nil
}
