// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func TestProposeBackupJobUpdatePersistsRecursive(t *testing.T) {
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
	s := &Service{DB: db}

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
	input := clusterServiceInterfaces.BackupJobReq{
		Name:          "recursive-job",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "zroot/data",
		CronExpr:      "0 0 * * *",
		Enabled:       &enabled,
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

	input.Recursive = true
	if err := s.ProposeBackupJobUpdate(job.ID, input, true); err != nil {
		t.Fatalf("update backup job: %v", err)
	}
	if err := db.First(&job, job.ID).Error; err != nil {
		t.Fatalf("reload backup job: %v", err)
	}
	if !job.Recursive {
		t.Fatal("recursive setting was not persisted by update")
	}
}
