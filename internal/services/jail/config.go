// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

func (s *Service) GetJailConfig(ctid uint) (string, error) {
	if ctid == 0 {
		return "", fmt.Errorf("invalid_ct_id")
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctid))

	config, err := os.ReadFile(jailConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed_to_read_jail_config: %w", err)
	}

	return string(config), nil
}

func (s *Service) SaveJailConfig(ctid uint, cfg string) error {
	if ctid == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	re := regexp.MustCompile(`\n{2,}`)
	cfg = re.ReplaceAllString(cfg, "\n")

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_directory: %w", err)
	}

	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctid))
	if err := os.WriteFile(jailConfigPath, []byte(cfg), 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_config: %w", err)
	}

	return nil
}

func (s *Service) AppendToConfig(ctid uint, current string, toAppend string) (string, error) {
	lastCurly := strings.LastIndex(current, "}")
	if lastCurly == -1 {
		return "", fmt.Errorf("invalid_config_format")
	}

	newConfig := current[:lastCurly] + toAppend + "\n" + current[lastCurly:]

	return newConfig, nil
}

func (s *Service) GetJailMountPoint(ctid uint) (string, error) {
	var jail jailModels.Jail

	err := s.DB.Where("ct_id = ?", ctid).First(&jail).Error
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jail: %w", err)
	}

	var dataset *zfs.Dataset

	datasets, err := zfs.Datasets("")
	if err != nil {
		return "", fmt.Errorf("failed_to_get_datasets: %w", err)
	}

	for _, ds := range datasets {
		guid, err := ds.GetProperty("guid")
		if err != nil {
			return "", fmt.Errorf("failed_to_get_dataset_guid: %w", err)
		}

		if guid == jail.Dataset {
			dataset = ds
			break
		}
	}

	if dataset == nil {
		return "", fmt.Errorf("failed_to_find_jail_dataset")
	}

	mountPoint, err := dataset.GetProperty("mountpoint")
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jail_mountpoint: %w", err)
	}

	return mountPoint, nil
}
