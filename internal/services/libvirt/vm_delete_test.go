// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"gorm.io/gorm"
)

func TestShouldPreserveVMStorageRootDataset(t *testing.T) {
	tests := []struct {
		name           string
		storageType    vmModels.VMStorageType
		deleteRawDisks bool
		deleteVolumes  bool
		want           bool
	}{
		{
			name:           "preserves raw when raw deletion unchecked",
			storageType:    vmModels.VMStorageTypeRaw,
			deleteRawDisks: false,
			deleteVolumes:  true,
			want:           true,
		},
		{
			name:           "does not preserve raw when raw deletion checked",
			storageType:    vmModels.VMStorageTypeRaw,
			deleteRawDisks: true,
			deleteVolumes:  false,
			want:           false,
		},
		{
			name:           "preserves zvol when volume deletion unchecked",
			storageType:    vmModels.VMStorageTypeZVol,
			deleteRawDisks: true,
			deleteVolumes:  false,
			want:           true,
		},
		{
			name:           "does not preserve zvol when volume deletion checked",
			storageType:    vmModels.VMStorageTypeZVol,
			deleteRawDisks: false,
			deleteVolumes:  true,
			want:           false,
		},
		{
			name:           "does not preserve non-zfs storage types",
			storageType:    vmModels.VMStorageTypeDiskImage,
			deleteRawDisks: false,
			deleteVolumes:  false,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPreserveVMStorageRootDataset(tt.storageType, tt.deleteRawDisks, tt.deleteVolumes)
			if got != tt.want {
				t.Fatalf("expected preserve=%t, got %t", tt.want, got)
			}
		})
	}
}

func TestBuildVMStorageRemovalPlanTreatsFilesystemAsIntentionallyRetained(t *testing.T) {
	vm := vmModels.VM{
		RID: 709,
		Storages: []vmModels.Storage{{
			Type: vmModels.VMStorageTypeFilesystem,
			Pool: "tank",
			Dataset: vmModels.VMStorageDataset{
				Pool: "tank",
				Name: "tank/shares/projects",
			},
		}},
	}

	plan := buildVMStorageRemovalPlan(vm, true, true)
	root := "tank/sylve/virtual-machines/709"
	if !slices.Equal(plan.retainedDatasets, []string{"tank/shares/projects", root}) {
		t.Fatalf("retained datasets = %v", plan.retainedDatasets)
	}
	if _, preserved := plan.preserveRoots[root]; !preserved {
		t.Fatalf("filesystem metadata root %q was not preserved", root)
	}
	if len(plan.deleteDatasets) != 0 {
		t.Fatalf("filesystem dataset scheduled for deletion: %v", plan.deleteDatasets)
	}
}

func TestCleanupVMMACObjects_SkipsTransactionWhenNoMACs(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	service := &Service{DB: db}

	if err := service.cleanupVMMACObjects(true, nil); err != nil {
		t.Fatalf("expected nil error for empty MAC cleanup, got %v", err)
	}

	if err := db.Create(&networkModels.Object{Name: "mac-1", Type: "Mac"}).Error; err != nil {
		t.Fatalf("expected database to remain writable after no-op MAC cleanup, got %v", err)
	}
}

func TestCleanupVMMACObjects_RemovesMACRecords(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	service := &Service{DB: db}

	obj := networkModels.Object{Name: "mac-1", Type: "Mac"}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: obj.ID, Value: "02:00:00:00:00:01"}).Error; err != nil {
		t.Fatalf("failed to seed object entry: %v", err)
	}
	if err := db.Create(&networkModels.ObjectResolution{ObjectID: obj.ID, ResolvedIP: "192.0.2.10"}).Error; err != nil {
		t.Fatalf("failed to seed object resolution: %v", err)
	}

	if err := service.cleanupVMMACObjects(true, []uint{obj.ID}); err != nil {
		t.Fatalf("expected cleanup to succeed, got %v", err)
	}

	var objectCount int64
	if err := db.Model(&networkModels.Object{}).Count(&objectCount).Error; err != nil {
		t.Fatalf("failed to count objects: %v", err)
	}
	if objectCount != 0 {
		t.Fatalf("expected object cleanup to delete rows, found %d objects", objectCount)
	}

	var entryCount int64
	if err := db.Model(&networkModels.ObjectEntry{}).Count(&entryCount).Error; err != nil {
		t.Fatalf("failed to count object entries: %v", err)
	}
	if entryCount != 0 {
		t.Fatalf("expected entry cleanup to delete rows, found %d entries", entryCount)
	}

	var resolutionCount int64
	if err := db.Model(&networkModels.ObjectResolution{}).Count(&resolutionCount).Error; err != nil {
		t.Fatalf("failed to count object resolutions: %v", err)
	}
	if resolutionCount != 0 {
		t.Fatalf("expected resolution cleanup to delete rows, found %d resolutions", resolutionCount)
	}
}

type vmDeleteSeed struct {
	VM           vmModels.VM
	RawDataset   string
	ZVolDataset  string
	SnapshotName string
	MACObjectID  uint
}

func newVMDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(
		t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&vmModels.VMStorageDataset{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMSnapshot{},
		&vmModels.VM{},
	)
}

func seedVMDeleteGraph(t *testing.T, db *gorm.DB, rid uint, pool string, includeZVol bool) vmDeleteSeed {
	t.Helper()

	vm := vmModels.VM{Name: "delete-test", RID: rid}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	macObject := networkModels.Object{Name: fmt.Sprintf("delete-mac-%d", rid), Type: "Mac"}
	if err := db.Create(&macObject).Error; err != nil {
		t.Fatalf("seed MAC object: %v", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{
		ObjectID: macObject.ID,
		Value:    fmt.Sprintf("02:00:00:00:%02x:%02x", rid/256, rid%256),
	}).Error; err != nil {
		t.Fatalf("seed MAC entry: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, MacID: &macObject.ID, MAC: "02:00:00:00:00:01",
		SwitchID: 9999, SwitchType: "standard", Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed VM network: %v", err)
	}

	rawID := uint(11)
	rawDatasetName := fmt.Sprintf("%s/sylve/virtual-machines/%d/raw-%d", pool, rid, rawID)
	rawDataset := vmModels.VMStorageDataset{Pool: pool, Name: rawDatasetName, GUID: "raw-guid"}
	if err := db.Create(&rawDataset).Error; err != nil {
		t.Fatalf("seed raw dataset row: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		ID: rawID, VMID: vm.ID, Type: vmModels.VMStorageTypeRaw,
		Pool: pool, DatasetID: &rawDataset.ID, Enable: true,
	}).Error; err != nil {
		t.Fatalf("seed raw storage: %v", err)
	}

	zvolDatasetName := ""
	if includeZVol {
		zvolID := uint(12)
		zvolDatasetName = fmt.Sprintf("%s/sylve/virtual-machines/%d/zvol-%d", pool, rid, zvolID)
		zvolDataset := vmModels.VMStorageDataset{Pool: pool, Name: zvolDatasetName, GUID: "zvol-guid"}
		if err := db.Create(&zvolDataset).Error; err != nil {
			t.Fatalf("seed zvol dataset row: %v", err)
		}
		if err := db.Create(&vmModels.Storage{
			ID: zvolID, VMID: vm.ID, Type: vmModels.VMStorageTypeZVol,
			Pool: pool, DatasetID: &zvolDataset.ID, Enable: true,
		}).Error; err != nil {
			t.Fatalf("seed zvol storage: %v", err)
		}
	}

	if err := db.Create(&vmModels.VMStats{VMID: vm.ID, CPUUsage: 1}).Error; err != nil {
		t.Fatalf("seed VM stats: %v", err)
	}
	if err := db.Create(&vmModels.VMCPUPinning{VMID: vm.ID, HostSocket: 0, HostCPU: []int{0}}).Error; err != nil {
		t.Fatalf("seed VM CPU pinning: %v", err)
	}
	rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, rid)
	snapshotName := fmt.Sprintf("svms_delete-test_%d", rid)
	if err := db.Create(&vmModels.VMSnapshot{
		VMID: vm.ID, RID: rid, Name: "snapshot", SnapshotName: snapshotName,
		RootDatasets: []string{rootDataset},
	}).Error; err != nil {
		t.Fatalf("seed VM snapshot: %v", err)
	}

	return vmDeleteSeed{
		VM: vm, RawDataset: rawDatasetName, ZVolDataset: zvolDatasetName,
		SnapshotName: snapshotName, MACObjectID: macObject.ID,
	}
}

func assertVMDeleteGraphCounts(t *testing.T, db *gorm.DB, seed vmDeleteSeed, want int64) {
	t.Helper()
	checks := []struct {
		name  string
		model any
		where string
		arg   any
	}{
		{name: "VM", model: &vmModels.VM{}, where: "id = ?", arg: seed.VM.ID},
		{name: "storage", model: &vmModels.Storage{}, where: "vm_id = ?", arg: seed.VM.ID},
		{name: "network", model: &vmModels.Network{}, where: "vm_id = ?", arg: seed.VM.ID},
		{name: "stats", model: &vmModels.VMStats{}, where: "vm_id = ?", arg: seed.VM.ID},
		{name: "CPU pinning", model: &vmModels.VMCPUPinning{}, where: "vm_id = ?", arg: seed.VM.ID},
		{name: "snapshot", model: &vmModels.VMSnapshot{}, where: "rid = ?", arg: seed.VM.RID},
	}
	for _, check := range checks {
		var count int64
		if err := db.Model(check.model).Where(check.where, check.arg).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if want == 0 && count != 0 {
			t.Fatalf("%s count = %d, want 0", check.name, count)
		}
		if want > 0 && count == 0 {
			t.Fatalf("%s count = 0, want existing rows", check.name)
		}
	}
}

func TestRemoveVMWithWarningsRetainsManagedStorageAndReleasesIdentity(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 710, "tank", true)
	service := &Service{DB: db}

	runtimeCalled := false
	actionLockHeldDuringRuntime := false
	crudLockHeldDuringRuntime := false
	result, err := service.removeVMWithWarnings(
		seed.VM.RID, true, false, false, t.Context(),
		func(rid uint) error {
			runtimeCalled = true
			if service.actionMutex.TryLock() {
				service.actionMutex.Unlock()
			} else {
				actionLockHeldDuringRuntime = true
			}
			if service.crudMutex.TryLock() {
				service.crudMutex.Unlock()
			} else {
				crudLockHeldDuringRuntime = true
			}
			if rid != seed.VM.RID {
				t.Fatalf("runtime RID = %d, want %d", rid, seed.VM.RID)
			}
			assertVMDeleteGraphCounts(t, db, seed, 1)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("remove VM retaining storage: %v", err)
	}
	if !runtimeCalled {
		t.Fatal("runtime remover was not called")
	}
	if !actionLockHeldDuringRuntime || !crudLockHeldDuringRuntime {
		t.Fatalf(
			"deletion locks held during runtime = action:%t crud:%t, want both true",
			actionLockHeldDuringRuntime,
			crudLockHeldDuringRuntime,
		)
	}
	if !service.actionMutex.TryLock() {
		t.Fatal("action lock remained held after deletion")
	}
	service.actionMutex.Unlock()
	if !service.crudMutex.TryLock() {
		t.Fatal("CRUD lock remained held after deletion")
	}
	service.crudMutex.Unlock()
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	wantRetained := []string{
		fmt.Sprintf("tank/sylve/virtual-machines/%d", seed.VM.RID),
		seed.RawDataset,
		seed.ZVolDataset,
	}
	if !slices.Equal(result.RetainedDatasets, wantRetained) {
		t.Fatalf("retained datasets = %v, want %v", result.RetainedDatasets, wantRetained)
	}
	assertVMDeleteGraphCounts(t, db, seed, 0)

	var datasetRows int64
	if err := db.Model(&vmModels.VMStorageDataset{}).Count(&datasetRows).Error; err != nil {
		t.Fatalf("count storage dataset rows: %v", err)
	}
	if datasetRows != 0 {
		t.Fatalf("storage dataset row count = %d, want 0", datasetRows)
	}

	var macObjects int64
	if err := db.Model(&networkModels.Object{}).Where("id = ?", seed.MACObjectID).Count(&macObjects).Error; err != nil {
		t.Fatalf("count MAC objects: %v", err)
	}
	if macObjects != 0 {
		t.Fatalf("MAC object count = %d, want 0", macObjects)
	}
}

func TestRemoveVMWithWarningsUsesCRUDThenActionLockOrder(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 713, "tank", false)
	service := &Service{DB: db}

	type removalOutcome struct {
		result VMRemovalResult
		err    error
	}
	started := make(chan struct{})
	completed := make(chan removalOutcome, 1)

	// Snapshot rollback holds crudMutex while acquiring actionMutex. Holding
	// actionMutex here lets the test observe which lock deletion takes first.
	service.actionMutex.Lock()
	go func() {
		close(started)
		result, err := service.removeVMWithWarnings(
			seed.VM.RID,
			false,
			false,
			false,
			t.Context(),
			func(uint) error { return nil },
		)
		completed <- removalOutcome{result: result, err: err}
	}()
	<-started

	crudAcquiredBeforeAction := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !service.crudMutex.TryLock() {
			crudAcquiredBeforeAction = true
			break
		}
		service.crudMutex.Unlock()
		runtime.Gosched()
	}
	service.actionMutex.Unlock()

	select {
	case outcome := <-completed:
		if outcome.err != nil {
			t.Fatalf("remove VM after lock-order probe: %v", outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("VM deletion did not complete after action lock was released")
	}
	if !crudAcquiredBeforeAction {
		t.Fatal("VM deletion did not acquire CRUD lock before waiting for action lock")
	}
}

func TestRemoveVMWithWarningsRuntimeFailureKeepsIdentityAndSkipsStorage(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 711, "tank", false)
	service := &Service{DB: db}
	runtimeErr := errors.New("runtime unavailable")

	result, err := service.removeVMWithWarnings(
		seed.VM.RID, true, true, true, t.Context(),
		func(uint) error { return runtimeErr },
	)
	if err == nil || !strings.Contains(err.Error(), runtimeErr.Error()) {
		t.Fatalf("runtime failure error = %v", err)
	}
	if len(result.Warnings) != 0 || len(result.RetainedDatasets) != 0 {
		t.Fatalf("failure returned a success result: %+v", result)
	}
	assertVMDeleteGraphCounts(t, db, seed, 1)
}

func TestRemoveVMWithWarningsDatabaseFailureRollsBackAndSkipsStorage(t *testing.T) {
	db := newVMDeleteTestDB(t)
	seed := seedVMDeleteGraph(t, db, 712, "tank", false)
	service := &Service{DB: db}
	ctx, cancel := context.WithCancel(t.Context())

	result, err := service.removeVMWithWarnings(
		seed.VM.RID, false, true, true, ctx,
		func(uint) error {
			cancel()
			return nil
		},
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		t.Fatalf("database cancellation error = %v", err)
	}
	if len(result.Warnings) != 0 || len(result.RetainedDatasets) != 0 {
		t.Fatalf("failure returned a success result: %+v", result)
	}
	assertVMDeleteGraphCounts(t, db, seed, 1)
}

func TestRemoveVMWithWarningsRealZFSStorageOutcomes(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS VM deletion integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	t.Run("retained dataset remains untouched", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		seed := seedVMDeleteGraph(t, db, 720, pool, false)
		zfstest.EnsureDataset(t, client, seed.RawDataset)
		before, err := client.ZFS.Get(t.Context(), seed.RawDataset, false)
		if err != nil || before == nil {
			t.Fatalf("read retained dataset before delete: %v", err)
		}
		beforeGUID := before.Properties["guid"].Value

		service := &Service{DB: db, GZFS: client}
		result, err := service.removeVMWithWarnings(
			seed.VM.RID, false, false, false, t.Context(), func(uint) error { return nil },
		)
		if err != nil {
			t.Fatalf("delete VM retaining real dataset: %v", err)
		}
		if len(result.Warnings) != 0 {
			t.Fatalf("unexpected retained-dataset warnings: %v", result.Warnings)
		}
		after, err := client.ZFS.Get(t.Context(), seed.RawDataset, false)
		if err != nil || after == nil {
			t.Fatalf("retained dataset missing after delete: %v", err)
		}
		if after.Properties["guid"].Value != beforeGUID {
			t.Fatalf("retained dataset GUID changed: %q -> %q", beforeGUID, after.Properties["guid"].Value)
		}
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, seed.VM.RID)
		if !slices.Contains(result.RetainedDatasets, rootDataset) {
			t.Fatalf("canonical retained root missing from result: %v", result.RetainedDatasets)
		}
	})

	t.Run("requested cleanup succeeds after identity removal", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		seed := seedVMDeleteGraph(t, db, 721, pool, false)
		zfstest.EnsureDataset(t, client, seed.RawDataset)
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, seed.VM.RID)
		root, err := client.ZFS.Get(t.Context(), rootDataset, false)
		if err != nil || root == nil {
			t.Fatalf("get VM root before recursive snapshot: %v", err)
		}
		if _, err := root.Snapshot(t.Context(), seed.SnapshotName, true); err != nil {
			t.Fatalf("create tracked recursive VM snapshot: %v", err)
		}
		service := &Service{DB: db, GZFS: client}

		result, err := service.removeVMWithWarnings(
			seed.VM.RID, false, true, false, t.Context(), func(uint) error { return nil },
		)
		if err != nil {
			t.Fatalf("delete VM and real dataset: %v", err)
		}
		if len(result.Warnings) != 0 || len(result.RetainedDatasets) != 0 {
			t.Fatalf("unexpected cleanup result: %+v", result)
		}
		if _, err := client.ZFS.Get(t.Context(), seed.RawDataset, false); err == nil || !isVMDatasetNotFoundError(err) {
			t.Fatalf("requested dataset still exists or lookup failed unexpectedly: %v", err)
		}
		if _, err := client.ZFS.Get(t.Context(), rootDataset, false); err == nil || !isVMDatasetNotFoundError(err) {
			t.Fatalf("VM root with cleaned tracked snapshot still exists or lookup failed unexpectedly: %v", err)
		}
		assertVMDeleteGraphCounts(t, db, seed, 0)
	})

	t.Run("requested cleanup failure warns but releases identity", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		seed := seedVMDeleteGraph(t, db, 722, pool, false)
		zfstest.EnsureDataset(t, client, seed.RawDataset)
		snapshot := seed.RawDataset + "@held"
		clone := fmt.Sprintf("%s/delete-held-clone-%d", pool, seed.VM.RID)
		if output, err := exec.Command("zfs", "snapshot", snapshot).CombinedOutput(); err != nil {
			t.Fatalf("create held snapshot: %v\n%s", err, output)
		}
		if output, err := exec.Command("zfs", "clone", snapshot, clone).CombinedOutput(); err != nil {
			t.Fatalf("create dependent clone: %v\n%s", err, output)
		}

		service := &Service{DB: db, GZFS: client}
		result, err := service.removeVMWithWarnings(
			seed.VM.RID, false, true, false, t.Context(), func(uint) error { return nil },
		)
		if err != nil {
			t.Fatalf("storage cleanup failure must not fail VM removal: %v", err)
		}
		if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
			return strings.HasPrefix(warning, "storage_cleanup_incomplete")
		}) {
			t.Fatalf("missing storage cleanup warning: %v", result.Warnings)
		}
		if !slices.Contains(result.RetainedDatasets, seed.RawDataset) {
			t.Fatalf("failed cleanup dataset missing from leftovers: %v", result.RetainedDatasets)
		}
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, seed.VM.RID)
		if !slices.Contains(result.RetainedDatasets, rootDataset) {
			t.Fatalf("failed cleanup root missing from leftovers: %v", result.RetainedDatasets)
		}
		assertVMDeleteGraphCounts(t, db, seed, 0)
		if _, err := client.ZFS.Get(t.Context(), seed.RawDataset, false); err != nil {
			t.Fatalf("failed-cleanup dataset unexpectedly disappeared: %v", err)
		}
	})

	t.Run("unknown child prevents canonical root deletion", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		seed := seedVMDeleteGraph(t, db, 723, pool, false)
		zfstest.EnsureDataset(t, client, seed.RawDataset)
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, seed.VM.RID)
		unknownChild := rootDataset + "/user-kept"
		zfstest.EnsureDataset(t, client, unknownChild)
		root, err := client.ZFS.Get(t.Context(), rootDataset, false)
		if err != nil || root == nil {
			t.Fatalf("get VM root before recursive snapshot: %v", err)
		}
		if _, err := root.Snapshot(t.Context(), seed.SnapshotName, true); err != nil {
			t.Fatalf("create tracked recursive VM snapshot: %v", err)
		}
		unknown, err := client.ZFS.Get(t.Context(), unknownChild, false)
		if err != nil || unknown == nil {
			t.Fatalf("get unknown child before private snapshot: %v", err)
		}
		adminSnapshotName := "administrator-kept"
		if _, err := unknown.Snapshot(t.Context(), adminSnapshotName, false); err != nil {
			t.Fatalf("create administrator snapshot: %v", err)
		}

		service := &Service{DB: db, GZFS: client}
		result, err := service.removeVMWithWarnings(
			seed.VM.RID, false, true, false, t.Context(), func(uint) error { return nil },
		)
		if err != nil {
			t.Fatalf("delete VM with unknown child: %v", err)
		}
		if !slices.ContainsFunc(result.Warnings, func(warning string) bool {
			return strings.Contains(warning, "root_not_empty")
		}) {
			t.Fatalf("missing non-empty root warning: %v", result.Warnings)
		}
		if !slices.Contains(result.RetainedDatasets, rootDataset) {
			t.Fatalf("canonical root missing from retained datasets: %v", result.RetainedDatasets)
		}
		if _, err := client.ZFS.Get(t.Context(), seed.RawDataset, false); err == nil || !isVMDatasetNotFoundError(err) {
			t.Fatalf("selected managed dataset still exists or lookup failed unexpectedly: %v", err)
		}
		if _, err := client.ZFS.Get(t.Context(), unknownChild, false); err != nil {
			t.Fatalf("unknown child was deleted: %v", err)
		}
		trackedChildSnapshot := fmt.Sprintf("%s@%s", unknownChild, seed.SnapshotName)
		if _, err := client.ZFS.Get(t.Context(), trackedChildSnapshot, false); err == nil || !isVMDatasetNotFoundError(err) {
			t.Fatalf("tracked recursive snapshot still exists or lookup failed unexpectedly: %v", err)
		}
		adminSnapshot := fmt.Sprintf("%s@%s", unknownChild, adminSnapshotName)
		if _, err := client.ZFS.Get(t.Context(), adminSnapshot, false); err != nil {
			t.Fatalf("administrator snapshot was deleted: %v", err)
		}
		if _, err := client.ZFS.Get(t.Context(), rootDataset, false); err != nil {
			t.Fatalf("canonical root was deleted: %v", err)
		}
		assertVMDeleteGraphCounts(t, db, seed, 0)
	})

	t.Run("orphan canonical root is discovered and retained", func(t *testing.T) {
		db := newVMDeleteTestDB(t)
		vm := vmModels.VM{Name: "orphan-root", RID: 724}
		if err := db.Create(&vm).Error; err != nil {
			t.Fatalf("seed VM without storage rows: %v", err)
		}
		rootDataset := fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID)
		zfstest.EnsureDataset(t, client, rootDataset)

		service := &Service{
			DB:     db,
			GZFS:   client,
			System: fakeVMCreateSystemService{pools: []*gzfs.ZPool{{Name: pool}}},
		}
		result, err := service.removeVMWithWarnings(
			vm.RID, false, true, true, t.Context(), func(uint) error { return nil },
		)
		if err != nil {
			t.Fatalf("delete VM with orphan canonical root: %v", err)
		}
		if len(result.Warnings) != 0 {
			t.Fatalf("unexpected orphan-root warnings: %v", result.Warnings)
		}
		if !slices.Equal(result.RetainedDatasets, []string{rootDataset}) {
			t.Fatalf("retained datasets = %v, want %v", result.RetainedDatasets, []string{rootDataset})
		}
		if _, err := client.ZFS.Get(t.Context(), rootDataset, false); err != nil {
			t.Fatalf("orphan canonical root was deleted: %v", err)
		}

		var count int64
		if err := db.Model(&vmModels.VM{}).Where("rid = ?", vm.RID).Count(&count).Error; err != nil {
			t.Fatalf("count deleted VM: %v", err)
		}
		if count != 0 {
			t.Fatalf("VM identity count = %d, want 0", count)
		}
	})
}
