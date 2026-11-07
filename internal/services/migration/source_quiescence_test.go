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

func TestParseSendSizeLineReadsStreamTotal(t *testing.T) {
	// Lines as zfs send -P -R prints them for a full and an incremental send.
	lines := []string{
		"full\tjt2/sylve/virtual-machines/106@sylve-migrate-initial-1\t50696",
		"full\tjt2/sylve/virtual-machines/106/raw-2@sylve-migrate-initial-1\t157355584",
		"incremental\tsylve-migrate-initial-1\tjt2/sylve/virtual-machines/106@sylve-migrate-final-2\t624",
		"size\t157664432",
	}
	var total uint64
	for _, line := range lines {
		if v, ok := parseSendSizeLine(line); ok {
			total = v
		}
	}
	if total != 157664432 {
		t.Fatalf("stream total = %d, want 157664432", total)
	}
}

func TestLatestCommonMigrationSnapshotOrdersPassesWithinASecond(t *testing.T) {
	snapshots := []string{
		"sylve-migrate-catchup-1700000000200000000",
		"sylve-migrate-catchup-1700000000900000000",
		"sylve-migrate-initial-1700000000100000000",
	}
	if got := latestCommonMigrationSnapshot(snapshots, snapshots); got != "sylve-migrate-catchup-1700000000900000000" {
		t.Fatalf("latest common snapshot = %q", got)
	}
}

func TestCatchUpMigrationStopsWhenPassesStopShrinking(t *testing.T) {
	const mib = 1 << 20
	for _, test := range []struct {
		name    string
		initial uint64
		passes  []uint64
		want    int
	}{
		{name: "small initial send", initial: 10 * mib, want: 0},
		{name: "converges", initial: 4096 * mib, passes: []uint64{512 * mib, 100 * mib, 8 * mib}, want: 3},
		{name: "small pass ends it", initial: 4096 * mib, passes: []uint64{32 * mib}, want: 1},
		{name: "not shrinking", initial: 4096 * mib, passes: []uint64{512 * mib, 600 * mib}, want: 2},
		{name: "pass limit", initial: 4096 * mib, passes: []uint64{2048 * mib, 1024 * mib, 512 * mib, 256 * mib}, want: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := catchUpMigration(test.initial, func() (uint64, error) {
				calls++
				return test.passes[calls-1], nil
			})
			if err != nil || calls != test.want {
				t.Fatalf("catch-up ran %d passes (err %v), want %d", calls, err, test.want)
			}
		})
	}

	sendErr := errors.New("send failed")
	if err := catchUpMigration(4096*mib, func() (uint64, error) { return 0, sendErr }); !errors.Is(err, sendErr) {
		t.Fatalf("catch-up error = %v, want %v", err, sendErr)
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
	roots := []string{"pool-a/sylve/virtual-machines/42", "pool-b/sylve/virtual-machines/42"}
	started, stopped := true, false
	valid := targetMigrationImportReceipt{
		GuestID: 42, OperationToken: "migration:source:42", StartGuest: &started,
		SourceDatasetRoots: []string{roots[1], roots[0]},
	}
	pools := map[string]string{"pool-b": "tank"}
	valid.TargetPools = map[string]string{"pool-b": "tank"}
	if err := validateTargetMigrationImportReceipt(valid, 42, "migration:source:42", true, roots, pools); err != nil {
		t.Fatalf("valid token-bound receipt rejected: %v", err)
	}

	for name, mutate := range map[string]func(*targetMigrationImportReceipt){
		"other guest":   func(r *targetMigrationImportReceipt) { r.GuestID = 43 },
		"stale token":   func(r *targetMigrationImportReceipt) { r.OperationToken = "stale" },
		"stopped guest": func(r *targetMigrationImportReceipt) { r.StartGuest = &stopped },
		"no run state":  func(r *targetMigrationImportReceipt) { r.StartGuest = nil },
		"missing root":  func(r *targetMigrationImportReceipt) { r.SourceDatasetRoots = roots[:1] },
		"pools ignored": func(r *targetMigrationImportReceipt) { r.TargetPools = nil },
	} {
		receipt := valid
		mutate(&receipt)
		if err := validateTargetMigrationImportReceipt(receipt, 42, "migration:source:42", true, roots, pools); err == nil {
			t.Fatalf("%s receipt was accepted: %+v", name, receipt)
		}
	}
}
