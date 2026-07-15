// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func TestAbortPreCutoverInterlockConvergesAfterTransientFailure(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   920,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationPreCutover,
		Token:     "migration:node-a:920",
		TaskID:    920,
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	calls := 0
	guard := &migrationWorkloadGuardStub{}
	guard.abortFn = func(_ context.Context, guestType string, guestID uint, token string) error {
		calls++
		if calls == 1 {
			return errors.New("leader changed")
		}
		return db.Where("guest_type = ? AND guest_id = ? AND token = ?", guestType, guestID, token).
			Delete(&clusterModels.ReplicationGuestOperation{}).Error
	}
	svc := &Service{DB: db, WorkloadGuard: guard}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := svc.abortPreCutoverInterlockConvergently(
		ctx, operation.GuestType, operation.GuestID, operation.Token,
	); err != nil {
		t.Fatalf("convergent abort: %v", err)
	}
	if calls != 2 {
		t.Fatalf("abort calls = %d, want 2", calls)
	}
	var got clusterModels.ReplicationGuestOperation
	if err := db.Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
		First(&got).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("operation still present: %v", err)
	}
}

func TestReconcileTerminalPreCutoverTaskAbortsExactToken(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationGuestOperation{},
		&taskModels.GuestLifecycleTask{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJail,
		GuestID:   921,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusFailed,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID:   task.GuestID,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationPreCutover,
		Token:     "migration:node-a:921",
		TaskID:    task.ID,
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	guard := &migrationWorkloadGuardStub{}
	guard.abortFn = func(_ context.Context, guestType string, guestID uint, token string) error {
		return db.Where("guest_type = ? AND guest_id = ? AND token = ?", guestType, guestID, token).
			Delete(&clusterModels.ReplicationGuestOperation{}).Error
	}
	svc := &Service{DB: db, WorkloadGuard: guard}
	if err := svc.reconcileMigrationOperation(t.Context(), operation); err != nil {
		t.Fatalf("reconcile operation: %v", err)
	}
	var count int64
	if err := db.Model(&clusterModels.ReplicationGuestOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("remaining operation count = %d, err=%v", count, err)
	}
}

func TestMigrationPhaseResumeOrdering(t *testing.T) {
	if migrationPhaseAtOrBefore(PhasePolicyAdjustment, PhaseStartTarget) {
		t.Fatal("resume would repeat an already completed target start")
	}
	if !migrationPhaseAtOrBefore(PhasePolicyAdjustment, PhaseCleanupSource) {
		t.Fatal("resume would skip pending source cleanup")
	}
	if !migrationPhaseAtOrBefore("", PhaseStopSource) {
		t.Fatal("unknown sealed phase must resume from source stop")
	}
}

func TestBindMigrationOperationTokenRejectsResumedPayloadMismatch(t *testing.T) {
	payload := migrationPayload{OperationToken: "migration:other-node:42"}
	if _, err := bindMigrationOperationToken(&payload, "node-a", 42); err == nil ||
		!strings.Contains(err.Error(), "migration_operation_token_mismatch") {
		t.Fatalf("mismatched persisted token result = %v", err)
	}
	if payload.OperationToken != "migration:other-node:42" {
		t.Fatalf("mismatched token was overwritten: %q", payload.OperationToken)
	}

	payload.OperationToken = ""
	token, err := bindMigrationOperationToken(&payload, "node-a", 42)
	if err != nil {
		t.Fatalf("bind new migration token: %v", err)
	}
	if token != "migration:node-a:42" || payload.OperationToken != token {
		t.Fatalf("bound token = %q, payload = %q", token, payload.OperationToken)
	}
}

func TestCutoverCheckpointMustPersistBeforeSeal(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	svc := &Service{DB: db}
	err := svc.persistTaskPhase(999, migrationPayload{
		TargetNodeUUID:     "node-b",
		OperationToken:     "migration:node-a:999",
		Phase:              PhaseInitialReplicaton,
		SourceDatasetRoots: []string{"zroot/sylve/virtual-machines/999"},
	})
	if err == nil {
		t.Fatal("missing task checkpoint was reported as durable")
	}
}

func TestCutoverCheckpointPersistsExactOperationToken(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   999,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed migration task: %v", err)
	}
	svc := &Service{DB: db}
	payload := migrationPayload{
		TargetNodeUUID:     "node-b",
		Phase:              PhaseInitialReplicaton,
		SourceDatasetRoots: []string{"zroot/sylve/virtual-machines/999"},
	}
	if err := svc.persistTaskPhase(task.ID, payload); err == nil ||
		!strings.Contains(err.Error(), "migration_operation_token_required") {
		t.Fatalf("token-less cutover checkpoint result = %v", err)
	}

	payload.OperationToken = fmt.Sprintf("migration:node-a:%d", task.ID)
	if err := svc.persistTaskPhase(task.ID, payload); err != nil {
		t.Fatalf("persist token-bound cutover checkpoint: %v", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload migration task: %v", err)
	}
	var persisted migrationPayload
	if err := json.Unmarshal([]byte(task.Payload), &persisted); err != nil {
		t.Fatalf("decode persisted checkpoint: %v", err)
	}
	if persisted.OperationToken != payload.OperationToken {
		t.Fatalf("persisted operation token = %q, want %q", persisted.OperationToken, payload.OperationToken)
	}
}

func TestDuplicateMigrationExecutionReturnsRetryPending(t *testing.T) {
	svc := &Service{active: map[uint]struct{}{77: {}}}
	err := svc.ExecuteMigration(t.Context(), 77)
	var pending *migrationRecoveryPendingError
	if !errors.As(err, &pending) || !errors.Is(err, ErrMigrationInProgress) {
		t.Fatalf("duplicate execution result = %v, want retry pending", err)
	}
}

func TestMigrationRecoveryRecognizesActiveExecution(t *testing.T) {
	svc := &Service{}
	if svc.isMigrationExecutionActive(77) {
		t.Fatal("unclaimed migration reported active")
	}
	if !svc.beginMigrationExecution(77) {
		t.Fatal("failed to claim migration execution")
	}
	if !svc.isMigrationExecutionActive(77) {
		t.Fatal("claimed migration was not reported active")
	}
	svc.endMigrationExecution(77)
	if svc.isMigrationExecutionActive(77) {
		t.Fatal("released migration still reported active")
	}
}

func TestMigrationRecoveryErrorLoggingClassification(t *testing.T) {
	if shouldLogMigrationRecoveryError(nil) {
		t.Fatal("nil recovery result should not be logged")
	}
	if shouldLogMigrationRecoveryError(&migrationRecoveryPendingError{cause: ErrMigrationInProgress}) {
		t.Fatal("active execution deferral should not be logged as a recovery failure")
	}
	if !shouldLogMigrationRecoveryError(&migrationRecoveryPendingError{cause: errors.New("leader unavailable")}) {
		t.Fatal("unexpected recovery failure should remain visible")
	}
}

func TestSealedMigrationRequiresDurableCheckpointBeforeEveryPhase(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	completeCalls := 0
	guard := &migrationWorkloadGuardStub{
		completeFn: func(context.Context, string, uint, string, string) error {
			completeCalls++
			return nil
		},
	}
	svc := &Service{DB: db, WorkloadGuard: guard}

	for _, test := range []struct {
		phase   string
		errCode string
	}{
		{PhaseStopSource, "migration_stop_source_checkpoint_failed"},
		{PhaseFinalSync, "migration_final_sync_checkpoint_failed"},
		{PhaseStartTarget, "migration_start_target_checkpoint_failed"},
		{PhasePolicyAdjustment, "migration_policy_adjustment_checkpoint_failed"},
		{PhaseCleanupSource, "migration_source_cleanup_checkpoint_failed"},
		{PhaseFinalize, "migration_finalize_checkpoint_failed"},
	} {
		t.Run(test.phase, func(t *testing.T) {
			payload := migrationPayload{
				TargetNodeUUID:     "node-b",
				OperationToken:     "migration:node-a:999",
				Phase:              test.phase,
				SourceDatasetRoots: []string{"zroot/sylve/virtual-machines/999"},
			}
			err := svc.executeSealedMigration(t.Context(), taskModels.GuestLifecycleTask{
				ID: 999, GuestType: taskModels.GuestTypeVM, GuestID: 999,
			}, &payload, "migration:node-a:999")
			if err == nil || !strings.Contains(err.Error(), test.errCode) {
				t.Fatalf("checkpoint failure = %v, want %s", err, test.errCode)
			}
		})
	}
	if completeCalls != 0 {
		t.Fatalf("cutover guard completed without a durable finalize checkpoint: calls=%d", completeCalls)
	}
}

func TestCancelMigrationRejectsDurableCutoverBeforePhaseCheckpoint(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   923,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
		Payload:   `{"targetNodeUuid":"node-b","phase":"initial_replication"}`,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed migration task: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        fmt.Sprintf("migration:node-a:%d", task.ID),
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       task.ID,
		AcquiredAt:   time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed sealed operation: %v", err)
	}

	svc := &Service{DB: db}
	if err := svc.CancelMigration(t.Context(), task.ID); !errors.Is(err, ErrCancelNotAllowed) {
		t.Fatalf("post-seal cancellation result = %v, want ErrCancelNotAllowed", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.OverrideRequested {
		t.Fatal("post-seal cancellation set override_requested")
	}
}

func TestFinalizeRecoveryDefersUnrelatedGuestOperation(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   924,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed migration task: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:   task.GuestType,
		GuestID:     task.GuestID,
		Operation:   clusterModels.ReplicationGuestOperationEmergencyRestore,
		State:       clusterModels.ReplicationGuestOperationPreCutover,
		Token:       "emergency_restore:fence-924",
		OwnerNodeID: "node-a",
		AcquiredAt:  time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("seed emergency restore operation: %v", err)
	}

	svc := &Service{DB: db}
	handled, err := svc.reconcileOperationAbsentFinalizeTask(t.Context(), task, migrationPayload{
		TargetNodeUUID:     "node-b",
		Phase:              PhaseFinalize,
		SourceDatasetRoots: []string{"zroot/sylve/virtual-machines/924"},
	})
	if !handled || err == nil || !strings.Contains(err.Error(), "guest_operation_in_progress") {
		t.Fatalf("finalize recovery result = handled:%v err:%v, want deferred operation", handled, err)
	}
}

func TestFinalizeRecoveryWithoutCompletionReceiptStaysPending(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   925,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed finalizing task: %v", err)
	}
	token := fmt.Sprintf("migration:node-a:%d", task.ID)
	task.Payload = fmt.Sprintf(
		`{"targetNodeUuid":"node-b","operationToken":%q,"phase":"finalize","sourceDatasetRoots":["zroot/sylve/virtual-machines/925"]}`,
		token,
	)
	if err := db.Model(&task).Update("payload", task.Payload).Error; err != nil {
		t.Fatalf("persist finalizing payload: %v", err)
	}

	svc := &Service{DB: db}
	err := svc.ExecuteMigration(t.Context(), task.ID)
	var pending *migrationRecoveryPendingError
	if !errors.As(err, &pending) ||
		!strings.Contains(err.Error(), "migration_finalize_recovery_completion_receipt_missing") {
		t.Fatalf("receipt-less finalize recovery result = %v, want recovery pending", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == taskModels.LifecycleTaskStatusSuccess ||
		task.Message != "migration_recovery_pending" || task.FinishedAt != nil {
		t.Fatalf(
			"receipt-less finalize recovery closed task: status=%q message=%q finishedAt=%v",
			task.Status,
			task.Message,
			task.FinishedAt,
		)
	}
}

func TestFinalizeRecoveryWithoutPersistedOperationTokenStaysPending(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   928,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
		Payload:   `{"targetNodeUuid":"node-b","phase":"finalize"}`,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed token-less finalizing task: %v", err)
	}

	svc := &Service{DB: db}
	err := svc.ExecuteMigration(t.Context(), task.ID)
	var pending *migrationRecoveryPendingError
	if !errors.As(err, &pending) ||
		!strings.Contains(err.Error(), "migration_finalize_recovery_operation_token_missing") {
		t.Fatalf("token-less finalize recovery result = %v, want recovery pending", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == taskModels.LifecycleTaskStatusSuccess {
		t.Fatal("token-less finalize recovery closed migration task")
	}
}

func TestFinalizeRecoveryRejectsMismatchedCompletionReceipt(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   926,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed finalizing task: %v", err)
	}
	token := fmt.Sprintf("migration:node-a:%d", task.ID)
	task.Payload = fmt.Sprintf(
		`{"targetNodeUuid":"node-b","operationToken":%q,"phase":"finalize"}`,
		token,
	)
	if err := db.Model(&task).Update("payload", task.Payload).Error; err != nil {
		t.Fatalf("persist finalizing payload: %v", err)
	}
	completedAt := time.Now().UTC()
	if err := db.Create(&clusterModels.ReplicationGuestOperationReceipt{
		Token:        token,
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		OwnerNodeID:  "wrong-source-node",
		TargetNodeID: "node-b",
		TaskID:       task.ID,
		AcquiredAt:   completedAt.Add(-time.Minute),
		CompletedAt:  completedAt,
	}).Error; err != nil {
		t.Fatalf("seed mismatched completion receipt: %v", err)
	}

	svc := &Service{DB: db}
	err := svc.ExecuteMigration(t.Context(), task.ID)
	var pending *migrationRecoveryPendingError
	if !errors.As(err, &pending) ||
		!strings.Contains(err.Error(), "migration_finalize_recovery_completion_receipt_missing") {
		t.Fatalf("mismatched finalize receipt result = %v, want recovery pending", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == taskModels.LifecycleTaskStatusSuccess {
		t.Fatal("mismatched completion receipt closed migration task")
	}
}

func TestExactMigrationCompletionReceiptRejectsWrongSourceAndStaleSuccess(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeJail,
		GuestID:   929,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed migration task: %v", err)
	}
	token := fmt.Sprintf("migration:node-a:%d", task.ID)
	now := time.Now().UTC()
	receipt := clusterModels.ReplicationGuestOperationReceipt{
		Token:        token,
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		OwnerNodeID:  "node-b",
		TargetNodeID: "node-c",
		TaskID:       task.ID,
		AcquiredAt:   now.Add(-time.Minute),
		CompletedAt:  now,
	}
	if err := db.Create(&receipt).Error; err != nil {
		t.Fatalf("seed wrong-source receipt: %v", err)
	}
	svc := &Service{DB: db}
	if found, err := svc.hasExactMigrationCompletionReceipt(
		t.Context(), task, "node-c", token,
	); err != nil || found {
		t.Fatalf("wrong-source receipt result = found:%v err:%v", found, err)
	}
	if err := db.Delete(&receipt).Error; err != nil {
		t.Fatalf("delete wrong-source receipt: %v", err)
	}

	staleToken := fmt.Sprintf("migration:node-a:%d", task.ID+1000)
	receipt.Token = staleToken
	receipt.OwnerNodeID = "node-a"
	receipt.TaskID = task.ID + 1000
	if err := db.Create(&receipt).Error; err != nil {
		t.Fatalf("seed stale success receipt: %v", err)
	}
	if found, err := svc.hasExactMigrationCompletionReceipt(
		t.Context(), task, "node-c", token,
	); err != nil || found {
		t.Fatalf("stale success receipt result = found:%v err:%v", found, err)
	}
}

func TestMigrationCompletionReceiptTokenCannotBeAmbiguous(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperationReceipt{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   930,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed migration task: %v", err)
	}
	token := fmt.Sprintf("migration:node-a:%d", task.ID)
	now := time.Now().UTC()
	receipt := clusterModels.ReplicationGuestOperationReceipt{
		Token:        token,
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       task.ID,
		AcquiredAt:   now.Add(-time.Minute),
		CompletedAt:  now,
	}
	if err := db.Create(&receipt).Error; err != nil {
		t.Fatalf("seed exact receipt: %v", err)
	}
	duplicate := receipt
	duplicate.TargetNodeID = "node-c"
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("dedicated receipt token primary key admitted an ambiguous second row")
	}
	svc := &Service{DB: db}
	if found, err := svc.hasExactMigrationCompletionReceipt(
		t.Context(), task, "node-b", token,
	); err != nil || !found {
		t.Fatalf("exact receipt result = found:%v err:%v", found, err)
	}
}

func TestExecuteMigrationReturnsSuccessfulTaskBeforePayloadValidation(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   927,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusSuccess,
		Message:   "migration_completed",
		Payload:   `{`,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed successful task: %v", err)
	}

	svc := &Service{DB: db}
	if err := svc.ExecuteMigration(t.Context(), task.ID); err != nil {
		t.Fatalf("stale successful task delivery returned error: %v", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusSuccess || task.Message != "migration_completed" {
		t.Fatalf("stale delivery overwrote successful task: status=%q message=%q", task.Status, task.Message)
	}
}
