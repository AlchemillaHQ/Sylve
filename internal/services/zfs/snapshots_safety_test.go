// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/rs/zerolog"
)

func TestReservedUserSnapshotNamespaces(t *testing.T) {
	for _, name := range []string{
		"ha_generation", "HA_generation", "bk_manual", "BK_manual",
		"sylve-migrate-final-123", "SYLVE-MIGRATE-final-123",
	} {
		if err := validateUserSnapshotNamespace(name); !errors.Is(err, ErrReservedSnapshotNamespace) {
			t.Fatalf("manual snapshot %q was not rejected: %v", name, err)
		}
	}
	for _, name := range []string{"manual", "before-upgrade", "sylve-migrate"} {
		if err := validateUserSnapshotNamespace(name); err != nil {
			t.Fatalf("manual snapshot %q was rejected: %v", name, err)
		}
	}
	for _, prefix := range []string{"ha_daily", "BK_daily", "sylve-migrate", "SYLVE-MIGRATE-hourly"} {
		if err := validatePeriodicSnapshotPrefix(prefix); !errors.Is(err, ErrReservedSnapshotNamespace) {
			t.Fatalf("periodic prefix %q was not rejected: %v", prefix, err)
		}
	}
	if err := validatePeriodicSnapshotPrefix("local"); err != nil {
		t.Fatalf("ordinary periodic prefix was rejected: %v", err)
	}
}

func newSnapshotSafetyService(
	t *testing.T,
	pool string,
	client *gzfs.Client,
) (*Service, string, string) {
	t.Helper()
	if err := db.SetupQueue(&internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("setup queue: %v", err)
	}
	database := testutil.NewSQLiteTestDB(t,
		&clusterModels.BackupEvent{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationGuestOperation{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&zfsModels.PeriodicSnapshot{},
	)
	vm := vmModels.VM{RID: 701, Name: "snapshot-safety-vm"}
	if err := database.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if err := database.Create(&vmModels.Storage{
		VMID: vm.ID, Name: "disk-0", Type: vmModels.VMStorageTypeZVol,
		Pool: pool, Enable: true,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}
	if err := database.Create(&clusterModels.ReplicationPolicy{
		ID: 7, Name: "enabled-policy", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: vm.RID,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "0 * * * *", Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}).Error; err != nil {
		t.Fatalf("create enabled replication policy: %v", err)
	}

	guestRoot := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID)
	controlRoot := pool + "/snapshot-control"
	zfstest.EnsureDataset(t, client, guestRoot)
	zfstest.EnsureDataset(t, client, controlRoot)
	return &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}, guestRoot, controlRoot
}

func datasetGUID(t *testing.T, client *gzfs.Client, name string) string {
	t.Helper()
	dataset, err := client.ZFS.Get(context.Background(), name, false)
	if err != nil || dataset == nil {
		t.Fatalf("get dataset %s: %v", name, err)
	}
	return dataset.GUID
}

func setSnapshotSafetyProperty(t *testing.T, dataset, property, value string) {
	t.Helper()
	if output, err := exec.Command("zfs", "set", property+"="+value, dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs set %s=%s %s: %v\n%s", property, value, dataset, err, output)
	}
}

func inheritSnapshotSafetyProperty(t *testing.T, dataset, property string) {
	t.Helper()
	if output, err := exec.Command("zfs", "inherit", property, dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs inherit %s %s: %v\n%s", property, dataset, err, output)
	}
}

func TestUserSnapshotCreationFencesRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	svc, guestRoot, _ := newSnapshotSafetyService(t, pool, client)
	guid := datasetGUID(t, client, guestRoot)
	ctx := context.Background()

	// An enabled HA policy alone must not block a local snapshot on the active node.
	if err := svc.CreateSnapshot(ctx, guid, "manual-with-ha-enabled", false); err != nil {
		t.Fatalf("enabled HA policy blocked local snapshot: %v", err)
	}

	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 701,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationPreCutover,
		Token:     "migration:node-a:701", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 701,
		AcquiredAt: time.Now().UTC(),
	}
	if err := svc.DB.Create(&operation).Error; err != nil {
		t.Fatalf("create guest operation: %v", err)
	}
	if err := svc.CreateSnapshot(ctx, guid, "manual-during-operation", false); !errors.Is(err, ErrSnapshotCreationBlocked) {
		t.Fatalf("active guest operation did not block snapshot: %v", err)
	}
	if err := svc.DB.Delete(&operation).Error; err != nil {
		t.Fatalf("delete guest operation: %v", err)
	}

	restore := clusterModels.BackupEvent{
		Mode: "restore", Status: "running", TargetEndpoint: guestRoot, StartedAt: time.Now().UTC(),
	}
	if err := svc.DB.Create(&restore).Error; err != nil {
		t.Fatalf("create running restore event: %v", err)
	}
	if err := svc.CreateSnapshot(ctx, guid, "manual-during-restore", false); !errors.Is(err, ErrSnapshotCreationBlocked) {
		t.Fatalf("running restore did not block snapshot: %v", err)
	}
	if err := svc.DB.Model(&restore).Update("status", "failed").Error; err != nil {
		t.Fatalf("finish restore event: %v", err)
	}

	setSnapshotSafetyProperty(t, guestRoot, "sylve:replication-role", "standby")
	if err := svc.CreateSnapshot(ctx, guid, "manual-on-promoted-active", false); err != nil {
		t.Fatalf("writable active HA dataset was mistaken for a standby: %v", err)
	}
	setSnapshotSafetyProperty(t, guestRoot, "readonly", "on")
	if err := svc.CreateSnapshot(ctx, guid, "manual-on-standby", false); !errors.Is(err, ErrSnapshotCreationBlocked) {
		t.Fatalf("standby provenance did not block snapshot: %v", err)
	}
	inheritSnapshotSafetyProperty(t, guestRoot, "sylve:replication-role")
	inheritSnapshotSafetyProperty(t, guestRoot, "readonly")

	if err := svc.CreateSnapshot(ctx, guid, "manual-after-fences", false); err != nil {
		t.Fatalf("snapshot remained blocked after fences cleared: %v", err)
	}
}

func TestPeriodicSnapshotSkipsDoNotAdvanceLastRunRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()
	svc, guestRoot, controlRoot := newSnapshotSafetyService(t, pool, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reservedRoot := pool + "/snapshot-reserved"
	zfstest.EnsureDataset(t, client, reservedRoot)
	baseline := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	jobs := []zfsModels.PeriodicSnapshot{
		{GUID: datasetGUID(t, client, reservedRoot), Pool: pool, Prefix: "BK_manual", Interval: 1, LastRunAt: baseline},
		{GUID: datasetGUID(t, client, guestRoot), Pool: pool, Prefix: "local", Interval: 1, LastRunAt: baseline},
		// Keep the control last so its persisted run also proves the skipped
		// jobs were visited before the test cancels the scheduler context.
		{GUID: datasetGUID(t, client, controlRoot), Pool: pool, Prefix: "local", Interval: 1, LastRunAt: baseline},
	}
	for i := range jobs {
		if err := svc.DB.Create(&jobs[i]).Error; err != nil {
			t.Fatalf("create periodic job: %v", err)
		}
	}
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 701,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationPreCutover,
		Token:     "migration:node-a:periodic", OwnerNodeID: "node-a", TargetNodeID: "node-b", TaskID: 702,
		AcquiredAt: time.Now().UTC(),
	}
	if err := svc.DB.Create(&operation).Error; err != nil {
		t.Fatalf("create guest operation: %v", err)
	}

	svc.StartSnapshotScheduler(ctx)
	deadline := time.Now().Add(12 * time.Second)
	for {
		var control zfsModels.PeriodicSnapshot
		if err := svc.DB.First(&control, jobs[2].ID).Error; err != nil {
			t.Fatalf("load control job: %v", err)
		}
		if control.LastRunAt.After(baseline) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control periodic snapshot did not run")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()

	for _, blocked := range jobs[:2] {
		var current zfsModels.PeriodicSnapshot
		if err := svc.DB.First(&current, blocked.ID).Error; err != nil {
			t.Fatalf("load blocked job: %v", err)
		}
		if !current.LastRunAt.Equal(baseline) {
			t.Fatalf("blocked job %d advanced LastRunAt: got %s want %s", current.ID, current.LastRunAt, baseline)
		}
	}

	for _, dataset := range []string{reservedRoot, guestRoot} {
		snapshots, err := client.ZFS.ListWithPrefix(context.Background(), gzfs.DatasetTypeSnapshot, dataset, false)
		if err != nil && !strings.Contains(err.Error(), "dataset does not exist") {
			t.Fatalf("list snapshots for %s: %v", dataset, err)
		}
		if len(snapshots) != 0 {
			t.Fatalf("blocked periodic job created snapshots on %s: %+v", dataset, snapshots)
		}
	}
}
