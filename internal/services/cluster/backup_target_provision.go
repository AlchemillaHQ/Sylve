// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func (s *Service) ProposeBackupTargetCreateCandidate(
	candidate *clusterModels.BackupTarget,
	bypassRaft bool,
) (*clusterModels.BackupTarget, error) {
	if candidate == nil {
		return nil, fmt.Errorf("backup_target_required")
	}
	target := *candidate
	target.Name = strings.TrimSpace(target.Name)
	target.SSHHost = strings.TrimSpace(target.SSHHost)
	target.SSHKey = strings.TrimSpace(target.SSHKey)
	target.SSHKeyPath = ""
	target.BackupRoot = strings.TrimSpace(target.BackupRoot)
	target.Description = strings.TrimSpace(target.Description)
	if target.Name == "" || target.SSHHost == "" || target.BackupRoot == "" {
		return nil, fmt.Errorf("invalid_backup_target")
	}
	if target.SSHKey == "" {
		return nil, fmt.Errorf("managed_ssh_key_required")
	}
	if target.SSHPort == 0 {
		target.SSHPort = 22
	}
	id, err := s.newRaftObjectID("backup_targets")
	if err != nil {
		return nil, fmt.Errorf("new_backup_target_id_failed: %w", err)
	}
	target.ID = id
	command := clusterModels.BackupTargetCreateV2{
		Target:              clusterModels.BackupTargetToReplicationPayload(target),
		ProposedFingerprint: clusterModels.BackupTargetConfigurationFingerprint(&target),
	}
	if bypassRaft {
		if err := clusterModels.ApplyBackupTargetCreateV2Txn(s.DB, &command); err != nil {
			return nil, err
		}
		return &target, nil
	}
	if s.Raft == nil {
		return nil, fmt.Errorf("raft_not_initialized")
	}
	data, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("failed_to_marshal_backup_target_payload: %w", err)
	}
	if err := s.applyRaftCommand(clusterModels.Command{Type: "backup_target", Action: "create_v2", Data: data}); err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *Service) ProposeBackupTargetUpdatePlan(plan *BackupTargetUpdatePlan, bypassRaft bool) error {
	if plan == nil || plan.Candidate == nil {
		return fmt.Errorf("backup_target_update_plan_required")
	}
	candidate := plan.Candidate
	command := clusterModels.BackupTargetUpdateV2{
		TargetID:            candidate.ID,
		Kind:                plan.Kind,
		ExpectedFingerprint: plan.ExpectedFingerprint,
		ProposedFingerprint: plan.ProposedFingerprint,
		Name:                candidate.Name,
		Description:         candidate.Description,
		Enabled:             candidate.Enabled,
		SSHKey:              candidate.SSHKey,
	}
	if bypassRaft {
		return clusterModels.ApplyBackupTargetUpdateV2Txn(s.DB, &command)
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	data, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_backup_target_update: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{Type: "backup_target", Action: "update_v2", Data: data})
}

func (s *Service) PrepareBackupTargetProvisionCreate(
	candidate *clusterModels.BackupTarget,
	token string,
	bypassRaft bool,
) (*clusterModels.BackupTargetProvisionOperation, error) {
	if candidate == nil {
		return nil, fmt.Errorf("backup_target_required")
	}
	target := *candidate
	target.Name = strings.TrimSpace(target.Name)
	target.SSHHost = strings.TrimSpace(target.SSHHost)
	target.SSHKey = strings.TrimSpace(target.SSHKey)
	target.SSHKeyPath = ""
	target.BackupRoot = strings.TrimSpace(target.BackupRoot)
	target.Description = strings.TrimSpace(target.Description)
	if target.SSHPort == 0 {
		target.SSHPort = 22
	}
	id, err := s.newRaftObjectID("backup_targets")
	if err != nil {
		return nil, fmt.Errorf("new_backup_target_id_failed: %w", err)
	}
	target.ID = id
	prepare := clusterModels.BackupTargetProvisionPrepare{
		Token:               strings.TrimSpace(token),
		Target:              clusterModels.BackupTargetToReplicationPayload(target),
		ProposedFingerprint: clusterModels.BackupTargetConfigurationFingerprint(&target),
	}
	if bypassRaft {
		if err := clusterModels.PrepareBackupTargetProvisionOperationTxn(s.DB, &prepare); err != nil {
			return nil, err
		}
	} else {
		if s.Raft == nil {
			return nil, fmt.Errorf("raft_not_initialized")
		}
		data, err := json.Marshal(prepare)
		if err != nil {
			return nil, fmt.Errorf("marshal_backup_target_provision_prepare: %w", err)
		}
		if err := s.applyRaftCommand(clusterModels.Command{
			Type: "backup_target_provision", Action: "prepare", Data: data,
		}); err != nil {
			return nil, err
		}
	}
	return s.GetBackupTargetProvisionOperation(prepare.Token)
}

func (s *Service) GetBackupTargetProvisionOperation(token string) (*clusterModels.BackupTargetProvisionOperation, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("backup_target_provision_token_required")
	}
	var operation clusterModels.BackupTargetProvisionOperation
	if err := s.DB.Where("token = ?", token).First(&operation).Error; err != nil {
		return nil, err
	}
	return &operation, nil
}

func (s *Service) ListPendingBackupTargetProvisionOperations() ([]clusterModels.BackupTargetProvisionOperation, error) {
	var operations []clusterModels.BackupTargetProvisionOperation
	err := s.DB.Where("state = ?", clusterModels.BackupTargetProvisionStatePending).
		Order("token ASC").Find(&operations).Error
	return operations, err
}

func (s *Service) transitionBackupTargetProvision(
	action string,
	transition clusterModels.BackupTargetProvisionTransition,
	bypassRaft bool,
) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if bypassRaft {
		switch action {
		case "complete":
			return clusterModels.CompleteBackupTargetProvisionOperationTxn(s.DB, &transition)
		case "fail":
			return clusterModels.FailBackupTargetProvisionOperationTxn(s.DB, &transition)
		default:
			return fmt.Errorf("invalid_backup_target_provision_action")
		}
	}
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	data, err := json.Marshal(transition)
	if err != nil {
		return fmt.Errorf("marshal_backup_target_provision_transition: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{Type: "backup_target_provision", Action: action, Data: data})
}

func (s *Service) CompleteBackupTargetProvision(
	operation *clusterModels.BackupTargetProvisionOperation,
	bypassRaft bool,
) error {
	if operation == nil {
		return fmt.Errorf("backup_target_provision_operation_required")
	}
	return s.transitionBackupTargetProvision("complete", clusterModels.BackupTargetProvisionTransition{
		Token:               operation.Token,
		TargetID:            operation.TargetID,
		ProposedFingerprint: operation.ProposedFingerprint,
	}, bypassRaft)
}

func (s *Service) FailBackupTargetProvision(
	operation *clusterModels.BackupTargetProvisionOperation,
	reason string,
	bypassRaft bool,
) error {
	if operation == nil {
		return fmt.Errorf("backup_target_provision_operation_required")
	}
	return s.transitionBackupTargetProvision("fail", clusterModels.BackupTargetProvisionTransition{
		Token:               operation.Token,
		TargetID:            operation.TargetID,
		ProposedFingerprint: operation.ProposedFingerprint,
		Error:               strings.TrimSpace(reason),
	}, bypassRaft)
}
