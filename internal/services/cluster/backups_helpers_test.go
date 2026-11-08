// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"

	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func (s *Service) ProposeBackupTargetCreate(input clusterServiceInterfaces.BackupTargetReq, hostKey string) error {
	resolvedSSHKey, err := resolveSSHKeyMaterial(input.SSHKey, input.SSHKeyPath)
	if err != nil {
		return err
	}
	input.SSHKey = resolvedSSHKey
	candidate, err := s.BuildBackupTargetCreateCandidate(input)
	if err != nil {
		return err
	}
	candidate.SSHHostKey = hostKey
	_, err = s.ProposeBackupTargetCreateCandidate(candidate)
	return err
}

func (s *Service) ProposeBackupTargetUpdate(input clusterServiceInterfaces.BackupTargetReq) error {
	if input.ID == 0 {
		return fmt.Errorf("invalid_target_id")
	}
	if !s.Standalone() && s.Raft() == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	existing, err := s.GetBackupTargetByID(input.ID)
	if err != nil {
		return err
	}
	plan, err := s.BuildBackupTargetUpdatePlan(existing, input)
	if err != nil {
		return err
	}
	return s.ProposeBackupTargetUpdatePlan(plan)
}

func (s *Service) ProposeBackupJobCreate(input clusterServiceInterfaces.BackupJobReq) error {
	return s.ProposeBackupJobCreateContext(context.Background(), input)
}

func (s *Service) ProposeBackupJobUpdate(id uint, input clusterServiceInterfaces.BackupJobReq) error {
	return s.ProposeBackupJobUpdateContext(context.Background(), id, input, BackupJobPlacementAuthorization{})
}

func resolveSSHKeyMaterial(sshKey, sshKeyPath string) (string, error) {
	if trimmedKey := strings.TrimSpace(sshKey); trimmedKey != "" {
		return trimmedKey, nil
	}
	trimmedPath := strings.TrimSpace(sshKeyPath)
	if trimmedPath == "" {
		return "", nil
	}
	raw, err := os.ReadFile(trimmedPath)
	if err != nil {
		return "", fmt.Errorf("ssh_key_file_not_found: %s: %w", trimmedPath, err)
	}
	return strings.TrimSpace(string(raw)), nil
}
