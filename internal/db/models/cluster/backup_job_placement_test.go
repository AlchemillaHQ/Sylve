// SPDX-License-Identifier: BSD-2-Clause

package clusterModels

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBackupJobPlacementFenceRejectsReplicatedStateChanges(t *testing.T) {
	for _, scenario := range []string{"policy", "operation"} {
		t.Run(scenario, func(t *testing.T) {
			db := newClusterModelTestDB(
				t,
				&BackupJob{},
				&ReplicationPolicy{},
				&ReplicationGuestOperation{},
			)
			fsm := NewFSMDispatcher(db)
			RegisterDefaultHandlers(fsm)

			expected, err := LoadBackupJobPlacementFence(db, BackupJobModeVM, 101, "node-b")
			if err != nil {
				t.Fatalf("load initial fence: %v", err)
			}

			switch scenario {
			case "policy":
				if err := db.Create(&ReplicationPolicy{
					ID: 3, Name: "new-policy", GuestType: BackupJobModeVM, GuestID: 101,
					SourceNodeID: "node-b", ActiveNodeID: "node-b", OwnerEpoch: 1,
					CronExpr: "0 * * * *", Enabled: true,
				}).Error; err != nil {
					t.Fatalf("create policy: %v", err)
				}
			case "operation":
				now := time.Now().UTC()
				if err := db.Create(&ReplicationGuestOperation{
					GuestType: BackupJobModeVM, GuestID: 101,
					Operation: ReplicationGuestOperationMigration,
					State:     ReplicationGuestOperationPreCutover,
					Token:     "migration:101", OwnerNodeID: "node-a", TargetNodeID: "node-b",
					TaskID: 9, AcquiredAt: now, UpdatedAt: now,
				}).Error; err != nil {
					t.Fatalf("create operation: %v", err)
				}
			}

			payload, err := json.Marshal(BackupJobCommandPayload{
				Job: BackupJob{
					ID: 11, Name: "vm-101", TargetID: 1, RunnerNodeID: "node-b",
					Mode: BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/101",
					Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
				},
				PlacementFence: &expected,
			})
			if err != nil {
				t.Fatalf("marshal command: %v", err)
			}
			err = applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: payload})
			if err == nil || !strings.Contains(err.Error(), "backup_job_placement_changed") {
				t.Fatalf("apply error = %v, want placement change", err)
			}

			var count int64
			if err := db.Model(&BackupJob{}).Where("id = ?", 11).Count(&count).Error; err != nil {
				t.Fatalf("count jobs: %v", err)
			}
			if count != 0 {
				t.Fatalf("job count = %d, want 0", count)
			}
		})
	}
}

func TestBackupJobPreviousPlacementFenceBlocksIdentityEscape(t *testing.T) {
	db := newClusterModelTestDB(
		t,
		&BackupJob{},
		&ReplicationPolicy{},
		&ReplicationGuestOperation{},
	)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	if err := db.Create(&BackupJob{
		ID: 31, Name: "vm-before", TargetID: 1, RunnerNodeID: "node-a",
		Mode: BackupJobModeVM, SourceDataset: "tank/sylve/virtual-machines/303",
		Recursive: true, CronExpr: "0 0 * * *", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed job: %v", err)
	}
	previous, err := LoadBackupJobPlacementFence(db, BackupJobModeVM, 303, "node-a")
	if err != nil {
		t.Fatalf("load previous fence: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&ReplicationGuestOperation{
		GuestType: BackupJobModeVM, GuestID: 303,
		Operation: ReplicationGuestOperationMigration, State: ReplicationGuestOperationPreCutover,
		Token: "migration:303", OwnerNodeID: "node-a", TargetNodeID: "node-b",
		TaskID: 30, AcquiredAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed operation: %v", err)
	}

	payload, _ := json.Marshal(BackupJobCommandPayload{
		Job: BackupJob{
			ID: 31, Name: "dataset-after", TargetID: 1, RunnerNodeID: "node-a",
			Mode: BackupJobModeDataset, SourceDataset: "tank/data",
			CronExpr: "0 0 * * *", Enabled: true,
		},
		PreviousPlacementFence: &previous,
	})
	err = applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "update", Data: payload})
	if err == nil || !strings.Contains(err.Error(), "backup_job_placement_changed") {
		t.Fatalf("identity-changing update error = %v", err)
	}
	var job BackupJob
	if err := db.First(&job, 31).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}
	if job.Mode != BackupJobModeVM || job.Name != "vm-before" {
		t.Fatalf("job changed despite stale previous fence: %+v", job)
	}
}

func TestBackupJobPlacementFenceEnvelopeAndLegacyReplay(t *testing.T) {
	db := newClusterModelTestDB(
		t,
		&BackupJob{},
		&ReplicationPolicy{},
		&ReplicationGuestOperation{},
	)
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	fence, err := LoadBackupJobPlacementFence(db, BackupJobModeJail, 202, "node-b")
	if err != nil {
		t.Fatalf("load fence: %v", err)
	}
	enveloped, _ := json.Marshal(BackupJobCommandPayload{
		Job: BackupJob{
			ID: 21, Name: "jail-202", TargetID: 1, RunnerNodeID: "node-b",
			Mode: BackupJobModeJail, JailRootDataset: "tank/sylve/jails/202",
			CronExpr: "0 0 * * *", Enabled: true,
		},
		PlacementFence: &fence,
	})
	if err := applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: enveloped}); err != nil {
		t.Fatalf("enveloped create: %v", err)
	}

	legacy, _ := json.Marshal(BackupJob{
		ID: 22, Name: "legacy", TargetID: 1, Mode: BackupJobModeDataset,
		SourceDataset: "tank/data", CronExpr: "0 0 * * *", Enabled: true,
	})
	if err := applyFSMCommand(t, fsm, Command{Type: "backup_job", Action: "create", Data: legacy}); err != nil {
		t.Fatalf("legacy replay: %v", err)
	}

	var count int64
	if err := db.Model(&BackupJob{}).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 2 {
		t.Fatalf("job count = %d, want 2", count)
	}
}
