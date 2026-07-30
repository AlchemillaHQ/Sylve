// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

type backupJobOperationHandle struct {
	JobID          uint
	Token          string
	Operation      string
	HolderNodeID   string
	RequestPayload string
}

type restoreOperationRequest struct {
	Snapshot      string `json:"snapshot"`
	RemoteDataset string `json:"remoteDataset"`
}

func (s *Service) backupJobOperationHolder() string {
	holder := strings.TrimSpace(s.localNodeID())
	if holder != "" {
		return holder
	}
	if s != nil && s.Cluster != nil && s.Cluster.Raft != nil {
		return ""
	}
	return "local"
}

func (s *Service) applyBackupJobOperation(
	ctx context.Context,
	action string,
	acquire clusterModels.BackupJobOperationAcquire,
	transition clusterModels.BackupJobOperationTransition,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_job_operation_service_unavailable")
	}
	action = strings.ToLower(strings.TrimSpace(action))

	applyLocal := func() error {
		switch action {
		case "acquire":
			return clusterModels.AcquireBackupJobOperationTxn(s.DB, &acquire)
		case "start":
			return clusterModels.StartBackupJobOperationTxn(s.DB, &transition)
		case "finish":
			return clusterModels.FinishBackupJobOperationTxn(s.DB, &transition)
		case "abort":
			return clusterModels.AbortBackupJobOperationTxn(s.DB, &transition)
		case "release":
			return clusterModels.ReleaseBackupJobOperationTxn(s.DB, &transition)
		default:
			return fmt.Errorf("invalid_backup_job_operation_action")
		}
	}

	if s.Cluster == nil {
		return applyLocal()
	}
	if s.Cluster.Raft == nil {
		if action == "acquire" {
			return s.Cluster.AcquireBackupJobOperation(acquire, true)
		}
		return s.Cluster.TransitionBackupJobOperation(action, transition, true)
	}

	payload := map[string]any{
		"action":         action,
		"jobId":          transition.JobID,
		"token":          transition.Token,
		"operation":      transition.Operation,
		"holderNodeId":   transition.HolderNodeID,
		"occurredAt":     transition.OccurredAt,
		"requestPayload": transition.RequestPayload,
	}
	if action == "acquire" {
		payload = map[string]any{
			"action":         action,
			"jobId":          acquire.JobID,
			"token":          acquire.Token,
			"operation":      acquire.Operation,
			"holderNodeId":   acquire.HolderNodeID,
			"occurredAt":     acquire.AcquiredAt,
			"requestPayload": acquire.RequestPayload,
		}
	}

	var lastErr error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Cluster.Raft.State() == raft.Leader {
			if action == "acquire" {
				lastErr = s.Cluster.AcquireBackupJobOperation(acquire, false)
			} else {
				lastErr = s.Cluster.TransitionBackupJobOperation(action, transition, false)
			}
		} else {
			_, leaderID := s.Cluster.Raft.LeaderWithID()
			leaderNodeID := strings.TrimSpace(string(leaderID))
			if leaderNodeID == "" {
				lastErr = fmt.Errorf("leader_not_available")
			} else {
				lastErr = s.forwardReplicationPolicyControl(
					leaderNodeID,
					"backup-job-operation",
					payload,
					replicationControlDefaultTimeout,
				)
			}
		}
		if lastErr == nil {
			return nil
		}
		if backupJobOperationApplicationError(lastErr) {
			return lastErr
		}
		if attempt < replicationControlForwardAttempts-1 {
			timer := time.NewTimer(replicationControlForwardBackoff * time.Duration(attempt+1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func backupJobOperationApplicationError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"backup_job_running",
		"backup_job_not_found",
		"backup_job_operation_runner_mismatch",
		"backup_job_operation_not_found",
		"backup_job_operation_token_mismatch",
		"backup_job_operation_finishing",
		"backup_job_operation_not_releasable",
		"backup_target_disabled",
		"invalid_backup_job_operation",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func backupJobOperationStaleMessage(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "backup_job_operation_not_found") ||
		strings.Contains(text, "backup_job_operation_token_mismatch")
}

func backupJobOperationFinishing(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "backup_job_operation_finishing")
}

func (s *Service) acquireDurableBackupJobOperation(
	ctx context.Context,
	jobID uint,
	operation string,
	requestPayload string,
) (backupJobOperationHandle, error) {
	holderNodeID := s.backupJobOperationHolder()
	if holderNodeID == "" {
		return backupJobOperationHandle{}, fmt.Errorf("local_node_id_unavailable")
	}
	handle := backupJobOperationHandle{
		JobID:          jobID,
		Token:          fmt.Sprintf("%s:%s:%s", operation, holderNodeID, uuid.NewString()),
		Operation:      operation,
		HolderNodeID:   holderNodeID,
		RequestPayload: strings.TrimSpace(requestPayload),
	}
	payload := clusterModels.BackupJobOperationAcquire{
		JobID: handle.JobID, Token: handle.Token, Operation: handle.Operation,
		HolderNodeID: handle.HolderNodeID, RequestPayload: handle.RequestPayload,
		AcquiredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
	if err := s.applyBackupJobOperation(ctx, "acquire", payload, clusterModels.BackupJobOperationTransition{}); err != nil {
		return backupJobOperationHandle{}, err
	}
	return handle, nil
}

func (s *Service) transitionDurableBackupJobOperation(
	ctx context.Context,
	action string,
	handle backupJobOperationHandle,
) error {
	return s.applyBackupJobOperation(ctx, action, clusterModels.BackupJobOperationAcquire{}, clusterModels.BackupJobOperationTransition{
		JobID: handle.JobID, Token: handle.Token, Operation: handle.Operation,
		HolderNodeID: handle.HolderNodeID, RequestPayload: handle.RequestPayload,
		OccurredAt: time.Now().UTC(), RequireEnabledTarget: true,
	})
}

func (s *Service) abortDurableBackupJobOperation(ctx context.Context, handle backupJobOperationHandle) error {
	err := s.transitionDurableBackupJobOperation(ctx, "abort", handle)
	if backupJobOperationStaleMessage(err) {
		return nil
	}
	return err
}

func (s *Service) finishDurableBackupJobOperation(handle backupJobOperationHandle) error {
	ctx, cancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
	defer cancel()

	if err := s.transitionDurableBackupJobOperation(ctx, "finish", handle); err != nil {
		if backupJobOperationStaleMessage(err) {
			return nil
		}
		return err
	}
	if err := s.transitionDurableBackupJobOperation(ctx, "release", handle); err != nil {
		if backupJobOperationStaleMessage(err) {
			return nil
		}
		return err
	}
	return nil
}

// prepareQueuedBackupJobOperation turns legacy queue messages into a durable
// reservation and advances the exact operation token to running. A stale
// duplicate is consumed without reacquiring work.
func (s *Service) prepareQueuedBackupJobOperation(
	ctx context.Context,
	jobID uint,
	operation string,
	token string,
	holderNodeID string,
	requestPayload string,
) (backupJobOperationHandle, bool, error) {
	handle := backupJobOperationHandle{
		JobID: jobID, Token: strings.TrimSpace(token), Operation: operation,
		HolderNodeID: strings.TrimSpace(holderNodeID), RequestPayload: strings.TrimSpace(requestPayload),
	}
	if handle.Token == "" {
		legacyHandle, err := s.acquireDurableBackupJobOperation(ctx, jobID, operation, requestPayload)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "backup_job_running") ||
				strings.Contains(strings.ToLower(err.Error()), "backup_job_not_found") {
				return backupJobOperationHandle{}, false, nil
			}
			return backupJobOperationHandle{}, false, err
		}
		handle = legacyHandle
	}
	if handle.HolderNodeID == "" {
		handle.HolderNodeID = s.backupJobOperationHolder()
	}
	localHolder := s.backupJobOperationHolder()
	if localHolder == "" {
		return handle, false, fmt.Errorf("local_node_id_unavailable")
	}
	if handle.HolderNodeID != localHolder {
		return handle, false, nil
	}

	err := s.transitionDurableBackupJobOperation(ctx, "start", handle)
	if err == nil {
		return handle, true, nil
	}
	if backupJobOperationFinishing(err) {
		if releaseErr := s.transitionDurableBackupJobOperation(ctx, "release", handle); releaseErr != nil &&
			!backupJobOperationStaleMessage(releaseErr) {
			return handle, false, releaseErr
		}
		return handle, false, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "backup_target_disabled") {
		if abortErr := s.abortDurableBackupJobOperation(ctx, handle); abortErr != nil {
			return handle, false, abortErr
		}
		return handle, false, nil
	}
	if backupJobOperationStaleMessage(err) {
		return handle, false, nil
	}
	return handle, false, err
}

// ReconcileBackupJobOperationsAfterRestart treats replicated queued/running
// operations as an outbox. Enqueueing the same token more than once is safe:
// concurrent duplicates are stopped by the local job lock and later duplicates
// observe a released token.
func (s *Service) ReconcileBackupJobOperationsAfterRestart(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	holder := s.backupJobOperationHolder()
	if holder == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}
	var operations []clusterModels.BackupJobOperation
	if err := s.DB.Where("holder_node_id = ?", holder).Order("job_id ASC").Find(&operations).Error; err != nil {
		return err
	}

	enqueue := func(name string, payload any) error {
		if s.backupOperationEnqueue != nil {
			return s.backupOperationEnqueue(ctx, name, payload)
		}
		return db.EnqueueJSON(ctx, name, payload)
	}

	var result error
	for _, operation := range operations {
		if !s.reserveJob(operation.JobID) {
			continue
		}
		var enqueueErr error
		switch operation.Operation {
		case clusterModels.BackupJobOperationBackup:
			enqueueErr = enqueue(backupJobQueueName, backupJobPayload{
				JobID: operation.JobID, OperationToken: operation.Token, HolderNodeID: operation.HolderNodeID,
			})
		case clusterModels.BackupJobOperationRestore:
			var request restoreOperationRequest
			if err := json.Unmarshal([]byte(operation.RequestPayload), &request); err != nil {
				enqueueErr = fmt.Errorf("decode_restore_operation_%d: %w", operation.JobID, err)
			} else if execution, err := s.restoreExecutionForOperation(operation.Token); err != nil {
				enqueueErr = err
			} else {
				enqueueErr = enqueue(restoreJobQueueName, restoreJobPayload{
					JobID: operation.JobID, Snapshot: request.Snapshot, RemoteDataset: request.RemoteDataset,
					OperationToken: operation.Token, HolderNodeID: operation.HolderNodeID,
					EventID: execution.EventID, AuditRecordID: execution.Audit.RecordID,
					AuditOperationID: execution.OperationID,
				})
			}
		default:
			enqueueErr = fmt.Errorf("invalid_backup_job_operation: %s", operation.Operation)
		}
		if enqueueErr != nil {
			s.releaseReservedJob(operation.JobID)
			result = errors.Join(result, enqueueErr)
		}
	}
	return result
}
