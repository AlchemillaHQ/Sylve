// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestIntegrationVerifyMigrationSourceCleanupUsesRealZFSAndMetadata(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	svc := &Service{DB: db, GZFS: client}

	tests := []struct {
		name      string
		guestType string
		guestID   uint
		root      string
		seed      func() error
	}{
		{
			name: "vm", guestType: taskModels.GuestTypeVM, guestID: 811,
			root: pool + "/sylve/virtual-machines/811",
			seed: func() error { return db.Create(&vmModels.VM{RID: 811, Name: "residue-vm"}).Error },
		},
		{
			name: "jail", guestType: taskModels.GuestTypeJail, guestID: 812,
			root: pool + "/sylve/jails/812",
			seed: func() error { return db.Create(&jailModels.Jail{CTID: 812, Name: "residue-jail"}).Error },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zfstest.EnsureDataset(t, client, test.root+"/child")
			err := svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, test.guestID)
			if err == nil || !strings.Contains(err.Error(), "migration_source_datasets_still_present") {
				t.Fatalf("dataset residue was not detected: %v", err)
			}

			if output, err := exec.Command("zfs", "destroy", "-r", test.root).CombinedOutput(); err != nil {
				t.Fatalf("destroy test guest root: %v\n%s", err, string(output))
			}
			if err := svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, test.guestID); err != nil {
				t.Fatalf("clean source was rejected: %v", err)
			}

			if err := test.seed(); err != nil {
				t.Fatalf("seed source metadata residue: %v", err)
			}
			err = svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, test.guestID)
			if err == nil || !strings.Contains(err.Error(), "migration_source_guest_metadata_still_present") {
				t.Fatalf("metadata residue was not detected: %v", err)
			}
		})
	}
}

func TestIntegrationPhaseCleanupSourceIsIdempotentWithRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	svc := &Service{DB: db, GZFS: client}
	for _, test := range []struct {
		name      string
		guestType string
		guestID   uint
		root      string
	}{
		{name: "vm", guestType: taskModels.GuestTypeVM, guestID: 916, root: pool + "/sylve/virtual-machines/916"},
		{name: "jail", guestType: taskModels.GuestTypeJail, guestID: 917, root: pool + "/sylve/jails/917"},
	} {
		t.Run(test.name, func(t *testing.T) {
			zfstest.EnsureDataset(t, client, test.root+"/child")
			payload := &migrationPayload{SourceDatasetRoots: []string{test.root}}
			task := taskModels.GuestLifecycleTask{GuestType: test.guestType, GuestID: test.guestID}
			if err := svc.phaseCleanupSource(t.Context(), payload, task); err != nil {
				t.Fatalf("first cleanup: %v", err)
			}
			if err := svc.phaseCleanupSource(t.Context(), payload, task); err != nil {
				t.Fatalf("idempotent cleanup retry: %v", err)
			}
			if err := svc.verifyMigrationSourceCleanup(
				t.Context(), test.guestType, test.guestID, []string{test.root},
			); err != nil {
				t.Fatalf("verify cleanup: %v", err)
			}
		})
	}
}

func TestIntegrationPhaseCleanupSourceUnmountsFstabMountTree(t *testing.T) {
	if runtime.GOOS != "freebsd" {
		t.Skip("requires FreeBSD filesystem mounts")
	}
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	const ctID uint = 918
	datasetName := fmt.Sprintf("%s/sylve/jails/%d", pool, ctID)
	zfstest.EnsureDataset(t, client, datasetName)
	dataset, err := client.ZFS.Get(t.Context(), datasetName, false)
	if err != nil {
		t.Fatalf("get jail dataset: %v", err)
	}

	devMount := filepath.Join(dataset.Mountpoint, "dev")
	fdMount := filepath.Join(devMount, "fd")
	if err := os.MkdirAll(fdMount, 0o755); err != nil {
		t.Fatalf("create runtime mountpoints: %v", err)
	}
	fstabPath := filepath.Join(t.TempDir(), "fstab")
	fstab := fmt.Sprintf(
		"devfs %s devfs rw 0 0\nfdescfs %s fdescfs rw 0 0\n",
		devMount,
		fdMount,
	)
	if err := os.WriteFile(fstabPath, []byte(fstab), 0o600); err != nil {
		t.Fatalf("write temporary fstab: %v", err)
	}

	t.Cleanup(func() {
		_, _ = exec.Command("/sbin/umount", "-f", fdMount).CombinedOutput()
		_, _ = exec.Command("/sbin/umount", "-f", devMount).CombinedOutput()
	})
	if output, err := exec.Command(
		"/sbin/mount", "-a", "-F", fstabPath,
	).CombinedOutput(); err != nil {
		t.Fatalf("mount temporary jail fstab: %v\n%s", err, output)
	}
	for _, target := range []string{devMount, fdMount} {
		if !integrationMountTargetPresent(t, target) {
			t.Fatalf("expected fstab target to be mounted: %s", target)
		}
	}

	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	svc := &Service{DB: db, GZFS: client}
	payload := &migrationPayload{SourceDatasetRoots: []string{datasetName}}
	task := taskModels.GuestLifecycleTask{GuestType: taskModels.GuestTypeJail, GuestID: ctID}
	if err := svc.phaseCleanupSource(t.Context(), payload, task); err != nil {
		t.Fatalf("cleanup fstab-mounted jail source: %v", err)
	}
	for _, target := range []string{fdMount, devMount} {
		if integrationMountTargetPresent(t, target) {
			t.Fatalf("runtime target remains mounted after cleanup: %s", target)
		}
	}
	if _, err := client.ZFS.Get(t.Context(), datasetName, false); err == nil {
		t.Fatalf("jail dataset remains after cleanup: %s", datasetName)
	}
	if err := svc.phaseCleanupSource(t.Context(), payload, task); err != nil {
		t.Fatalf("idempotent cleanup retry: %v", err)
	}
}

func integrationMountTargetPresent(t *testing.T, target string) bool {
	t.Helper()
	output, err := exec.Command("/sbin/mount", "-p").Output()
	if err != nil {
		t.Fatalf("list live mounts: %v", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == target {
			return true
		}
	}
	return false
}

func TestIntegrationVerifyMigrationSourceCleanupDoesNotMatchAdjacentGuestIDs(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	svc := &Service{DB: db, GZFS: client}

	tests := []struct {
		name      string
		guestType string
		base      string
	}{
		{name: "vm", guestType: taskModels.GuestTypeVM, base: pool + "/sylve/virtual-machines"},
		{name: "jail", guestType: taskModels.GuestTypeJail, base: pool + "/sylve/jails"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zfstest.EnsureDataset(t, client, test.base+"/10/child")
			zfstest.EnsureDataset(t, client, test.base+"/11/child")

			if err := svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, 1); err != nil {
				t.Fatalf("guest 1 incorrectly matched adjacent guest 10 or 11: %v", err)
			}

			zfstest.EnsureDataset(t, client, test.base+"/1/child")
			err := svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, 1)
			if err == nil || !strings.Contains(err.Error(), "migration_source_datasets_still_present") {
				t.Fatalf("exact guest 1 residue was not detected: %v", err)
			}

			if output, err := exec.Command("zfs", "destroy", "-r", test.base+"/1").CombinedOutput(); err != nil {
				t.Fatalf("destroy exact guest 1 root: %v\n%s", err, string(output))
			}
			if err := svc.verifyMigrationSourceCleanup(t.Context(), test.guestType, 1); err != nil {
				t.Fatalf("adjacent guest datasets were treated as guest 1 residue after cleanup: %v", err)
			}
		})
	}
}

func TestIntegrationVerifyMigrationSourceCleanupRejectsExportedBackingPool(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client := zfstest.DedicatedPool(t)
	root := pool + "/sylve/virtual-machines/913"
	zfstest.EnsureDataset(t, client, root+"/disk-0")
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{}, &jailModels.Jail{})
	svc := &Service{DB: db, GZFS: client}

	zfstest.ExportDedicatedPool(t, pool)

	err := svc.verifyMigrationSourceCleanup(
		t.Context(), taskModels.GuestTypeVM, 913, []string{root},
	)
	if err == nil || !strings.Contains(err.Error(), "migration_source_pool_unavailable") {
		t.Fatalf("exported pool did not block cleanup completion: %v", err)
	}

	zfstest.ImportDedicatedPool(t, pool)
	if err := svc.verifyMigrationSourceCleanup(
		t.Context(), taskModels.GuestTypeVM, 913, []string{root},
	); err == nil || !strings.Contains(err.Error(), "migration_source_datasets_still_present") {
		t.Fatalf("re-imported source residue was not detected: %v", err)
	}
}

func TestIntegrationReconcileCompletedMigrationAfterGuardRemovalUsesRealZFSProof(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{}, &jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	root := pool + "/sylve/virtual-machines/914"
	taskID := uint(914)
	operationToken := fmt.Sprintf("migration:node-a:%d", taskID)
	task := taskModels.GuestLifecycleTask{
		ID:        taskID,
		GuestType: taskModels.GuestTypeVM,
		GuestID:   914,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
		Payload: fmt.Sprintf(
			`{"targetNodeUuid":"node-b","operationToken":%q,"phase":"finalize","sourceDatasetRoots":[%q]}`,
			operationToken, root,
		),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed finalizing task: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		Name: "reenabled-after-migration-914", GuestType: task.GuestType, GuestID: task.GuestID,
		ActiveNodeID: "node-b", SourceNodeID: "node-b", OwnerEpoch: 2,
		CronExpr: "0 * * * *", Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed re-enabled policy: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationLease{
		PolicyID: policy.ID, GuestType: task.GuestType, GuestID: task.GuestID,
		OwnerNodeID: "node-b", OwnerEpoch: policy.OwnerEpoch,
		ExpiresAt: time.Now().UTC().Add(time.Hour), Version: 1,
	}).Error; err != nil {
		t.Fatalf("seed re-enabled lease: %v", err)
	}
	completedAt := time.Now().UTC()
	if err := db.Create(&clusterModels.ReplicationGuestOperationReceipt{
		Token:        operationToken,
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       task.ID,
		AcquiredAt:   completedAt.Add(-time.Minute),
		CompletedAt:  completedAt,
	}).Error; err != nil {
		t.Fatalf("seed migration completion receipt: %v", err)
	}
	zfstest.EnsureDataset(t, client, root+"/replica")
	if output, err := exec.Command("zfs", "set", "readonly=on", root).CombinedOutput(); err != nil {
		t.Fatalf("mark recreated source replica read-only: %v\n%s", err, string(output))
	}
	svc := &Service{DB: db, GZFS: client}
	if err := svc.reconcileCompletedMigrationTasks(t.Context()); err != nil {
		t.Fatalf("reconcile completed task: %v", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusSuccess || task.FinishedAt == nil {
		t.Fatalf("task was not finalized after guard removal: status=%q finishedAt=%v", task.Status, task.FinishedAt)
	}
}

func TestIntegrationExecuteMigrationReconcilesFinalizeTaskAfterGuardRemoval(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(t,
		&vmModels.VM{}, &jailModels.Jail{},
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationGuestOperationReceipt{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	root := pool + "/sylve/virtual-machines/918"
	taskID := uint(918)
	operationToken := fmt.Sprintf("migration:node-a:%d", taskID)
	task := taskModels.GuestLifecycleTask{
		ID:        taskID,
		GuestType: taskModels.GuestTypeVM,
		GuestID:   918,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
		Payload: fmt.Sprintf(
			`{"targetNodeUuid":"node-b","operationToken":%q,"phase":"finalize","sourceDatasetRoots":[%q]}`,
			operationToken, root,
		),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed finalizing task: %v", err)
	}
	policy := clusterModels.ReplicationPolicy{
		Name: "reenabled-after-migration-918", GuestType: task.GuestType, GuestID: task.GuestID,
		ActiveNodeID: "node-b", SourceNodeID: "node-b", OwnerEpoch: 2,
		CronExpr: "0 * * * *", Enabled: true,
		ProtectionState: clusterModels.ReplicationProtectionStateArmed,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed re-enabled policy: %v", err)
	}
	if err := db.Create(&clusterModels.ReplicationLease{
		PolicyID: policy.ID, GuestType: task.GuestType, GuestID: task.GuestID,
		OwnerNodeID: "node-b", OwnerEpoch: policy.OwnerEpoch,
		ExpiresAt: time.Now().UTC().Add(time.Hour), Version: 1,
	}).Error; err != nil {
		t.Fatalf("seed re-enabled lease: %v", err)
	}
	completedAt := time.Now().UTC()
	if err := db.Create(&clusterModels.ReplicationGuestOperationReceipt{
		Token:        operationToken,
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-b",
		TaskID:       task.ID,
		AcquiredAt:   completedAt.Add(-time.Minute),
		CompletedAt:  completedAt,
	}).Error; err != nil {
		t.Fatalf("seed migration completion receipt: %v", err)
	}
	zfstest.EnsureDataset(t, client, root+"/replica")
	if output, err := exec.Command("zfs", "set", "readonly=on", root).CombinedOutput(); err != nil {
		t.Fatalf("mark recreated source replica read-only: %v\n%s", err, string(output))
	}

	svc := &Service{DB: db, GZFS: client}
	if err := svc.ExecuteMigration(t.Context(), task.ID); err != nil {
		t.Fatalf("operation-absent finalize recovery: %v", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusSuccess || task.FinishedAt == nil ||
		task.Message != "migration_completed" {
		t.Fatalf("finalize recovery restarted or failed task: status=%q phase-payload=%s message=%q",
			task.Status, task.Payload, task.Message)
	}
	if !strings.Contains(task.Payload, `"phase":"finalize"`) {
		t.Fatalf("finalize recovery overwrote durable phase: %s", task.Payload)
	}
}

func TestIntegrationSealedMigrationFailureRemainsRecoverable(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	localNodeID, err := utils.GetSystemUUID()
	if err != nil || strings.TrimSpace(localNodeID) == "" {
		t.Skipf("local node ID unavailable: %v", err)
	}
	db := testutil.NewSQLiteTestDB(t,
		&taskModels.GuestLifecycleTask{},
		&clusterModels.ReplicationGuestOperation{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
	)
	task := taskModels.GuestLifecycleTask{
		GuestType: taskModels.GuestTypeVM,
		GuestID:   915,
		Action:    "migrate",
		Source:    taskModels.LifecycleTaskSourceUser,
		Status:    taskModels.LifecycleTaskStatusRunning,
		Payload: fmt.Sprintf(
			`{"targetNodeUuid":"node-b","phase":"finalize","sourceDatasetRoots":[%q]}`,
			pool+"/sylve/virtual-machines/915",
		),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	token := fmt.Sprintf("migration:%s:%d", strings.TrimSpace(localNodeID), task.ID)
	if err := db.Create(&clusterModels.ReplicationGuestOperation{
		GuestType:    task.GuestType,
		GuestID:      task.GuestID,
		Operation:    clusterModels.ReplicationGuestOperationMigration,
		State:        clusterModels.ReplicationGuestOperationCutover,
		Token:        token,
		OwnerNodeID:  strings.TrimSpace(localNodeID),
		TargetNodeID: "node-b",
		TaskID:       task.ID,
	}).Error; err != nil {
		t.Fatalf("seed cutover operation: %v", err)
	}
	guard := &migrationWorkloadGuardStub{
		completeFn: func(context.Context, string, uint, string, string) error {
			return errors.New("leader temporarily unavailable")
		},
	}
	svc := &Service{
		DB: db, GZFS: client, WorkloadGuard: guard,
		Cluster: &clusterService.Service{DB: db},
	}
	err = svc.ExecuteMigration(t.Context(), task.ID)
	var pending *migrationRecoveryPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("sealed failure was not marked recoverable: %v", err)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != taskModels.LifecycleTaskStatusRunning || task.FinishedAt != nil ||
		task.Message != "migration_recovery_pending" {
		t.Fatalf("sealed failure became terminal: status=%q finishedAt=%v message=%q", task.Status, task.FinishedAt, task.Message)
	}
}

func TestIntegrationResolveGuestDatasetsDoesNotMatchAdjacentGuestIDs(t *testing.T) {
	zfstest.SkipIfUnavailable(t)

	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&jailModels.Jail{},
		&jailModels.Storage{},
	)
	svc := &Service{DB: db, GZFS: client}

	vm := vmModels.VM{RID: 1, Name: "boundary-vm-1"}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create VM 1 metadata: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeRaw, Pool: pool, Name: "disk-0",
	}).Error; err != nil {
		t.Fatalf("create VM 1 storage metadata: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeDiskImage, Pool: "legacy-iso-pool", Name: "installer",
	}).Error; err != nil {
		t.Fatalf("create VM 1 ISO metadata: %v", err)
	}

	jail := jailModels.Jail{CTID: 1, Name: "boundary-jail-1"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail 1 metadata: %v", err)
	}
	if err := db.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   pool,
		GUID:   "boundary-jail-1-storage",
		Name:   "root",
	}).Error; err != nil {
		t.Fatalf("create jail 1 storage metadata: %v", err)
	}

	tests := []struct {
		name      string
		guestType string
		base      string
		excluded  string
		resolve   func() ([]string, error)
	}{
		{
			name: "vm", guestType: taskModels.GuestTypeVM, base: pool + "/sylve/virtual-machines",
			excluded: "legacy-iso-pool/sylve/virtual-machines/1",
			resolve:  func() ([]string, error) { return svc.resolveVMDatasets(t.Context(), 1) },
		},
		{
			name: "jail", guestType: taskModels.GuestTypeJail, base: pool + "/sylve/jails",
			resolve: func() ([]string, error) { return svc.resolveJailDatasets(t.Context(), 1) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zfstest.EnsureDataset(t, client, test.base+"/1/child")
			zfstest.EnsureDataset(t, client, test.base+"/10/child")
			zfstest.EnsureDataset(t, client, test.base+"/11/child")

			datasets, err := test.resolve()
			if err != nil {
				t.Fatalf("resolve guest 1 datasets: %v", err)
			}

			seenRoot := false
			seenChild := false
			for _, dataset := range datasets {
				if dataset == test.excluded {
					t.Fatalf("guest dataset resolution included disk-image pool hint %q", dataset)
				}
				switch dataset {
				case test.base + "/1":
					seenRoot = true
				case test.base + "/1/child":
					seenChild = true
				}
				if isCanonicalMigrationGuestDataset(dataset, test.guestType, 10) ||
					isCanonicalMigrationGuestDataset(dataset, test.guestType, 11) {
					t.Fatalf("guest 1 resolution included adjacent guest dataset %q", dataset)
				}
			}
			if !seenRoot || !seenChild {
				t.Fatalf("guest 1 root/descendant missing from resolution: %v", datasets)
			}
		})
	}
}
