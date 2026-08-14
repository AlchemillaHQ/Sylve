// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/system/samba"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

const (
	SambaActionKeep   = "keep"
	SambaActionUpsert = "upsert"
	SambaActionRemove = "remove"
)

type ImportableUnixUser = serviceInterfaces.ImportableUnixUser

var (
	sambaUserExistsFn     = samba.SambaUserExists
	createSambaUserFn     = samba.CreateSambaUser
	editSambaUserFn       = samba.EditSambaUser
	deleteSambaUserFn     = samba.DeleteSambaUser
	doasAvailableFn       = system.DoasAvailable
	addDoasPermFn         = system.AddDoasPerm
	removeDoasPermFn      = system.RemoveDoasPerm
	sambaToolsAvailableFn = func() bool {
		for _, path := range []string{"/usr/local/bin/pdbedit", "/usr/local/bin/smbpasswd"} {
			if _, err := os.Stat(path); err != nil {
				return false
			}
		}
		return true
	}
)

func normalizeSambaAction(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return SambaActionKeep, nil
	}
	switch action {
	case SambaActionKeep, SambaActionUpsert, SambaActionRemove:
		return action, nil
	default:
		return "", userValidationError("invalid_samba_action")
	}
}

func validatePAMUsername(username string) error {
	if len(username) < 3 || len(username) > 128 {
		return userValidationError("invalid_username_length")
	}
	if !utils.IsValidUsername(username) {
		return userValidationError("invalid_username_format")
	}
	if isProtectedSystemUser(username) {
		return userConflictError("protected_system_user")
	}
	return nil
}

func validatePAMUID(uid int) error {
	if uid < 1000 || uid > 65533 {
		return userValidationError("invalid_uid")
	}
	return nil
}

func validatePAMShell(shell string) error {
	if shell == "" || !filepath.IsAbs(shell) || filepath.Clean(shell) != shell ||
		strings.ContainsAny(shell, "\x00\r\n\t ") {
		return userValidationError("invalid_shell")
	}
	return nil
}

func validatePAMHomePath(home string) error {
	if home == "/nonexistent" {
		return nil
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" {
		return userValidationError("invalid_home_directory")
	}

	parts := strings.Split(strings.TrimPrefix(home, "/"), "/")
	if len(parts) < 2 {
		return userValidationError("unsafe_home_directory")
	}

	if strings.HasPrefix(home, "/usr/home/") {
		return nil
	}
	for _, protected := range []string{
		"/bin", "/boot", "/compat", "/dev", "/entropy", "/etc", "/lib", "/libexec",
		"/net", "/proc", "/rescue", "/root", "/sbin", "/sys", "/usr", "/var",
	} {
		if home == protected || strings.HasPrefix(home, protected+"/") {
			return userValidationError("unsafe_home_directory")
		}
	}

	if info, err := os.Lstat(home); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return userValidationError("unsafe_home_directory")
	} else if err != nil && !os.IsNotExist(err) {
		return userDependencyError("home_directory_lookup_failed", err)
	}
	return nil
}

func validateHomePermissions(perms uint) error {
	if perms == 0 || perms > 0o777 {
		return userValidationError("invalid_home_directory_permissions")
	}
	return nil
}

func ensureHomeAvailable(db *gorm.DB, home string, excludeUserID uint) error {
	if home == "/nonexistent" {
		return nil
	}

	query := db.Where("home_directory = ?", home)
	if excludeUserID > 0 {
		query = query.Where("id != ?", excludeUserID)
	}
	var count int64
	if err := query.Model(&models.User{}).Count(&count).Error; err != nil {
		return userInternalError("home_directory_check_failed", err)
	}
	if count > 0 {
		return userConflictError("home_directory_already_in_use")
	}

	entries, err := os.ReadDir(home)
	if err == nil {
		if len(entries) > 0 {
			return userConflictError("home_directory_is_not_empty")
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return userDependencyError("home_directory_lookup_failed", err)
}

func appendUniqueGroup(groups []models.Group, group models.Group) []models.Group {
	for _, existing := range groups {
		if existing.ID == group.ID {
			return groups
		}
	}
	return append(groups, group)
}

func groupIDs(groups []models.Group) map[uint]models.Group {
	result := make(map[uint]models.Group, len(groups))
	for _, group := range groups {
		result[group.ID] = group
	}
	return result
}

func (s *Service) loadManagedGroupByID(id uint) (models.Group, error) {
	if id == 0 {
		return models.Group{}, userValidationError("invalid_group_id")
	}
	var group models.Group
	if err := s.DB.First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Group{}, userNotFoundError("group_not_found")
		}
		return models.Group{}, userInternalError("group_lookup_failed", err)
	}
	exists, err := system.UnixGroupExistsWithError(group.Name)
	if err != nil {
		return models.Group{}, userDependencyError("unix_group_lookup_failed", err)
	}
	if !exists {
		return models.Group{}, userConflictError("unix_group_does_not_exist")
	}
	return group, nil
}

func (s *Service) loadManagedGroupByName(name string) (models.Group, error) {
	var group models.Group
	if err := s.DB.Where("name = ?", name).First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Group{}, userNotFoundError("group_not_found")
		}
		return models.Group{}, userInternalError("group_lookup_failed", err)
	}
	exists, err := system.UnixGroupExistsWithError(group.Name)
	if err != nil {
		return models.Group{}, userDependencyError("unix_group_lookup_failed", err)
	}
	if !exists {
		return models.Group{}, userConflictError("unix_group_does_not_exist")
	}
	return group, nil
}

func primaryGroupError(err error) error {
	var operationError *UserOperationError
	if errors.As(err, &operationError) &&
		operationError.Kind == UserOperationNotFound && operationError.Code == "group_not_found" {
		return userNotFoundError("primary_group_not_found")
	}
	return err
}

func (s *Service) loadAuxiliaryGroups(ids []uint, primaryID *uint) ([]models.Group, error) {
	groups := make([]models.Group, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, userValidationError("invalid_auxiliary_group_id")
		}
		if primaryID != nil && id == *primaryID {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		group, err := s.loadManagedGroupByID(id)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (s *Service) validateNewPAMIdentity(user *models.User) error {
	if err := validatePAMUsername(user.Username); err != nil {
		return err
	}
	if user.Email != "" && !utils.IsValidEmail(user.Email) {
		return userValidationError("invalid_email_format")
	}
	if err := validatePAMUID(user.UID); err != nil {
		return err
	}
	if err := validatePAMShell(user.Shell); err != nil {
		return err
	}
	if err := validatePAMHomePath(user.HomeDirectory); err != nil {
		return err
	}
	if err := validateHomePermissions(user.HomeDirPerms); err != nil {
		return err
	}
	if len(user.Password) < 8 || len(user.Password) > 128 {
		return userValidationError("invalid_password_length")
	}

	existing, err := s.findUsernameConflict(user.Username, 0)
	if err != nil {
		return userInternalError("username_check_failed", err)
	}
	if existing != nil {
		if existing.Source == "local" {
			return userConflictError("user_source_conflict")
		}
		return userConflictError("username_already_exists")
	}

	exists, err := system.UnixUserExists(user.Username)
	if err != nil {
		return userDependencyError("unix_user_lookup_failed", err)
	}
	if exists {
		return userConflictError("unix_username_already_exists")
	}

	uidExists, err := system.UnixUIDExists(user.UID)
	if err != nil {
		return userDependencyError("unix_uid_lookup_failed", err)
	}
	if uidExists {
		return userConflictError("uid_already_in_use")
	}

	return ensureHomeAvailable(s.DB, user.HomeDirectory, 0)
}

func (s *Service) createPamUser(user *models.User, opts CreateUserOpts) error {
	if user.HomeDirectory == "" {
		user.HomeDirectory = "/nonexistent"
	}
	if user.Shell == "" {
		user.Shell = "/usr/sbin/nologin"
	}
	if user.HomeDirPerms == 0 {
		user.HomeDirPerms = 0o755
	}

	plainPassword := user.Password
	defer func() { plainPassword = "" }()
	if err := s.validateNewPAMIdentity(user); err != nil {
		user.Password = ""
		return err
	}
	hashedPassword, err := s.passwordHasher.Hash(plainPassword)
	if err != nil {
		user.Password = ""
		return userInternalError("password_hash_failed", err)
	}
	user.Password = ""

	if opts.NewPrimaryGroup && user.PrimaryGroupID != nil {
		return userValidationError("conflicting_primary_group_options")
	}
	if user.DoasEnabled && !doasAvailableFn() {
		return userConflictError("doas_unavailable")
	}
	if opts.CreateSamba {
		if !sambaToolsAvailableFn() {
			return userDependencyError("samba_unavailable", nil)
		}
		exists, err := sambaUserExistsFn(user.Username)
		if err != nil {
			return userDependencyError("samba_user_lookup_failed", err)
		}
		if exists {
			return userConflictError("samba_user_already_exists")
		}
	}

	var (
		primaryGroup     models.Group
		primaryGroupID   *uint
		primaryGroupName string
		createdUnixGroup bool
		createdDBGroup   bool
		createdUnixUser  bool
		createdSambaUser bool
		createdDoasEntry bool
		completed        bool
	)

	defer func() {
		if completed {
			return
		}
		if createdSambaUser {
			if err := deleteSambaUserFn(user.Username); err != nil {
				logger.L.Error().Err(err).Str("username", user.Username).Msg("pam_create_rollback_samba_failed")
			}
		}
		if createdDoasEntry {
			if err := removeDoasPermFn(user.Username); err != nil {
				logger.L.Error().Err(err).Str("username", user.Username).Msg("pam_create_rollback_doas_failed")
			}
		}
		if createdUnixUser {
			if err := system.DeleteUnixUser(user.Username, true); err != nil {
				logger.L.Error().Err(err).Str("username", user.Username).Msg("pam_create_rollback_unix_user_failed")
			}
		}
		if createdUnixGroup {
			if err := system.DeleteUnixGroup(user.Username); err != nil {
				logger.L.Error().Err(err).Str("group", user.Username).Msg("pam_create_rollback_unix_group_failed")
			}
		}
		if createdDBGroup && primaryGroup.ID > 0 {
			if err := s.DB.Delete(&primaryGroup).Error; err != nil {
				logger.L.Error().Err(err).Uint("group_id", primaryGroup.ID).Msg("pam_create_rollback_db_group_failed")
			}
		}
	}()

	sylveGroup, err := s.loadManagedGroupByName("sylve_g")
	if err != nil {
		return err
	}

	var auxGroups []models.Group
	if opts.NewPrimaryGroup {
		auxGroups, err = s.loadAuxiliaryGroups(opts.AuxGroupIDs, nil)
		if err != nil {
			return err
		}

		primaryGroupName = user.Username
		exists, err := system.UnixGroupExistsWithError(primaryGroupName)
		if err != nil {
			return userDependencyError("unix_group_lookup_failed", err)
		}
		if !exists {
			if err := system.CreateUnixGroup(primaryGroupName); err != nil {
				return userDependencyError("primary_group_create_failed", err)
			}
			createdUnixGroup = true
		}

		err = s.DB.Where("name = ?", primaryGroupName).First(&primaryGroup).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			primaryGroup = models.Group{Name: primaryGroupName}
			if err := s.DB.Create(&primaryGroup).Error; err != nil {
				return userInternalError("primary_group_record_create_failed", err)
			}
			createdDBGroup = true
		} else if err != nil {
			return userInternalError("primary_group_lookup_failed", err)
		}
		primaryGroupID = &primaryGroup.ID
	} else if user.PrimaryGroupID != nil {
		primaryGroup, err = s.loadManagedGroupByID(*user.PrimaryGroupID)
		if err != nil {
			return primaryGroupError(err)
		}
		primaryGroupName = primaryGroup.Name
		primaryGroupID = &primaryGroup.ID
		auxGroups, err = s.loadAuxiliaryGroups(opts.AuxGroupIDs, primaryGroupID)
		if err != nil {
			return err
		}
	} else {
		primaryGroup = sylveGroup
		primaryGroupName = primaryGroup.Name
		auxGroups, err = s.loadAuxiliaryGroups(opts.AuxGroupIDs, nil)
		if err != nil {
			return err
		}
	}

	filteredAuxGroups := auxGroups[:0]
	for _, group := range auxGroups {
		if group.ID != sylveGroup.ID && group.Name != primaryGroupName {
			filteredAuxGroups = append(filteredAuxGroups, group)
		}
	}
	auxGroups = filteredAuxGroups

	if err := system.CreateUnixUserFull(system.UnixUserCreateOpts{
		Name:       user.Username,
		UID:        user.UID,
		Shell:      user.Shell,
		Dir:        user.HomeDirectory,
		Group:      primaryGroupName,
		CreateHome: user.HomeDirectory != "/nonexistent",
	}); err != nil {
		return userDependencyError("unix_user_create_failed", err)
	}
	createdUnixUser = true

	if err := system.SetUnixUserPassword(user.Username, plainPassword); err != nil {
		return userDependencyError("unix_password_update_failed", err)
	}

	if user.HomeDirectory != "/nonexistent" {
		if err := os.Chmod(user.HomeDirectory, os.FileMode(user.HomeDirPerms)); err != nil {
			return userDependencyError("home_permissions_update_failed", err)
		}
		if user.SSHPublicKey != "" {
			if err := system.WriteSSHAuthorizedKey(user.HomeDirectory, user.SSHPublicKey); err != nil {
				return userDependencyError("ssh_key_update_failed", err)
			}
		}
	}

	for _, group := range auxGroups {
		if err := system.AddUserToGroup(user.Username, group.Name); err != nil {
			return userDependencyError("auxiliary_group_update_failed", err)
		}
	}
	if primaryGroupName != sylveGroup.Name {
		if err := system.AddUserToGroup(user.Username, sylveGroup.Name); err != nil {
			return userDependencyError("sylve_group_update_failed", err)
		}
	}

	if user.DisablePassword {
		if err := system.DisableUnixUserPassword(user.Username); err != nil {
			return userDependencyError("unix_password_disable_failed", err)
		}
	}
	if user.Locked {
		if err := system.LockUnixUser(user.Username); err != nil {
			return userDependencyError("unix_user_lock_failed", err)
		}
	}
	if user.DoasEnabled {
		if err := addDoasPermFn(user.Username); err != nil {
			return userDependencyError("doas_update_failed", err)
		}
		createdDoasEntry = true
	}
	if opts.CreateSamba {
		if err := createSambaUserFn(user.Username, plainPassword); err != nil {
			return userDependencyError("samba_user_create_failed", err)
		}
		createdSambaUser = true
	}

	if user.HomeDirectory != "/nonexistent" {
		if err := system.ChownHome(user.HomeDirectory, user.UID, primaryGroupName); err != nil {
			return userDependencyError("home_ownership_update_failed", err)
		}
	}

	user.Source = "pam"
	user.Password = hashedPassword
	user.PrimaryGroupID = primaryGroupID
	groups := []models.Group{primaryGroup}
	for _, group := range auxGroups {
		groups = appendUniqueGroup(groups, group)
	}
	groups = appendUniqueGroup(groups, sylveGroup)

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		if err := tx.Model(user).Association("Groups").Replace(&groups); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return userInternalError("pam_user_record_create_failed", err)
	}
	user.Groups = groups
	completed = true
	return nil
}

func (s *Service) importPamUser(username, sylvePassword string, admin bool) (*models.User, error) {
	if err := validatePAMUsername(username); err != nil {
		return nil, err
	}
	existing, err := s.findUsernameConflict(username, 0)
	if err != nil {
		return nil, userInternalError("username_check_failed", err)
	}
	if existing != nil {
		if existing.Source == "local" {
			return nil, userConflictError("user_source_conflict")
		}
		return nil, userConflictError("username_already_exists")
	}

	exists, err := system.UnixUserExists(username)
	if err != nil {
		return nil, userDependencyError("unix_user_lookup_failed", err)
	}
	if !exists {
		return nil, userNotFoundError("unix_user_not_found")
	}

	info, err := system.GetUnixUserInfoFull(username)
	if err != nil {
		return nil, userDependencyError("unix_user_lookup_failed", err)
	}
	if err := validatePAMUID(info.UID); err != nil {
		return nil, userConflictError("protected_system_user")
	}
	if info.Username != username {
		return nil, userConflictError("unix_identity_mismatch")
	}
	if err := validatePAMShell(info.Shell); err != nil {
		return nil, err
	}
	if err := validatePAMHomePath(info.HomeDir); err != nil {
		return nil, err
	}
	var uidCount int64
	if err := s.DB.Model(&models.User{}).Where("uid = ? AND uid > 0", info.UID).Count(&uidCount).Error; err != nil {
		return nil, userInternalError("uid_check_failed", err)
	}
	if uidCount > 0 {
		return nil, userConflictError("uid_already_in_use")
	}

	hashedPassword := ""
	if sylvePassword != "" {
		if len(sylvePassword) < 8 || len(sylvePassword) > 128 {
			return nil, userValidationError("invalid_password_length")
		}
		hashedPassword, err = s.passwordHasher.Hash(sylvePassword)
		if err != nil {
			return nil, userInternalError("password_hash_failed", err)
		}
	}

	primaryGroupName, err := system.GetUnixGroupNameByGID(info.GID)
	if err != nil {
		return nil, userDependencyError("unix_primary_group_lookup_failed", err)
	}
	unixGroups, err := system.GetUnixUserGroups(username)
	if err != nil {
		return nil, userDependencyError("unix_group_membership_lookup_failed", err)
	}
	groupNames := append(unixGroups, primaryGroupName)
	sort.Strings(groupNames)

	homePerms := uint(0o755)
	if info.HomeDir != "/nonexistent" {
		if stat, err := os.Stat(info.HomeDir); err == nil {
			homePerms = uint(stat.Mode().Perm())
		} else if !os.IsNotExist(err) {
			return nil, userDependencyError("home_directory_lookup_failed", err)
		}
	}

	user := &models.User{
		Username:      username,
		FullName:      info.FullName,
		Password:      hashedPassword,
		Admin:         admin,
		UID:           info.UID,
		Shell:         info.Shell,
		HomeDirectory: info.HomeDir,
		HomeDirPerms:  homePerms,
		Source:        "pam",
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		groups := make([]models.Group, 0, len(groupNames))
		seen := make(map[string]struct{}, len(groupNames))
		var primaryGroup models.Group
		for _, name := range groupNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}

			var group models.Group
			if err := tx.Where("name = ?", name).First(&group).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				group = models.Group{Name: name}
				if err := tx.Create(&group).Error; err != nil {
					return err
				}
			}
			groups = append(groups, group)
			if name == primaryGroupName {
				primaryGroup = group
			}
		}
		if primaryGroup.ID == 0 {
			return fmt.Errorf("primary group %s was not resolved", primaryGroupName)
		}
		user.PrimaryGroupID = &primaryGroup.ID
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		if err := tx.Model(user).Association("Groups").Replace(&groups); err != nil {
			return err
		}
		user.Groups = groups
		return nil
	})
	if err != nil {
		return nil, userInternalError("user_import_failed", err)
	}
	return user, nil
}

func (s *Service) listImportablePamUsers() ([]ImportableUnixUser, error) {
	allUnix, err := system.ListAllUnixUsers()
	if err != nil {
		return nil, userDependencyError("unix_user_discovery_failed", err)
	}

	var dbUsers []struct {
		Username string
		UID      int
	}
	if err := s.DB.Model(&models.User{}).Select("username", "uid").Find(&dbUsers).Error; err != nil {
		return nil, userInternalError("user_list_failed", err)
	}
	dbUsernameSet := make(map[string]struct{}, len(dbUsers))
	dbUIDSet := make(map[int]struct{}, len(dbUsers))
	for _, user := range dbUsers {
		dbUsernameSet[user.Username] = struct{}{}
		if user.UID > 0 {
			dbUIDSet[user.UID] = struct{}{}
		}
	}

	result := make([]ImportableUnixUser, 0)
	for _, user := range allUnix {
		if validatePAMUsername(user.Username) != nil ||
			validatePAMUID(user.UID) != nil ||
			validatePAMShell(user.Shell) != nil ||
			validatePAMHomePath(user.HomeDir) != nil {
			continue
		}
		if _, exists := dbUsernameSet[user.Username]; exists {
			continue
		}
		if _, exists := dbUIDSet[user.UID]; exists {
			continue
		}
		result = append(result, ImportableUnixUser{
			Username:      user.Username,
			FullName:      user.FullName,
			UID:           user.UID,
			GID:           user.GID,
			Shell:         user.Shell,
			HomeDirectory: user.HomeDir,
		})
	}
	return result, nil
}

func (s *Service) editPamUser(user *models.User, opts EditUserOpts) error {
	if opts.Username != user.Username {
		return userConflictError("cannot_change_pam_username")
	}
	if opts.Email != "" && !utils.IsValidEmail(opts.Email) {
		return userValidationError("invalid_email_format")
	}
	if opts.Password != "" && (len(opts.Password) < 8 || len(opts.Password) > 128) {
		return userValidationError("invalid_password_length")
	}
	if user.DisablePassword && !opts.DisablePassword && opts.Password == "" {
		return userValidationError("password_required_to_enable")
	}
	if user.Username != "root" {
		if err := validatePAMUID(opts.UID); err != nil {
			return err
		}
		if err := validatePAMShell(opts.Shell); err != nil {
			return err
		}
		if err := validatePAMHomePath(opts.HomeDirectory); err != nil {
			return err
		}
		if err := validateHomePermissions(opts.HomeDirPerms); err != nil {
			return err
		}
	}
	sambaAction, err := normalizeSambaAction(opts.SambaAction)
	if err != nil {
		return err
	}
	if sambaAction == SambaActionUpsert && opts.Password == "" {
		return userValidationError("samba_password_required")
	}
	if sambaAction != SambaActionKeep && !sambaToolsAvailableFn() {
		return userDependencyError("samba_unavailable", nil)
	}

	exists, err := system.UnixUserExists(user.Username)
	if err != nil {
		return userDependencyError("unix_user_lookup_failed", err)
	}
	if !exists {
		return userNotFoundError("unix_user_not_found")
	}

	uidChanged := opts.UID != user.UID
	if uidChanged {
		if user.Username == "root" {
			return userConflictError("cannot_change_root_uid")
		}
		if err := validatePAMUID(opts.UID); err != nil {
			return err
		}
		var count int64
		if err := s.DB.Model(&models.User{}).Where("uid = ? AND id != ?", opts.UID, user.ID).Count(&count).Error; err != nil {
			return userInternalError("uid_check_failed", err)
		}
		if count > 0 {
			return userConflictError("uid_already_in_use")
		}
		uidExists, err := system.UnixUIDExists(opts.UID)
		if err != nil {
			return userDependencyError("unix_uid_lookup_failed", err)
		}
		if uidExists {
			return userConflictError("uid_already_in_use")
		}
	}

	shellChanged := opts.Shell != user.Shell
	if shellChanged {
		if err := validatePAMShell(opts.Shell); err != nil {
			return err
		}
	}
	homeChanged := opts.HomeDirectory != user.HomeDirectory
	if homeChanged {
		if err := validatePAMHomePath(opts.HomeDirectory); err != nil {
			return err
		}
		if err := ensureHomeAvailable(s.DB, opts.HomeDirectory, user.ID); err != nil {
			return err
		}
	}
	permsChanged := opts.HomeDirPerms != user.HomeDirPerms
	if permsChanged {
		if err := validateHomePermissions(opts.HomeDirPerms); err != nil {
			return err
		}
	}
	if opts.SSHPublicKey != user.SSHPublicKey &&
		opts.SSHPublicKey != "" &&
		opts.HomeDirectory == "/nonexistent" {
		return userValidationError("ssh_key_requires_home_directory")
	}
	if opts.DoasEnabled && !user.DoasEnabled && !doasAvailableFn() {
		return userConflictError("doas_unavailable")
	}
	if opts.NewPrimaryGroup && opts.PrimaryGroupID != nil {
		return userValidationError("conflicting_primary_group_options")
	}

	var (
		primaryGroup     models.Group
		primaryGroupID   *uint
		primaryGroupName string
		newPrimaryGroup  bool
	)
	if opts.NewPrimaryGroup {
		primaryGroupName = user.Username
		newPrimaryGroup = true
	} else if opts.PrimaryGroupID != nil {
		primaryGroup, err = s.loadManagedGroupByID(*opts.PrimaryGroupID)
		if err != nil {
			return primaryGroupError(err)
		}
		primaryGroupName = primaryGroup.Name
		primaryGroupID = &primaryGroup.ID
	} else {
		primaryGroup, err = s.loadManagedGroupByName("sylve_g")
		if err != nil {
			return err
		}
		primaryGroupName = primaryGroup.Name
	}
	auxGroups, err := s.loadAuxiliaryGroups(opts.AuxGroupIDs, primaryGroupID)
	if err != nil {
		return err
	}
	filteredAuxGroups := auxGroups[:0]
	for _, group := range auxGroups {
		if group.Name != primaryGroupName {
			filteredAuxGroups = append(filteredAuxGroups, group)
		}
	}
	auxGroups = filteredAuxGroups

	if user.Username == "root" {
		for _, current := range user.Groups {
			if current.Name != "wheel" {
				continue
			}
			keepWheel := false
			for _, desired := range auxGroups {
				if desired.ID == current.ID {
					keepWheel = true
					break
				}
			}
			if !keepWheel && (primaryGroupID == nil || *primaryGroupID != current.ID) {
				return userConflictError("cannot_remove_root_from_wheel")
			}
		}
	}

	sambaExists := false
	if sambaAction == SambaActionRemove {
		sambaExists, err = sambaUserExistsFn(user.Username)
		if err != nil {
			return userDependencyError("samba_user_lookup_failed", err)
		}
	}

	hashedPassword := ""
	if opts.Password != "" {
		hashedPassword, err = s.passwordHasher.Hash(opts.Password)
		if err != nil {
			return userInternalError("password_hash_failed", err)
		}
	}

	applied := false
	failIntegration := func(code string, cause error) error {
		return userIntegrationError(code, cause, applied)
	}
	revokeSessions := opts.Password != "" || opts.Admin != user.Admin ||
		opts.Locked != user.Locked || opts.DisablePassword != user.DisablePassword
	if revokeSessions {
		if err := s.revokeUserTokens(s.DB, user.ID); err != nil {
			return userInternalError("session_revoke_failed", err)
		}
	}

	if newPrimaryGroup {
		exists, err := system.UnixGroupExistsWithError(primaryGroupName)
		if err != nil {
			return failIntegration("unix_group_lookup_failed", err)
		}
		if !exists {
			if err := system.CreateUnixGroup(primaryGroupName); err != nil {
				return failIntegration("primary_group_create_failed", err)
			}
			applied = true
		}
		if err := s.DB.Where("name = ?", primaryGroupName).First(&primaryGroup).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return failIntegration("primary_group_lookup_failed", err)
			}
			primaryGroup = models.Group{Name: primaryGroupName}
			if err := s.DB.Create(&primaryGroup).Error; err != nil {
				return failIntegration("primary_group_record_create_failed", err)
			}
			applied = true
		}
		primaryGroupID = &primaryGroup.ID
	}

	primaryChanged := (user.PrimaryGroupID == nil) != (primaryGroupID == nil)
	if user.PrimaryGroupID != nil && primaryGroupID != nil && *user.PrimaryGroupID != *primaryGroupID {
		primaryChanged = true
	}

	if uidChanged {
		if err := system.ChangeUnixUserUID(user.Username, opts.UID); err != nil {
			return failIntegration("unix_uid_update_failed", err)
		}
		applied = true
	}
	if shellChanged {
		if err := system.SetUnixUserShell(user.Username, opts.Shell); err != nil {
			return failIntegration("unix_shell_update_failed", err)
		}
		applied = true
	}
	if homeChanged {
		if err := system.ChangeUnixUserHomeDir(user.Username, opts.HomeDirectory, opts.HomeDirectory != "/nonexistent"); err != nil {
			return failIntegration("unix_home_update_failed", err)
		}
		applied = true
	}
	if (homeChanged || permsChanged) && opts.HomeDirectory != "/nonexistent" {
		if err := os.Chmod(opts.HomeDirectory, os.FileMode(opts.HomeDirPerms)); err != nil {
			return failIntegration("home_permissions_update_failed", err)
		}
		applied = true
	}
	if opts.SSHPublicKey != user.SSHPublicKey {
		if opts.SSHPublicKey == "" {
			if err := system.RemoveSSHAuthorizedKey(opts.HomeDirectory); err != nil {
				return failIntegration("ssh_key_update_failed", err)
			}
		} else {
			if err := system.WriteSSHAuthorizedKey(opts.HomeDirectory, opts.SSHPublicKey); err != nil {
				return failIntegration("ssh_key_update_failed", err)
			}
		}
		applied = true
	}
	if primaryChanged {
		if err := system.ChangeUnixUserPrimaryGroup(user.Username, primaryGroupName); err != nil {
			return failIntegration("primary_group_update_failed", err)
		}
		applied = true
	}

	desiredGroups := []models.Group{primaryGroup}
	for _, group := range auxGroups {
		desiredGroups = appendUniqueGroup(desiredGroups, group)
	}
	currentAuxGroups := groupIDs(user.Groups)
	if user.PrimaryGroupID != nil {
		delete(currentAuxGroups, *user.PrimaryGroupID)
	} else {
		for id, group := range currentAuxGroups {
			if group.Name == "sylve_g" {
				delete(currentAuxGroups, id)
			}
		}
	}
	desiredAuxGroups := groupIDs(auxGroups)
	for id, group := range currentAuxGroups {
		if _, keep := desiredAuxGroups[id]; keep {
			continue
		}
		if err := system.RemoveUserFromGroup(user.Username, group.Name); err != nil {
			return failIntegration("auxiliary_group_update_failed", err)
		}
		applied = true
	}
	for id, group := range desiredAuxGroups {
		if _, already := currentAuxGroups[id]; already {
			continue
		}
		if err := system.AddUserToGroup(user.Username, group.Name); err != nil {
			return failIntegration("auxiliary_group_update_failed", err)
		}
		applied = true
	}

	if (uidChanged || homeChanged || permsChanged || primaryChanged || opts.SSHPublicKey != user.SSHPublicKey) &&
		opts.HomeDirectory != "/nonexistent" {
		if err := system.ChownHome(opts.HomeDirectory, opts.UID, primaryGroupName); err != nil {
			return failIntegration("home_ownership_update_failed", err)
		}
		applied = true
	}
	if opts.Password != "" {
		if err := system.SetUnixUserPassword(user.Username, opts.Password); err != nil {
			return failIntegration("unix_password_update_failed", err)
		}
		applied = true
	}
	if opts.DisablePassword && (!user.DisablePassword || opts.Password != "") {
		if err := system.DisableUnixUserPassword(user.Username); err != nil {
			return failIntegration("unix_password_disable_failed", err)
		}
		applied = true
	}
	if opts.Locked != user.Locked {
		if opts.Locked {
			err = system.LockUnixUser(user.Username)
		} else {
			err = system.UnlockUnixUser(user.Username)
		}
		if err != nil {
			return failIntegration("unix_user_lock_update_failed", err)
		}
		applied = true
	}
	if opts.DoasEnabled != user.DoasEnabled {
		if opts.DoasEnabled {
			err = addDoasPermFn(user.Username)
		} else {
			err = removeDoasPermFn(user.Username)
		}
		if err != nil {
			return failIntegration("doas_update_failed", err)
		}
		applied = true
	}
	switch sambaAction {
	case SambaActionUpsert:
		if err := editSambaUserFn(user.Username, opts.Password); err != nil {
			return failIntegration("samba_user_update_failed", err)
		}
		applied = true
	case SambaActionRemove:
		if sambaExists {
			if err := deleteSambaUserFn(user.Username); err != nil {
				return failIntegration("samba_user_delete_failed", err)
			}
			applied = true
		}
	}

	updates := map[string]any{
		"full_name":        opts.FullName,
		"email":            opts.Email,
		"admin":            opts.Admin,
		"uid":              opts.UID,
		"shell":            opts.Shell,
		"home_directory":   opts.HomeDirectory,
		"home_dir_perms":   opts.HomeDirPerms,
		"ssh_public_key":   opts.SSHPublicKey,
		"disable_password": opts.DisablePassword,
		"locked":           opts.Locked,
		"doas_enabled":     opts.DoasEnabled,
		"primary_group_id": primaryGroupID,
	}
	if hashedPassword != "" {
		updates["password"] = hashedPassword
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(user).Association("Groups").Replace(&desiredGroups); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if applied {
			return userIntegrationError("pam_user_update_partially_applied", err, true)
		}
		return userInternalError("pam_user_update_failed", err)
	}

	if opts.Password != "" {
		s.loginMu.Lock()
		delete(s.loginAttempts, user.Username)
		s.loginMu.Unlock()
	}
	return nil
}

func (s *Service) deletePamUser(user *models.User) error {
	if user.Username == "root" {
		return userConflictError("cannot_delete_root_user")
	}

	applied := false
	failIntegration := func(code string, cause error) error {
		return userIntegrationError(code, cause, applied)
	}

	unixUserExists, err := system.UnixUserExists(user.Username)
	if err != nil {
		return failIntegration("unix_user_lookup_failed", err)
	}
	if unixUserExists {
		info, err := system.GetUnixUserInfoFull(user.Username)
		if err != nil {
			return failIntegration("unix_user_lookup_failed", err)
		}
		if info.Username != user.Username || info.UID < 1000 || info.UID > 65533 {
			return userConflictError("protected_system_user")
		}
		if err := validatePAMHomePath(info.HomeDir); err != nil {
			return userConflictError("unsafe_home_directory")
		}
	}

	sambaExists := false
	if sambaToolsAvailableFn() {
		sambaExists, err = sambaUserExistsFn(user.Username)
		if err != nil {
			return failIntegration("samba_user_lookup_failed", err)
		}
	}

	if err := s.revokeUserTokens(s.DB, user.ID); err != nil {
		return userInternalError("session_revoke_failed", err)
	}

	if sambaExists {
		if err := deleteSambaUserFn(user.Username); err != nil {
			return failIntegration("samba_user_delete_failed", err)
		}
		applied = true
	}
	if err := removeDoasPermFn(user.Username); err != nil {
		return failIntegration("doas_cleanup_failed", err)
	}
	applied = true

	if unixUserExists {
		if err := system.DeleteUnixUser(user.Username, true); err != nil {
			return failIntegration("unix_user_delete_failed", err)
		}
		applied = true
	}

	if err := s.deleteUserDatabaseState(user.ID); err != nil {
		if applied {
			return userIntegrationError("pam_user_delete_partially_applied", err, true)
		}
		return userInternalError("user_delete_failed", err)
	}
	return nil
}

func (s *Service) deleteUserDatabaseState(userID uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.revokeUserTokens(tx, userID); err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&models.WebAuthnCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&models.WebAuthnChallenge{}).Error; err != nil {
			return err
		}
		user := models.User{ID: userID}
		if err := tx.Model(&user).Association("Groups").Clear(); err != nil {
			return err
		}
		if err := tx.Delete(&models.User{}, userID).Error; err != nil {
			return err
		}
		return nil
	})
}
