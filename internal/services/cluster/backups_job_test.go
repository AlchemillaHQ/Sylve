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

func TestUpdateBackupJobRuntimeStatePreservesOmittedEncryption(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupJob{})
	s := &Service{DB: db}
	job := clusterModels.BackupJob{
		ID: 41, Name: "runtime-state", TargetID: 9,
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "tank/data",
		CronExpr: "0 0 * * *", Enabled: true, Encrypted: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}

	if err := s.UpdateBackupJobRuntimeState(BackupJobRuntimeStateUpdate{
		JobID: 41, LastStatus: "failed", LastError: "legacy runner",
	}, true); err != nil {
		t.Fatalf("legacy update: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload legacy update: %v", err)
	}
	if !job.Encrypted {
		t.Fatal("omitted encrypted field cleared committed value")
	}

	encrypted := false
	if err := s.UpdateBackupJobRuntimeState(BackupJobRuntimeStateUpdate{
		Version: BackupJobRuntimeStateVersion, JobID: 41,
		LastStatus: "success", Encrypted: &encrypted,
	}, true); err != nil {
		t.Fatalf("explicit unencrypted update: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload explicit update: %v", err)
	}
	if job.Encrypted {
		t.Fatal("explicit encrypted=false was not applied")
	}
}

func TestValidateBackupJobUpdateIdentity(t *testing.T) {
	dataset := clusterModels.BackupJob{
		TargetID: 1, RunnerNodeID: "node-a", Mode: clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/data", Recursive: false,
	}
	vm := clusterModels.BackupJob{
		TargetID: 1, RunnerNodeID: "node-a", Mode: clusterModels.BackupJobModeVM,
		SourceDataset: "fast/sylve/virtual-machines/71", Recursive: true,
	}
	jail := clusterModels.BackupJob{
		TargetID: 1, RunnerNodeID: "node-a", Mode: clusterModels.BackupJobModeJail,
		JailRootDataset: "fast/sylve/jails/81",
	}
	legacyVM := clusterModels.BackupJob{
		TargetID: 1, RunnerNodeID: "node-a", Mode: clusterModels.BackupJobModeVM,
		SourceDataset: "legacy/vm/root",
	}

	tests := []struct {
		name     string
		existing clusterModels.BackupJob
		mutate   func(*clusterModels.BackupJob)
		wantErr  string
	}{
		{
			name: "policy fields remain editable", existing: dataset,
			mutate: func(job *clusterModels.BackupJob) {
				job.Name = "renamed"
				job.Recursive = true
				job.PruneKeepLast = 12
			},
		},
		{
			name: "target is immutable", existing: dataset,
			mutate:  func(job *clusterModels.BackupJob) { job.TargetID = 2 },
			wantErr: "backup_job_target_immutable",
		},
		{
			name: "mode is immutable", existing: dataset,
			mutate:  func(job *clusterModels.BackupJob) { job.Mode = clusterModels.BackupJobModeJail },
			wantErr: "backup_job_mode_immutable",
		},
		{
			name: "dataset source is immutable", existing: dataset,
			mutate:  func(job *clusterModels.BackupJob) { job.SourceDataset = "tank/other" },
			wantErr: "backup_job_source_immutable",
		},
		{
			name: "dataset runner is immutable", existing: dataset,
			mutate:  func(job *clusterModels.BackupJob) { job.RunnerNodeID = "node-b" },
			wantErr: "backup_job_runner_immutable",
		},
		{
			name: "same VM may relocate", existing: vm,
			mutate: func(job *clusterModels.BackupJob) {
				job.SourceDataset = "slow/sylve/virtual-machines/71"
				job.RunnerNodeID = "node-b"
			},
		},
		{
			name: "different VM is immutable", existing: vm,
			mutate:  func(job *clusterModels.BackupJob) { job.SourceDataset = "fast/sylve/virtual-machines/72" },
			wantErr: "backup_job_source_immutable",
		},
		{
			name: "same jail may relocate", existing: jail,
			mutate: func(job *clusterModels.BackupJob) {
				job.JailRootDataset = "slow/sylve/jails/81"
				job.RunnerNodeID = "node-b"
			},
		},
		{
			name: "different jail is immutable", existing: jail,
			mutate:  func(job *clusterModels.BackupJob) { job.JailRootDataset = "fast/sylve/jails/82" },
			wantErr: "backup_job_source_immutable",
		},
		{
			name: "unresolved guest identity cannot relocate", existing: legacyVM,
			mutate:  func(job *clusterModels.BackupJob) { job.RunnerNodeID = "node-b" },
			wantErr: "backup_job_runner_immutable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proposed := test.existing
			test.mutate(&proposed)
			err := validateBackupJobUpdateIdentity(&test.existing, &proposed)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("identity update rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("identity update error = %v, want %s", err, test.wantErr)
			}
		})
	}
}

func TestProposeBackupJobCreateAndUpdatePersistsEncryption(t *testing.T) {
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
	s := &Service{DB: db, backupTargetValidator: func(context.Context, *clusterModels.BackupTarget) error { return nil }}

	target := clusterModels.BackupTarget{
		Name:       "recursive-job-target",
		SSHHost:    "user@backup-host",
		BackupRoot: "tank/backups",
		Enabled:    true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}

	enabled := true
	encrypted := true
	input := clusterServiceInterfaces.BackupJobReq{
		Name:          "recursive-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "zroot/data",
		CronExpr:      "0 0 * * *",
		Enabled:       &enabled,
		Encrypted:     &encrypted,
	}
	if err := s.ProposeBackupJobCreate(input, true); err != nil {
		t.Fatalf("create backup job: %v", err)
	}

	var job clusterModels.BackupJob
	if err := db.Where("name = ?", input.Name).First(&job).Error; err != nil {
		t.Fatalf("load backup job: %v", err)
	}
	if job.Recursive {
		t.Fatal("new job unexpectedly recursive")
	}
	if !job.Encrypted {
		t.Fatal("new job did not persist the verified encryption state")
	}

	input.Recursive = true
	input.Encrypted = nil
	if err := s.ProposeBackupJobUpdate(job.ID, input, true); err != nil {
		t.Fatalf("update backup job: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload backup job: %v", err)
	}
	if !job.Recursive {
		t.Fatal("recursive setting was not persisted by update")
	}
	if !job.Encrypted {
		t.Fatal("omitted encryption state cleared the persisted value")
	}

	encrypted = false
	input.Encrypted = &encrypted
	if err := s.ProposeBackupJobUpdate(job.ID, input, true); err != nil {
		t.Fatalf("update backup job encryption: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload backup job encryption: %v", err)
	}
	if job.Encrypted {
		t.Fatal("explicit unencrypted state was not persisted")
	}
}

func TestProposeBackupJobCreateRetriesOccupiedGeneratedID(t *testing.T) {
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
	target := clusterModels.BackupTarget{
		Name:       "collision-target",
		SSHHost:    "user@backup-host",
		BackupRoot: "tank/backups",
		Enabled:    true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	existing := clusterModels.BackupJob{
		ID: 7001, Name: "existing-job", TargetID: target.ID, RunnerNodeID: "node-create",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "zroot/existing",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing job: %v", err)
	}

	freeID := uint(maxSafeJSInt.Uint64() - 1)
	candidates := []uint{existing.ID, freeID}
	calls := 0
	service := &Service{
		DB: db, NodeID: "node-create",
		backupTargetValidator: func(context.Context, *clusterModels.BackupTarget) error {
			return nil
		},
		backupJobIDGenerator: func() (uint, error) {
			id := candidates[calls]
			calls++
			return id, nil
		},
	}
	enabled := true
	err := service.ProposeBackupJobCreate(clusterServiceInterfaces.BackupJobReq{
		Name: "new-job", TargetID: target.ID, RunnerNodeID: "node-create",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "zroot/new",
		CronExpr: "0 0 * * *", Enabled: &enabled,
	}, true)
	if err != nil {
		t.Fatalf("create after ID collision: %v", err)
	}
	if calls != 2 {
		t.Fatalf("ID generator calls = %d, want 2", calls)
	}

	var unchanged clusterModels.BackupJob
	if err := db.First(&unchanged, existing.ID).Error; err != nil {
		t.Fatalf("reload existing job: %v", err)
	}
	if unchanged.Name != existing.Name || unchanged.SourceDataset != existing.SourceDataset {
		t.Fatalf("collision retry changed existing job: %+v", unchanged)
	}
	var created clusterModels.BackupJob
	if err := db.First(&created, freeID).Error; err != nil {
		t.Fatalf("load retried job: %v", err)
	}
	if created.Name != "new-job" {
		t.Fatalf("retried job = %+v", created)
	}
}

func TestProposeBackupJobCreateReturnsConflictAfterOccupiedCandidates(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupJob{})
	existing := clusterModels.BackupJob{
		ID: 7002, Name: "existing-job", TargetID: 1, RunnerNodeID: "node-create",
		Mode: clusterModels.BackupJobModeDataset, SourceDataset: "zroot/existing",
		CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing job: %v", err)
	}

	calls := 0
	service := &Service{
		DB: db,
		backupJobIDGenerator: func() (uint, error) {
			calls++
			return existing.ID, nil
		},
	}
	err := service.ProposeBackupJobCreate(clusterServiceInterfaces.BackupJobReq{}, true)
	if err == nil || !strings.Contains(err.Error(), "backup_job_id_conflict") {
		t.Fatalf("exhausted collision error = %v", err)
	}
	if calls != backupJobIDGenerationAttempts {
		t.Fatalf("ID generator calls = %d, want %d", calls, backupJobIDGenerationAttempts)
	}

	var unchanged clusterModels.BackupJob
	if err := db.First(&unchanged, existing.ID).Error; err != nil {
		t.Fatalf("reload existing job: %v", err)
	}
	if unchanged.Name != existing.Name || unchanged.SourceDataset != existing.SourceDataset {
		t.Fatalf("exhausted collision changed existing job: %+v", unchanged)
	}
}

func TestProposeBackupJobUpdateDoesNotBypassPendingRunnerRebind(t *testing.T) {
	db := newClusterServiceTestDB(
		t,
		&clusterModels.BackupJob{},
		&clusterModels.BackupJobRunnerRebind{},
		&clusterModels.BackupJobRunnerRebindItem{},
	)
	service := &Service{DB: db}
	job := clusterModels.BackupJob{
		ID: 42, Name: "pending-rebind", TargetID: 1, RunnerNodeID: "node-old",
		Mode: clusterModels.BackupJobModeVM, SourceDataset: "fast/sylve/virtual-machines/812",
		Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := db.Create(&clusterModels.BackupJobRunnerRebind{
		Token: "failover-812", Kind: clusterModels.BackupJobRunnerRebindKindFailover,
		GuestType: clusterModels.BackupJobModeVM, GuestID: 812,
		OldRunnerNodeID: "node-old", NewRunnerNodeID: "node-new",
		State: clusterModels.BackupJobRunnerRebindStateReady, Revision: 1,
	}).Error; err != nil {
		t.Fatalf("seed rebind: %v", err)
	}
	if err := db.Create(&clusterModels.BackupJobRunnerRebindItem{
		OperationToken: "failover-812", JobID: job.ID,
		ExpectedRunnerID: "node-old", ExpectedFingerprint: "fingerprint",
		State: clusterModels.BackupJobRunnerRebindItemPending, Revision: 1,
	}).Error; err != nil {
		t.Fatalf("seed rebind item: %v", err)
	}

	enabled := true
	err := service.ProposeBackupJobUpdate(job.ID, clusterServiceInterfaces.BackupJobReq{
		Name: job.Name, TargetID: job.TargetID, RunnerNodeID: "node-new",
		Mode: job.Mode, SourceDataset: job.SourceDataset, Recursive: true,
		CronExpr: job.CronExpr, Enabled: &enabled,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "backup_job_runner_rebind_pending") {
		t.Fatalf("pending rebind update error = %v", err)
	}
	var unchanged clusterModels.BackupJob
	if err := db.First(&unchanged, job.ID).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if unchanged.RunnerNodeID != "node-old" {
		t.Fatalf("pending rebind update changed runner: %+v", unchanged)
	}
}
