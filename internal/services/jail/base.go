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

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) FindBaseByUUID(uuid string) (string, error) {
	if uuid == "" {
		return "", fmt.Errorf("base_download_uuid_required")
	}

	var download utilitiesModels.Downloads
	if err := s.DB.
		Where("uuid = ?", uuid).
		First(&download).Error; err != nil {
		return "", fmt.Errorf("failed_to_find_download: %w", err)
	}

	if download.UType != utilitiesModels.DownloadUTypeBase {
		return "", fmt.Errorf("download_is_not_base_or_rootfs: %s", uuid)
	}

	return download.ExtractedPath, nil
}

func (s *Service) ExtractBase(mountPoint, baseTxz string) (string, error) {
	args := []string{"-C", mountPoint, "-xf", baseTxz}
	return utils.RunCommand("tar", args...)
}
