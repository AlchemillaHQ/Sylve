// SPDX-License-Identifier: BSD-2-Clause

package migrationHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	goLibvirt "github.com/digitalocean/go-libvirt"
	"github.com/gin-gonic/gin"
)

func runTargetMigrationImportRequest(
	t *testing.T,
	handler gin.HandlerFunc,
	guestID uint,
	operationToken string,
	startGuest bool,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(targetMigrationImportRequest{
		GuestID:            guestID,
		OperationToken:     operationToken,
		StartGuest:         &startGuest,
		SourceDatasetRoots: []string{"pool/sylve/virtual-machines/1"},
	})
	if err != nil {
		t.Fatalf("marshal target import request: %v", err)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/intra-cluster/migration/import", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(ctx)
	return recorder
}

func TestTargetMigrationImportSerializesSameGuest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var active atomic.Bool
	var imports atomic.Int32
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})

	ops := targetMigrationImportOperations{
		GuestType:            clusterModels.ReplicationGuestTypeVM,
		ImportedMessage:      "vm_imported_and_started",
		AlreadyActiveMessage: "vm_already_imported_and_active",
		Authorize:            func(context.Context, uint, string) error { return nil },
		ValidateRoots: func(_ context.Context, _ uint, roots []string) ([]string, error) {
			return append([]string(nil), roots...), nil
		},
		RuntimeState: func(uint) (targetMigrationRuntimeState, error) {
			if active.Load() {
				return targetMigrationRuntimeActive, nil
			}
			return targetMigrationRuntimeInactive, nil
		},
		Import: func(context.Context, uint, []string) ([]string, error) {
			if imports.Add(1) == 1 {
				close(entered)
			}
			current := concurrent.Add(1)
			for {
				maximum := maxConcurrent.Load()
				if current <= maximum || maxConcurrent.CompareAndSwap(maximum, current) {
					break
				}
			}
			<-release
			concurrent.Add(-1)
			return nil, nil
		},
		SetIntentionalStop: func(uint, bool) error { return nil },
		Start: func(uint) error {
			active.Store(true)
			return nil
		},
	}
	handler := targetMigrationImportHandler(ops)

	results := make(chan *httptest.ResponseRecorder, 2)
	go func() {
		results <- runTargetMigrationImportRequest(t, handler, 1, "migration:source:1", true)
	}()
	<-entered
	go func() {
		results <- runTargetMigrationImportRequest(t, handler, 1, "migration:source:1", true)
	}()

	time.Sleep(50 * time.Millisecond)
	close(release)
	for range 2 {
		response := <-results
		if response.Code != http.StatusOK {
			t.Fatalf("serialized import returned %d: %s", response.Code, response.Body.String())
		}
	}
	if got := maxConcurrent.Load(); got != 1 {
		t.Fatalf("maximum concurrent imports = %d, want 1", got)
	}
	if got := imports.Load(); got != 1 {
		t.Fatalf("imports = %d, want one import plus one active retry", got)
	}
}

func TestTargetMigrationGuestLockSetReleasesEntries(t *testing.T) {
	locks := &targetMigrationGuestLockSet{}
	release := locks.acquire(77)
	locks.mu.Lock()
	countWhileHeld := len(locks.locks)
	locks.mu.Unlock()
	if countWhileHeld != 1 {
		t.Fatalf("lock entries while held = %d, want 1", countWhileHeld)
	}
	release()
	locks.mu.Lock()
	countAfterRelease := len(locks.locks)
	locks.mu.Unlock()
	if countAfterRelease != 0 {
		t.Fatalf("lock entries after release = %d, want 0", countAfterRelease)
	}
}

func TestTargetMigrationImportPreservesStoppedState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, initiallyActive := range []bool{false, true} {
		initiallyActive := initiallyActive
		name := "inactive_target"
		if initiallyActive {
			name = "active_target_rejected"
		}
		t.Run(name, func(t *testing.T) {
			active := initiallyActive
			imports := 0
			starts := 0
			intentionalStop := false
			ops := targetMigrationImportOperations{
				GuestType:              clusterModels.ReplicationGuestTypeVM,
				ImportedMessage:        "vm_imported_and_started",
				ImportedStoppedMessage: "vm_imported_and_left_stopped",
				AlreadyActiveMessage:   "vm_already_imported_and_active",
				Authorize:              func(context.Context, uint, string) error { return nil },
				ValidateRoots: func(_ context.Context, _ uint, roots []string) ([]string, error) {
					return append([]string(nil), roots...), nil
				},
				RuntimeState: func(uint) (targetMigrationRuntimeState, error) {
					if active {
						return targetMigrationRuntimeActive, nil
					}
					return targetMigrationRuntimeInactive, nil
				},
				Import: func(context.Context, uint, []string) ([]string, error) {
					imports++
					return nil, nil
				},
				SetIntentionalStop: func(_ uint, stopped bool) error {
					intentionalStop = stopped
					return nil
				},
				Start: func(uint) error {
					starts++
					active = true
					return nil
				},
			}

			recorder := runTargetMigrationImportRequest(
				t, targetMigrationImportHandler(ops), 1, "migration:source:1", false,
			)
			if initiallyActive {
				if recorder.Code != http.StatusConflict {
					t.Fatalf("active target status = %d, want conflict: %s", recorder.Code, recorder.Body.String())
				}
				if imports != 0 || starts != 0 {
					t.Fatalf("active target import/start calls = %d/%d, want 0/0", imports, starts)
				}
				return
			}

			if recorder.Code != http.StatusOK {
				t.Fatalf("stopped import status = %d, want OK: %s", recorder.Code, recorder.Body.String())
			}
			if imports != 1 || starts != 0 || active || !intentionalStop {
				t.Fatalf(
					"stopped import calls/active/intent = %d/%d/%v/%v, want 1/0/false/true",
					imports, starts, active, intentionalStop,
				)
			}
			var response struct {
				Message    string `json:"message"`
				StartGuest *bool  `json:"startGuest"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("parse stopped import response: %v", err)
			}
			if response.Message != "vm_imported_and_left_stopped" || response.StartGuest == nil || *response.StartGuest {
				t.Fatalf("unexpected stopped import receipt: %+v", response)
			}
		})
	}
}

func TestClassifyMigratedVMRuntimeStateFailsClosedOutsideRunningAndShutoff(t *testing.T) {
	if got := classifyMigratedVMRuntimeState(goLibvirt.DomainRunning); got != targetMigrationRuntimeActive {
		t.Fatalf("running state = %q", got)
	}
	if got := classifyMigratedVMRuntimeState(goLibvirt.DomainShutoff); got != targetMigrationRuntimeInactive {
		t.Fatalf("shutoff state = %q", got)
	}
	for _, state := range []goLibvirt.DomainState{
		goLibvirt.DomainNostate,
		goLibvirt.DomainBlocked,
		goLibvirt.DomainPaused,
		goLibvirt.DomainShutdown,
		goLibvirt.DomainCrashed,
		goLibvirt.DomainPmsuspended,
	} {
		if got := classifyMigratedVMRuntimeState(state); got != targetMigrationRuntimeUnsafe {
			t.Fatalf("state %d classified as %q, want unsafe", state, got)
		}
	}
}

func TestRequireExactMigrationTargetCutover(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
	now := time.Now().UTC()
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    clusterModels.ReplicationGuestTypeVM,
		GuestID:      901,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        "migration:node-a:901",
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       901,
		AcquiredAt:   now,
		SealedAt:     &now,
	}
	if err := db.Create(&operation).Error; err != nil {
		t.Fatalf("seed migration cutover operation: %v", err)
	}

	if err := requireExactMigrationTargetCutover(
		context.Background(), db, "node-b", clusterModels.ReplicationGuestTypeVM, 901, operation.Token,
	); err != nil {
		t.Fatalf("exact target cutover guard rejected: %v", err)
	}

	tests := []struct {
		name      string
		nodeID    string
		guestType string
		guestID   uint
		token     string
	}{
		{name: "wrong token", nodeID: "node-b", guestType: "vm", guestID: 901, token: "stale-token"},
		{name: "wrong target", nodeID: "node-c", guestType: "vm", guestID: 901, token: operation.Token},
		{name: "wrong guest type", nodeID: "node-b", guestType: "jail", guestID: 901, token: operation.Token},
		{name: "wrong guest id", nodeID: "node-b", guestType: "vm", guestID: 902, token: operation.Token},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := requireExactMigrationTargetCutover(
				context.Background(), db, test.nodeID, test.guestType, test.guestID, test.token,
			); err == nil {
				t.Fatal("non-exact target cutover guard was accepted")
			}
		})
	}

	if err := db.Model(&clusterModels.ReplicationGuestOperation{}).
		Where("guest_type = ? AND guest_id = ?", operation.GuestType, operation.GuestID).
		Update("state", clusterModels.ReplicationGuestOperationPreCutover).Error; err != nil {
		t.Fatalf("move operation back to pre-cutover: %v", err)
	}
	if err := requireExactMigrationTargetCutover(
		context.Background(), db, "node-b", clusterModels.ReplicationGuestTypeVM, 901, operation.Token,
	); err == nil {
		t.Fatal("pre-cutover guard authorized target import")
	}
}

func TestTargetMigrationImportIsTokenScopedAndIdempotentForVMAndJail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, guestType := range []string{
		clusterModels.ReplicationGuestTypeVM,
		clusterModels.ReplicationGuestTypeJail,
	} {
		guestType := guestType
		t.Run(guestType, func(t *testing.T) {
			tests := []struct {
				name                  string
				requestGuestID        uint
				requestToken          string
				operationState        string
				initiallyActive       bool
				unsafeRuntime         bool
				importRemovesGuard    bool
				startActivatesWithErr bool
				wantStatus            int
				wantMessage           string
				wantActiveChecks      int
				wantImports           int
				wantStarts            int
			}{
				{
					name: "first import", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover,
					wantStatus:     http.StatusOK, wantMessage: guestType + "_imported_and_started",
					wantActiveChecks: 2, wantImports: 1, wantStarts: 1,
				},
				{
					name: "lost response retry already active", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantStatus: http.StatusOK, wantMessage: guestType + "_already_imported_and_active",
					wantActiveChecks: 1,
				},
				{
					name: "stale token cannot claim active guest", requestGuestID: 1, requestToken: "stale-token",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantStatus: http.StatusConflict, wantMessage: "migration_target_cutover_guard_rejected",
				},
				{
					name: "wrong guest cannot reuse token", requestGuestID: 2, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantStatus: http.StatusConflict, wantMessage: "migration_target_cutover_guard_rejected",
				},
				{
					name: "pre-cutover cannot import", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationPreCutover,
					wantStatus:     http.StatusConflict, wantMessage: "migration_target_cutover_guard_rejected",
				},
				{
					name: "non-quiescent runtime cannot be re-imported", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, unsafeRuntime: true,
					wantStatus: http.StatusConflict, wantMessage: "migration_target_runtime_state_not_safe_for_import",
					wantActiveChecks: 1,
				},
				{
					name: "guard removed during import cannot start", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, importRemovesGuard: true,
					wantStatus: http.StatusConflict, wantMessage: "migration_target_cutover_guard_rejected",
					wantActiveChecks: 1, wantImports: 1,
				},
				{
					name: "start side effect wins over returned error", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, startActivatesWithErr: true,
					wantStatus: http.StatusOK, wantMessage: guestType + "_already_imported_and_active",
					wantActiveChecks: 2, wantImports: 1, wantStarts: 1,
				},
				{
					name: "missing token", requestGuestID: 1,
					operationState: clusterModels.ReplicationGuestOperationCutover,
					wantStatus:     http.StatusBadRequest,
					wantMessage:    "guest_id_operation_token_start_state_and_dataset_roots_required",
				},
			}

			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					db := testutil.NewSQLiteTestDB(t, &clusterModels.ReplicationGuestOperation{})
					now := time.Now().UTC()
					operation := clusterModels.ReplicationGuestOperation{
						GuestType: guestType, GuestID: 1,
						Operation: clusterModels.ReplicationGuestOperationMigration,
						State:     test.operationState, Token: "migration:source:1",
						OwnerNodeID: "source", TargetNodeID: "target", TaskID: 1,
						AcquiredAt: now, SealedAt: &now,
					}
					if err := db.Create(&operation).Error; err != nil {
						t.Fatalf("seed guest operation: %v", err)
					}

					active := test.initiallyActive
					activeChecks := 0
					imports := 0
					starts := 0
					ops := targetMigrationImportOperations{
						GuestType:            guestType,
						ImportedMessage:      guestType + "_imported_and_started",
						AlreadyActiveMessage: guestType + "_already_imported_and_active",
						Authorize: func(ctx context.Context, guestID uint, operationToken string) error {
							return requireExactMigrationTargetCutover(
								ctx, db, "target", guestType, guestID, operationToken,
							)
						},
						ValidateRoots: func(_ context.Context, _ uint, roots []string) ([]string, error) {
							return append([]string(nil), roots...), nil
						},
						RuntimeState: func(uint) (targetMigrationRuntimeState, error) {
							activeChecks++
							if test.unsafeRuntime {
								return targetMigrationRuntimeUnsafe, nil
							}
							if active {
								return targetMigrationRuntimeActive, nil
							}
							return targetMigrationRuntimeInactive, nil
						},
						Import: func(context.Context, uint, []string) ([]string, error) {
							imports++
							if test.importRemovesGuard {
								if err := db.Where("guest_type = ? AND guest_id = ?", guestType, uint(1)).
									Delete(&clusterModels.ReplicationGuestOperation{}).Error; err != nil {
									return nil, err
								}
							}
							return []string{"test-warning"}, nil
						},
						SetIntentionalStop: func(uint, bool) error { return nil },
						Start: func(uint) error {
							starts++
							active = true
							if test.startActivatesWithErr {
								return errors.New("start response lost")
							}
							return nil
						},
					}

					recorder := runTargetMigrationImportRequest(
						t, targetMigrationImportHandler(ops), test.requestGuestID, test.requestToken, true,
					)
					if recorder.Code != test.wantStatus {
						t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
					}
					var response map[string]any
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatalf("parse response: %v", err)
					}
					if got, _ := response["message"].(string); got != test.wantMessage {
						t.Fatalf("message = %q, want %q", got, test.wantMessage)
					}
					if recorder.Code == http.StatusOK {
						if got, _ := response["operationToken"].(string); got != test.requestToken {
							t.Fatalf("receipt token = %q, want %q", got, test.requestToken)
						}
						if got, _ := response["guestId"].(float64); uint(got) != test.requestGuestID {
							t.Fatalf("receipt guest id = %v, want %d", response["guestId"], test.requestGuestID)
						}
					}
					if activeChecks != test.wantActiveChecks || imports != test.wantImports || starts != test.wantStarts {
						t.Fatalf(
							"calls active/import/start = %d/%d/%d, want %d/%d/%d",
							activeChecks, imports, starts,
							test.wantActiveChecks, test.wantImports, test.wantStarts,
						)
					}
				})
			}
		})
	}
}
