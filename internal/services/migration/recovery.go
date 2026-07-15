// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

const migrationRecoveryInterval = 15 * time.Second

type migrationRecoveryPendingError struct {
	cause error
}

func (e *migrationRecoveryPendingError) Error() string {
	if e == nil || e.cause == nil {
		return "migration_recovery_pending"
	}
	return fmt.Sprintf("migration_recovery_pending: %v", e.cause)
}

func (e *migrationRecoveryPendingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// LifecycleRetryPending is intentionally a structural interface so the
// lifecycle package can preserve the task as running without importing this
// package and creating a dependency cycle.
func (e *migrationRecoveryPendingError) LifecycleRetryPending() bool { return true }

type migrationTaskResultPersistedError struct {
	cause error
}

func (e *migrationTaskResultPersistedError) Error() string {
	if e == nil || e.cause == nil {
		return "migration_result_already_persisted"
	}
	return e.cause.Error()
}

func (e *migrationTaskResultPersistedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *migrationTaskResultPersistedError) LifecycleResultAlreadyPersisted() bool { return true }

func (s *Service) beginMigrationExecution(taskID uint) bool {
	if s == nil || taskID == 0 {
		return false
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.active == nil {
		s.active = make(map[uint]struct{})
	}
	if _, exists := s.active[taskID]; exists {
		return false
	}
	s.active[taskID] = struct{}{}
	return true
}

func (s *Service) endMigrationExecution(taskID uint) {
	s.activeMu.Lock()
	delete(s.active, taskID)
	s.activeMu.Unlock()
}

func (s *Service) isMigrationExecutionActive(taskID uint) bool {
	if s == nil || taskID == 0 {
		return false
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	_, active := s.active[taskID]
	return active
}

func shouldLogMigrationRecoveryError(err error) bool {
	return err != nil && !errors.Is(err, ErrMigrationInProgress)
}

func (s *Service) exactMigrationOperationForTask(task taskModels.GuestLifecycleTask, targetNodeID, token string) (*clusterModels.ReplicationGuestOperation, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("migration_operation_lookup_unavailable")
	}
	var operation clusterModels.ReplicationGuestOperation
	err := s.DB.Where("guest_type = ? AND guest_id = ?", task.GuestType, task.GuestID).
		First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if operation.Operation != clusterModels.ReplicationGuestOperationMigration ||
		operation.TaskID != task.ID ||
		strings.TrimSpace(operation.Token) != strings.TrimSpace(token) {
		return nil, fmt.Errorf("migration_operation_identity_mismatch")
	}
	if strings.TrimSpace(targetNodeID) != "" &&
		strings.TrimSpace(operation.TargetNodeID) != strings.TrimSpace(targetNodeID) {
		return nil, fmt.Errorf("migration_operation_identity_mismatch")
	}
	return &operation, nil
}

func (s *Service) waitForExactMigrationOperationAbsent(ctx context.Context, guestType string, guestID uint, token string) error {
	if ctx == nil || s == nil || s.DB == nil || guestID == 0 || strings.TrimSpace(token) == "" {
		return fmt.Errorf("migration_operation_release_check_invalid")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var operation clusterModels.ReplicationGuestOperation
		err := s.DB.Where("guest_type = ? AND guest_id = ?", guestType, guestID).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(operation.Token) != strings.TrimSpace(token) {
			return fmt.Errorf("migration_operation_release_token_mismatch")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) abortPreCutoverInterlockConvergently(ctx context.Context, guestType string, guestID uint, token string) error {
	if s == nil || s.WorkloadGuard == nil {
		return fmt.Errorf("migration_workload_guard_unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	retry := time.NewTicker(time.Second)
	defer retry.Stop()
	var lastErr error
	for {
		if err := s.WorkloadGuard.AbortGuestMigrationInterlock(ctx, guestType, guestID, token); err != nil {
			lastErr = err
		} else if err := s.waitForExactMigrationOperationAbsent(ctx, guestType, guestID, token); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return fmt.Errorf("migration_pre_cutover_interlock_abort_unconfirmed: %w", lastErr)
		case <-retry.C:
		}
	}
}

// StartRecoveryTicker retries durable migration operations. Pre-cutover rows
// are aborted only for terminal/missing tasks; cutover rows are never expired
// and instead resume using their original task and exact token.
func (s *Service) StartRecoveryTicker(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.scheduleMigrationRecovery(ctx)
	ticker := time.NewTicker(migrationRecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleMigrationRecovery(ctx)
		}
	}
}

func (s *Service) scheduleMigrationRecovery(ctx context.Context) {
	if err := s.ReconcileMigrationOperations(ctx); err != nil {
		logger.L.Error().Err(err).Msg("migration_operation_reconciliation_failed")
	}
}

func (s *Service) ReconcileMigrationOperations(ctx context.Context) error {
	if s == nil || s.DB == nil || s.Cluster == nil || s.WorkloadGuard == nil {
		return fmt.Errorf("migration_reconciliation_unavailable")
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("local_node_id_unavailable")
	}

	var operations []clusterModels.ReplicationGuestOperation
	if err := s.DB.Where("operation = ? AND owner_node_id = ?",
		clusterModels.ReplicationGuestOperationMigration, localNodeID).
		Find(&operations).Error; err != nil {
		return err
	}
	for i := range operations {
		operation := operations[i]
		if s.isMigrationExecutionActive(operation.TaskID) {
			continue
		}
		go func() {
			if err := s.reconcileMigrationOperation(ctx, operation); err != nil {
				// The lifecycle worker may have claimed the task after the active
				// check above. Its execution guard is authoritative; this is an
				// expected deferral, not a recovery failure.
				if !shouldLogMigrationRecoveryError(err) {
					return
				}
				logger.L.Error().Err(err).
					Str("guest_type", operation.GuestType).
					Uint("guest_id", operation.GuestID).
					Uint("task_id", operation.TaskID).
					Str("state", operation.State).
					Msg("migration_operation_recovery_attempt_failed")
			}
		}()
	}
	return s.reconcileCompletedMigrationTasks(ctx)
}

func (s *Service) reconcileMigrationOperation(ctx context.Context, operation clusterModels.ReplicationGuestOperation) error {
	var task taskModels.GuestLifecycleTask
	err := s.DB.First(&task, operation.TaskID).Error
	if operation.State == clusterModels.ReplicationGuestOperationPreCutover {
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil &&
			(task.Status == taskModels.LifecycleTaskStatusFailed || task.Status == taskModels.LifecycleTaskStatusSuccess)) {
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			return s.abortPreCutoverInterlockConvergently(
				abortCtx, operation.GuestType, operation.GuestID, operation.Token,
			)
		}
		if err != nil {
			return err
		}
		return s.ExecuteMigration(ctx, task.ID)
	}

	if operation.State != clusterModels.ReplicationGuestOperationCutover {
		return fmt.Errorf("invalid_migration_operation_state: %s", operation.State)
	}
	if err != nil {
		return fmt.Errorf("sealed_migration_task_unavailable: %w", err)
	}
	return s.ExecuteMigration(ctx, task.ID)
}

// reconcileOperationAbsentFinalizeTask closes the crash window after the
// durable cutover operation has been completed but before the lifecycle task
// result was persisted. A finalize checkpoint without an operation must never
// be interpreted as authority to start a fresh migration: all completion
// postconditions are re-proven before the existing task is marked successful.
func (s *Service) reconcileOperationAbsentFinalizeTask(
	ctx context.Context,
	task taskModels.GuestLifecycleTask,
	payload migrationPayload,
) (bool, error) {
	if strings.TrimSpace(payload.Phase) != PhaseFinalize {
		return false, nil
	}
	if s == nil || s.DB == nil || strings.TrimSpace(payload.TargetNodeUUID) == "" {
		return true, fmt.Errorf("migration_finalize_recovery_checkpoint_invalid")
	}

	var operation clusterModels.ReplicationGuestOperation
	operationErr := s.DB.WithContext(ctx).
		Where("guest_type = ? AND guest_id = ?", task.GuestType, task.GuestID).
		First(&operation).Error
	if operationErr == nil {
		if operation.Operation == clusterModels.ReplicationGuestOperationMigration &&
			operation.TaskID == task.ID {
			return false, nil
		}
		return true, fmt.Errorf(
			"migration_finalize_recovery_guest_operation_in_progress: %s",
			strings.TrimSpace(operation.Operation),
		)
	}
	if !errors.Is(operationErr, gorm.ErrRecordNotFound) {
		return true, fmt.Errorf("migration_finalize_recovery_guard_lookup_failed: %w", operationErr)
	}
	if strings.TrimSpace(payload.OperationToken) == "" ||
		payload.OperationToken != strings.TrimSpace(payload.OperationToken) {
		return true, fmt.Errorf("migration_finalize_recovery_operation_token_missing")
	}

	receiptFound, err := s.hasExactMigrationCompletionReceipt(
		ctx, task, payload.TargetNodeUUID, payload.OperationToken,
	)
	if err != nil {
		return true, fmt.Errorf("migration_finalize_recovery_receipt_lookup_failed: %w", err)
	}
	if !receiptFound {
		return true, fmt.Errorf("migration_finalize_recovery_completion_receipt_missing")
	}

	if err := s.persistRecoveredMigrationSuccess(ctx, task.ID); err != nil {
		return true, err
	}
	return true, nil
}

// hasExactMigrationCompletionReceipt proves that Complete removed the exact
// cutover operation for this task. Source datasets are intentionally not part
// of the proof: once replication is re-enabled, the former source can
// legitimately contain a newly-created read-only replica of the guest.
func (s *Service) hasExactMigrationCompletionReceipt(
	ctx context.Context,
	task taskModels.GuestLifecycleTask,
	targetNodeID string,
	operationToken string,
) (bool, error) {
	if s == nil || s.DB == nil || task.ID == 0 || task.GuestID == 0 ||
		strings.TrimSpace(task.GuestType) == "" ||
		strings.TrimSpace(targetNodeID) == "" || targetNodeID != strings.TrimSpace(targetNodeID) ||
		strings.TrimSpace(operationToken) == "" || operationToken != strings.TrimSpace(operationToken) {
		return false, fmt.Errorf("migration_completion_receipt_lookup_invalid")
	}

	var receipt clusterModels.ReplicationGuestOperationReceipt
	if err := s.DB.WithContext(ctx).
		Where("token = ?", operationToken).
		First(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	ownerNodeID := strings.TrimSpace(receipt.OwnerNodeID)
	expectedToken := fmt.Sprintf("migration:%s:%d", ownerNodeID, task.ID)
	if ownerNodeID == "" || receipt.OwnerNodeID != ownerNodeID ||
		receipt.Token != operationToken || receipt.Token != expectedToken ||
		receipt.GuestType != task.GuestType || receipt.GuestID != task.GuestID ||
		receipt.Operation != clusterModels.ReplicationGuestOperationMigration ||
		receipt.TargetNodeID != targetNodeID || receipt.TaskID != task.ID ||
		receipt.AcquiredAt.IsZero() || receipt.CompletedAt.IsZero() {
		return false, nil
	}
	return true, nil
}

func (s *Service) persistRecoveredMigrationSuccess(ctx context.Context, taskID uint) error {
	finishedAt := time.Now().UTC()
	result := s.DB.WithContext(ctx).Model(&taskModels.GuestLifecycleTask{}).
		Where("id = ? AND action = ? AND status IN ?", taskID, "migrate", []string{
			taskModels.LifecycleTaskStatusQueued,
			taskModels.LifecycleTaskStatusRunning,
			taskModels.LifecycleTaskStatusFailed,
		}).Updates(map[string]any{
		"status":      taskModels.LifecycleTaskStatusSuccess,
		"finished_at": finishedAt,
		"message":     "migration_completed",
		"error":       "",
	})
	if result.Error != nil {
		return fmt.Errorf("migration_finalize_recovery_result_persist_failed: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return nil
	}

	var current taskModels.GuestLifecycleTask
	if err := s.DB.WithContext(ctx).First(&current, taskID).Error; err != nil {
		return fmt.Errorf("migration_finalize_recovery_result_unconfirmed: %w", err)
	}
	if current.Status != taskModels.LifecycleTaskStatusSuccess {
		return fmt.Errorf("migration_finalize_recovery_result_unconfirmed")
	}
	return nil
}

// reconcileCompletedMigrationTasks closes the narrow crash window after Raft
// has removed a completed cutover guard but before the source task was marked
// successful. Finalize is the only eligible phase, and all Complete
// transaction postconditions are captured by an atomic completion receipt
// before the cutover operation disappears. Current policy, lease, and source
// dataset state are intentionally not used because replication may already
// have resumed after migration completion.
func (s *Service) reconcileCompletedMigrationTasks(ctx context.Context) error {
	var tasks []taskModels.GuestLifecycleTask
	if err := s.DB.Where("action = ? AND status IN ?", "migrate", []string{
		taskModels.LifecycleTaskStatusRunning,
		taskModels.LifecycleTaskStatusFailed,
	}).Find(&tasks).Error; err != nil {
		return err
	}
	for i := range tasks {
		task := tasks[i]
		var payload migrationPayload
		if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil ||
			payload.Phase != PhaseFinalize ||
			strings.TrimSpace(payload.TargetNodeUUID) == "" ||
			strings.TrimSpace(payload.OperationToken) == "" ||
			payload.OperationToken != strings.TrimSpace(payload.OperationToken) {
			continue
		}
		var operationCount int64
		if err := s.DB.Model(&clusterModels.ReplicationGuestOperation{}).
			Where("guest_type = ? AND guest_id = ?", task.GuestType, task.GuestID).
			Count(&operationCount).Error; err != nil {
			return err
		}
		if operationCount != 0 {
			continue
		}
		receiptFound, err := s.hasExactMigrationCompletionReceipt(
			ctx, task, payload.TargetNodeUUID, payload.OperationToken,
		)
		if err != nil {
			return err
		}
		if !receiptFound {
			continue
		}
		if err := s.persistRecoveredMigrationSuccess(ctx, task.ID); err != nil {
			return err
		}
	}
	return nil
}
