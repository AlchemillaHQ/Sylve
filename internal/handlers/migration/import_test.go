// SPDX-License-Identifier: BSD-2-Clause

package migrationHandlers

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	goLibvirt "github.com/digitalocean/go-libvirt"
)

func runTargetMigrationImport(
	t *testing.T,
	ops targetMigrationImportOperations,
	guestID uint,
	operationToken string,
	startGuest bool,
) (targetMigrationImportReceipt, error) {
	t.Helper()
	return targetMigrationImport(ops)(t.Context(), "", targetMigrationImportRequest{
		GuestID:            guestID,
		OperationToken:     operationToken,
		StartGuest:         &startGuest,
		SourceDatasetRoots: []string{"pool/sylve/virtual-machines/1"},
	})
}

func TestTargetMigrationImportSerializesSameGuest(t *testing.T) {
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
		Import: func(context.Context, uint, []string, map[string]string) ([]string, error) {
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
		Start: func(context.Context, uint) error {
			active.Store(true)
			return nil
		},
	}
	call := targetMigrationImport(ops)
	request := func() error {
		startGuest := true
		_, err := call(t.Context(), "", targetMigrationImportRequest{
			GuestID: 1, OperationToken: "migration:source:1", StartGuest: &startGuest,
			SourceDatasetRoots: []string{"pool/sylve/virtual-machines/1"},
		})
		return err
	}

	results := make(chan error, 2)
	go func() { results <- request() }()
	<-entered
	go func() { results <- request() }()

	time.Sleep(50 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("serialized import failed: %v", err)
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
	for _, initiallyActive := range []bool{false, true} {
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
				Import: func(context.Context, uint, []string, map[string]string) ([]string, error) {
					imports++
					return nil, nil
				},
				SetIntentionalStop: func(_ uint, stopped bool) error {
					intentionalStop = stopped
					return nil
				},
				Start: func(context.Context, uint) error {
					starts++
					active = true
					return nil
				},
			}

			receipt, err := runTargetMigrationImport(t, ops, 1, "migration:source:1", false)
			if initiallyActive {
				if err == nil || !strings.Contains(err.Error(), "migration_target_runtime_state_not_safe_for_import") {
					t.Fatalf("active target error = %v", err)
				}
				if imports != 0 || starts != 0 {
					t.Fatalf("active target import/start calls = %d/%d, want 0/0", imports, starts)
				}
				return
			}

			if err != nil {
				t.Fatalf("stopped import: %v", err)
			}
			if imports != 1 || starts != 0 || active || !intentionalStop {
				t.Fatalf(
					"stopped import calls/active/intent = %d/%d/%v/%v, want 1/0/false/true",
					imports, starts, active, intentionalStop,
				)
			}
			if receipt.Message != "vm_imported_and_left_stopped" || receipt.StartGuest == nil || *receipt.StartGuest {
				t.Fatalf("unexpected stopped import receipt: %+v", receipt)
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
	for _, guestType := range []string{
		clusterModels.ReplicationGuestTypeVM,
		clusterModels.ReplicationGuestTypeJail,
	} {
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
				wantOK                bool
				wantMessage           string
				wantActiveChecks      int
				wantImports           int
				wantStarts            int
			}{
				{
					name: "first import", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover,
					wantOK:         true, wantMessage: guestType + "_imported_and_started",
					wantActiveChecks: 2, wantImports: 1, wantStarts: 1,
				},
				{
					name: "lost response retry already active", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantOK: true, wantMessage: guestType + "_already_imported_and_active",
					wantActiveChecks: 1,
				},
				{
					name: "stale token cannot claim active guest", requestGuestID: 1, requestToken: "stale-token",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantMessage: "migration_target_cutover_guard_rejected",
				},
				{
					name: "wrong guest cannot reuse token", requestGuestID: 2, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, initiallyActive: true,
					wantMessage: "migration_target_cutover_guard_rejected",
				},
				{
					name: "pre-cutover cannot import", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationPreCutover,
					wantMessage:    "migration_target_cutover_guard_rejected",
				},
				{
					name: "non-quiescent runtime cannot be re-imported", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, unsafeRuntime: true,
					wantMessage: "migration_target_runtime_state_not_safe_for_import", wantActiveChecks: 1,
				},
				{
					name: "guard removed during import cannot start", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, importRemovesGuard: true,
					wantMessage: "migration_target_cutover_guard_rejected", wantActiveChecks: 1, wantImports: 1,
				},
				{
					name: "start side effect wins over returned error", requestGuestID: 1, requestToken: "migration:source:1",
					operationState: clusterModels.ReplicationGuestOperationCutover, startActivatesWithErr: true,
					wantOK: true, wantMessage: guestType + "_already_imported_and_active",
					wantActiveChecks: 2, wantImports: 1, wantStarts: 1,
				},
				{
					name: "missing token", requestGuestID: 1,
					operationState: clusterModels.ReplicationGuestOperationCutover,
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
						Import: func(context.Context, uint, []string, map[string]string) ([]string, error) {
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
						Start: func(context.Context, uint) error {
							starts++
							active = true
							if test.startActivatesWithErr {
								return errors.New("start response lost")
							}
							return nil
						},
					}

					receipt, err := runTargetMigrationImport(t, ops, test.requestGuestID, test.requestToken, true)
					if test.wantOK {
						if err != nil {
							t.Fatalf("import: %v", err)
						}
						if receipt.Message != test.wantMessage || receipt.OperationToken != test.requestToken ||
							receipt.GuestID != test.requestGuestID {
							t.Fatalf("receipt = %+v, want message %q", receipt, test.wantMessage)
						}
					} else if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
						t.Fatalf("import error = %v, want %q", err, test.wantMessage)
					} else if test.wantImports != 0 && !strings.Contains(err.Error(), "test-warning") {
						t.Fatalf("import error %v lost the import's warnings", err)
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

func TestTargetMigrationImportReceivesIntoMappedPools(t *testing.T) {
	var validated, imported []string
	var importedPools map[string]string
	ops := targetMigrationImportOperations{
		GuestType:              clusterModels.ReplicationGuestTypeVM,
		ImportedStoppedMessage: "vm_imported_and_left_stopped",
		Authorize:              func(context.Context, uint, string) error { return nil },
		ValidateRoots: func(_ context.Context, _ uint, roots []string) ([]string, error) {
			validated = roots
			return roots, nil
		},
		RuntimeState: func(uint) (targetMigrationRuntimeState, error) {
			return targetMigrationRuntimeInactive, nil
		},
		Import: func(_ context.Context, _ uint, roots []string, pools map[string]string) ([]string, error) {
			imported, importedPools = roots, pools
			return nil, nil
		},
		SetIntentionalStop: func(uint, bool) error { return nil },
		Start:              func(context.Context, uint) error { return nil },
	}
	startGuest := false
	sourceRoots := []string{"tank/sylve/virtual-machines/40", "fast/sylve/virtual-machines/40"}
	pools := map[string]string{"tank": "data"}
	request := targetMigrationImportRequest{
		GuestID: 40, OperationToken: "migration:source:1", StartGuest: &startGuest,
		SourceDatasetRoots: sourceRoots, TargetPools: pools,
	}

	receipt, err := targetMigrationImport(ops)(t.Context(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	localRoots := []string{"fast/sylve/virtual-machines/40", "data/sylve/virtual-machines/40"}
	if !slices.Equal(validated, localRoots) || !slices.Equal(imported, localRoots) || !maps.Equal(importedPools, pools) {
		t.Fatalf("validated %v, imported %v with pools %v", validated, imported, importedPools)
	}
	if !slices.Equal(receipt.SourceDatasetRoots, sourceRoots) || !maps.Equal(receipt.TargetPools, pools) {
		t.Fatalf("receipt roots %v, pools %v", receipt.SourceDatasetRoots, receipt.TargetPools)
	}

	// tank merges into fast: its datasets are moved out of staging before the
	// one remaining root is validated and imported.
	request.TargetPools = map[string]string{"tank": "fast"}
	if _, err := targetMigrationImport(ops)(t.Context(), "", request); err == nil ||
		!strings.Contains(err.Error(), "migration_target_merge_not_supported") {
		t.Fatalf("merge without MergeRoots: err = %v", err)
	}
	var merged map[string][]string
	ops.MergeRoots = func(_ context.Context, _ uint, staged map[string][]string) error {
		merged = staged
		return nil
	}
	if _, err := targetMigrationImport(ops)(t.Context(), "", request); err != nil {
		t.Fatal(err)
	}
	root := "fast/sylve/virtual-machines/40"
	if !slices.Equal(merged[root], []string{root + ".merge-1"}) || len(merged) != 1 ||
		!slices.Equal(validated, []string{root}) || !slices.Equal(imported, []string{root}) {
		t.Fatalf("merged %v, validated %v, imported %v", merged, validated, imported)
	}
}
