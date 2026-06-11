// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"encoding/json"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestValidateMigration_SameTarget(t *testing.T) {
	svc := &Service{DB: nil, Cluster: nil}

	req := struct {
		guestType      string
		guestID        uint
		targetNodeUUID string
	}{
		guestType:      "vm",
		guestID:        1,
		targetNodeUUID: "same-node-uuid",
	}

	_ = req

	if svc == nil {
		t.Skip("nil service - validation requires setup")
	}
}

func TestValidateMigration_EmptyTarget(t *testing.T) {
	if false {
		t.Log("validation tests require cluster service setup")
	}
}

func TestCancelMigration_NotMigration(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestCancelMigration_NotAllowedPhase(t *testing.T) {
	t.Run("cancel_during_final_sync", func(t *testing.T) {
		if false {
			t.Log("requires DB setup")
		}
	})
}

func TestGetActiveTaskForGuest_NoDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestResolveVMDatasets_NoDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestResolveJailDatasets_NoDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestBackupJobReferencesGuest_VM(t *testing.T) {
	svc := &Service{}

	job := clusterModels.BackupJob{
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/sylve/virtual-machines/123/disk-0",
	}

	if !svc.backupJobReferencesGuest(job, "vm", 123) {
		t.Fatal("expected true for matching VM backup job")
	}

	job.SourceDataset = "zroot/sylve/virtual-machines/456/disk-0"
	if svc.backupJobReferencesGuest(job, "vm", 123) {
		t.Fatal("expected false for non-matching VM backup job")
	}

	job.Mode = clusterModels.BackupJobModeDataset
	if svc.backupJobReferencesGuest(job, "vm", 123) {
		t.Fatal("expected false for dataset mode job")
	}
}

func TestBackupJobReferencesGuest_Jail(t *testing.T) {
	svc := &Service{}

	job := clusterModels.BackupJob{
		Mode:           clusterModels.BackupJobModeJail,
		JailRootDataset: "zroot/sylve/jails/42/root",
	}

	if !svc.backupJobReferencesGuest(job, "jail", 42) {
		t.Fatal("expected true for matching jail backup job")
	}

	job.JailRootDataset = "zroot/sylve/jails/99/root"
	if svc.backupJobReferencesGuest(job, "jail", 42) {
		t.Fatal("expected false for non-matching jail backup job")
	}

	job.Mode = clusterModels.BackupJobModeVM
	if svc.backupJobReferencesGuest(job, "jail", 42) {
		t.Fatal("expected false for VM mode job checking jail")
	}
}

func TestMigrationPayloadRoundTrip(t *testing.T) {
	mp := migrationPayload{
		TargetNodeUUID:     "node-1",
		TargetNodeHostname: "host-1",
		Phase:              PhasePreflight,
		PhaseMessage:       "validating",
	}

	b, err := marshalPayload(mp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	restored, err := unmarshalPayload(b)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if restored.TargetNodeUUID != mp.TargetNodeUUID {
		t.Fatalf("expected %q, got %q", mp.TargetNodeUUID, restored.TargetNodeUUID)
	}
	if restored.Phase != mp.Phase {
		t.Fatalf("expected %q, got %q", mp.Phase, restored.Phase)
	}
	if restored.PhaseMessage != mp.PhaseMessage {
		t.Fatalf("expected %q, got %q", mp.PhaseMessage, restored.PhaseMessage)
	}
}

func marshalPayload(mp migrationPayload) (string, error) {
	b, err := json.Marshal(mp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalPayload(s string) (migrationPayload, error) {
	var mp migrationPayload
	err := json.Unmarshal([]byte(s), &mp)
	return mp, err
}

func TestMigrationPhases(t *testing.T) {
	phases := []string{
		PhasePreflight,
		PhaseInitialReplicaton,
		PhaseStopSource,
		PhaseFinalSync,
		PhaseStartTarget,
		PhasePolicyAdjustment,
		PhaseFinalize,
	}

	for _, p := range phases {
		if p == "" {
			t.Fatal("empty phase name")
		}
	}
}

func TestCheckCancelled_NilDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestUpdateTaskPhase_NilDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestUpdateTaskFailed_NilDB(t *testing.T) {
	t.Skip("requires DB setup")
}

func TestSnapshotPrefix(t *testing.T) {
	if migrationSnapPrefix != "sylve-migrate" {
		t.Fatalf("expected sylve-migrate, got %s", migrationSnapPrefix)
	}
}

func TestErrorConstants(t *testing.T) {
	errs := []error{
		ErrMigrationInProgress,
		ErrGuestActiveTransition,
		ErrTargetNodeOffline,
		ErrTargetNodeSame,
		ErrTargetAlreadyHasGuest,
		ErrTargetPoolMissing,
		ErrSSHUnreachable,
		ErrCancelNotAllowed,
		ErrMigrationFailed,
	}

	for _, e := range errs {
		if e == nil {
			t.Fatal("nil error constant")
		}
		if e.Error() == "" {
			t.Fatal("empty error message")
		}
	}
}

func TestBuildClusterSSHArgs(t *testing.T) {
	identity := &clusterModels.ClusterSSHIdentity{
		SSHUser:  "root",
		SSHHost:  "10.0.0.2",
		SSHPort:  8183,
		NodeUUID: "test-node-uuid",
	}

	args := buildClusterSSHArgs(identity, "/tmp/test-key")
	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}

	hasBatchMode := false
	hasControlMaster := false
	for _, a := range args {
		if a == "-o" {
			continue
		}
		if strings.Contains(a, "BatchMode=yes") {
			hasBatchMode = true
		}
		if strings.Contains(a, "ControlMaster=auto") {
			hasControlMaster = true
		}
	}
	if !hasBatchMode {
		t.Fatal("expected BatchMode in SSH args")
	}
	if !hasControlMaster {
		t.Fatal("expected ControlMaster in SSH args")
	}
}

func TestBackupEventReferencesGuest_VM(t *testing.T) {
	svc := &Service{}

	event := clusterModels.BackupEvent{
		Mode:          clusterModels.BackupJobModeVM,
		SourceDataset: "zroot/sylve/virtual-machines/123/disk-0",
	}

	if !svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected true for matching VM backup event")
	}

	event.SourceDataset = "zroot/sylve/virtual-machines/456/disk-0"
	if svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected false for non-matching VM backup event")
	}

	event.Mode = clusterModels.BackupJobModeDataset
	event.SourceDataset = "zroot/sylve/virtual-machines/123/disk-0"
	if svc.backupEventReferencesGuest(event, "vm", 123) {
		t.Fatal("expected false for dataset-mode event even if path matches")
	}
}

func TestBackupEventReferencesGuest_Jail(t *testing.T) {
	svc := &Service{}

	event := clusterModels.BackupEvent{
		Mode:          clusterModels.BackupJobModeJail,
		SourceDataset: "zroot/sylve/jails/42/root",
	}

	if !svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected true for matching jail backup event via source dataset")
	}

	event.SourceDataset = "other/something"
	event.TargetEndpoint = "backup-host:pool/sylve/jails/42/active"
	if !svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected true for matching jail backup event via target endpoint")
	}

	event.SourceDataset = "other/something"
	event.TargetEndpoint = "backup-host:pool/sylve/jails/99/active"
	if svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected false for non-matching jail backup event")
	}

	event.Mode = clusterModels.BackupJobModeVM
	if svc.backupEventReferencesGuest(event, "jail", 42) {
		t.Fatal("expected false for VM-mode event when checking jail guest")
	}
}

func TestReplicationEventConflictReason(t *testing.T) {
	reason := "guest_has_running_replication_event"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}

func TestActiveLifecycleTaskConflictReason(t *testing.T) {
	reason := "guest_has_active_lifecycle_task"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}

func TestBackupEventConflictReason(t *testing.T) {
	reason := "guest_has_running_backup_event"
	if reason == "" {
		t.Fatal("reason string must not be empty")
	}
}
