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
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

const (
	wheelGroupName = "wheel"
	sylveGroupName = "sylve_g"
)

type membershipChange struct {
	username string
	added    bool
}

func (s *Service) ListGroups() ([]models.Group, error) {
	var groups []models.Group
	if err := s.DB.Preload("Users").Order("name ASC").Find(&groups).Error; err != nil {
		return nil, groupInternalError("group_list_failed", err)
	}
	return groups, nil
}

func validateRequestedGroupMembers(members []string, requireMembers bool) ([]string, error) {
	if requireMembers && len(members) == 0 {
		return nil, groupValidationError("group_members_required")
	}

	result := make([]string, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, raw := range members {
		username := strings.TrimSpace(raw)
		if username == "" || username != raw {
			return nil, groupValidationError("invalid_group_member")
		}
		if _, exists := seen[username]; exists {
			return nil, groupValidationError("duplicate_group_member")
		}
		seen[username] = struct{}{}
		result = append(result, username)
	}
	return result, nil
}

func (s *Service) loadRequestedPAMUsers(usernames []string) ([]models.User, error) {
	if len(usernames) == 0 {
		return []models.User{}, nil
	}

	var found []models.User
	if err := s.DB.Where("username IN ?", usernames).Find(&found).Error; err != nil {
		return nil, groupInternalError("group_member_lookup_failed", err)
	}

	byUsername := make(map[string]models.User, len(found))
	for _, user := range found {
		byUsername[user.Username] = user
	}

	ordered := make([]models.User, 0, len(usernames))
	for _, username := range usernames {
		user, exists := byUsername[username]
		if !exists {
			return nil, groupNotFoundError("group_member_not_found")
		}
		if user.Source != "pam" {
			return nil, groupConflictError("group_members_require_pam_users")
		}
		ordered = append(ordered, user)
	}

	return ordered, nil
}

func ensureUnixUsersExist(users []models.User) error {
	for _, user := range users {
		exists, err := system.UnixUserExists(user.Username)
		if err != nil {
			return groupDependencyError("unix_user_lookup_failed", err)
		}
		if !exists {
			return groupConflictError("unix_user_does_not_exist")
		}
	}
	return nil
}

func (s *Service) CreateGroup(name string, members []string) (models.Group, error) {
	name = strings.TrimSpace(name)
	if !utils.IsValidGroupName(name) {
		return models.Group{}, groupValidationError("invalid_group_name")
	}

	members, err := validateRequestedGroupMembers(members, true)
	if err != nil {
		return models.Group{}, err
	}

	var count int64
	if err := s.DB.Model(&models.Group{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return models.Group{}, groupInternalError("group_lookup_failed", err)
	}
	if count > 0 {
		return models.Group{}, groupConflictError("group_already_exists")
	}

	exists, err := system.UnixGroupExistsWithError(name)
	if err != nil {
		return models.Group{}, groupDependencyError("unix_group_lookup_failed", err)
	}
	if exists {
		return models.Group{}, groupConflictError("group_already_exists")
	}

	users, err := s.loadRequestedPAMUsers(members)
	if err != nil {
		return models.Group{}, err
	}
	if err := ensureUnixUsersExist(users); err != nil {
		return models.Group{}, err
	}

	if err := system.CreateUnixGroup(name); err != nil {
		return models.Group{}, groupDependencyError("unix_group_create_failed", err)
	}

	rollbackCreatedGroup := func(cause error) error {
		if rollbackErr := system.DeleteUnixGroup(name); rollbackErr != nil {
			return groupPartialError("group_create_partially_applied", errors.Join(cause, rollbackErr))
		}
		return cause
	}

	for _, user := range users {
		if err := system.AddUserToGroup(user.Username, name); err != nil {
			return models.Group{}, rollbackCreatedGroup(
				groupDependencyError("unix_group_membership_update_failed", err),
			)
		}
	}

	group := models.Group{Name: name}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		if err := tx.Model(&group).Association("Users").Replace(&users); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		code := "group_record_create_failed"
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			code = "group_already_exists"
		}
		cleanupErr := system.DeleteUnixGroup(name)
		if cleanupErr != nil {
			return models.Group{}, groupPartialError("group_create_partially_applied", errors.Join(err, cleanupErr))
		}
		if code == "group_already_exists" {
			return models.Group{}, groupConflictError(code)
		}
		return models.Group{}, groupInternalError(code, err)
	}

	group.Users = users
	return group, nil
}

func (s *Service) groupHasSambaReferences(groupID uint) (bool, error) {
	for _, table := range []string{
		"samba_share_read_only_groups",
		"samba_share_writeable_groups",
	} {
		if !s.DB.Migrator().HasTable(table) {
			continue
		}
		var count int64
		if err := s.DB.Table(table).Where("group_id = ?", groupID).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) DeleteGroup(id uint) (models.Group, error) {
	var group models.Group
	if err := s.DB.Preload("Users").First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Group{}, groupNotFoundError("group_not_found")
		}
		return models.Group{}, groupInternalError("group_lookup_failed", err)
	}

	if group.Name == wheelGroupName || group.Name == sylveGroupName {
		return models.Group{}, groupConflictError("protected_group_cannot_be_deleted")
	}

	var primaryUsers int64
	if err := s.DB.Model(&models.User{}).Where("primary_group_id = ?", group.ID).Count(&primaryUsers).Error; err != nil {
		return models.Group{}, groupInternalError("group_reference_check_failed", err)
	}
	if primaryUsers > 0 {
		return models.Group{}, groupConflictError("group_is_primary_for_users")
	}

	referencedBySamba, err := s.groupHasSambaReferences(group.ID)
	if err != nil {
		return models.Group{}, groupInternalError("group_reference_check_failed", err)
	}
	if referencedBySamba {
		return models.Group{}, groupConflictError("group_in_use_by_samba_share")
	}

	exists, err := system.UnixGroupExistsWithError(group.Name)
	if err != nil {
		return models.Group{}, groupDependencyError("unix_group_lookup_failed", err)
	}
	unixDeleted := false
	if exists {
		if err := system.DeleteUnixGroup(group.Name); err != nil {
			return models.Group{}, groupDependencyError("unix_group_delete_failed", err)
		}
		unixDeleted = true
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&group).Association("Users").Clear(); err != nil {
			return err
		}
		return tx.Delete(&group).Error
	})
	if err != nil {
		if unixDeleted {
			return models.Group{}, groupPartialError("group_delete_partially_applied", err)
		}
		return models.Group{}, groupInternalError("group_record_delete_failed", err)
	}

	return group, nil
}

func requiredGroupMember(group models.Group, user models.User) bool {
	if user.Source != "pam" {
		return false
	}
	if group.Name == wheelGroupName && user.Username == "root" {
		return true
	}
	if user.PrimaryGroupID != nil && *user.PrimaryGroupID == group.ID {
		return true
	}
	return group.Name == sylveGroupName && user.PrimaryGroupID == nil
}

func rollbackMembershipChanges(groupName string, changes []membershipChange) error {
	var rollbackErrors []error
	for index := len(changes) - 1; index >= 0; index-- {
		change := changes[index]
		var err error
		if change.added {
			err = system.RemoveUserFromGroup(change.username, groupName)
		} else {
			err = system.AddUserToGroup(change.username, groupName)
		}
		if err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func (s *Service) SyncGroupMembers(id uint, usernames []string) (models.Group, error) {
	usernames, err := validateRequestedGroupMembers(usernames, false)
	if err != nil {
		return models.Group{}, err
	}

	var group models.Group
	if err := s.DB.Preload("Users").First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.Group{}, groupNotFoundError("group_not_found")
		}
		return models.Group{}, groupInternalError("group_lookup_failed", err)
	}

	targetUsers, err := s.loadRequestedPAMUsers(usernames)
	if err != nil {
		return models.Group{}, err
	}

	desired := make(map[string]struct{}, len(targetUsers))
	unixUsers := make(map[string]models.User, len(group.Users)+len(targetUsers))
	for _, user := range targetUsers {
		desired[user.Username] = struct{}{}
		unixUsers[user.Username] = user
	}
	if group.Name == wheelGroupName {
		if _, hasRoot := desired["root"]; !hasRoot {
			return models.Group{}, groupConflictError("cannot_remove_root_from_wheel")
		}
	}
	for _, user := range group.Users {
		if _, keep := desired[user.Username]; !keep && requiredGroupMember(group, user) {
			if group.Name == wheelGroupName && user.Username == "root" {
				return models.Group{}, groupConflictError("cannot_remove_root_from_wheel")
			}
			return models.Group{}, groupConflictError("cannot_remove_primary_group_member")
		}
		if user.Source == "pam" {
			unixUsers[user.Username] = user
		}
	}

	exists, err := system.UnixGroupExistsWithError(group.Name)
	if err != nil {
		return models.Group{}, groupDependencyError("unix_group_lookup_failed", err)
	}
	if !exists {
		return models.Group{}, groupConflictError("unix_group_does_not_exist")
	}

	orderedUsernames := make([]string, 0, len(unixUsers))
	for username := range unixUsers {
		orderedUsernames = append(orderedUsernames, username)
	}
	sort.Strings(orderedUsernames)

	actual := make(map[string]bool, len(orderedUsernames))
	for _, username := range orderedUsernames {
		user := unixUsers[username]
		if err := ensureUnixUsersExist([]models.User{user}); err != nil {
			return models.Group{}, err
		}
		inGroup, err := system.IsUserInGroup(username, group.Name)
		if err != nil {
			return models.Group{}, groupDependencyError("unix_group_membership_lookup_failed", err)
		}
		actual[username] = inGroup
	}

	changes := make([]membershipChange, 0)
	for _, username := range orderedUsernames {
		_, shouldBeMember := desired[username]
		if actual[username] == shouldBeMember {
			continue
		}

		if shouldBeMember {
			err = system.AddUserToGroup(username, group.Name)
		} else {
			err = system.RemoveUserFromGroup(username, group.Name)
		}
		if err != nil {
			rollbackErr := rollbackMembershipChanges(group.Name, changes)
			if rollbackErr != nil {
				return models.Group{}, groupPartialError(
					"group_membership_partially_applied",
					errors.Join(err, rollbackErr),
				)
			}
			return models.Group{}, groupDependencyError("unix_group_membership_update_failed", err)
		}
		changes = append(changes, membershipChange{username: username, added: shouldBeMember})
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		return tx.Model(&group).Association("Users").Replace(&targetUsers)
	})
	if err != nil {
		rollbackErr := rollbackMembershipChanges(group.Name, changes)
		if rollbackErr != nil {
			return models.Group{}, groupPartialError(
				"group_membership_partially_applied",
				errors.Join(err, rollbackErr),
			)
		}
		return models.Group{}, groupInternalError("group_membership_record_update_failed", err)
	}

	group.Users = targetUsers
	return group, nil
}
