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
	"slices"

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

// DisableMissingShares reconciles enabled shares with ZFS before Samba's
// configuration is generated. A lookup error is not treated as proof that a
// dataset was deleted; only a successful lookup returning nil disables a share.
func (s *Service) DisableMissingShares(ctx context.Context) error {
	var shares []sambaModels.SambaShare
	if err := s.DB.Where("enabled = ?", true).Find(&shares).Error; err != nil {
		return fmt.Errorf("failed_to_get_enabled_shares: %w", err)
	}

	for _, share := range shares {
		dataset, err := s.GZFS.ZFS.GetByGUID(ctx, share.Dataset, false)
		if err != nil {
			return fmt.Errorf("failed_to_fetch_dataset_for_share_%s: %w", share.Name, err)
		}
		if dataset != nil {
			continue
		}

		if err := s.DB.Model(&sambaModels.SambaShare{}).
			Where("id = ? AND enabled = ?", share.ID, true).
			Update("enabled", false).Error; err != nil {
			return fmt.Errorf("failed_to_disable_missing_share_%s: %w", share.Name, err)
		}

		logger.L.Warn().
			Str("share", share.Name).
			Str("dataset_guid", share.Dataset).
			Msg("disabled Samba share because its dataset no longer exists")
	}

	return nil
}

// DisableSharesForDatasets is called after managed ZFS deletion paths have
// successfully destroyed datasets.
func (s *Service) DisableSharesForDatasets(ctx context.Context, guids []string) error {
	seen := make(map[string]struct{}, len(guids))
	uniqueGUIDs := make([]string, 0, len(guids))
	for _, guid := range guids {
		if guid == "" {
			continue
		}
		if _, exists := seen[guid]; exists {
			continue
		}
		seen[guid] = struct{}{}
		uniqueGUIDs = append(uniqueGUIDs, guid)
	}
	guids = uniqueGUIDs
	if len(guids) == 0 {
		return nil
	}

	var shares []sambaModels.SambaShare
	if err := s.DB.Where("enabled = ? AND dataset IN ?", true, guids).Find(&shares).Error; err != nil {
		return fmt.Errorf("failed_to_get_shares_for_deleted_datasets: %w", err)
	}
	if len(shares) == 0 {
		return nil
	}

	ids := make([]int, 0, len(shares))
	for _, share := range shares {
		ids = append(ids, share.ID)
	}
	if err := s.DB.Model(&sambaModels.SambaShare{}).
		Where("id IN ?", ids).
		Update("enabled", false).Error; err != nil {
		return fmt.Errorf("failed_to_disable_shares_for_deleted_datasets: %w", err)
	}

	for _, share := range shares {
		logger.L.Warn().Str("share", share.Name).Str("dataset_guid", share.Dataset).
			Msg("disabled Samba share after its dataset was deleted")
	}

	var settings models.BasicSettings
	if err := s.DB.First(&settings).Error; err != nil {
		return fmt.Errorf("shares_disabled_but_failed_to_get_service_settings: %w", err)
	}
	if !slices.Contains(settings.Services, models.SambaServer) {
		return nil
	}

	if err := sambaWriteConfig(s, ctx, true); err != nil {
		return fmt.Errorf("shares_disabled_but_failed_to_write_samba_config: %w", err)
	}
	return nil
}

func (s *Service) SetShareEnabled(ctx context.Context, id uint, enabled bool) error {
	var share sambaModels.SambaShare
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		First(&share, id).Error; err != nil {
		return fmt.Errorf("share_not_found: %w", err)
	}
	if share.Enabled == enabled {
		return nil
	}

	if enabled {
		dataset, err := s.GZFS.ZFS.GetByGUID(ctx, share.Dataset, false)
		if err != nil {
			return fmt.Errorf("failed_to_fetch_dataset: %v", err)
		}
		if dataset == nil {
			return fmt.Errorf("dataset_not_found")
		}
		if dataset.Mountpoint == "" || dataset.Mountpoint == "-" {
			return fmt.Errorf("dataset_not_mounted")
		}
		if err := s.ensureSambaDatasetACLProperties(ctx, dataset, true); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_acl_properties: %w", err)
		}

		principals := namesFromShareAssociations(share)
		if share.GuestOk {
			if err := s.syncSambaDatasetGuestACL(dataset.Mountpoint, true, !share.ReadOnly, true); err != nil {
				return fmt.Errorf("failed_to_enforce_samba_dataset_guest_acl: %w", err)
			}
		} else if err := s.syncSambaDatasetPrincipalACLs(dataset.Mountpoint, sambaPrincipalNames{}, principals, true); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_principal_acls: %w", err)
		}
	}

	if err := s.DB.Model(&share).Update("enabled", enabled).Error; err != nil {
		return fmt.Errorf("failed_to_update_share_enabled: %w", err)
	}
	return sambaWriteConfig(s, ctx, true)
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
	auditEnabled bool,
	auditRetentionDays uint32,
	auditedOperations []string,
	enabled bool,
) error {
	if err := validateSambaShareInput(name, createMask, directoryMask, auditedOperations); err != nil {
		return err
	}

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
		AuditEnabled:       auditEnabled,
		AuditRetentionDays: sambaModels.AuditRetentionDaysPointer(auditRetentionDays),
		AuditedOperations:  auditedOperations,
		Enabled:            true,
	}

	tx := s.DB.Begin()
	if err := tx.Create(&share).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_create_share: %w", err)
	}

	if !enabled {
		if err := tx.Model(&share).Update("enabled", false).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_set_initial_share_enabled_state: %w", err)
		}
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
	auditEnabled bool,
	auditRetentionDays uint32,
	auditedOperations []string,
	enabled *bool,
) error {
	if err := validateSambaShareInput(name, createMask, directoryMask, auditedOperations); err != nil {
		return err
	}

	var share sambaModels.SambaShare
	if err := s.DB.
		Preload("ReadOnlyUsers").
		Preload("WriteableUsers").
		Preload("ReadOnlyGroups").
		Preload("WriteableGroups").
		First(&share, id).Error; err != nil {
		return fmt.Errorf("share_not_found: %w", err)
	}

	desiredEnabled := share.Enabled
	if enabled != nil {
		desiredEnabled = *enabled
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

	allowUnavailableDataset := !desiredEnabled && dataset == share.Dataset
	fDataset, err := s.GZFS.ZFS.GetByGUID(ctx, dataset, false)
	if err != nil && !allowUnavailableDataset {
		return fmt.Errorf("failed_to_fetch_dataset: %v", err)
	}
	if err != nil {
		fDataset = nil
	}

	if fDataset == nil && !allowUnavailableDataset {
		return fmt.Errorf("dataset_not_found")
	}
	if fDataset != nil && (fDataset.Mountpoint == "" || fDataset.Mountpoint == "-") {
		if !allowUnavailableDataset {
			return fmt.Errorf("dataset_not_mounted")
		}
		fDataset = nil
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

	if fDataset != nil {
		if err := s.ensureSambaDatasetACLProperties(ctx, fDataset, true); err != nil {
			return fmt.Errorf("failed_to_enforce_samba_dataset_acl_properties: %w", err)
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
	share.AuditEnabled = auditEnabled
	share.AuditRetentionDays = sambaModels.AuditRetentionDaysPointer(auditRetentionDays)
	share.AuditedOperations = auditedOperations
	share.Enabled = desiredEnabled

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

	if err := sambaWriteConfig(s, ctx, true); err != nil {
		return err
	}

	if s.auditDB().Migrator().HasTable(&sambaModels.SambaAuditLog{}) {
		if err := s.auditDB().Model(&sambaModels.SambaAuditLog{}).
			Where("share_id = ?", share.ID).
			Update("retention_days", auditRetentionDays).Error; err != nil {
			logger.L.Warn().Err(err).Uint("share_id", uint(share.ID)).Msg("failed to update retention on existing Samba audit records")
		}
	}

	return nil
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
