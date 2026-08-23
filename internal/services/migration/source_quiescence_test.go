// SPDX-License-Identifier: BSD-2-Clause

package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
)

func TestWaitForMigrationSourceStoppedPollsUntilInactive(t *testing.T) {
	polls := 0
	err := waitForMigrationSourceStopped(t.Context(), time.Microsecond, func() (bool, error) {
		polls++
		return polls < 3, nil
	})
	if err != nil {
		t.Fatalf("wait for source stop: %v", err)
	}
	if polls != 3 {
		t.Fatalf("polls = %d, want 3", polls)
	}
}

func TestWaitForMigrationSourceStoppedFailsClosedOnStateError(t *testing.T) {
	want := errors.New("runtime query failed")
	err := waitForMigrationSourceStopped(t.Context(), time.Microsecond, func() (bool, error) {
		return false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestWaitForMigrationSourceStoppedHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	err := waitForMigrationSourceStopped(ctx, time.Microsecond, func() (bool, error) {
		return true, nil
	})
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestPersistMigrationOriginalRunningIsDurableAndOnceOnly(t *testing.T) {
	mp := &migrationPayload{}
	persisted := 0
	if err := persistMigrationOriginalRunning(mp, true, func() error {
		persisted++
		if mp.OriginalRunning == nil || !*mp.OriginalRunning {
			t.Fatal("running state was not set before persistence")
		}
		return nil
	}); err != nil {
		t.Fatalf("persist original running state: %v", err)
	}

	if err := persistMigrationOriginalRunning(mp, false, func() error {
		persisted++
		return nil
	}); err != nil {
		t.Fatalf("repeat original running checkpoint: %v", err)
	}
	if persisted != 1 {
		t.Fatalf("persistence calls = %d, want 1", persisted)
	}
	if mp.OriginalRunning == nil || !*mp.OriginalRunning {
		t.Fatal("recovery-time stopped state overwrote original running state")
	}
}

func TestPersistMigrationOriginalRunningClearsUndurableValueOnFailure(t *testing.T) {
	mp := &migrationPayload{}
	want := errors.New("database unavailable")
	err := persistMigrationOriginalRunning(mp, true, func() error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("checkpoint error = %v, want %v", err, want)
	}
	if mp.OriginalRunning != nil {
		t.Fatal("failed checkpoint left an in-memory running-state value")
	}
}

func TestLatestCommonMigrationSnapshotUsesNewestConfirmedRemoteSnapshot(t *testing.T) {
	local := []string{
		"sylve-migrate-initial-100",
		"sylve-migrate-final-200",
		"sylve-migrate-final-300",
		"sylve-migrate-user-999",
	}
	remote := []string{
		"sylve-migrate-initial-100",
		"sylve-migrate-final-200",
		"sylve-migrate-user-999",
	}
	if got := latestCommonMigrationSnapshot(local, remote); got != "sylve-migrate-final-200" {
		t.Fatalf("latest common snapshot = %q", got)
	}
	if got := latestCommonMigrationSnapshot(local, []string{"unrelated"}); got != "" {
		t.Fatalf("missing common snapshot = %q, want empty", got)
	}
}

func TestMigrationSnapshotOwnershipDoesNotClaimBackupOrReplicationSnapshots(t *testing.T) {
	for _, snapshot := range []string{"ha_generation", "bk_job_1", "manual", "sylve-migrate"} {
		if isMigrationOwnedSnapshot(snapshot) {
			t.Fatalf("non-migration snapshot %q was claimed", snapshot)
		}
	}
	if !isMigrationOwnedSnapshot("sylve-migrate-initial-100") {
		t.Fatal("migration-owned snapshot was not recognized")
	}
}

func TestMigrationGuestMetadataFlushIsFatalForVMAndJail(t *testing.T) {
	for _, guestType := range []string{taskModels.GuestTypeVM, taskModels.GuestTypeJail} {
		guestType := guestType
		t.Run(guestType, func(t *testing.T) {
			calls := 0
			err := flushMigrationGuestMetadata(guestType, 73, func(guestID uint) error {
				calls++
				if guestID != 73 {
					t.Fatalf("guest id = %d", guestID)
				}
				return errors.New("metadata filesystem is readonly")
			})
			if err == nil || !strings.Contains(err.Error(), "flush_failed") {
				t.Fatalf("metadata flush failure was not fatal: %v", err)
			}
			if calls != 1 {
				t.Fatalf("metadata writer calls = %d, want 1", calls)
			}
		})
	}
}

func TestExplicitMigrationCancellationDoesNotTreatPollFailuresAsCancellation(t *testing.T) {
	if !isExplicitMigrationCancellation(errors.New("migration_cancelled")) {
		t.Fatal("explicit cancellation was not recognized")
	}
	for _, err := range []error{
		errors.New("database is locked"),
		errors.New("invalid payload: migration_cancelled field malformed"),
		context.DeadlineExceeded,
	} {
		if isExplicitMigrationCancellation(err) {
			t.Fatalf("poll failure was treated as cancellation: %v", err)
		}
	}
}

func TestTargetMigrationImportReceiptMustMatchTokenGuestAndRootManifest(t *testing.T) {
	const valid = `{"status":"success","guestId":42,"operationToken":"migration:source:42","startGuest":true,"sourceDatasetRoots":["pool-b/sylve/virtual-machines/42","pool-a/sylve/virtual-machines/42"]}`
	roots := []string{"pool-a/sylve/virtual-machines/42", "pool-b/sylve/virtual-machines/42"}
	if _, err := validateTargetMigrationImportReceipt(
		[]byte(valid), 42, "migration:source:42", true, roots,
	); err != nil {
		t.Fatalf("valid token-bound receipt rejected: %v", err)
	}

	invalid := []string{
		`not-json`,
		`{"status":"error","guestId":42,"operationToken":"migration:source:42","startGuest":true,"sourceDatasetRoots":["pool-a/sylve/virtual-machines/42","pool-b/sylve/virtual-machines/42"]}`,
		`{"status":"success","guestId":43,"operationToken":"migration:source:42","startGuest":true,"sourceDatasetRoots":["pool-a/sylve/virtual-machines/42","pool-b/sylve/virtual-machines/42"]}`,
		`{"status":"success","guestId":42,"operationToken":"stale","startGuest":true,"sourceDatasetRoots":["pool-a/sylve/virtual-machines/42","pool-b/sylve/virtual-machines/42"]}`,
		`{"status":"success","guestId":42,"operationToken":"migration:source:42","startGuest":false,"sourceDatasetRoots":["pool-a/sylve/virtual-machines/42","pool-b/sylve/virtual-machines/42"]}`,
		`{"status":"success","guestId":42,"operationToken":"migration:source:42","sourceDatasetRoots":["pool-a/sylve/virtual-machines/42","pool-b/sylve/virtual-machines/42"]}`,
		`{"status":"success","guestId":42,"operationToken":"migration:source:42","startGuest":true,"sourceDatasetRoots":["pool-a/sylve/virtual-machines/42"]}`,
	}
	for _, raw := range invalid {
		if _, err := validateTargetMigrationImportReceipt(
			[]byte(raw), 42, "migration:source:42", true, roots,
		); err == nil {
			t.Fatalf("invalid receipt was accepted: %s", raw)
		}
	}
}
