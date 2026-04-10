// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"fmt"

	"github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/logger"
)

type sambaPermissionIDs struct {
	ReadUserIDs   []uint
	WriteUserIDs  []uint
	ReadGroupIDs  []uint
	WriteGroupIDs []uint
}

type sambaPrincipalNames struct {
	ReadUsers   []string
	WriteUsers  []string
	ReadGroups  []string
	WriteGroups []string
}

var sambaWriteConfig = func(s *Service, ctx context.Context, reload bool) error {
	return s.WriteConfig(ctx, reload)
}

func uniqueUint(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))

	for _, id := range ids {
		if id == 0 {
			continue
		}

		if _, exists := seen[id]; exists {
			continue
		}

		seen[id] = struct{}{}
		out = append(out, id)
	}

	return out
}

func normalizeSambaPermissionIDs(
	readUserIDs []uint,
	writeUserIDs []uint,
	readGroupIDs []uint,
	writeGroupIDs []uint,
) sambaPermissionIDs {
	normalized := sambaPermissionIDs{
		ReadUserIDs:   uniqueUint(readUserIDs),
		WriteUserIDs:  uniqueUint(writeUserIDs),
		ReadGroupIDs:  uniqueUint(readGroupIDs),
		WriteGroupIDs: uniqueUint(writeGroupIDs),
	}

	writeUsers := make(map[uint]struct{}, len(normalized.WriteUserIDs))
	for _, id := range normalized.WriteUserIDs {
		writeUsers[id] = struct{}{}
	}

	filteredReadUsers := make([]uint, 0, len(normalized.ReadUserIDs))
	for _, id := range normalized.ReadUserIDs {
		if _, exists := writeUsers[id]; exists {
			continue
		}
		filteredReadUsers = append(filteredReadUsers, id)
	}
	normalized.ReadUserIDs = filteredReadUsers

	writeGroups := make(map[uint]struct{}, len(normalized.WriteGroupIDs))
	for _, id := range normalized.WriteGroupIDs {
		writeGroups[id] = struct{}{}
	}

	filteredReadGroups := make([]uint, 0, len(normalized.ReadGroupIDs))
	for _, id := range normalized.ReadGroupIDs {
		if _, exists := writeGroups[id]; exists {
			continue
		}
		filteredReadGroups = append(filteredReadGroups, id)
	}
	normalized.ReadGroupIDs = filteredReadGroups

	return normalized
}

func (ids sambaPermissionIDs) principalCount() int {
	return len(ids.ReadUserIDs) + len(ids.WriteUserIDs) + len(ids.ReadGroupIDs) + len(ids.WriteGroupIDs)
}

func collectMissingIDs(expected []uint, present map[uint]struct{}) []uint {
	missing := make([]uint, 0)

	for _, id := range expected {
		if _, exists := present[id]; exists {
			continue
		}
		missing = append(missing, id)
	}

	return missing
}

func usersByIDs(users []models.User) map[uint]models.User {
	byID := make(map[uint]models.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	return byID
}

func groupsByIDs(groups []models.Group) map[uint]models.Group {
	byID := make(map[uint]models.Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	return byID
}

func usersForIDs(ids []uint, byID map[uint]models.User) []models.User {
	out := make([]models.User, 0, len(ids))
	for _, id := range ids {
		if user, exists := byID[id]; exists {
			out = append(out, user)
		}
	}
	return out
}

func groupsForIDs(ids []uint, byID map[uint]models.Group) []models.Group {
	out := make([]models.Group, 0, len(ids))
	for _, id := range ids {
		if group, exists := byID[id]; exists {
			out = append(out, group)
		}
	}
	return out
}

func usernames(users []models.User) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Username)
	}
	return names
}

func groupNames(groups []models.Group) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.Name)
	}
	return names
}

func (s *Service) loadUsersAndGroupsByIDs(
	readUserIDs []uint,
	writeUserIDs []uint,
	readGroupIDs []uint,
	writeGroupIDs []uint,
) ([]models.User, []models.User, []models.Group, []models.Group, error) {
	allUserIDs := uniqueUint(append(append([]uint{}, readUserIDs...), writeUserIDs...))
	allGroupIDs := uniqueUint(append(append([]uint{}, readGroupIDs...), writeGroupIDs...))

	var users []models.User
	if len(allUserIDs) > 0 {
		if err := s.DB.Where("id IN ?", allUserIDs).Find(&users).Error; err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed_to_fetch_users: %w", err)
		}
	}

	foundUsers := make(map[uint]struct{}, len(users))
	for _, user := range users {
		foundUsers[user.ID] = struct{}{}
	}
	if missing := collectMissingIDs(allUserIDs, foundUsers); len(missing) > 0 {
		return nil, nil, nil, nil, fmt.Errorf("user_not_found: %d", missing[0])
	}

	var groups []models.Group
	if len(allGroupIDs) > 0 {
		if err := s.DB.Where("id IN ?", allGroupIDs).Find(&groups).Error; err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed_to_fetch_groups: %w", err)
		}
	}

	foundGroups := make(map[uint]struct{}, len(groups))
	for _, group := range groups {
		foundGroups[group.ID] = struct{}{}
	}
	if missing := collectMissingIDs(allGroupIDs, foundGroups); len(missing) > 0 {
		return nil, nil, nil, nil, fmt.Errorf("group_not_found: %d", missing[0])
	}

	usersByID := usersByIDs(users)
	groupsByID := groupsByIDs(groups)

	readUsers := usersForIDs(readUserIDs, usersByID)
	writeUsers := usersForIDs(writeUserIDs, usersByID)
	readGroups := groupsForIDs(readGroupIDs, groupsByID)
	writeGroups := groupsForIDs(writeGroupIDs, groupsByID)

	return readUsers, writeUsers, readGroups, writeGroups, nil
}

func namesFromShareAssociations(share sambaModels.SambaShare) sambaPrincipalNames {
	return sambaPrincipalNames{
		ReadUsers:   usernames(share.ReadOnlyUsers),
		WriteUsers:  usernames(share.WriteableUsers),
		ReadGroups:  groupNames(share.ReadOnlyGroups),
		WriteGroups: groupNames(share.WriteableGroups),
	}
}

func namesFromACLPrincipals(
	readUsers []models.User,
	writeUsers []models.User,
	readGroups []models.Group,
	writeGroups []models.Group,
) sambaPrincipalNames {
	return sambaPrincipalNames{
		ReadUsers:   usernames(readUsers),
		WriteUsers:  usernames(writeUsers),
		ReadGroups:  groupNames(readGroups),
		WriteGroups: groupNames(writeGroups),
	}
}

func (s *Service) GetShares() ([]sambaModels.SambaShare, error) {
	var shares []sambaModels.SambaShare
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		Find(&shares).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_shares: %w", err)
	}
	return shares, nil
}

func (s *Service) CreateShare(
	ctx context.Context,
	name string,
	dataset string,
	readUserIDs []uint,
	writeUserIDs []uint,
	readGroupIDs []uint,
	writeGroupIDs []uint,
	guestEnabled bool,
	guestWriteable bool,
	createMask string,
	directoryMask string,
	timeMachine bool,
	timeMachineMaxSize uint64,
) error {
	var nameConflictCount int64
	if err := s.DB.Model(&sambaModels.SambaShare{}).
		Where("name = ?", name).
		Count(&nameConflictCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_name_conflict: %w", err)
	}
	if nameConflictCount > 0 {
		return fmt.Errorf("share_with_name_exists")
	}

	var datasetConflictCount int64
	if err := s.DB.Model(&sambaModels.SambaShare{}).
		Where("dataset = ?", dataset).
		Count(&datasetConflictCount).Error; err != nil {
		return fmt.Errorf("failed_to_check_dataset_conflict: %w", err)
	}
	if datasetConflictCount > 0 {
		return fmt.Errorf("share_with_dataset_exists")
	}

	normalized := normalizeSambaPermissionIDs(readUserIDs, writeUserIDs, readGroupIDs, writeGroupIDs)

	if guestEnabled && normalized.principalCount() > 0 {
		return fmt.Errorf("guest_only_share_cannot_have_principals")
	}

	if !guestEnabled && normalized.principalCount() == 0 {
		return fmt.Errorf("no_principals_selected_and_guests_not_allowed")
	}

	fDataset, err := s.GZFS.ZFS.GetByGUID(ctx, dataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_fetch_dataset: %v", err)
	}

	if fDataset == nil {
		return fmt.Errorf("dataset_not_found")
	}

	if fDataset.Mountpoint == "" || fDataset.Mountpoint == "-" {
		return fmt.Errorf("dataset_not_mounted")
	}

	if err := s.ensureSambaDatasetACLProperties(ctx, fDataset, true); err != nil {
		return fmt.Errorf("failed_to_enforce_samba_dataset_acl_properties: %w", err)
	}

	readUsers, writeUsers, readGroups, writeGroups, err := s.loadUsersAndGroupsByIDs(
		normalized.ReadUserIDs,
		normalized.WriteUserIDs,
		normalized.ReadGroupIDs,
		normalized.WriteGroupIDs,
	)
	if err != nil {
		return err
	}

	desiredPrincipals := namesFromACLPrincipals(readUsers, writeUsers, readGroups, writeGroups)
	if !guestEnabled {
		if err := s.syncSambaDatasetPrincipalACLs(
			fDataset.Mountpoint,
			sambaPrincipalNames{},
			desiredPrincipals,
			true,
		); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_principal_acls: %w", err)
		}
	}

	if err := s.syncSambaDatasetGuestACL(
		fDataset.Mountpoint,
		guestEnabled,
		guestWriteable,
		true,
	); err != nil {
		return fmt.Errorf("failed_to_enforce_samba_dataset_guest_acl: %w", err)
	}

	share := sambaModels.SambaShare{
		Name:               name,
		Dataset:            dataset,
		CreateMask:         createMask,
		DirectoryMask:      directoryMask,
		GuestOk:            guestEnabled,
		ReadOnly:           !guestWriteable && guestEnabled,
		TimeMachine:        timeMachine,
		TimeMachineMaxSize: timeMachineMaxSize,
	}

	tx := s.DB.Begin()
	if err := tx.Create(&share).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_create_share: %w", err)
	}

	if len(readUsers) > 0 {
		if err := tx.Model(&share).Association("ReadOnlyUsers").Append(readUsers); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_read_only_users: %w", err)
		}
	}

	if len(writeUsers) > 0 {
		if err := tx.Model(&share).Association("WriteableUsers").Append(writeUsers); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_writeable_users: %w", err)
		}
	}

	if len(readGroups) > 0 {
		if err := tx.Model(&share).Association("ReadOnlyGroups").Append(readGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_read_only_groups: %w", err)
		}
	}

	if len(writeGroups) > 0 {
		if err := tx.Model(&share).Association("WriteableGroups").Append(writeGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_writeable_groups: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_transaction: %w", err)
	}

	return sambaWriteConfig(s, ctx, true)
}

func (s *Service) UpdateShare(
	ctx context.Context,
	id uint,
	name string,
	dataset string,
	readUserIDs []uint,
	writeUserIDs []uint,
	readGroupIDs []uint,
	writeGroupIDs []uint,
	guestEnabled bool,
	guestWriteable bool,
	createMask string,
	directoryMask string,
	timeMachine bool,
	timeMachineMaxSize uint64,
) error {
	var share sambaModels.SambaShare
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		First(&share, id).Error; err != nil {
		return fmt.Errorf("share_not_found: %w", err)
	}

	if name != share.Name {
		var count int64
		if err := s.DB.Model(&sambaModels.SambaShare{}).
			Where("name = ? AND id != ?", name, id).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_name_conflict: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("share_with_name_exists")
		}
	}

	if dataset != share.Dataset {
		var count int64
		if err := s.DB.Model(&sambaModels.SambaShare{}).
			Where("dataset = ? AND id != ?", dataset, id).
			Count(&count).Error; err != nil {
			return fmt.Errorf("failed_to_check_dataset_conflict: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("share_with_dataset_exists")
		}
	}

	normalized := normalizeSambaPermissionIDs(readUserIDs, writeUserIDs, readGroupIDs, writeGroupIDs)

	if guestEnabled && normalized.principalCount() > 0 {
		return fmt.Errorf("guest_only_share_cannot_have_principals")
	}

	if !guestEnabled && normalized.principalCount() == 0 {
		return fmt.Errorf("no_principals_selected_and_guests_not_allowed")
	}

	fDataset, err := s.GZFS.ZFS.GetByGUID(ctx, dataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_fetch_dataset: %v", err)
	}

	if fDataset == nil {
		return fmt.Errorf("dataset_not_found")
	}

	if fDataset.Mountpoint == "" || fDataset.Mountpoint == "-" {
		return fmt.Errorf("dataset_not_mounted")
	}

	if err := s.ensureSambaDatasetACLProperties(ctx, fDataset, true); err != nil {
		return fmt.Errorf("failed_to_enforce_samba_dataset_acl_properties: %w", err)
	}

	readUsers, writeUsers, readGroups, writeGroups, err := s.loadUsersAndGroupsByIDs(
		normalized.ReadUserIDs,
		normalized.WriteUserIDs,
		normalized.ReadGroupIDs,
		normalized.WriteGroupIDs,
	)
	if err != nil {
		return err
	}

	previousPrincipals := namesFromShareAssociations(share)
	desiredPrincipals := sambaPrincipalNames{}
	if !guestEnabled {
		desiredPrincipals = namesFromACLPrincipals(readUsers, writeUsers, readGroups, writeGroups)
	}

	if dataset == share.Dataset {
		if err := s.syncSambaDatasetPrincipalACLs(
			fDataset.Mountpoint,
			previousPrincipals,
			desiredPrincipals,
			true,
		); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_principal_acls: %w", err)
		}

		if err := s.syncSambaDatasetGuestACL(
			fDataset.Mountpoint,
			guestEnabled,
			guestWriteable,
			true,
		); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_guest_acl: %w", err)
		}
	} else {
		oldDataset, oldDatasetErr := s.GZFS.ZFS.GetByGUID(ctx, share.Dataset, false)
		if oldDatasetErr != nil {
			return fmt.Errorf("failed_to_fetch_previous_dataset: %v", oldDatasetErr)
		}

		if oldDataset != nil && oldDataset.Mountpoint != "" && oldDataset.Mountpoint != "-" {
			if err := s.syncSambaDatasetPrincipalACLs(
				oldDataset.Mountpoint,
				previousPrincipals,
				sambaPrincipalNames{},
				true,
			); err != nil {
				return fmt.Errorf("failed_to_cleanup_previous_samba_dataset_principal_acls: %w", err)
			}

			if err := s.syncSambaDatasetGuestACL(
				oldDataset.Mountpoint,
				false,
				false,
				true,
			); err != nil {
				return fmt.Errorf("failed_to_cleanup_previous_samba_dataset_guest_acl: %w", err)
			}
		}

		if err := s.syncSambaDatasetPrincipalACLs(
			fDataset.Mountpoint,
			sambaPrincipalNames{},
			desiredPrincipals,
			true,
		); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_principal_acls: %w", err)
		}

		if err := s.syncSambaDatasetGuestACL(
			fDataset.Mountpoint,
			guestEnabled,
			guestWriteable,
			true,
		); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_guest_acl: %w", err)
		}
	}

	tx := s.DB.Begin()

	if err := tx.Model(&share).Association("ReadOnlyUsers").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_read_only_users: %w", err)
	}

	if err := tx.Model(&share).Association("WriteableUsers").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_writeable_users: %w", err)
	}

	if err := tx.Model(&share).Association("ReadOnlyGroups").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_read_only_groups: %w", err)
	}

	if err := tx.Model(&share).Association("WriteableGroups").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_writeable_groups: %w", err)
	}

	share.Name = name
	share.Dataset = dataset
	share.CreateMask = createMask
	share.DirectoryMask = directoryMask
	share.GuestOk = guestEnabled
	share.ReadOnly = !guestWriteable && guestEnabled
	share.TimeMachine = timeMachine
	share.TimeMachineMaxSize = timeMachineMaxSize

	if err := tx.Save(&share).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_update_share_fields: %w", err)
	}

	if len(readUsers) > 0 {
		if err := tx.Model(&share).Association("ReadOnlyUsers").Append(readUsers); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_read_only_users: %w", err)
		}
	}

	if len(writeUsers) > 0 {
		if err := tx.Model(&share).Association("WriteableUsers").Append(writeUsers); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_writeable_users: %w", err)
		}
	}

	if len(readGroups) > 0 {
		if err := tx.Model(&share).Association("ReadOnlyGroups").Append(readGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_read_only_groups: %w", err)
		}
	}

	if len(writeGroups) > 0 {
		if err := tx.Model(&share).Association("WriteableGroups").Append(writeGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_writeable_groups: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_transaction: %w", err)
	}

	return sambaWriteConfig(s, ctx, true)
}

func (s *Service) DeleteShare(ctx context.Context, id uint) error {
	var share sambaModels.SambaShare
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		First(&share, id).Error; err != nil {
		return fmt.Errorf("share_not_found: %w", err)
	}

	previousPrincipals := namesFromShareAssociations(share)
	dataset, err := s.GZFS.ZFS.GetByGUID(ctx, share.Dataset, false)
	if err != nil {
		logger.L.Warn().Err(err).Int("share_id", share.ID).Msg("failed to fetch dataset while cleaning samba ACL principals")
	} else if dataset != nil && dataset.Mountpoint != "" && dataset.Mountpoint != "-" {
		_ = s.syncSambaDatasetPrincipalACLs(dataset.Mountpoint, previousPrincipals, sambaPrincipalNames{}, false)
		_ = s.syncSambaDatasetGuestACL(dataset.Mountpoint, false, false, false)
	}

	tx := s.DB.Begin()

	if err := tx.Model(&share).Association("ReadOnlyUsers").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_read_only_users: %w", err)
	}

	if err := tx.Model(&share).Association("WriteableUsers").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_writeable_users: %w", err)
	}

	if err := tx.Model(&share).Association("ReadOnlyGroups").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_read_only_groups: %w", err)
	}

	if err := tx.Model(&share).Association("WriteableGroups").Clear(); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_clear_writeable_groups: %w", err)
	}

	if err := tx.Delete(&share).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_delete_share: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_transaction: %w", err)
	}

	return sambaWriteConfig(s, ctx, true)
}
