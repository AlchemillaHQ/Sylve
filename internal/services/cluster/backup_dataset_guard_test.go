// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func TestDatasetPathEqualOrAncestorNormalized(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		dataset  string
		expected bool
	}{
		{name: "equal", source: " zroot//sylve/jails/100/ ", dataset: "/zroot/sylve/jails/100", expected: true},
		{name: "ancestor", source: "zroot//sylve", dataset: "zroot/sylve/jails/100", expected: true},
		{name: "pool ancestor", source: "/zroot/", dataset: "zroot/sylve/virtual-machines/200", expected: true},
		{name: "descendant is not ancestor", source: "zroot/sylve/jails/100/root", dataset: "zroot/sylve/jails/100", expected: false},
		{name: "component boundary", source: "zroot/sylve", dataset: "zroot/sylve-old/jails/100", expected: false},
		{name: "different pool", source: "tank/data", dataset: "zroot/sylve/jails/100", expected: false},
		{name: "parent traversal rejected", source: "zroot/../tank", dataset: "tank/jails/100", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := datasetPathEqualOrAncestor(test.source, test.dataset); got != test.expected {
				t.Fatalf("datasetPathEqualOrAncestor(%q, %q) = %v, want %v", test.source, test.dataset, got, test.expected)
			}
		})
	}
}

func newManagedDatasetGuardTestService(t *testing.T) *Service {
	t.Helper()
	db := newClusterServiceTestDB(
		t,
		&clusterModels.BackupTarget{},
		&clusterModels.BackupJob{},
		&clusterModels.ClusterNode{},
		&jailModels.Jail{},
		&jailModels.Storage{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
	)
	return &Service{DB: db}
}

func seedManagedDatasetGuardGuests(t *testing.T, service *Service) {
	t.Helper()

	jail := jailModels.Jail{
		CTID: 100,
		Name: "guard-jail",
		Type: jailModels.JailTypeFreeBSD,
	}
	if err := service.DB.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}
	if err := service.DB.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   " /zroot// ",
		GUID:   "guard-jail-guid",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("create jail storage: %v", err)
	}

	vm := vmModels.VM{RID: 200, Name: "guard-vm"}
	if err := service.DB.Create(&vm).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}
	dataset := vmModels.VMStorageDataset{
		Pool: "fast",
		Name: " /fast//sylve/virtual-machines/200/zvol-1/ ",
		GUID: "guard-vm-guid",
	}
	if err := service.DB.Create(&dataset).Error; err != nil {
		t.Fatalf("create VM dataset: %v", err)
	}
	if err := service.DB.Create(&vmModels.Storage{
		VMID:      vm.ID,
		Type:      vmModels.VMStorageTypeZVol,
		Pool:      " fast/ ",
		Enable:    true,
		DatasetID: &dataset.ID,
	}).Error; err != nil {
		t.Fatalf("create VM storage: %v", err)
	}
}

func TestValidateDatasetBackupSourceAgainstManagedGuests(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	seedManagedDatasetGuardGuests(t, service)

	tests := []struct {
		name      string
		source    string
		wantError string
	}{
		{name: "ordinary dataset", source: "tank/ordinary"},
		{name: "jail ancestor", source: " /zroot//sylve/ ", wantError: "dataset_backup_source_reserved_managed_scope"},
		{name: "jail root", source: "zroot/sylve/jails/100", wantError: "dataset_backup_source_reserved_managed_scope"},
		{name: "VM root", source: "fast/sylve/virtual-machines/200", wantError: "dataset_backup_source_reserved_managed_scope"},
		{name: "VM storage", source: "fast/sylve/virtual-machines/200/zvol-1", wantError: "guest_type=vm"},
		{name: "VM storage descendant", source: "fast/sylve/virtual-machines/200/zvol-1/data"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := service.ValidateDatasetBackupSource(context.Background(), test.source)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestValidateDatasetBackupSourceRejectsReservedScopesWithoutInventory(t *testing.T) {
	service := &Service{}
	tests := []string{
		"tank",
		"tank/sylve",
		"tank/sylve/jails",
		"tank/sylve/virtual-machines",
		"tank/sylve/jails/100",
		"tank/sylve/virtual-machines/200",
	}
	for _, source := range tests {
		err := service.ValidateDatasetBackupSource(context.Background(), source)
		if err == nil || !strings.Contains(err.Error(), "dataset_backup_source_reserved_managed_scope") {
			t.Fatalf("reserved source %q error = %v", source, err)
		}
	}
}

func TestValidateDatasetBackupSourceFailsClosedForUnresolvedVMRoot(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	if err := service.DB.Create(&vmModels.VM{RID: 333, Name: "diskless-unresolved"}).Error; err != nil {
		t.Fatalf("create VM: %v", err)
	}

	err := service.ValidateDatasetBackupSource(
		context.Background(),
		"tank/sylve/virtual-machines/333/disk0",
	)
	if err == nil || !strings.Contains(err.Error(), "managed_vm_root_dataset_unresolved") {
		t.Fatalf("unresolved VM inventory error = %v", err)
	}
}

func TestBackupJobValidationRejectsManagedGuestAncestorsOnlyForDatasetMode(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	seedManagedDatasetGuardGuests(t, service)

	target := clusterModels.BackupTarget{
		Name:       "guard-target",
		SSHHost:    "user@backup-host",
		BackupRoot: "backup/root",
		Enabled:    true,
	}
	if err := service.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	enabled := true

	datasetInput := clusterServiceInterfaces.BackupJobReq{
		Name:          "unsafe-dataset-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "zroot/sylve",
		CronExpr:      "0 0 * * *",
		Enabled:       &enabled,
	}
	if err := service.ProposeBackupJobCreate(datasetInput, true); err == nil ||
		!strings.Contains(err.Error(), "dataset_backup_source_reserved_managed_scope") {
		t.Fatalf("unsafe dataset create error = %v", err)
	}

	datasetInput.Name = "ordinary-dataset-job"
	datasetInput.SourceDataset = "tank/ordinary"
	if err := service.ProposeBackupJobCreate(datasetInput, true); err != nil {
		t.Fatalf("ordinary dataset create: %v", err)
	}
	var ordinary clusterModels.BackupJob
	if err := service.DB.Where("name = ?", datasetInput.Name).First(&ordinary).Error; err != nil {
		t.Fatalf("load ordinary dataset job: %v", err)
	}
	datasetInput.SourceDataset = "fast/sylve"
	if err := service.ProposeBackupJobUpdate(ordinary.ID, datasetInput, true); err == nil ||
		!strings.Contains(err.Error(), "dataset_backup_source_reserved_managed_scope") {
		t.Fatalf("unsafe dataset update error = %v", err)
	}

	jailInput := clusterServiceInterfaces.BackupJobReq{
		Name:            "guest-aware-jail-job",
		TargetID:        target.ID,
		Mode:            clusterModels.BackupJobModeJail,
		JailRootDataset: "zroot/sylve/jails/100",
		CronExpr:        "0 1 * * *",
		Enabled:         &enabled,
	}
	if err := service.ProposeBackupJobCreate(jailInput, true); err != nil {
		t.Fatalf("jail-mode create: %v", err)
	}

	vmInput := clusterServiceInterfaces.BackupJobReq{
		Name:          "guest-aware-vm-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "fast/sylve/virtual-machines/200",
		CronExpr:      "0 2 * * *",
		Enabled:       &enabled,
		Recursive:     true,
	}
	if err := service.ProposeBackupJobCreate(vmInput, true); err != nil {
		t.Fatalf("VM-mode create: %v", err)
	}
}

func TestValidateGuestBackupRootsRequireRegisteredCanonicalIdentity(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	seedManagedDatasetGuardGuests(t, service)

	tests := []struct {
		name      string
		job       clusterModels.BackupJob
		wantError string
	}{
		{
			name: "registered normalized jail root",
			job: clusterModels.BackupJob{
				Mode:            clusterModels.BackupJobModeJail,
				JailRootDataset: " /zroot//sylve/jails/100/ ",
			},
		},
		{
			name: "broad sylve jail root",
			job: clusterModels.BackupJob{
				Mode:            clusterModels.BackupJobModeJail,
				JailRootDataset: "zroot/sylve",
			},
			wantError: "jail_backup_requires_registered_canonical_root",
		},
		{
			name: "broad jails namespace",
			job: clusterModels.BackupJob{
				Mode:            clusterModels.BackupJobModeJail,
				JailRootDataset: "zroot/sylve/jails",
			},
			wantError: "jail_backup_requires_registered_canonical_root",
		},
		{
			name: "stale jail identity",
			job: clusterModels.BackupJob{
				Mode:            clusterModels.BackupJobModeJail,
				JailRootDataset: "zroot/sylve/jails/999",
			},
			wantError: "jail_backup_requires_registered_canonical_root",
		},
		{
			name: "registered recursive VM root",
			job: clusterModels.BackupJob{
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: " /fast//sylve/virtual-machines/200/ ",
				Recursive:     true,
			},
		},
		{
			name: "nonrecursive VM root",
			job: clusterModels.BackupJob{
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: "fast/sylve/virtual-machines/200",
			},
			wantError: "vm_backup_requires_recursive",
		},
		{
			name: "VM mode pointed at jail",
			job: clusterModels.BackupJob{
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: "zroot/sylve/jails/100",
				Recursive:     true,
			},
			wantError: "vm_backup_requires_registered_canonical_root",
		},
		{
			name: "stale VM identity",
			job: clusterModels.BackupJob{
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: "fast/sylve/virtual-machines/999",
				Recursive:     true,
			},
			wantError: "vm_backup_requires_registered_canonical_root",
		},
		{
			name: "VM child dataset is not canonical root",
			job: clusterModels.BackupJob{
				Mode:          clusterModels.BackupJobModeVM,
				SourceDataset: "fast/sylve/virtual-machines/200/zvol-1",
				Recursive:     true,
			},
			wantError: "vm_backup_requires_registered_canonical_root",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBackupJobSafetyWithDB(context.Background(), service.DB, &test.job)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestBackupJobCreateAndUpdateEnforceGuestModeSafety(t *testing.T) {
	service := newManagedDatasetGuardTestService(t)
	seedManagedDatasetGuardGuests(t, service)

	target := clusterModels.BackupTarget{
		Name:       "guest-guard-target",
		SSHHost:    "user@backup-host",
		BackupRoot: "backup/root",
		Enabled:    true,
	}
	if err := service.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	enabled := true

	vmInput := clusterServiceInterfaces.BackupJobReq{
		Name:          "guarded-vm-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "fast/sylve/virtual-machines/200",
		CronExpr:      "0 3 * * *",
		Enabled:       &enabled,
	}
	if err := service.ProposeBackupJobCreate(vmInput, true); err == nil ||
		!strings.Contains(err.Error(), "vm_backup_requires_recursive") {
		t.Fatalf("nonrecursive VM create error = %v", err)
	}

	vmInput.Recursive = true
	vmInput.SourceDataset = "zroot/sylve/jails/100"
	if err := service.ProposeBackupJobCreate(vmInput, true); err == nil ||
		!strings.Contains(err.Error(), "vm_backup_requires_registered_canonical_root") {
		t.Fatalf("cross-kind VM create error = %v", err)
	}

	vmInput.SourceDataset = " /fast//sylve/virtual-machines/200/ "
	if err := service.ProposeBackupJobCreate(vmInput, true); err != nil {
		t.Fatalf("canonical recursive VM create: %v", err)
	}
	var vmJob clusterModels.BackupJob
	if err := service.DB.Where("name = ?", vmInput.Name).First(&vmJob).Error; err != nil {
		t.Fatalf("load VM job: %v", err)
	}
	if vmJob.SourceDataset != "fast/sylve/virtual-machines/200" || !vmJob.Recursive {
		t.Fatalf("stored VM job = source %q recursive=%v", vmJob.SourceDataset, vmJob.Recursive)
	}
	vmInput.Recursive = false
	if err := service.ProposeBackupJobUpdate(vmJob.ID, vmInput, true); err == nil ||
		!strings.Contains(err.Error(), "vm_backup_requires_recursive") {
		t.Fatalf("nonrecursive VM update error = %v", err)
	}

	jailInput := clusterServiceInterfaces.BackupJobReq{
		Name:            "guarded-jail-job",
		TargetID:        target.ID,
		Mode:            clusterModels.BackupJobModeJail,
		JailRootDataset: "zroot/sylve/jails",
		CronExpr:        "0 4 * * *",
		Enabled:         &enabled,
	}
	if err := service.ProposeBackupJobCreate(jailInput, true); err == nil ||
		!strings.Contains(err.Error(), "jail_backup_requires_registered_canonical_root") {
		t.Fatalf("broad jail create error = %v", err)
	}
	jailInput.JailRootDataset = "zroot/sylve/jails/999"
	if err := service.ProposeBackupJobCreate(jailInput, true); err == nil ||
		!strings.Contains(err.Error(), "jail_backup_requires_registered_canonical_root") {
		t.Fatalf("stale jail create error = %v", err)
	}
	jailInput.JailRootDataset = " /zroot//sylve/jails/100/ "
	if err := service.ProposeBackupJobCreate(jailInput, true); err != nil {
		t.Fatalf("canonical jail create: %v", err)
	}
	var jailJob clusterModels.BackupJob
	if err := service.DB.Where("name = ?", jailInput.Name).First(&jailJob).Error; err != nil {
		t.Fatalf("load jail job: %v", err)
	}
	if jailJob.JailRootDataset != "zroot/sylve/jails/100" {
		t.Fatalf("stored jail root = %q", jailJob.JailRootDataset)
	}
	jailInput.JailRootDataset = "zroot/sylve"
	if err := service.ProposeBackupJobUpdate(jailJob.ID, jailInput, true); err == nil ||
		!strings.Contains(err.Error(), "jail_backup_requires_registered_canonical_root") {
		t.Fatalf("broad jail update error = %v", err)
	}
}
