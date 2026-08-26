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

type backupTargetRestoreOperationHandle struct {
	Token              string
	TargetID           uint
	HolderNodeID       string
	DestinationDataset string
	RequestPayload     string
}

type backupTargetRestoreOperationRequest struct {
	TargetID           uint   `json:"targetId"`
	RemoteDataset      string `json:"remoteDataset"`
	Snapshot           string `json:"snapshot"`
	DestinationDataset string `json:"destinationDataset"`
	RestoreNetwork     bool   `json:"restoreNetwork"`
}

func normalizeRestoreFromTargetOperationRequest(
	payload restoreFromTargetPayload,
) (backupTargetRestoreOperationRequest, restoreFromTargetPayload, error) {
	if payload.TargetID == 0 {
		return backupTargetRestoreOperationRequest{}, payload, fmt.Errorf("invalid_target_id")
	}
	remoteDataset, snapshot, destinationDataset, err := CanonicalRestoreFromTargetInput(
		payload.RemoteDataset,
		payload.Snapshot,
		payload.DestinationDataset,
	)
	if err != nil {
		return backupTargetRestoreOperationRequest{}, payload, err
	}
	request := backupTargetRestoreOperationRequest{
		TargetID:           payload.TargetID,
		RemoteDataset:      remoteDataset,
		Snapshot:           snapshot,
		DestinationDataset: destinationDataset,
		RestoreNetwork:     true,
	}
	if payload.RestoreNetwork != nil {
		request.RestoreNetwork = *payload.RestoreNetwork
	}
	payload.TargetID = request.TargetID
	payload.RemoteDataset = request.RemoteDataset
	payload.Snapshot = request.Snapshot
	payload.DestinationDataset = request.DestinationDataset
	restoreNetwork := request.RestoreNetwork
	payload.RestoreNetwork = &restoreNetwork
	return request, payload, nil
}

func marshalBackupTargetRestoreOperationRequest(payload restoreFromTargetPayload) (string, restoreFromTargetPayload, error) {
	request, normalized, err := normalizeRestoreFromTargetOperationRequest(payload)
	if err != nil {
		return "", payload, err
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return "", payload, err
	}
	return string(requestBytes), normalized, nil
}

func backupTargetRestoreOperationTransition(
	handle backupTargetRestoreOperationHandle,
) clusterModels.BackupTargetRestoreOperationTransition {
	return clusterModels.BackupTargetRestoreOperationTransition{
		Token: handle.Token, TargetID: handle.TargetID, HolderNodeID: handle.HolderNodeID,
		DestinationDataset: handle.DestinationDataset, RequestPayload: handle.RequestPayload,
		OccurredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
}

func (s *Service) applyBackupTargetRestoreOperation(
	ctx context.Context,
	action string,
	acquire clusterModels.BackupTargetRestoreOperationAcquire,
	transition clusterModels.BackupTargetRestoreOperationTransition,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("backup_target_restore_operation_service_unavailable")
	}
	action = strings.ToLower(strings.TrimSpace(action))

	applyLocal := func() error {
		switch action {
		case "acquire":
			return clusterModels.AcquireBackupTargetRestoreOperationTxn(s.DB, &acquire)
		case "start":
			return clusterModels.StartBackupTargetRestoreOperationTxn(s.DB, &transition)
		case "finish":
			return clusterModels.FinishBackupTargetRestoreOperationTxn(s.DB, &transition)
		case "requeue":
			return clusterModels.RequeueBackupTargetRestoreOperationTxn(s.DB, &transition)
		case "abort":
			return clusterModels.AbortBackupTargetRestoreOperationTxn(s.DB, &transition)
		case "release":
			return clusterModels.ReleaseBackupTargetRestoreOperationTxn(s.DB, &transition)
		default:
			return fmt.Errorf("invalid_backup_target_restore_operation_action")
		}
	}

	if s.Cluster == nil {
		return applyLocal()
	}
	if s.Cluster.Raft == nil {
		if action == "acquire" {
			return s.Cluster.AcquireBackupTargetRestoreOperation(acquire, true)
		}
		return s.Cluster.TransitionBackupTargetRestoreOperation(action, transition, true)
	}

	payload := map[string]any{
		"action":             action,
		"token":              transition.Token,
		"targetId":           transition.TargetID,
		"holderNodeId":       transition.HolderNodeID,
		"destinationDataset": transition.DestinationDataset,
		"requestPayload":     transition.RequestPayload,
		"occurredAt":         transition.OccurredAt,
	}
	if action == "acquire" {
		payload = map[string]any{
			"action":             action,
			"token":              acquire.Token,
			"targetId":           acquire.TargetID,
			"holderNodeId":       acquire.HolderNodeID,
			"destinationDataset": acquire.DestinationDataset,
			"requestPayload":     acquire.RequestPayload,
			"occurredAt":         acquire.AcquiredAt,
		}
	}

	var lastErr error
	for attempt := 0; attempt < replicationControlForwardAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Cluster.Raft.State() == raft.Leader {
			if action == "acquire" {
				lastErr = s.Cluster.AcquireBackupTargetRestoreOperation(acquire, false)
			} else {
				lastErr = s.Cluster.TransitionBackupTargetRestoreOperation(action, transition, false)
			}
		} else {
			_, leaderID := s.Cluster.Raft.LeaderWithID()
			leaderNodeID := strings.TrimSpace(string(leaderID))
			if leaderNodeID == "" {
				lastErr = fmt.Errorf("leader_not_available")
			} else if targetAPI, resolveErr := s.Cluster.ResolveIntraClusterVoterAPI(leaderNodeID); resolveErr != nil {
				lastErr = resolveErr
			} else {
				_, lastErr = s.forwardReplicationPolicyControlReadAtAPI(
					targetAPI,
					"backup-target-restore-operation",
					payload,
					replicationControlDefaultTimeout,
				)
			}
		}
		if lastErr == nil {
			return nil
		}
		if backupTargetRestoreOperationApplicationError(lastErr) {
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

func backupTargetRestoreOperationApplicationError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"restore_destination_reserved",
		"backup_target_not_found",
		"backup_target_disabled",
		"backup_target_restore_operation_not_found",
		"backup_target_restore_operation_token_mismatch",
		"backup_target_restore_operation_already_started",
		"backup_target_restore_operation_already_completed",
		"backup_target_restore_operation_finishing",
		"backup_target_restore_operation_not_abortable",
		"backup_target_restore_operation_not_finishable",
		"backup_target_restore_operation_not_releasable",
		"invalid_backup_target_restore_operation",
		"invalid_target_id",
		"destination_dataset_invalid",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func backupTargetRestoreOperationStaleMessage(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "backup_target_restore_operation_not_found") ||
		strings.Contains(text, "backup_target_restore_operation_token_mismatch") ||
		strings.Contains(text, "backup_target_restore_operation_already_started") ||
		strings.Contains(text, "backup_target_restore_operation_already_completed")
}

func backupTargetRestoreOperationFinishing(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "backup_target_restore_operation_finishing")
}

func backupTargetRestoreOperationTargetUnavailable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "backup_target_disabled") ||
		strings.Contains(text, "backup_target_not_found")
}

func backupTargetRestoreQueuePayloadInvalid(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"invalid_target_id",
		"remote_dataset_required",
		"snapshot_required",
		"destination_dataset_required",
		"destination_dataset_invalid",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (s *Service) acquireDurableBackupTargetRestoreOperation(
	ctx context.Context,
	payload restoreFromTargetPayload,
	operationID string,
) (backupTargetRestoreOperationHandle, restoreFromTargetPayload, error) {
	holderNodeID := s.backupJobOperationHolder()
	if holderNodeID == "" {
		return backupTargetRestoreOperationHandle{}, payload, fmt.Errorf("local_node_id_unavailable")
	}
	requestPayload, normalizedPayload, err := marshalBackupTargetRestoreOperationRequest(payload)
	if err != nil {
		return backupTargetRestoreOperationHandle{}, payload, err
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		operationID = uuid.NewString()
	} else {
		parsedOperationID, parseErr := uuid.Parse(operationID)
		if parseErr != nil {
			return backupTargetRestoreOperationHandle{}, payload, fmt.Errorf("restore_operation_id_invalid")
		}
		operationID = parsedOperationID.String()
	}
	handle := backupTargetRestoreOperationHandle{
		Token:    fmt.Sprintf("target-restore:%s:%s", holderNodeID, operationID),
		TargetID: normalizedPayload.TargetID, HolderNodeID: holderNodeID,
		DestinationDataset: normalizedPayload.DestinationDataset,
		RequestPayload:     requestPayload,
	}
	acquire := clusterModels.BackupTargetRestoreOperationAcquire{
		Token: handle.Token, TargetID: handle.TargetID, HolderNodeID: handle.HolderNodeID,
		DestinationDataset: handle.DestinationDataset, RequestPayload: handle.RequestPayload,
		AcquiredAt: time.Now().UTC(), RequireEnabledTarget: true,
	}
	if err := s.applyBackupTargetRestoreOperation(
		ctx,
		"acquire",
		acquire,
		clusterModels.BackupTargetRestoreOperationTransition{},
	); err != nil {
		return backupTargetRestoreOperationHandle{}, payload, err
	}
	return handle, normalizedPayload, nil
}

func (s *Service) transitionDurableBackupTargetRestoreOperation(
	ctx context.Context,
	action string,
	handle backupTargetRestoreOperationHandle,
) error {
	return s.applyBackupTargetRestoreOperation(
		ctx,
		action,
		clusterModels.BackupTargetRestoreOperationAcquire{},
		backupTargetRestoreOperationTransition(handle),
	)
}

func (s *Service) abortDurableBackupTargetRestoreOperation(
	ctx context.Context,
	handle backupTargetRestoreOperationHandle,
) error {
	err := s.transitionDurableBackupTargetRestoreOperation(ctx, "abort", handle)
	if backupTargetRestoreOperationStaleMessage(err) {
		return nil
	}
	return err
}

func (s *Service) finishDurableBackupTargetRestoreOperation(
	handle backupTargetRestoreOperationHandle,
) error {
	defer s.releaseActiveBackupTargetRestoreToken(handle.Token)
	ctx, cancel := context.WithTimeout(context.Background(), replicationControlDefaultTimeout)
	defer cancel()

	if err := s.transitionDurableBackupTargetRestoreOperation(ctx, "finish", handle); err != nil {
		if backupTargetRestoreOperationStaleMessage(err) {
			return nil
		}
		return err
	}
	if err := s.transitionDurableBackupTargetRestoreOperation(ctx, "release", handle); err != nil {
		if backupTargetRestoreOperationStaleMessage(err) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) claimActiveBackupTargetRestoreToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	s.targetRestoreOperationMu.Lock()
	defer s.targetRestoreOperationMu.Unlock()
	if s.activeTargetRestoreTokens == nil {
		s.activeTargetRestoreTokens = make(map[string]struct{})
	}
	if _, exists := s.activeTargetRestoreTokens[token]; exists {
		return false
	}
	s.activeTargetRestoreTokens[token] = struct{}{}
	return true
}

func (s *Service) releaseActiveBackupTargetRestoreToken(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	s.targetRestoreOperationMu.Lock()
	defer s.targetRestoreOperationMu.Unlock()
	delete(s.activeTargetRestoreTokens, token)
}

func (s *Service) prepareQueuedBackupTargetRestoreOperation(
	ctx context.Context,
	payload restoreFromTargetPayload,
) (backupTargetRestoreOperationHandle, restoreFromTargetPayload, bool, error) {
	requestPayload, normalizedPayload, err := marshalBackupTargetRestoreOperationRequest(payload)
	if err != nil {
		return backupTargetRestoreOperationHandle{}, payload, false, err
	}
	handle := backupTargetRestoreOperationHandle{
		Token: strings.TrimSpace(payload.OperationToken), TargetID: normalizedPayload.TargetID,
		HolderNodeID:       strings.TrimSpace(payload.HolderNodeID),
		DestinationDataset: normalizedPayload.DestinationDataset,
		RequestPayload:     requestPayload,
	}
	if handle.Token == "" {
		legacyHandle, legacyPayload, acquireErr := s.acquireDurableBackupTargetRestoreOperation(ctx, normalizedPayload, "")
		if acquireErr != nil {
			if backupTargetRestoreOperationApplicationError(acquireErr) {
				return backupTargetRestoreOperationHandle{}, normalizedPayload, false, nil
			}
			return backupTargetRestoreOperationHandle{}, normalizedPayload, false, acquireErr
		}
		handle = legacyHandle
		normalizedPayload = legacyPayload
	}
	if handle.HolderNodeID == "" {
		handle.HolderNodeID = s.backupJobOperationHolder()
	}
	localHolder := s.backupJobOperationHolder()
	if localHolder == "" {
		return handle, normalizedPayload, false, fmt.Errorf("local_node_id_unavailable")
	}
	if handle.HolderNodeID != localHolder {
		return handle, normalizedPayload, false, nil
	}
	if !s.claimActiveBackupTargetRestoreToken(handle.Token) {
		return handle, normalizedPayload, false, nil
	}

	err = s.transitionDurableBackupTargetRestoreOperation(ctx, "start", handle)
	if err == nil {
		return handle, normalizedPayload, true, nil
	}
	defer s.releaseActiveBackupTargetRestoreToken(handle.Token)
	if backupTargetRestoreOperationFinishing(err) {
		if releaseErr := s.transitionDurableBackupTargetRestoreOperation(ctx, "release", handle); releaseErr != nil &&
			!backupTargetRestoreOperationStaleMessage(releaseErr) {
			return handle, normalizedPayload, false, releaseErr
		}
		return handle, normalizedPayload, false, nil
	}
	if backupTargetRestoreOperationTargetUnavailable(err) {
		if abortErr := s.abortDurableBackupTargetRestoreOperation(ctx, handle); abortErr != nil {
			return handle, normalizedPayload, false, abortErr
		}
		// The request was already accepted with durable event/audit correlation.
		// Return the admission failure so the queue wrapper can publish that exact
		// terminal outcome after removing the now-unexecutable reservation.
		return handle, normalizedPayload, false, err
	}
	if strings.Contains(strings.ToLower(err.Error()), "backup_target_restore_operation_already_started") {
		// No worker in this process owns the token (the local claim above won),
		// so this is a retained message after an uncertain cleanup response.
		// Finalize the same logical operation rather than executing it twice.
		if finishErr := s.transitionDurableBackupTargetRestoreOperation(ctx, "finish", handle); finishErr != nil &&
			!backupTargetRestoreOperationStaleMessage(finishErr) {
			return handle, normalizedPayload, false, finishErr
		}
		if releaseErr := s.transitionDurableBackupTargetRestoreOperation(ctx, "release", handle); releaseErr != nil &&
			!backupTargetRestoreOperationStaleMessage(releaseErr) {
			return handle, normalizedPayload, false, releaseErr
		}
		return handle, normalizedPayload, false, nil
	}
	if backupTargetRestoreOperationStaleMessage(err) {
		return handle, normalizedPayload, false, nil
	}
	return handle, normalizedPayload, false, err
}

func (s *Service) enqueueRestoreFromTargetOperation(ctx context.Context, payload restoreFromTargetPayload) error {
	if s.restoreFromTargetOperationEnqueue != nil {
		return s.restoreFromTargetOperationEnqueue(ctx, restoreFromTargetQueueName, payload)
	}
	return db.EnqueueJSON(ctx, restoreFromTargetQueueName, payload)
}

func (s *Service) waitForBackupTargetRestoreRaftCatchUp(ctx context.Context) error {
	if s == nil || s.Cluster == nil || s.Cluster.Raft == nil {
		return nil
	}
	r := s.Cluster.Raft
	stableIndex := ^uint64(0)
	var stableSince time.Time
	var lastBarrierErr error
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if r.State() == raft.Leader {
			if err := r.Barrier(replicationControlDefaultTimeout).Error(); err == nil {
				return nil
			} else {
				lastBarrierErr = err
			}
		}
		_, leaderID := r.LeaderWithID()
		lastContact := r.LastContact()
		lastIndex := r.LastIndex()
		if strings.TrimSpace(string(leaderID)) != "" && !lastContact.IsZero() &&
			time.Since(lastContact) <= 2*time.Second && r.AppliedIndex() >= lastIndex {
			if stableIndex != lastIndex {
				stableIndex = lastIndex
				stableSince = time.Now()
			} else if time.Since(stableSince) >= time.Second {
				return nil
			}
		} else {
			stableIndex = ^uint64(0)
			stableSince = time.Time{}
		}

		select {
		case <-ctx.Done():
			if lastBarrierErr != nil {
				return errors.Join(ctx.Err(), fmt.Errorf(
					"backup_target_restore_reconcile_barrier_failed: %w",
					lastBarrierErr,
				))
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ReconcileBackupTargetRestoreOperationsAfterRestart treats queued reservations
// as an outbox. An interrupted running token is first reset to queued; if the
// queue database still contains its old message, queued->running CAS ensures
// that only one copy executes. Finishing rows are terminal and only released.
func (s *Service) ReconcileBackupTargetRestoreOperationsAfterRestart(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	if err := s.waitForBackupTargetRestoreRaftCatchUp(ctx); err != nil {
		return err
	}
	holder := s.backupJobOperationHolder()
	if holder == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}
	var operations []clusterModels.BackupTargetRestoreOperation
	if err := s.DB.Where("holder_node_id = ?", holder).
		Order("destination_dataset ASC, token ASC").
		Find(&operations).Error; err != nil {
		return err
	}

	var result error
	for _, operation := range operations {
		handle := backupTargetRestoreOperationHandle{
			Token: operation.Token, TargetID: operation.TargetID, HolderNodeID: operation.HolderNodeID,
			DestinationDataset: operation.DestinationDataset, RequestPayload: operation.RequestPayload,
		}
		switch operation.State {
		case clusterModels.BackupTargetRestoreOperationFinishing:
			if err := s.transitionDurableBackupTargetRestoreOperation(ctx, "release", handle); err != nil &&
				!backupTargetRestoreOperationStaleMessage(err) {
				result = errors.Join(result, err)
			}
			continue
		case clusterModels.BackupTargetRestoreOperationRunning:
			if err := s.transitionDurableBackupTargetRestoreOperation(ctx, "requeue", handle); err != nil {
				result = errors.Join(result, err)
				continue
			}
		case clusterModels.BackupTargetRestoreOperationQueued:
		case clusterModels.BackupTargetRestoreOperationCompleted:
			continue
		default:
			result = errors.Join(result, fmt.Errorf(
				"invalid_backup_target_restore_operation_state: token=%s state=%s",
				operation.Token,
				operation.State,
			))
			continue
		}

		var request backupTargetRestoreOperationRequest
		if err := json.Unmarshal([]byte(operation.RequestPayload), &request); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"decode_backup_target_restore_operation_%s: %w",
				operation.Token,
				err,
			))
			continue
		}
		restoreNetwork := request.RestoreNetwork
		payload := restoreFromTargetPayload{
			TargetID: request.TargetID, RemoteDataset: request.RemoteDataset, Snapshot: request.Snapshot,
			DestinationDataset: request.DestinationDataset, RestoreNetwork: &restoreNetwork,
			OperationToken: operation.Token, HolderNodeID: operation.HolderNodeID,
		}
		canonicalRequest, normalizedPayload, err := marshalBackupTargetRestoreOperationRequest(payload)
		if err != nil || canonicalRequest != strings.TrimSpace(operation.RequestPayload) {
			if err == nil {
				err = fmt.Errorf("backup_target_restore_operation_request_mismatch")
			}
			result = errors.Join(result, fmt.Errorf("reconcile_operation_%s: %w", operation.Token, err))
			continue
		}
		execution, err := s.restoreExecutionForOperation(operation.Token)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		normalizedPayload.EventID = execution.EventID
		normalizedPayload.AuditRecordID = execution.Audit.RecordID
		normalizedPayload.AuditOperationID = execution.OperationID
		if err := s.enqueueRestoreFromTargetOperation(ctx, normalizedPayload); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
