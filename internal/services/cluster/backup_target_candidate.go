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
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type BackupTargetUpdatePlan struct {
	Candidate           *clusterModels.BackupTarget
	Kind                string
	ExpectedFingerprint string
	ProposedFingerprint string
}

func normalizedBackupTargetPort(port int) int {
	if port == 0 {
		return 22
	}
	return port
}

func backupTargetCandidateFromInput(
	input clusterServiceInterfaces.BackupTargetReq,
) (*clusterModels.BackupTarget, error) {
	destination, root, err := canonicalBackupTargetInput(input)
	if err != nil {
		return nil, err
	}

	key := strings.TrimSpace(input.SSHKey)
	if key == "" {
		return nil, fmt.Errorf("managed_ssh_key_required")
	}
	candidate := &clusterModels.BackupTarget{
		ID:               input.ID,
		Name:             strings.TrimSpace(input.Name),
		SSHHost:          destination.String(),
		SSHPort:          normalizedBackupTargetPort(input.SSHPort),
		SSHKeyPath:       "",
		SSHKey:           key,
		BackupRoot:       root.String(),
		CreateBackupRoot: utils.PtrToBool(input.CreateBackupRoot),
		Description:      strings.TrimSpace(input.Description),
		Enabled:          boolPtrDefaultTrue(input.Enabled),
	}
	return candidate, nil
}

func (s *Service) BuildBackupTargetCreateCandidate(
	input clusterServiceInterfaces.BackupTargetReq,
) (*clusterModels.BackupTarget, error) {
	return backupTargetCandidateFromInput(input)
}

func (s *Service) BuildBackupTargetUpdatePlan(
	existing *clusterModels.BackupTarget,
	input clusterServiceInterfaces.BackupTargetReq,
) (*BackupTargetUpdatePlan, error) {
	if existing == nil || existing.ID == 0 {
		return nil, fmt.Errorf("backup_target_not_found")
	}
	incomingHost, incomingRoot, err := canonicalBackupTargetInput(input)
	if err != nil {
		return nil, err
	}
	if input.ID != 0 && input.ID != existing.ID {
		return nil, fmt.Errorf("backup_target_id_mismatch")
	}

	existingHost, err := remoteexec.ParseSSHDestination(existing.SSHHost)
	if err != nil {
		return nil, fmt.Errorf("backup_target_existing_endpoint_invalid: %w", err)
	}
	if existing.SSHPort < 0 || existing.SSHPort > 65535 {
		return nil, fmt.Errorf("backup_target_existing_endpoint_invalid: invalid_ssh_port")
	}
	existingRoot, err := remoteexec.ParseZFSDataset(existing.BackupRoot)
	if err != nil {
		return nil, fmt.Errorf("backup_target_existing_root_invalid: %w", err)
	}
	incomingPort := normalizedBackupTargetPort(existing.SSHPort)
	if input.SSHPort != 0 {
		incomingPort = normalizedBackupTargetPort(input.SSHPort)
	}
	incomingCreateRoot := existing.CreateBackupRoot
	if input.CreateBackupRoot != nil {
		incomingCreateRoot = *input.CreateBackupRoot
	}
	if incomingHost.String() != existingHost.String() || incomingPort != normalizedBackupTargetPort(existing.SSHPort) {
		return nil, fmt.Errorf("backup_target_endpoint_immutable")
	}
	if incomingRoot.String() != existingRoot.String() {
		return nil, fmt.Errorf("backup_target_root_immutable")
	}
	if incomingCreateRoot != existing.CreateBackupRoot {
		return nil, fmt.Errorf("backup_target_create_root_immutable")
	}

	proposedKey := strings.TrimSpace(input.SSHKey)
	if proposedKey == "" {
		proposedKey = strings.TrimSpace(existing.SSHKey)
	}
	proposedEnabled := existing.Enabled
	if input.Enabled != nil {
		proposedEnabled = *input.Enabled
	}
	candidate := &clusterModels.BackupTarget{
		ID:               existing.ID,
		Name:             strings.TrimSpace(input.Name),
		SSHHost:          strings.TrimSpace(existing.SSHHost),
		SSHPort:          normalizedBackupTargetPort(existing.SSHPort),
		SSHKeyPath:       strings.TrimSpace(existing.SSHKeyPath),
		SSHKey:           proposedKey,
		BackupRoot:       strings.TrimSpace(existing.BackupRoot),
		CreateBackupRoot: existing.CreateBackupRoot,
		Description:      strings.TrimSpace(input.Description),
		Enabled:          proposedEnabled,
	}
	if proposedKey != "" {
		candidate.SSHKeyPath = ""
	}

	metadataChanged := candidate.Name != strings.TrimSpace(existing.Name) ||
		candidate.Description != strings.TrimSpace(existing.Description)
	keyChanged := candidate.SSHKey != strings.TrimSpace(existing.SSHKey)
	enabledChanged := candidate.Enabled != existing.Enabled

	kind := clusterModels.BackupTargetUpdateKindMetadata
	switch {
	case keyChanged:
		if metadataChanged || enabledChanged {
			return nil, fmt.Errorf("backup_target_update_mode_conflict")
		}
		if existing.Enabled || candidate.Enabled {
			return nil, fmt.Errorf("backup_target_must_be_disabled_for_key_rotation")
		}
		kind = clusterModels.BackupTargetUpdateKindRotateKey
	case enabledChanged:
		if metadataChanged {
			return nil, fmt.Errorf("backup_target_update_mode_conflict")
		}
		if candidate.Enabled {
			if candidate.SSHKey == "" {
				return nil, fmt.Errorf("managed_ssh_key_required")
			}
			kind = clusterModels.BackupTargetUpdateKindEnable
		} else {
			kind = clusterModels.BackupTargetUpdateKindDisable
		}
	default:
		kind = clusterModels.BackupTargetUpdateKindMetadata
	}

	return &BackupTargetUpdatePlan{
		Candidate:           candidate,
		Kind:                kind,
		ExpectedFingerprint: clusterModels.BackupTargetConfigurationFingerprint(existing),
		ProposedFingerprint: clusterModels.BackupTargetConfigurationFingerprint(candidate),
	}, nil
}

func (s *Service) BuildBackupTargetUpdateCandidate(
	existing *clusterModels.BackupTarget,
	input clusterServiceInterfaces.BackupTargetReq,
) (*clusterModels.BackupTarget, error) {
	plan, err := s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		return nil, err
	}
	return plan.Candidate, nil
}
