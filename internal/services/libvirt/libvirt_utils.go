// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
)

func (s *Service) FindISOByUUID(uuid string, includeImg bool) (string, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.
		Preload("Files").
		Where("uuid = ?", uuid).
		First(&download).Error; err != nil {
		return "", fmt.Errorf("failed_to_find_download: %w", err)
	}

	hasAllowedExt := func(p string) bool {
		if p == "" {
			return false
		}
		l := strings.ToLower(p)
		return strings.HasSuffix(l, ".iso") || (includeImg && strings.HasSuffix(l, ".img"))
	}

	fileExists := func(p string) bool {
		if p == "" {
			return false
		}
		fi, err := os.Stat(p)
		return err == nil && !fi.IsDir()
	}

	switch download.Type {
	case "http":
		downloadsDir := config.GetDownloadsPath("http")
		isoPath := filepath.Join(downloadsDir, download.Name)

		mainExists := fileExists(isoPath)
		extractExists := fileExists(download.ExtractedPath)

		if mainExists && hasAllowedExt(isoPath) {
			return isoPath, nil
		}

		// Otherwise, use extracted if it's usable.
		if extractExists && hasAllowedExt(download.ExtractedPath) {
			return download.ExtractedPath, nil
		}

		// Nothing usable; craft a helpful error (often main is compressed like .iso.bz2).
		return "", fmt.Errorf(
			"iso_or_img_not_found: main=%s (exists=%t, allowed=%t) extracted=%s (exists=%t, allowed=%t)",
			isoPath, mainExists, hasAllowedExt(isoPath),
			download.ExtractedPath, extractExists, hasAllowedExt(download.ExtractedPath),
		)

	case "torrent":
		torrentsDir := config.GetDownloadsPath("torrents")

		var isoCandidate, imgCandidate string
		for _, f := range download.Files {
			full := filepath.Join(torrentsDir, uuid, f.Name)
			if !fileExists(full) {
				continue
			}
			l := strings.ToLower(f.Name)
			if strings.HasSuffix(l, ".iso") && isoCandidate == "" {
				isoCandidate = full
			} else if includeImg && strings.HasSuffix(l, ".img") && imgCandidate == "" {
				imgCandidate = full
			}
		}

		if isoCandidate != "" {
			return isoCandidate, nil
		}
		if includeImg && imgCandidate != "" {
			return imgCandidate, nil
		}
		return "", fmt.Errorf("iso_or_img_not_found_in_torrent: %s", uuid)

	default:
		return "", fmt.Errorf("unsupported_download_type: %s", download.Type)
	}
}
