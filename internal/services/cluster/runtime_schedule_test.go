// SPDX-License-Identifier: BSD-2-Clause

package cluster

import (
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestRuntimeStateAuthorityUsesPersistedClusterMode(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t,
		&clusterModels.Cluster{},
		&clusterModels.BackupJob{},
	)
	service := &Service{DB: database}

	if err := database.Create(&clusterModels.Cluster{ID: 1, Enabled: false}).Error; err != nil {
		t.Fatalf("seed standalone state: %v", err)
	}
	bypass, err := service.RuntimeStateBypassRaft()
	if err != nil || !bypass {
		t.Fatalf("standalone authority bypass=%v err=%v", bypass, err)
	}

	if err := database.Model(&clusterModels.Cluster{}).Where("id = ?", 1).
		Update("enabled", true).Error; err != nil {
		t.Fatalf("enable cluster: %v", err)
	}
	bypass, err = service.RuntimeStateBypassRaft()
	if err == nil || bypass || !strings.Contains(err.Error(), "cluster_enabled_raft_unavailable") {
		t.Fatalf("enabled cluster without raft bypass=%v err=%v", bypass, err)
	}

	job := clusterModels.BackupJob{
		ID: 1, Name: "authority", TargetID: 1,
		Mode: clusterModels.BackupJobModeDataset, CronExpr: "0 * * * *",
	}
	if err := database.Create(&job).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	err = service.UpdateBackupJobRuntimeState(BackupJobRuntimeStateUpdate{
		JobID: 1, LastStatus: "failed", LastError: "must not apply",
	}, true)
	if err == nil {
		t.Fatal("cluster-enabled local runtime write unexpectedly succeeded")
	}
	if err := database.First(&job, 1).Error; err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if job.LastStatus != "" || job.LastError != "" {
		t.Fatalf("forbidden local write mutated job: %+v", job)
	}
}
