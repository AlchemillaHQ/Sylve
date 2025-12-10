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
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetShares() ([]sambaModels.SambaShare, error) {
	var shares []sambaModels.SambaShare
	if err := s.DB.Preload("ReadOnlyGroups").Preload("WriteableGroups").Find(&shares).Error; err != nil {
		return nil, fmt.Errorf("failed_to_get_shares: %w", err)
	}
	return shares, nil
}

func (s *Service) CreateShare(
	ctx context.Context,
	name string,
	dataset string,
	readOnlyGroups []string,
	writeableGroups []string,
	createMask string,
	directoryMask string,
	guestOk bool,
	readOnly bool) error {
	if err := s.DB.Where("name = ?", name).First(&sambaModels.SambaShare{}).Error; err == nil {
		return fmt.Errorf("share_with_name_exists")
	}

	if len(readOnlyGroups) > 0 && readOnly {
		return fmt.Errorf("cannot_create_read_only_share_with_read_only_groups")
	}

	fDataset, err := s.GZFS.ZFS.GetByGUID(ctx, dataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_fetch_dataset: %v", err)
	}

	if fDataset == nil {
		return fmt.Errorf("dataset_not_found")
	}

	if fDataset.Mountpoint == "" {
		return fmt.Errorf("dataset_not_mounted")
	}

	allGroups := utils.JoinStringSlices(readOnlyGroups, writeableGroups)

	if len(allGroups) == 0 && !guestOk {
		return fmt.Errorf("no_groups_selected_and_guests_not_allowed")
	}

	for _, group := range allGroups {
		if err := s.DB.Where("name = ?", group).First(&models.Group{}).Error; err != nil {
			return fmt.Errorf("group_not_found: %s", group)
		}
	}

	var roGroups []models.Group
	var wrGroups []models.Group

	for _, group := range readOnlyGroups {
		var g models.Group
		if err := s.DB.Where("name = ?", group).First(&g).Error; err != nil {
			return fmt.Errorf("read_only_group_not_found: %s", group)
		}
		roGroups = append(roGroups, g)
	}

	for _, group := range writeableGroups {
		var g models.Group
		if err := s.DB.Where("name = ?", group).First(&g).Error; err != nil {
			return fmt.Errorf("writeable_group_not_found: %s", group)
		}
		wrGroups = append(wrGroups, g)
	}

	share := sambaModels.SambaShare{
		Name:            name,
		Dataset:         dataset,
		ReadOnlyGroups:  roGroups,
		WriteableGroups: wrGroups,
		CreateMask:      createMask,
		DirectoryMask:   directoryMask,
		GuestOk:         guestOk,
		ReadOnly:        readOnly,
	}

	if err := s.DB.Create(&share).Error; err != nil {
		return fmt.Errorf("failed_to_create_share: %w", err)
	}

	return s.WriteConfig(ctx, true)
}

func (s *Service) UpdateShare(
	ctx context.Context,
	id uint,
	name string,
	dataset string,
	readOnlyGroups []string,
	writeableGroups []string,
	createMask string,
	directoryMask string,
	guestOk bool,
	readOnly bool,
) error {
	var share sambaModels.SambaShare
	if err := s.DB.Preload("ReadOnlyGroups").Preload("WriteableGroups").First(&share, id).Error; err != nil {
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

	if len(readOnlyGroups) > 0 && readOnly {
		return fmt.Errorf("cannot_create_read_only_share_with_read_only_groups")
	}

	fDataset, err := s.GZFS.ZFS.GetByGUID(ctx, dataset, false)
	if err != nil {
		return fmt.Errorf("failed_to_fetch_dataset: %v", err)
	}

	if fDataset == nil {
		return fmt.Errorf("dataset_not_found")
	}

	if fDataset.Mountpoint == "" {
		return fmt.Errorf("dataset_not_mounted")
	}

	allGroups := utils.JoinStringSlices(readOnlyGroups, writeableGroups)

	if len(allGroups) == 0 && !guestOk {
		return fmt.Errorf("no_groups_selected_and_guests_not_allowed")
	}

	for _, group := range allGroups {
		if err := s.DB.Where("name = ?", group).First(&models.Group{}).Error; err != nil {
			return fmt.Errorf("group_not_found: %s", group)
		}
	}

	var roGroups, wrGroups []models.Group

	for _, gname := range readOnlyGroups {
		var g models.Group
		if err := s.DB.Where("name = ?", gname).First(&g).Error; err != nil {
			return fmt.Errorf("read_only_group_not_found: %s", gname)
		}
		roGroups = append(roGroups, g)
	}

	for _, gname := range writeableGroups {
		var g models.Group
		if err := s.DB.Where("name = ?", gname).First(&g).Error; err != nil {
			return fmt.Errorf("writeable_group_not_found: %s", gname)
		}
		wrGroups = append(wrGroups, g)
	}

	tx := s.DB.Begin()

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
	share.GuestOk = guestOk
	share.ReadOnly = readOnly

	if err := tx.Save(&share).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed_to_update_share_fields: %w", err)
	}

	if len(roGroups) > 0 {
		if err := tx.Model(&share).Association("ReadOnlyGroups").Append(roGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_read_only_groups: %w", err)
		}
	}
	if len(wrGroups) > 0 {
		if err := tx.Model(&share).Association("WriteableGroups").Append(wrGroups); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed_to_append_writeable_groups: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_transaction: %w", err)
	}

	return s.WriteConfig(ctx, true)
}

func (s *Service) DeleteShare(ctx context.Context, id uint) error {
	var share sambaModels.SambaShare
	if err := s.DB.Where("id = ?", id).First(&share).Error; err != nil {
		return fmt.Errorf("share_not_found: %w", err)
	}

	if err := s.DB.Delete(&share).Error; err != nil {
		return fmt.Errorf("failed_to_delete_share: %w", err)
	}

	return s.WriteConfig(ctx, true)
}
