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

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

func (s *Service) ListGroups() ([]models.Group, error) {
	var groups []models.Group
	if err := s.DB.Preload("Users").Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("failed_to_list_groups: %w", err)
	}
	return groups, nil
}

func (s *Service) CreateGroup(name string, members []string) error {
	valid := utils.IsValidGroupName(name)

	if !valid {
		return fmt.Errorf("invalid_group_name: %s", name)
	}

	exists := system.UnixGroupExists(name)

	if exists {
		return fmt.Errorf("group_already_exists: %s", name)
	}

	var users []models.User
	for _, member := range members {
		var user models.User
		if err := s.DB.Where("username = ?", member).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user_not_found: %s", member)
			}
			return fmt.Errorf("failed_to_check_user: %w", err)
		}
		users = append(users, user)
	}

	group := models.Group{
		Name:  name,
		Users: users,
	}

	if err := s.DB.Create(&group).Error; err != nil {
		return fmt.Errorf("failed_to_create_group: %w", err)
	}

	if err := system.CreateUnixGroup(name); err != nil {
		s.DB.Delete(&group)
		return fmt.Errorf("failed_to_create_unix_group: %w", err)
	}

	for _, member := range members {
		if err := system.AddUserToGroup(member, name); err != nil {
			s.DB.Delete(&group)
			system.DeleteUnixGroup(name)
			return fmt.Errorf("failed_to_add_user_to_unix_group: %w", err)
		}
	}

	return nil
}

func (s *Service) DeleteGroup(id uint) error {
	var group models.Group
	if err := s.DB.First(&group, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("group_not_found: %d", id)
		}
		return fmt.Errorf("failed_to_find_group: %w", err)
	}

	if err := s.DB.Delete(&group).Error; err != nil {
		return fmt.Errorf("failed_to_delete_group: %w", err)
	}

	if err := system.DeleteUnixGroup(group.Name); err != nil {
		return fmt.Errorf("failed_to_delete_unix_group: %w", err)
	}

	return nil
}

func (s *Service) AddUsersToGroup(usernames []string, groupName string) error {
	var group models.Group
	if err := s.DB.Where("name = ?", groupName).First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("group_not_found: %s", groupName)
		}
		return fmt.Errorf("failed_to_find_group: %w", err)
	}

	for _, username := range usernames {
		var user models.User
		if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user_not_found: %s", username)
			}
			return fmt.Errorf("failed_to_find_user: %w", err)
		}

		var cnt int64
		if err := s.DB.
			Table("user_groups").
			Where("user_id = ? AND group_id = ?", user.ID, group.ID).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("failed_to_check_membership: %w", err)
		}

		if cnt > 0 {
			continue
		}

		if err := s.DB.Model(&group).Association("Users").Append(&user); err != nil {
			return fmt.Errorf("failed_to_add_user_to_group: %w", err)
		}

		inGroup, err := system.IsUserInGroup(user.Username, groupName)
		if err != nil {
			return err
		}
		if !inGroup {
			if err := system.AddUserToGroup(user.Username, groupName); err != nil {
				s.DB.Model(&group).Association("Users").Delete(&user)
				return fmt.Errorf("failed_to_add_user_to_unix_group: %w", err)
			}
		}
	}

	return nil
}
