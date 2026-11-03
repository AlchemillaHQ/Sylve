// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	_ "github.com/mattn/go-sqlite3"
)

type fullBackupJailStub struct {
	jailServiceInterfaces.JailServiceInterface
	ctid    uint
	ctErr   error
	stopErr error
}

func (s fullBackupJailStub) GetJailCTIDFromDataset(_ string) (uint, error) {
	return s.ctid, s.ctErr
}

func (s fullBackupJailStub) JailAction(_ int, action string) error {
	return s.stopErr
}

func (s fullBackupJailStub) WriteJailJSON(uint) error {
	return nil
}

type fullBackupVMStub struct {
	libvirtServiceInterfaces.LibvirtServiceInterface
}

func (fullBackupVMStub) WriteVMJson(uint) error {
	return nil
}

func newBackupServiceForIntegration(t *testing.T) *Service {
	t.Helper()
	db := newZeltaServiceTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupTarget{},
		&clusterModels.BackupEvent{},
		&jailModels.Jail{},
		&jailModels.Storage{},
		&vmModels.VM{},
		&vmModels.Storage{},
		&vmModels.VMStorageDataset{},
		&vmModels.Network{},
		&vmModels.VMCPUPinning{},
	)
	return &Service{
		DB:                db,
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
}

func TestRunBackupJobZeltaBinaryNotInstalled(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 200, "no-binary", "dataset", 1, "tank/data")

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	t.Setenv("PATH", t.TempDir())

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil {
		t.Fatal("expected error when zelta binary missing")
	}

	updated := fetchJob(t, svc.DB, 200)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status, got %q", updated.LastStatus)
	}
	if updated.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

func TestRunBackupJobStopBeforeBackupWithoutJailService(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	svc.Jail = fullBackupJailStub{ctid: 42, ctErr: nil}
	seedBackupTarget(t, svc.DB, 1, "t1")
	jail := jailModels.Jail{CTID: 42, Name: "integration-jail", Type: jailModels.JailTypeFreeBSD}
	if err := svc.DB.Create(&jail).Error; err != nil {
		t.Fatalf("failed to seed registered jail: %v", err)
	}
	if err := svc.DB.Create(&jailModels.Storage{
		JailID: jail.ID,
		Pool:   "zroot",
		GUID:   "integration-jail-root",
		Name:   "Base Filesystem",
		IsBase: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed registered jail storage: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 400, Name: "jail-stop", Mode: "jail", TargetID: 1,
		JailRootDataset: "zroot/sylve/jails/42", CronExpr: "0 0 * * *",
		Enabled: true, StopBeforeBackup: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	svc.DB.Preload("Target").First(&loaded, 400)

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	t.Setenv("PATH", t.TempDir())

	err := svc.runBackupJob(context.Background(), &loaded)
	if err == nil {
		t.Fatal("expected error from zelta binary not found")
	}
	if strings.Contains(err.Error(), "backup_job_safety_validation_failed") {
		t.Fatalf("registered canonical jail failed runtime safety guard: %v", err)
	}

	updated := fetchJob(t, svc.DB, 400)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status, got %q with error %q", updated.LastStatus, updated.LastError)
	}
}

func TestRunBackupJobVMWithZeltaBinaryMissing(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	svc.VM = fullBackupVMStub{}
	seedBackupTarget(t, svc.DB, 1, "t1")
	vm := vmModels.VM{RID: 42, Name: "integration-vm"}
	if err := svc.DB.Create(&vm).Error; err != nil {
		t.Fatalf("failed to seed registered VM: %v", err)
	}
	if err := svc.DB.Create(&vmModels.Storage{
		VMID:   vm.ID,
		Type:   vmModels.VMStorageTypeFilesystem,
		Pool:   "zroot",
		Enable: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed registered VM storage: %v", err)
	}

	job := clusterModels.BackupJob{
		ID: 500, Name: "vm-backup", Mode: "vm", TargetID: 1,
		SourceDataset: "zroot/sylve/virtual-machines/42", CronExpr: "0 0 * * *",
		Enabled: true, Recursive: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	svc.DB.Preload("Target").First(&loaded, 500)

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	t.Setenv("PATH", t.TempDir())

	err := svc.runBackupJob(context.Background(), &loaded)
	if err == nil {
		t.Fatal("expected error from VM backup without zelta")
	}
	if strings.Contains(err.Error(), "backup_job_safety_validation_failed") {
		t.Fatalf("registered canonical VM failed runtime safety guard: %v", err)
	}
}
