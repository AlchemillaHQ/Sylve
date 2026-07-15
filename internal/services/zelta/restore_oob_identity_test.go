// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestResolveOOBGuestRestoreDestinationIdentity(t *testing.T) {
	tests := []struct {
		name        string
		remote      string
		destination string
		want        *oobGuestRestoreDestination
		wantError   string
	}{
		{
			name:        "VM restore as new",
			remote:      "backup/root/virtual-machines/107",
			destination: "/tank/sylve/virtual-machines/108/",
			want: &oobGuestRestoreDestination{
				Kind: clusterModels.BackupJobModeVM, GuestID: 108,
				Dataset: "tank/sylve/virtual-machines/108",
			},
		},
		{
			name:        "jail restore as new",
			remote:      "backup/root/jails/107",
			destination: "tank/sylve/jails/108",
			want: &oobGuestRestoreDestination{
				Kind: clusterModels.BackupJobModeJail, GuestID: 108,
				Dataset: "tank/sylve/jails/108",
			},
		},
		{
			name:        "same source and destination ID is valid when unused",
			remote:      "backup/root/virtual-machines/107",
			destination: "tank/sylve/virtual-machines/107",
			want: &oobGuestRestoreDestination{
				Kind: clusterModels.BackupJobModeVM, GuestID: 107,
				Dataset: "tank/sylve/virtual-machines/107",
			},
		},
		{
			name:        "ordinary dataset",
			remote:      "backup/root/data/source",
			destination: "tank/data/restored",
		},
		{
			name:        "VM cannot become jail",
			remote:      "backup/root/virtual-machines/107",
			destination: "tank/sylve/jails/108",
			wantError:   "restore_guest_destination_kind_mismatch",
		},
		{
			name:        "dataset cannot become VM",
			remote:      "backup/root/data/source",
			destination: "tank/sylve/virtual-machines/108",
			wantError:   "restore_guest_destination_kind_mismatch",
		},
		{
			name:        "guest destination must be canonical",
			remote:      "backup/root/virtual-machines/107",
			destination: "tank/virtual-machines/108",
			wantError:   "restore_guest_destination_must_be_canonical_root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOOBGuestRestoreDestination("backup/root", tt.remote, tt.destination)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve OOB destination: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("destination = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCanonicalGuestRestoreDestination(t *testing.T) {
	tests := []struct {
		dataset string
		kind    string
		id      uint
		want    bool
	}{
		{"tank/sylve/virtual-machines/108", clusterModels.BackupJobModeVM, 108, true},
		{"tank/sylve/jails/108", clusterModels.BackupJobModeJail, 108, true},
		{"tank/sylve/virtual-machines/108/disk", clusterModels.BackupJobModeVM, 108, false},
		{"tank/virtual-machines/108", clusterModels.BackupJobModeVM, 108, false},
		{"tank/sylve/virtual-machines/0108", clusterModels.BackupJobModeVM, 108, false},
		{"tank/sylve/virtual-machines/108", clusterModels.BackupJobModeJail, 108, false},
	}
	for _, tt := range tests {
		if got := canonicalGuestRestoreDestination(tt.dataset, tt.kind, tt.id); got != tt.want {
			t.Errorf("canonicalGuestRestoreDestination(%q, %q, %d) = %t, want %t", tt.dataset, tt.kind, tt.id, got, tt.want)
		}
	}
}

func TestOOBVMIdentityRewrite107To108(t *testing.T) {
	if got := canonicalVMDatasetRoot("tank/sylve/virtual-machines/107/zvol-9", 108); got != "tank/sylve/virtual-machines/108" {
		t.Fatalf("canonical VM root = %q", got)
	}
	if got := destinationVMRootFromRemoteRoot(
		"backup/root",
		"backup/root/virtual-machines/107",
		"tank/sylve/virtual-machines/108",
		108,
	); got != "tank/sylve/virtual-machines/108" {
		t.Fatalf("destination VM root = %q", got)
	}

	metadata := &restoredVMMetadata{VM: vmModels.VM{
		RID: 107,
		Storages: []vmModels.Storage{
			{
				ID: 9, Pool: "stale",
				Dataset: vmModels.VMStorageDataset{
					ID: 19, Pool: "stale", Name: "tank/sylve/virtual-machines/107/zvol-9",
				},
			},
			{
				ID: 10, Pool: "old",
				Dataset: vmModels.VMStorageDataset{
					ID: 20, Pool: "old", Name: "fast/sylve/virtual-machines/107/raw-10",
				},
			},
		},
	}, Snapshots: []vmModels.VMSnapshot{{
		VMID: 44,
		RID:  107,
		RootDatasets: []string{
			"tank/sylve/virtual-machines/107",
			"fast/sylve/virtual-machines/107",
		},
	}}}
	rewriteRestoredVMMetadataIdentity(metadata, 108)

	if metadata.VM.RID != 108 {
		t.Fatalf("metadata RID = %d, want 108", metadata.VM.RID)
	}
	wantDatasets := []string{
		"tank/sylve/virtual-machines/108/zvol-9",
		"fast/sylve/virtual-machines/108/raw-10",
	}
	for i, want := range wantDatasets {
		storage := metadata.VM.Storages[i]
		if storage.Dataset.Name != want {
			t.Fatalf("storage %d dataset = %q, want %q", i, storage.Dataset.Name, want)
		}
		wantPool := strings.Split(want, "/")[0]
		if storage.Pool != wantPool || storage.Dataset.Pool != wantPool {
			t.Fatalf("storage %d pools = (%q, %q), want %q", i, storage.Pool, storage.Dataset.Pool, wantPool)
		}
	}
	if metadata.Snapshots[0].RID != 108 || metadata.Snapshots[0].VMID != 0 {
		t.Fatalf("snapshot identity = RID %d VMID %d", metadata.Snapshots[0].RID, metadata.Snapshots[0].VMID)
	}
	wantSnapshotRoots := []string{
		"tank/sylve/virtual-machines/108",
		"fast/sylve/virtual-machines/108",
	}
	if !reflect.DeepEqual(metadata.Snapshots[0].RootDatasets, wantSnapshotRoots) {
		t.Fatalf("snapshot roots = %v, want %v", metadata.Snapshots[0].RootDatasets, wantSnapshotRoots)
	}
}

func TestOOBVMPrimaryRootRebasePreservesSecondaryRoots(t *testing.T) {
	metadata := &restoredVMMetadata{VM: vmModels.VM{
		RID: 107,
		Storages: []vmModels.Storage{
			{
				Type: vmModels.VMStorageTypeZVol,
				Pool: "zroot",
				Dataset: vmModels.VMStorageDataset{
					Pool: "zroot", Name: "zroot/sylve/virtual-machines/107/zvol-9",
				},
			},
			{
				Type: vmModels.VMStorageTypeRaw,
				Pool: "fast",
				Dataset: vmModels.VMStorageDataset{
					Pool: "fast", Name: "fast/sylve/virtual-machines/107/raw-10",
				},
			},
		},
	}, Snapshots: []vmModels.VMSnapshot{{
		RID: 107,
		RootDatasets: []string{
			"zroot/sylve/virtual-machines/107",
			"fast/sylve/virtual-machines/107",
		},
	}}}

	rewriteRestoredVMMetadataIdentity(metadata, 108)
	rebaseRestoredVMMetadataRoot(
		metadata,
		"zroot/sylve/virtual-machines/107",
		"tank/sylve/virtual-machines/108",
	)

	wantDatasets := []string{
		"tank/sylve/virtual-machines/108/zvol-9",
		"fast/sylve/virtual-machines/108/raw-10",
	}
	for idx, want := range wantDatasets {
		storage := metadata.VM.Storages[idx]
		if storage.Dataset.Name != want {
			t.Fatalf("storage %d dataset = %q, want %q", idx, storage.Dataset.Name, want)
		}
		wantPool := strings.Split(want, "/")[0]
		if storage.Pool != wantPool || storage.Dataset.Pool != wantPool {
			t.Fatalf("storage %d pools = (%q, %q), want %q", idx, storage.Pool, storage.Dataset.Pool, wantPool)
		}
	}

	wantSnapshotRoots := []string{
		"tank/sylve/virtual-machines/108",
		"fast/sylve/virtual-machines/108",
	}
	if !reflect.DeepEqual(metadata.Snapshots[0].RootDatasets, wantSnapshotRoots) {
		t.Fatalf("snapshot roots = %v, want %v", metadata.Snapshots[0].RootDatasets, wantSnapshotRoots)
	}
}

func TestOOBVMLegacyPoolOnlyPrimaryRootRebase(t *testing.T) {
	metadata := &restoredVMMetadata{VM: vmModels.VM{
		RID: 108,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeZVol, Pool: "zroot"},
			{Type: vmModels.VMStorageTypeRaw, Pool: "fast"},
		},
	}, Snapshots: []vmModels.VMSnapshot{{
		RID: 108,
		RootDatasets: []string{
			"zroot/sylve/virtual-machines/108",
			"fast/sylve/virtual-machines/108",
		},
	}}}

	rebaseRestoredVMMetadataRoot(
		metadata,
		"zroot/sylve/virtual-machines/107",
		"tank/sylve/virtual-machines/108",
	)

	if got := metadata.VM.Storages[0].Pool; got != "tank" {
		t.Fatalf("primary legacy storage pool = %q, want tank", got)
	}
	if got := metadata.VM.Storages[0].Dataset.Pool; got != "tank" {
		t.Fatalf("primary legacy dataset pool = %q, want tank", got)
	}
	if got := metadata.VM.Storages[1].Pool; got != "fast" {
		t.Fatalf("secondary legacy storage pool = %q, want fast", got)
	}
	wantSnapshotRoots := []string{
		"tank/sylve/virtual-machines/108",
		"fast/sylve/virtual-machines/108",
	}
	if !reflect.DeepEqual(metadata.Snapshots[0].RootDatasets, wantSnapshotRoots) {
		t.Fatalf("snapshot roots = %v, want %v", metadata.Snapshots[0].RootDatasets, wantSnapshotRoots)
	}
}

func TestOOBVMAmbiguousLegacyRootIsNotGuessed(t *testing.T) {
	metadata := &restoredVMMetadata{VM: vmModels.VM{
		RID: 108,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeZVol, Pool: "zroot"},
			{Type: vmModels.VMStorageTypeRaw, Pool: "fast"},
		},
	}}

	rebaseRestoredVMMetadataRoot(
		metadata,
		"virtual-machines/107",
		"tank/sylve/virtual-machines/108",
	)

	if metadata.VM.Storages[0].Pool != "zroot" || metadata.VM.Storages[1].Pool != "fast" {
		t.Fatalf("ambiguous legacy pools were changed: %+v", metadata.VM.Storages)
	}
}

func TestOOBVMKnownPrimaryPoolNeverRebasesSoleSecondaryRoot(t *testing.T) {
	metadata := &restoredVMMetadata{VM: vmModels.VM{
		RID: 108,
		Storages: []vmModels.Storage{
			{Type: vmModels.VMStorageTypeDiskImage, Pool: "zroot"},
			{Type: vmModels.VMStorageTypeRaw, Pool: "fast"},
		},
	}}

	rebaseRestoredVMMetadataRoot(
		metadata,
		"zroot/sylve/virtual-machines/107",
		"tank/sylve/virtual-machines/108",
	)

	if metadata.VM.Storages[1].Pool != "fast" {
		t.Fatalf("secondary storage pool = %q, want fast", metadata.VM.Storages[1].Pool)
	}
}

func TestSelectPrimaryRemoteVMRootHonorsSelectedStoragePool(t *testing.T) {
	roots := []string{
		"backup/root/fast/sylve/virtual-machines/107",
		"backup/root/zroot/sylve/virtual-machines/107",
	}
	selected := selectPrimaryRemoteVMRoot(
		"backup/root",
		"backup/root/zroot/sylve/virtual-machines/107_gen-5",
		roots,
		107,
	)
	if selected != roots[1] {
		t.Fatalf("primary remote root = %q, want %q", selected, roots[1])
	}
	if got := destinationVMRootForRestore(
		"backup/root",
		selected,
		selected,
		"tank/sylve/virtual-machines/108",
		108,
	); got != "tank/sylve/virtual-machines/108" {
		t.Fatalf("selected destination root = %q", got)
	}
	if got := destinationVMRootForRestore(
		"backup/root",
		roots[0],
		selected,
		"tank/sylve/virtual-machines/108",
		108,
	); got != "fast/sylve/virtual-machines/108" {
		t.Fatalf("secondary destination root = %q", got)
	}
}

func TestOOBVMRestoreRootCollisionHardFails(t *testing.T) {
	roots := []string{
		"backup/root/zroot/sylve/virtual-machines/107",
		"backup/root/tank/sylve/virtual-machines/107",
	}
	primary := roots[0]
	_, err := buildVMRestoreRootPlans(roots, func(remoteRoot string) string {
		return destinationVMRootForRestore(
			"backup/root",
			remoteRoot,
			primary,
			"tank/sylve/virtual-machines/108",
			108,
		)
	})
	if err == nil || !strings.Contains(err.Error(), "restore_vm_destination_root_collision") {
		t.Fatalf("restore root collision error = %v", err)
	}
}

func TestOOBJailIdentityRewrite107To108(t *testing.T) {
	metadata := &restoredJailMetadata{Jail: jailModels.Jail{CTID: 107, Name: "jail-107"}}
	rewriteRestoredJailMetadataIdentity(metadata, 108)
	if metadata.Jail.CTID != 108 {
		t.Fatalf("metadata CTID = %d, want 108", metadata.Jail.CTID)
	}
}

func TestOOBJailReconcileNeverUpdatesExistingDestinationRecord(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &jailModels.Jail{}, &vmModels.VM{})
	existing := jailModels.Jail{CTID: 108, Name: "existing-108", Type: jailModels.JailTypeFreeBSD}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing jail: %v", err)
	}

	service := &Service{DB: database}
	_, err := service.upsertRestoredJailState(
		t.Context(),
		"tank/sylve/jails/108",
		&restoredJailMetadata{Jail: jailModels.Jail{CTID: 108, Name: "replacement-108"}},
		false,
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
		t.Fatalf("strict jail reconcile error = %v", err)
	}

	var reloaded jailModels.Jail
	if err := database.First(&reloaded, existing.ID).Error; err != nil {
		t.Fatalf("reload existing jail: %v", err)
	}
	if reloaded.Name != "existing-108" {
		t.Fatalf("existing jail was modified: name=%q", reloaded.Name)
	}
}

func TestVMRuntimeArtifactNamesRestoreAsNew(t *testing.T) {
	want := []vmRuntimeArtifactName{
		{Source: "107_vars.fd", Destination: "108_vars.fd"},
		{Source: "107_tpm.log", Destination: "108_tpm.log"},
		{Source: "107_tpm.state", Destination: "108_tpm.state"},
	}
	if got := vmRuntimeArtifactNames(107, 108); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime artifact names = %+v, want %+v", got, want)
	}
}

func TestOOBRestorePreflightUsesSharedLiveGuestIDNamespace(t *testing.T) {
	tests := []struct {
		name        string
		remote      string
		destination string
		occupied    any
	}{
		{
			name:        "VM RID conflicts with jail CTID",
			remote:      "backup/root/virtual-machines/107",
			destination: "tank/sylve/virtual-machines/108",
			occupied:    &jailModels.Jail{CTID: 108, Name: "jail-108"},
		},
		{
			name:        "jail CTID conflicts with VM RID",
			remote:      "backup/root/jails/107",
			destination: "tank/sylve/jails/108",
			occupied:    &vmModels.VM{RID: 108, Name: "vm-108"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(
				t,
				&clusterModels.Cluster{},
				&vmModels.VM{},
				&jailModels.Jail{},
			)
			if err := database.Create(tt.occupied).Error; err != nil {
				t.Fatalf("seed occupied guest ID: %v", err)
			}
			service := &Service{
				DB: database,
				Cluster: &clusterService.Service{
					DB: database, NodeID: "standalone-node",
				},
			}
			_, err := service.preflightOOBGuestRestoreDestination(
				t.Context(),
				&clusterModels.BackupTarget{BackupRoot: "backup/root"},
				tt.remote,
				tt.destination,
			)
			if err == nil || !strings.Contains(err.Error(), "guest_id_already_in_use") {
				t.Fatalf("shared guest-ID preflight error = %v", err)
			}
		})
	}
}

func TestOOBRestoreExistingCanonicalDestinationIsNeverReplacedRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if testing.Short() {
		t.Skip("skipping real ZFS OOB restore identity integration test in short mode")
	}

	pool, client, cleanup := zfstest.Pool(t)
	defer cleanup()

	restorePath := pool + "/restore-candidate"
	destination := pool + "/sylve/virtual-machines/108"
	zfstest.EnsureDataset(t, client, restorePath+"/candidate-only")
	zfstest.EnsureDataset(t, client, destination+"/existing-only")

	database := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	service := &Service{
		DB: database,
		Cluster: &clusterService.Service{
			DB: database, NodeID: "standalone-node",
		},
		GZFS: client,
	}
	target := &clusterModels.BackupTarget{BackupRoot: "backup/root"}
	freeDestination := pool + "/sylve/virtual-machines/109"
	if _, err := service.preflightOOBGuestRestoreDestination(
		t.Context(), target,
		"backup/root/virtual-machines/107",
		freeDestination,
	); err != nil {
		t.Fatalf("unused OOB destination rejected: %v", err)
	}
	_, err := service.preflightOOBGuestRestoreDestination(
		t.Context(), target,
		"backup/root/virtual-machines/107",
		destination,
	)
	if err == nil || !strings.Contains(err.Error(), "restore_destination_guest_dataset_exists") {
		t.Fatalf("existing canonical destination preflight error = %v", err)
	}

	destinationGUID, err := service.runLocalZFSGet(t.Context(), "guid", destination)
	if err != nil {
		t.Fatalf("read destination GUID before promotion: %v", err)
	}
	if err := service.promoteRestoredDatasetAsNew(t.Context(), restorePath, destination); err == nil ||
		!strings.Contains(err.Error(), "restore_destination_guest_dataset_exists") {
		t.Fatalf("create-only promotion error = %v", err)
	}
	afterGUID, err := service.runLocalZFSGet(t.Context(), "guid", destination)
	if err != nil {
		t.Fatalf("read destination GUID after promotion: %v", err)
	}
	if strings.TrimSpace(afterGUID) != strings.TrimSpace(destinationGUID) {
		t.Fatalf("destination was replaced: GUID %q became %q", destinationGUID, afterGUID)
	}

	for _, dataset := range []string{
		restorePath,
		restorePath + "/candidate-only",
		destination,
		destination + "/existing-only",
	} {
		exists, checkErr := service.localDatasetExists(t.Context(), dataset)
		if checkErr != nil {
			t.Fatalf("check dataset %s: %v", dataset, checkErr)
		}
		if !exists {
			t.Fatalf("promotion removed or replaced %s", dataset)
		}
	}
	if exists, checkErr := service.localDatasetExists(t.Context(), destination+"/candidate-only"); checkErr != nil {
		t.Fatalf("check candidate under destination: %v", checkErr)
	} else if exists {
		t.Fatal("restore candidate contents appeared below the existing destination")
	}
}
