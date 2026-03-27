// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestListBackupTargetsOrdersByName(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{})
	s := &Service{DB: db}

	targets := []clusterModels.BackupTarget{
		{Name: "Zulu", SSHHost: "user@z", SSHPort: 22, BackupRoot: "tank/z", Enabled: true},
		{Name: "Alpha", SSHHost: "user@a", SSHPort: 22, BackupRoot: "tank/a", Enabled: true},
		{Name: "Echo", SSHHost: "user@e", SSHPort: 22, BackupRoot: "tank/e", Enabled: true},
	}
	if err := db.Create(&targets).Error; err != nil {
		t.Fatalf("failed to seed backup targets: %v", err)
	}

	got, err := s.ListBackupTargets()
	if err != nil {
		t.Fatalf("ListBackupTargets failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(got))
	}
	if got[0].Name != "Alpha" || got[1].Name != "Echo" || got[2].Name != "Zulu" {
		t.Fatalf("expected name ordering Alpha, Echo, Zulu; got %q, %q, %q", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestProposeBackupTargetCRUDBypassRaft(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	s := &Service{DB: db}

	createInput := clusterServiceInterfaces.BackupTargetReq{
		Name:       "  target-one  ",
		SSHHost:    "  user@host-one  ",
		SSHPort:    0,
		SSHKey:     "  key-one  ",
		BackupRoot: "  tank/backups-one  ",
		// nil CreateBackupRoot and Enabled intentionally test PtrToBool defaults.
		Description: "  first target  ",
	}

	if err := s.ProposeBackupTargetCreate(createInput, true); err != nil {
		t.Fatalf("ProposeBackupTargetCreate bypass failed: %v", err)
	}

	var created clusterModels.BackupTarget
	if err := db.Where("name = ?", "target-one").First(&created).Error; err != nil {
		t.Fatalf("failed to fetch created backup target: %v", err)
	}

	if created.SSHHost != "user@host-one" {
		t.Fatalf("expected SSH host to be trimmed, got %q", created.SSHHost)
	}
	if created.SSHPort != 22 {
		t.Fatalf("expected default SSH port 22, got %d", created.SSHPort)
	}
	if created.BackupRoot != "tank/backups-one" {
		t.Fatalf("expected backup root to be trimmed, got %q", created.BackupRoot)
	}
	if created.Description != "first target" {
		t.Fatalf("expected description to be trimmed, got %q", created.Description)
	}
	if created.CreateBackupRoot {
		t.Fatal("expected createBackupRoot default false when pointer is nil")
	}
	if !created.Enabled {
		t.Fatal("expected enabled default true when pointer is nil")
	}
	if created.SSHKey != "key-one" {
		t.Fatalf("expected SSH key to be trimmed and persisted, got %q", created.SSHKey)
	}

	updateInput := clusterServiceInterfaces.BackupTargetReq{
		ID:               created.ID,
		Name:             "  target-updated  ",
		SSHHost:          "  user@host-two  ",
		SSHPort:          0,
		SSHKey:           " updated-key ",
		BackupRoot:       "  tank/backups-two ",
		CreateBackupRoot: boolPtr(true),
		Description:      " updated description ",
		Enabled:          boolPtr(true),
	}

	if err := s.ProposeBackupTargetUpdate(updateInput, true); err != nil {
		t.Fatalf("ProposeBackupTargetUpdate bypass failed: %v", err)
	}

	var updated clusterModels.BackupTarget
	if err := db.First(&updated, created.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated backup target: %v", err)
	}
	if updated.Name != "target-updated" ||
		updated.SSHHost != "user@host-two" ||
		updated.SSHPort != 22 ||
		updated.BackupRoot != "tank/backups-two" ||
		updated.Description != "updated description" ||
		updated.SSHKey != "updated-key" ||
		!updated.CreateBackupRoot ||
		!updated.Enabled {
		t.Fatalf("unexpected updated backup target: %+v", updated)
	}

	if err := s.ProposeBackupTargetDelete(created.ID, true); err != nil {
		t.Fatalf("ProposeBackupTargetDelete bypass failed: %v", err)
	}

	var count int64
	if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", created.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count backup target after delete: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected backup target to be deleted, found %d row(s)", count)
	}
}

func TestProposeBackupTargetCreateResolvesSSHKeyFromPath(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{})
	s := &Service{DB: db}

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "target-key")
	if err := os.WriteFile(keyPath, []byte("path-key\n"), 0600); err != nil {
		t.Fatalf("failed to write temp ssh key file: %v", err)
	}

	input := clusterServiceInterfaces.BackupTargetReq{
		Name:       "target-path",
		SSHHost:    "user@host",
		BackupRoot: "tank/path",
		SSHKeyPath: keyPath,
	}

	if err := s.ProposeBackupTargetCreate(input, true); err != nil {
		t.Fatalf("ProposeBackupTargetCreate bypass failed: %v", err)
	}

	var created clusterModels.BackupTarget
	if err := db.Where("name = ?", "target-path").First(&created).Error; err != nil {
		t.Fatalf("failed to fetch created target: %v", err)
	}
	if created.SSHKey != "path-key" {
		t.Fatalf("expected ssh key to be loaded from path, got %q", created.SSHKey)
	}
}

func TestProposeBackupTargetDeleteBlockedWhenJobUsesTarget(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	s := &Service{DB: db}

	target := clusterModels.BackupTarget{
		Name:       "in-use-target",
		SSHHost:    "user@host",
		SSHPort:    22,
		BackupRoot: "tank/in-use",
		Enabled:    true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to create target: %v", err)
	}

	job := clusterModels.BackupJob{
		Name:          "job-1",
		TargetID:      target.ID,
		Mode:          clusterModels.BackupJobModeDataset,
		SourceDataset: "tank/source",
		CronExpr:      "* * * * *",
		Enabled:       true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to create job using target: %v", err)
	}

	err := s.ProposeBackupTargetDelete(target.ID, true)
	if err == nil {
		t.Fatal("expected delete-in-use error, got nil")
	}
	if !strings.Contains(err.Error(), "target_in_use_by_backup_jobs") {
		t.Fatalf("expected target_in_use_by_backup_jobs error, got: %v", err)
	}

	var count int64
	if err := db.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count target after blocked delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected target to remain after blocked delete, found %d row(s)", count)
	}
}

func TestProposeBackupTargetRequiresRaftWhenBypassDisabled(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupTarget{}, &clusterModels.BackupJob{})
	s := &Service{DB: db, Raft: nil}

	createInput := clusterServiceInterfaces.BackupTargetReq{
		Name:       "target-one",
		SSHHost:    "user@host",
		BackupRoot: "tank/backups",
	}

	err := s.ProposeBackupTargetCreate(createInput, false)
	if err == nil {
		t.Fatal("expected raft_not_initialized error for create, got nil")
	}
	if !strings.Contains(err.Error(), "raft_not_initialized") {
		t.Fatalf("unexpected create error: %v", err)
	}

	updateInput := clusterServiceInterfaces.BackupTargetReq{
		ID:         1,
		Name:       "target-one",
		SSHHost:    "user@host",
		BackupRoot: "tank/backups",
	}

	err = s.ProposeBackupTargetUpdate(updateInput, false)
	if err == nil {
		t.Fatal("expected raft_not_initialized error for update, got nil")
	}
	if !strings.Contains(err.Error(), "raft_not_initialized") {
		t.Fatalf("unexpected update error: %v", err)
	}

	err = s.ProposeBackupTargetDelete(1, false)
	if err == nil {
		t.Fatal("expected raft_not_initialized error for delete, got nil")
	}
	if !strings.Contains(err.Error(), "raft_not_initialized") {
		t.Fatalf("unexpected delete error: %v", err)
	}
}

func TestSyncBackupJobFriendlySourceByGuestBypassRaftUpdatesMatchingJobs(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.BackupJob{})
	s := &Service{DB: db}

	jobs := []clusterModels.BackupJob{
		{
			ID:            11,
			Name:          "vm-job-match",
			TargetID:      1,
			Mode:          clusterModels.BackupJobModeVM,
			SourceDataset: "zroot/sylve/virtual-machines/901",
			FriendlySrc:   "vm-old",
			CronExpr:      "* * * * *",
			Enabled:       true,
		},
		{
			ID:            12,
			Name:          "vm-job-other",
			TargetID:      1,
			Mode:          clusterModels.BackupJobModeVM,
			SourceDataset: "zroot/sylve/virtual-machines/902",
			FriendlySrc:   "vm-other",
			CronExpr:      "* * * * *",
			Enabled:       true,
		},
		{
			ID:              13,
			Name:            "jail-job-match",
			TargetID:        1,
			Mode:            clusterModels.BackupJobModeJail,
			JailRootDataset: "zroot/sylve/jails/903",
			FriendlySrc:     "jail-old",
			CronExpr:        "* * * * *",
			Enabled:         true,
		},
	}
	if err := db.Create(&jobs).Error; err != nil {
		t.Fatalf("failed to seed backup jobs: %v", err)
	}

	if err := s.SyncBackupJobFriendlySourceByGuest(BackupJobFriendlySourceUpdate{
		GuestType:   clusterModels.ReplicationGuestTypeVM,
		GuestID:     901,
		FriendlySrc: "vm-new",
	}, true); err != nil {
		t.Fatalf("vm friendly source sync failed: %v", err)
	}

	if err := s.SyncBackupJobFriendlySourceByGuest(BackupJobFriendlySourceUpdate{
		GuestType:   clusterModels.ReplicationGuestTypeJail,
		GuestID:     903,
		FriendlySrc: "jail-new",
	}, true); err != nil {
		t.Fatalf("jail friendly source sync failed: %v", err)
	}

	var refreshed []clusterModels.BackupJob
	if err := db.Order("id asc").Find(&refreshed).Error; err != nil {
		t.Fatalf("failed to read refreshed jobs: %v", err)
	}
	if len(refreshed) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(refreshed))
	}

	if refreshed[0].FriendlySrc != "vm-new" {
		t.Fatalf("expected vm matching job friendly source to be updated, got %q", refreshed[0].FriendlySrc)
	}
	if refreshed[1].FriendlySrc != "vm-other" {
		t.Fatalf("expected non-matching vm job friendly source unchanged, got %q", refreshed[1].FriendlySrc)
	}
	if refreshed[2].FriendlySrc != "jail-new" {
		t.Fatalf("expected jail matching job friendly source to be updated, got %q", refreshed[2].FriendlySrc)
	}
}
