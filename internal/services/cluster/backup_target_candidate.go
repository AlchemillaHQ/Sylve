// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func backupTargetCandidateFromInput(
	input clusterServiceInterfaces.BackupTargetReq,
	existing *clusterModels.BackupTarget,
) (*clusterModels.BackupTarget, error) {
	if err := validateBackupTargetInput(input); err != nil {
		return nil, err
	}

	key := strings.TrimSpace(input.SSHKey)
	if key == "" && existing != nil {
		key = strings.TrimSpace(existing.SSHKey)
	}
	if key == "" {
		return nil, fmt.Errorf("managed_ssh_key_required")
	}

	enabled := boolPtrDefaultTrue(input.Enabled)
	id := input.ID
	if existing != nil {
		if input.ID != 0 && input.ID != existing.ID {
			return nil, fmt.Errorf("backup_target_id_mismatch")
		}
		id = existing.ID
		if input.Enabled == nil {
			enabled = existing.Enabled
		}
	}

	candidate := &clusterModels.BackupTarget{
		ID:               id,
		Name:             strings.TrimSpace(input.Name),
		SSHHost:          strings.TrimSpace(input.SSHHost),
		SSHPort:          input.SSHPort,
		SSHKeyPath:       "",
		SSHKey:           key,
		BackupRoot:       strings.TrimSpace(input.BackupRoot),
		CreateBackupRoot: utils.PtrToBool(input.CreateBackupRoot),
		Description:      strings.TrimSpace(input.Description),
		Enabled:          enabled,
	}
	if candidate.SSHPort == 0 {
		candidate.SSHPort = 22
	}
	return candidate, nil
}

func (s *Service) BuildBackupTargetCreateCandidate(
	input clusterServiceInterfaces.BackupTargetReq,
) (*clusterModels.BackupTarget, error) {
	return backupTargetCandidateFromInput(input, nil)
}

func (s *Service) BuildBackupTargetUpdateCandidate(
	existing *clusterModels.BackupTarget,
	input clusterServiceInterfaces.BackupTargetReq,
) (*clusterModels.BackupTarget, error) {
	if existing == nil || existing.ID == 0 {
		return nil, fmt.Errorf("backup_target_not_found")
	}
	return backupTargetCandidateFromInput(input, existing)
}
