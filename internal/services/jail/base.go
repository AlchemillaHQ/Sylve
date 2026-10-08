// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"fmt"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
)

func (s *Service) FindBaseByUUID(uuid string) (string, error) {
	path, release, err := s.acquireBaseByUUID(uuid)
	if release != nil {
		release()
	}
	return path, err
}

// Hold admission through the copy, not just the initial path lookup.
func (s *Service) acquireBaseByUUID(uuid string) (string, func(), error) {
	if uuid == "" {
		return "", nil, fmt.Errorf("base_download_uuid_required")
	}

	var download utilitiesModels.Downloads
	if err := s.DB.
		Where("uuid = ?", uuid).
		First(&download).Error; err != nil {
		return "", nil, fmt.Errorf("failed_to_find_download: %w", err)
	}

	if download.UType != utilitiesModels.DownloadUTypeBase {
		return "", nil, fmt.Errorf("download_is_not_base_or_rootfs: %s", uuid)
	}
	release := s.DownloadStorage.ReadLock(download.StoragePool)
	path, err := s.resolveBaseDownload(download)
	if err != nil {
		release()
		return "", nil, err
	}
	return path, release, nil
}

func (s *Service) resolveBaseDownload(download utilitiesModels.Downloads) (string, error) {
	layout, err := s.DownloadStorage.Resolve(context.Background(), download.DownloadStorage, false)
	if err != nil {
		return "", err
	}
	if download.Status != utilitiesModels.DownloadStatusDone || download.ExtractedPath == "" {
		return "", fmt.Errorf("base_download_not_ready")
	}
	if err := layout.ValidateDownload(download); err != nil {
		return "", err
	}
	if err := layout.ValidateTree(download.ExtractedPath); err != nil {
		return "", err
	}

	return download.ExtractedPath, nil
}
