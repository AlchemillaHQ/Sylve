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
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) FindBaseByUUID(uuid string) (string, error) {
	if uuid == "" {
		return "", fmt.Errorf("base_download_uuid_required")
	}

	// First try the new approach: look for the file directly in jail_templates directory
	jailTemplatesDir := config.GetDownloadsPath("jail_templates")

	// Try to find the file by UUID (which might be a filename in the new system)
	entries, err := os.ReadDir(jailTemplatesDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			// Check if the filename matches the UUID or if the UUID is derived from the path
			if entry.Name() == uuid || strings.Contains(entry.Name(), uuid) {
				bPath := filepath.Join(jailTemplatesDir, entry.Name())
				if _, err := os.Stat(bPath); err == nil {
					return bPath, nil
				}
			}
		}
	}

	// Fallback to the old database approach for backward compatibility
	var download utilitiesModels.Downloads
	if err := s.DB.
		Preload("Files").
		Where("uuid = ?", uuid).
		First(&download).Error; err != nil {
		// If database lookup fails, try to find by treating uuid as filename
		bPath := filepath.Join(jailTemplatesDir, uuid)
		if _, err := os.Stat(bPath); err == nil {
			return bPath, nil
		}
		return "", fmt.Errorf("failed_to_find_download: %w", err)
	}

	var bPath string

	switch download.Type {
	case "http":
		// Try new path first
		bPath = filepath.Join(jailTemplatesDir, download.Name)
		if _, err := os.Stat(bPath); os.IsNotExist(err) {
			// Fallback to old path
			downloadsDir := config.GetDownloadsPath("http")
			extractsDir := config.GetDownloadsPath("extracted")
			bPath = filepath.Join(downloadsDir, download.Name)

			if strings.HasSuffix(bPath, ".txz") {
				bPath = filepath.Join(extractsDir, download.UUID)
			}
		}
	case "torrent":
		// Try new path first
		bPath = filepath.Join(jailTemplatesDir, download.Name)
		if _, err := os.Stat(bPath); os.IsNotExist(err) {
			// Fallback to old path
			torrentsDir := config.GetDownloadsPath("torrents")
			for _, file := range download.Files {
				if strings.HasSuffix(file.Name, ".txz") {
					bPath = filepath.Join(torrentsDir, uuid, file.Name)
				}
			}
		}
	case "jail_templates":
		// New download type
		bPath = filepath.Join(jailTemplatesDir, download.Name)
	}

	if bPath == "" {
		return "", fmt.Errorf("base_file_not_found_in_download: %s", uuid)
	}

	if _, err := os.Stat(bPath); os.IsNotExist(err) {
		return "", fmt.Errorf("base_file_not_found: %s", bPath)
	}

	return bPath, nil
}

func (s *Service) ExtractBase(mountPoint, baseTxz string) (string, error) {
	args := []string{"-C", mountPoint, "-xf", baseTxz}
	return utils.RunCommand("tar", args...)
}

func (s *Service) DoesPathHaveBase(root string) (bool, error) {
	if root == "" {
		return false, fmt.Errorf("path_required")
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("path_does_not_exist: %s", root)
		}
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("not_a_directory: %s", root)
	}

	required := []string{
		"bin/freebsd-version",
		"bin/sh",
		"libexec/ld-elf.so.1",
		"lib/libc.so.7",
		"etc/os-release",
	}

	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false, nil
		}
	}

	return true, nil
}
