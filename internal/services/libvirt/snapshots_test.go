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
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

func TestCreateVMSnapshotHoldsSwitchLifecycleThroughZFSSnapshot(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := testutil.NewSQLiteTestDB(t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&vmModels.VMStorageDataset{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMStats{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMSnapshot{},
		&vmModels.VM{},
	)
	vm := vmModels.VM{Name: "snapshot-lock", RID: 151}
	if err := db.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	sw := networkModels.StandardSwitch{Name: "tenant", BridgeName: "vm-tenant"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatalf("create Standard Switch: %v", err)
	}
	if err := db.Create(&vmModels.Network{
		VMID: vm.ID, SwitchID: sw.ID, SwitchType: "standard", Enable: true,
	}).Error; err != nil {
		t.Fatalf("create VM network: %v", err)
	}
	if err := db.Create(&vmModels.Storage{
		VMID: vm.ID, Type: vmModels.VMStorageTypeRaw, Pool: "tank", Enable: true,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}

	root := "tank/sylve/virtual-machines/151"
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	runner := &storageTestZFSRunner{
		datasets: map[string]storageTestDataset{
			root: {
				name: root, pool: "tank", kind: gzfs.DatasetTypeFilesystem,
				mountpoint: t.TempDir(),
			},
		},
		snapshotStarted: snapshotStarted,
		releaseSnapshot: releaseSnapshot,
	}
	service := &Service{
		DB:   db,
		GZFS: gzfs.NewClient(gzfs.Options{Runner: runner}),
	}
	snapshotDone := make(chan error, 1)
	go func() {
		_, err := service.CreateVMSnapshot(context.Background(), vm.RID, "locked", "")
		snapshotDone <- err
	}()

	select {
	case <-snapshotStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("VM snapshot did not reach the ZFS boundary")
	}

	lockAttempted := make(chan struct{})
	lockAcquired := make(chan struct{})
	go func() {
		close(lockAttempted)
		unlock := bridgevlan.LockStandardSwitchLifecycle(sw.BridgeName)
		close(lockAcquired)
		unlock()
	}()
	<-lockAttempted
	select {
	case <-lockAcquired:
		t.Fatal("switch lifecycle lock was released before the ZFS snapshot")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseSnapshot)
	if err := <-snapshotDone; err != nil {
		t.Fatalf("create VM snapshot: %v", err)
	}
	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("switch lifecycle lock was not released after the VM snapshot")
	}
}

func TestRollbackVMSnapshotRequiresAcknowledgementForNewerSnapshots(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMSnapshot{})
	if err := db.AutoMigrate(&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationLease{}); err != nil {
		t.Fatalf("migrate replication guard tables: %v", err)
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	selected := vmModels.VMSnapshot{
		VMID: 1, RID: 42, Name: "selected", SnapshotName: "selected-zfs", CreatedAt: createdAt,
	}
	if err := db.Create(&selected).Error; err != nil {
		t.Fatalf("create selected snapshot: %v", err)
	}
	newer := vmModels.VMSnapshot{
		VMID: 1, RID: 42, Name: "newer", SnapshotName: "newer-zfs", CreatedAt: createdAt.Add(time.Second),
	}
	if err := db.Create(&newer).Error; err != nil {
		t.Fatalf("create newer snapshot: %v", err)
	}

	service := &Service{DB: db}
	result, err := service.RollbackVMSnapshotWithDestroyNewer(context.Background(), 42, selected.ID, false)
	if err == nil || !strings.Contains(err.Error(), "newer_snapshots_require_acknowledgement") {
		t.Fatalf("rollback error = %v", err)
	}
	if result.NewerSnapshotsDestroyed != 0 {
		t.Fatalf("rollback result = %#v", result)
	}

	var count int64
	if err := db.Model(&vmModels.VMSnapshot{}).Count(&count).Error; err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if count != 2 {
		t.Fatalf("rollback preflight changed snapshot records: count = %d", count)
	}
}

func TestDetachedVMSnapshotContextOutlivesCanceledRequest(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := detachedVMSnapshotContext(parent, time.Minute)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("detached context was canceled with request: %v", ctx.Err())
	default:
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("detached context must retain a bounded deadline")
	}
}

func TestReparentAndDeleteVMSnapshotRecord(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VMSnapshot{})

	parent := vmModels.VMSnapshot{VMID: 1, RID: 42, Name: "parent", SnapshotName: "parent-zfs"}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create parent: %v", err)
	}
	middleParentID := parent.ID
	middle := vmModels.VMSnapshot{
		VMID:             1,
		RID:              42,
		ParentSnapshotID: &middleParentID,
		Name:             "middle",
		SnapshotName:     "middle-zfs",
	}
	if err := db.Create(&middle).Error; err != nil {
		t.Fatalf("create middle: %v", err)
	}
	childParentID := middle.ID
	child := vmModels.VMSnapshot{
		VMID:             1,
		RID:              42,
		ParentSnapshotID: &childParentID,
		Name:             "child",
		SnapshotName:     "child-zfs",
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child: %v", err)
	}

	if err := reparentAndDeleteVMSnapshotRecord(db, middle); err != nil {
		t.Fatalf("delete middle snapshot: %v", err)
	}

	var restoredChild vmModels.VMSnapshot
	if err := db.First(&restoredChild, child.ID).Error; err != nil {
		t.Fatalf("load child: %v", err)
	}
	if restoredChild.ParentSnapshotID == nil || *restoredChild.ParentSnapshotID != parent.ID {
		t.Fatalf("child parent = %v, want %d", restoredChild.ParentSnapshotID, parent.ID)
	}
	if err := db.First(&vmModels.VMSnapshot{}, middle.ID).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("middle snapshot should be deleted, got %v", err)
	}
}

func TestNormalizeRestoredVNCPreservesCurrentSettingsOnConflict(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &vmModels.VM{})
	current := vmModels.VM{
		Name:          "current",
		RID:           42,
		VNCEnabled:    true,
		VNCPort:       5900,
		VNCBind:       "127.0.0.1",
		VNCPassword:   "current-secret",
		VNCResolution: "800x600",
	}
	other := vmModels.VM{
		Name:          "other",
		RID:           43,
		VNCEnabled:    true,
		VNCPort:       5901,
		VNCBind:       "127.0.0.1",
		VNCResolution: "800x600",
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create current VM: %v", err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("create other VM: %v", err)
	}

	restored := current
	restored.VNCPort = other.VNCPort
	restored.VNCPassword = "historical-secret"
	service := &Service{DB: db}
	warnings, err := service.normalizeRestoredVNC(current.RID, current, &restored)
	if err != nil {
		t.Fatalf("normalize restored VNC: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one conflict warning", warnings)
	}
	if restored.VNCPort != current.VNCPort ||
		restored.VNCPassword != current.VNCPassword ||
		restored.VNCResolution != current.VNCResolution {
		t.Fatalf("current VNC settings were not preserved: %+v", restored)
	}
}

func TestRestoreVMDatabaseFromSnapshotPreservesResourceIdentity(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&vmModels.VM{},
		&vmModels.VMCPUPinning{},
		&vmModels.VMStorageDataset{},
		&vmModels.Storage{},
		&vmModels.Network{},
		&vmModels.VMSnapshot{},
	)
	current := vmModels.VM{
		Name:          "current-name",
		Description:   "current description",
		RID:           42,
		CPUSockets:    1,
		CPUCores:      1,
		CPUThreads:    1,
		RAM:           1024,
		VNCEnabled:    false,
		VNCBind:       DefaultVNCBindAddress,
		VNCResolution: "800x600",
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create current VM: %v", err)
	}

	restored := current
	restored.Name = "historical-name"
	restored.Description = "restored description"
	restored.RAM = 2048
	service := &Service{DB: db}
	if err := service.restoreVMDatabaseFromSnapshotConfig(current.RID, restored); err != nil {
		t.Fatalf("restore VM config: %v", err)
	}

	var got vmModels.VM
	if err := db.Where("rid = ?", current.RID).First(&got).Error; err != nil {
		t.Fatalf("load restored VM: %v", err)
	}
	if got.Name != current.Name {
		t.Fatalf("VM name changed to %q, want %q", got.Name, current.Name)
	}
	if got.RID != current.RID {
		t.Fatalf("VM RID changed to %d, want %d", got.RID, current.RID)
	}
	if got.Description != restored.Description || got.RAM != restored.RAM {
		t.Fatalf("configuration was not restored: %+v", got)
	}
}

func TestRestoredVMStorageDatasetMustBelongToRecordedRoot(t *testing.T) {
	roots := []string{"zroot/sylve/virtual-machines/42"}
	if !datasetBelongsToVMRoots("zroot/sylve/virtual-machines/42/raw-1", roots) {
		t.Fatal("expected VM child dataset to belong to root")
	}
	if datasetBelongsToVMRoots("tank/sylve/virtual-machines/42/raw-1", roots) {
		t.Fatal("dataset from an unrecorded pool must not belong to the VM root")
	}
}

func TestNormalizeRestoredVMNetworksRejectsChangedVLANAttachment(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&vmModels.Network{},
	)
	currentVLAN := 20
	switchModel := networkModels.StandardSwitch{
		Name: "LAN", BridgeName: "bridge0", VLANFiltering: true, DefaultAccessVLAN: &currentVLAN,
	}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("create switch: %v", err)
	}

	attachment := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindVM,
		SwitchName: switchModel.Name, SwitchType: "standard", VLANFiltering: false,
	}
	_, _, err := (&Service{DB: db}).normalizeRestoredVMNetworks(1, []vmModels.Network{{
		SwitchID: switchModel.ID, SwitchType: "standard", Attachment: &attachment, Enable: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "vm_network_vlan_mode_mismatch") {
		t.Fatalf("VLAN attachment mismatch error = %v", err)
	}
}

func TestNormalizeRestoredVMNetworksRejectsMissingSwitch(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t,
		&networkModels.StandardSwitch{},
		&vmModels.Network{},
	)
	attachment := networkAttachment.Contract{
		Version:    networkAttachment.CurrentVersion,
		Kind:       networkAttachment.KindVM,
		SwitchName: "missing",
		SwitchType: "standard",
	}

	_, _, err := (&Service{DB: db}).normalizeRestoredVMNetworks(1, []vmModels.Network{{
		Enable:     true,
		Attachment: &attachment,
	}})
	if err == nil || !strings.Contains(err.Error(), "switch_not_found") {
		t.Fatalf("missing switch error = %v", err)
	}
}
