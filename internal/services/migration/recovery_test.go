// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
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

func TestReconcileTerminalPreCutoverTaskCleansSnapshotsBeforeAbort(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationGuestOperation{},
		&taskModels.GuestLifecycleTask{},
	)
	payload, err := json.Marshal(migrationPayload{
		TargetNodeUUID:     "node-b",
		OperationToken:     "migration:node-a:922",
		Phase:              PhaseInitialReplicaton,
		SourceDatasetRoots: []string{"zroot/sylve/virtual-machines/922"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task := taskModels.GuestLifecycleTask{
		ID:        922,
		GuestType: taskModels.GuestTypeVM,
		GuestID:   922,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusFailed,
		Payload:   string(payload),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationPreCutover,
		Token:        "migration:node-a:922",
		TaskID:       task.ID,
		TargetNodeID: "node-b",
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	order := make([]string, 0, 2)
	guard := &migrationWorkloadGuardStub{}
	guard.abortFn = func(_ context.Context, guestType string, guestID uint, token string) error {
		order = append(order, "abort")
		return db.Where("guest_type = ? AND guest_id = ? AND token = ?", guestType, guestID, token).
			Delete(&clusterModels.ReplicationGuestOperation{}).Error
	}
	svc := &Service{
		DB:            db,
		WorkloadGuard: guard,
		preCutoverSnapshotCleanup: func(_ context.Context, gotTask taskModels.GuestLifecycleTask, gotPayload migrationPayload) error {
			order = append(order, "cleanup")
			if gotTask.ID != task.ID || !sameMigrationDatasetRootManifest(gotPayload.SourceDatasetRoots, []string{"zroot/sylve/virtual-machines/922"}) {
				return fmt.Errorf("unexpected_cleanup_scope")
			}
			return nil
		},
	}
	if err := svc.reconcileMigrationOperation(t.Context(), operation); err != nil {
		t.Fatalf("reconcile operation: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"cleanup", "abort"}) {
		t.Fatalf("reconciliation order = %v", order)
	}
}

func TestReconcileTerminalPreCutoverTaskRetainsGuardWhenSnapshotCleanupFails(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationGuestOperation{},
		&taskModels.GuestLifecycleTask{},
	)
	payload, err := json.Marshal(migrationPayload{
		TargetNodeUUID:     "node-b",
		OperationToken:     "migration:node-a:923",
		Phase:              PhaseInitialReplicaton,
		SourceDatasetRoots: []string{"zroot/sylve/jails/923"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task := taskModels.GuestLifecycleTask{
		ID:        923,
		GuestType: taskModels.GuestTypeJail,
		GuestID:   923,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusFailed,
		Payload:   string(payload),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeJail,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationPreCutover,
		Token:        "migration:node-a:923",
		TaskID:       task.ID,
		TargetNodeID: "node-b",
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}
	abortCalls := 0
	guard := &migrationWorkloadGuardStub{}
	guard.abortFn = func(context.Context, string, uint, string) error {
		abortCalls++
		return nil
	}
	svc := &Service{
		DB:            db,
		WorkloadGuard: guard,
		preCutoverSnapshotCleanup: func(context.Context, taskModels.GuestLifecycleTask, migrationPayload) error {
			return errors.New("target unavailable")
		},
	}
	err = svc.reconcileMigrationOperation(t.Context(), operation)
	if err == nil || !strings.Contains(err.Error(), "migration_pre_cutover_snapshot_cleanup_failed") {
		t.Fatalf("reconcile result = %v", err)
	}
	if abortCalls != 0 {
		t.Fatalf("abort calls = %d, want 0", abortCalls)
	}
	var count int64
	if err := db.Model(&clusterModels.ReplicationGuestOperation{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("remaining operation count = %d, err=%v", count, err)
	}
}

func TestMigrationPhaseResumeOrdering(t *testing.T) {
	if !migrationPhaseAtOrBefore(PhaseOwnershipTransfer, PhaseStartTarget) {
		t.Fatal("identity ownership transfer would not precede target import")
	}
	if migrationPhaseAtOrBefore(PhaseStartTarget, PhaseOwnershipTransfer) {
		t.Fatal("ordinary phase ordering would move backward from target import")
	}
	if !migrationPhaseAtOrBefore(PhaseStartTarget, PhasePolicyAdjustment) {
		t.Fatal("backup policy adjustment would precede target import")
	}
	if !migrationPhaseAtOrBefore(PhasePolicyAdjustment, PhaseCleanupSource) {
		t.Fatal("resume would skip pending source cleanup")
	}
	if !migrationPhaseAtOrBefore("", PhaseStopSource) {
		t.Fatal("unknown sealed phase must resume from source stop")
	}
}

func TestSealedMigrationTransfersOwnershipBeforeTargetImportAndRetry(t *testing.T) {
	for _, test := range []struct {
		name      string
		guestType string
		path      string
		dataset   string
	}{
		{
			name:      "vm",
			guestType: taskModels.GuestTypeVM,
			path:      "/api/intra-cluster/migration/import-vm",
			dataset:   "zroot/sylve/virtual-machines/940",
		},
		{
			name:      "jail",
			guestType: taskModels.GuestTypeJail,
			path:      "/api/intra-cluster/migration/import-jail",
			dataset:   "zroot/sylve/jails/940",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testutil.NewSQLiteTestDB(t,
				&taskModels.GuestLifecycleTask{},
				&clusterModels.ClusterNode{},
			)
			task := taskModels.GuestLifecycleTask{
				GuestType: test.guestType,
				GuestID:   940,
				Action:    "migrate",
				Source:    taskModels.LifecycleTaskSourceUser,
				Status:    taskModels.LifecycleTaskStatusRunning,
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatalf("seed migration task: %v", err)
			}

			operationToken := fmt.Sprintf("migration:node-a:%d", task.ID)
			ownershipTransfers := make(chan struct{}, 2)
			guard := &migrationWorkloadGuardStub{
				identityFn: func(_ context.Context, guestType string, guestID uint, newOwnerNodeID, token string) error {
					if guestType != test.guestType || guestID != task.GuestID ||
						newOwnerNodeID != "node-b" || token != operationToken {
						t.Errorf(
							"ownership transfer = (%q, %d, %q, %q)",
							guestType,
							guestID,
							newOwnerNodeID,
							token,
						)
					}
					ownershipTransfers <- struct{}{}
					return nil
				},
			}

			importRequests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Errorf("target import request = %s %s", r.Method, r.URL.Path)
				}
				select {
				case <-ownershipTransfers:
				default:
					t.Error("target import ran before ownership transfer")
				}

				var request struct {
					GuestID            uint     `json:"guestId"`
					OperationToken     string   `json:"operationToken"`
					StartGuest         *bool    `json:"startGuest"`
					SourceDatasetRoots []string `json:"sourceDatasetRoots"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode target import request: %v", err)
				}
				importRequests++
				if importRequests == 1 {
					http.Error(w, "temporary target failure", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(targetMigrationImportReceipt{
					Status:             "success",
					GuestID:            request.GuestID,
					OperationToken:     request.OperationToken,
					StartGuest:         request.StartGuest,
					SourceDatasetRoots: request.SourceDatasetRoots,
				})
			}))
			defer server.Close()

			if err := db.Create(&clusterModels.ClusterNode{
				NodeUUID: "node-b",
				API:      strings.TrimPrefix(server.URL, "https://"),
			}).Error; err != nil {
				t.Fatalf("seed target node: %v", err)
			}
			svc := &Service{
				DB:            db,
				Cluster:       &clusterService.Service{AuthService: &migrationTargetAuthStub{}},
				WorkloadGuard: guard,
			}
			originalRunning := false
			payload := migrationPayload{
				TargetNodeUUID:     "node-b",
				OperationToken:     operationToken,
				OriginalRunning:    &originalRunning,
				Phase:              PhaseOwnershipTransfer,
				SourceDatasetRoots: []string{test.dataset},
			}

			err := svc.executeSealedMigration(t.Context(), task, &payload, operationToken)
			if err == nil || !strings.Contains(err.Error(), "import_on_target_failed") {
				t.Fatalf("first target import result = %v", err)
			}
			if payload.Phase != PhaseStartTarget {
				t.Fatalf("phase after target failure = %q", payload.Phase)
			}

			err = svc.executeSealedMigration(t.Context(), task, &payload, operationToken)
			if err == nil || !strings.Contains(err.Error(), "migration_cleanup_unavailable") {
				t.Fatalf("target import retry result = %v", err)
			}
			if importRequests != 2 || guard.identityCalls != 2 || guard.ownershipCalls != 1 {
				t.Fatalf("calls = imports:%d identity:%d policies:%d, want 2/2/1",
					importRequests, guard.identityCalls, guard.ownershipCalls)
			}
		})
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
	if shouldLogMigrationRecoveryError(clusterService.ErrNodeReaddressFenced) {
		t.Fatal("cluster lifecycle fence should not be logged as a recovery failure")
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
		{PhaseOwnershipTransfer, "migration_ownership_transfer_checkpoint_failed"},
		{PhaseStartTarget, "migration_ownership_transfer_checkpoint_failed"},
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
