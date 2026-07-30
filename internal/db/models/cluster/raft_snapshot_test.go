// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/hashicorp/raft"
)

func allSnapshotModels() []any {
	return []any{
		&ClusterNote{},
		&ClusterOption{},
		&BackupTarget{},
		&BackupTargetNodeReadiness{},
		&BackupJob{},
		&BackupJobOperation{},
		&BackupTargetRestoreOperation{},
		&BackupJobRunnerRebind{},
		&BackupJobRunnerRebindItem{},
		&ReplicationPolicy{},
		&ReplicationPolicyTarget{},
		&ReplicationLease{},
		&ReplicationGuestOperation{},
		&ReplicationGuestOperationReceipt{},
		&ReplicationEvent{},
		&ClusterSSHIdentity{},
		&EncryptionKey{},
	}
}

func TestClusterSnapshotRoundTrip(t *testing.T) {
	sourceDB := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	fsmSrc := NewFSMDispatcher(sourceDB)
	RegisterDefaultHandlers(fsmSrc)
	if err := sourceDB.Create(&ClusterNote{ID: 1, Title: "note1", Content: "c1"}).Error; err != nil {
		t.Fatalf("failed to seed cluster note: %v", err)
	}
	if err := sourceDB.Create(&ClusterNote{ID: 2, Title: "note2", Content: "c2"}).Error; err != nil {
		t.Fatalf("failed to seed second note: %v", err)
	}

	if err := sourceDB.Create(&ClusterOption{ID: 1, KeyboardLayout: "us"}).Error; err != nil {
		t.Fatalf("failed to seed option: %v", err)
	}

	target := BackupTarget{
		ID: 100, Name: "t1", SSHHost: "localhost", SSHPort: 22,
		SSHKeyPath: "/leader/local/target-100_id", SSHKey: "snapshot-key", BackupRoot: "/backup",
	}
	if err := sourceDB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed backup target: %v", err)
	}
	readinessVerifiedAt := time.Date(2026, time.January, 1, 1, 2, 3, 0, time.UTC)
	readinessUntil := readinessVerifiedAt.Add(10 * time.Minute)
	if err := sourceDB.Create(&BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "node-1",
		TargetFingerprint:   BackupTargetConnectivityFingerprint(&target),
		ValidationSucceeded: true, LastVerifiedAt: readinessVerifiedAt,
		ReadyUntil: &readinessUntil, Revision: 2, RaftAppliedIndex: 42, UpdatedAt: readinessVerifiedAt,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup target readiness: %v", err)
	}

	if err := sourceDB.Create(&BackupJob{
		ID: 200, Name: "job1", TargetID: target.ID, Mode: BackupJobModeDataset,
		CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("failed to seed backup job: %v", err)
	}
	operationTime := time.Date(2026, time.January, 1, 2, 3, 4, 0, time.UTC)
	if err := sourceDB.Create(&BackupJobOperation{
		JobID: 200, Token: "backup:node-1:snapshot", Operation: BackupJobOperationBackup,
		State: BackupJobOperationRunning, HolderNodeID: "node-1", Revision: 2,
		AcquiredAt: operationTime, UpdatedAt: operationTime,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup job operation: %v", err)
	}
	if err := sourceDB.Create(&BackupTargetRestoreOperation{
		Token: "target-restore:node-1:snapshot", TargetID: target.ID, HolderNodeID: "node-1",
		DestinationDataset: "zroot/restored", RequestPayload: `{"snapshot":"@snapshot"}`,
		State: BackupTargetRestoreOperationCompleted, Revision: 4,
		AcquiredAt: operationTime, UpdatedAt: operationTime,
	}).Error; err != nil {
		t.Fatalf("failed to seed target restore operation: %v", err)
	}
	if err := sourceDB.Create(&BackupJobRunnerRebind{
		Token: "failover-200-snapshot", Kind: BackupJobRunnerRebindKindFailover,
		GuestType: BackupJobModeVM, GuestID: 200, OldRunnerNodeID: "node-1", NewRunnerNodeID: "node-2",
		State: BackupJobRunnerRebindStateReady, Revision: 3,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup-job rebind: %v", err)
	}
	if err := sourceDB.Create(&BackupJobRunnerRebindItem{
		OperationToken: "failover-200-snapshot", JobID: 200,
		ExpectedRunnerID: "node-1", ExpectedFingerprint: "snapshot-fingerprint",
		State: BackupJobRunnerRebindItemPending, Error: "retry", Revision: 2,
	}).Error; err != nil {
		t.Fatalf("failed to seed backup-job rebind item: %v", err)
	}

	policy := ReplicationPolicy{
		ID: 300, Name: "r1", GuestType: "vm", GuestID: 1,
		SourceNodeID: "node-1", CronExpr: "*/5 * * * *",
	}
	if err := sourceDB.Create(&policy).Error; err != nil {
		t.Fatalf("failed to seed policy: %v", err)
	}
	if err := sourceDB.Create(&ReplicationPolicyTarget{
		ID: 400, PolicyID: policy.ID, NodeID: "node-2",
	}).Error; err != nil {
		t.Fatalf("failed to seed policy target: %v", err)
	}

	if err := sourceDB.Create(&ReplicationLease{
		ID: 500, PolicyID: policy.ID, GuestType: "vm", GuestID: 1,
		OwnerNodeID: "node-1", OwnerEpoch: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed lease: %v", err)
	}
	if err := sourceDB.Create(&ReplicationGuestOperation{
		GuestType: "jail", GuestID: 42, Operation: ReplicationGuestOperationMigration,
		State: ReplicationGuestOperationPreCutover, Token: "migration:node-1:42",
		OwnerNodeID: "node-1", TargetNodeID: "node-2", TaskID: 42, AcquiredAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("failed to seed guest operation: %v", err)
	}
	completedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := sourceDB.Create(&ReplicationGuestOperationReceipt{
		Token: "migration:node-1:completed-41", GuestType: ReplicationGuestTypeVM,
		GuestID: 41, Operation: ReplicationGuestOperationMigration,
		OwnerNodeID: "node-1", TargetNodeID: "node-2", TaskID: 41,
		AcquiredAt: completedAt.Add(-time.Minute), CompletedAt: completedAt,
	}).Error; err != nil {
		t.Fatalf("failed to seed guest operation receipt: %v", err)
	}

	if err := sourceDB.Create(&ReplicationEvent{
		ID: 600, EventType: "incremental", Status: "success",
		TransitionRunID: "transition-snapshot",
		SourceNodeID:    "node-1", TargetNodeID: "node-2",
	}).Error; err != nil {
		t.Fatalf("failed to seed replication event: %v", err)
	}

	if err := sourceDB.Create(&ClusterSSHIdentity{
		ID: 700, NodeUUID: "node-1", SSHUser: "root",
		SSHHost: "10.0.0.1", SSHPort: 8183, PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKTEST",
	}).Error; err != nil {
		t.Fatalf("failed to seed ssh identity: %v", err)
	}

	if err := sourceDB.Create(&EncryptionKey{
		ID: 800, UUID: "key-1", KeyData: "super-secret-data", KeyFormat: "passphrase",
	}).Error; err != nil {
		t.Fatalf("failed to seed encryption key: %v", err)
	}

	snap, err := fsmSrc.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() failed: %v", err)
	}

	var buf bytes.Buffer
	sink := &raft.DiscardSnapshotSink{}
	_ = sink

	type pipeSnapSink struct {
		buf *bytes.Buffer
	}

	writeCloser := &writerSnapSink{buf: &buf}
	if err := snap.Persist(writeCloser); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}
	destDB := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	fsmDest := NewFSMDispatcher(destDB)
	RegisterDefaultHandlers(fsmDest)
	if err := fsmDest.Restore(io.NopCloser(bytes.NewReader(buf.Bytes()))); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	var notes []ClusterNote
	destDB.Find(&notes)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes after restore, got %d", len(notes))
	}

	var opts []ClusterOption
	destDB.Find(&opts)
	if len(opts) != 1 || opts[0].KeyboardLayout != "us" {
		t.Fatalf("options mismatch: %+v", opts)
	}

	var targets []BackupTarget
	destDB.Find(&targets)
	if len(targets) != 1 || targets[0].Name != "t1" ||
		targets[0].SSHKeyPath != "" || targets[0].SSHKey != "snapshot-key" {
		t.Fatalf("targets mismatch: %+v", targets)
	}
	var targetReadiness []BackupTargetNodeReadiness
	destDB.Find(&targetReadiness)
	if len(targetReadiness) != 1 || targetReadiness[0].NodeID != "node-1" ||
		targetReadiness[0].TargetFingerprint != BackupTargetConnectivityFingerprint(&target) ||
		!targetReadiness[0].ValidationSucceeded || targetReadiness[0].Revision != 2 ||
		targetReadiness[0].RaftAppliedIndex != 42 ||
		!targetReadiness[0].LastVerifiedAt.Equal(readinessVerifiedAt) {
		t.Fatalf("target readiness mismatch: %+v", targetReadiness)
	}

	var jobs []BackupJob
	destDB.Find(&jobs)
	if len(jobs) != 1 || jobs[0].Name != "job1" {
		t.Fatalf("jobs mismatch: %+v", jobs)
	}

	var backupOperations []BackupJobOperation
	destDB.Find(&backupOperations)
	if len(backupOperations) != 1 || backupOperations[0].Token != "backup:node-1:snapshot" ||
		backupOperations[0].State != BackupJobOperationRunning || backupOperations[0].Revision != 2 {
		t.Fatalf("backup job operations mismatch: %+v", backupOperations)
	}
	var targetRestoreOperations []BackupTargetRestoreOperation
	destDB.Find(&targetRestoreOperations)
	if len(targetRestoreOperations) != 1 ||
		targetRestoreOperations[0].Token != "target-restore:node-1:snapshot" ||
		targetRestoreOperations[0].DestinationDataset != "zroot/restored" ||
		targetRestoreOperations[0].State != BackupTargetRestoreOperationCompleted ||
		targetRestoreOperations[0].Revision != 4 {
		t.Fatalf("target restore operations mismatch: %+v", targetRestoreOperations)
	}
	var rebinds []BackupJobRunnerRebind
	destDB.Find(&rebinds)
	if len(rebinds) != 1 || rebinds[0].Token != "failover-200-snapshot" ||
		rebinds[0].Kind != BackupJobRunnerRebindKindFailover ||
		rebinds[0].State != BackupJobRunnerRebindStateReady || rebinds[0].Revision != 3 {
		t.Fatalf("backup job rebinds mismatch: %+v", rebinds)
	}
	var rebindItems []BackupJobRunnerRebindItem
	destDB.Find(&rebindItems)
	if len(rebindItems) != 1 || rebindItems[0].OperationToken != "failover-200-snapshot" ||
		rebindItems[0].State != BackupJobRunnerRebindItemPending || rebindItems[0].Error != "retry" ||
		rebindItems[0].Revision != 2 {
		t.Fatalf("backup job rebind items mismatch: %+v", rebindItems)
	}

	var pols []ReplicationPolicy
	destDB.Find(&pols)
	if len(pols) != 1 || pols[0].Name != "r1" {
		t.Fatalf("policies mismatch: %+v", pols)
	}

	var ptargets []ReplicationPolicyTarget
	destDB.Find(&ptargets)
	if len(ptargets) != 1 || ptargets[0].NodeID != "node-2" {
		t.Fatalf("policy targets mismatch: %+v", ptargets)
	}

	var leases []ReplicationLease
	destDB.Find(&leases)
	if len(leases) != 1 || leases[0].OwnerNodeID != "node-1" {
		t.Fatalf("leases mismatch: %+v", leases)
	}

	var events []ReplicationEvent
	destDB.Find(&events)
	if len(events) != 1 || events[0].Status != "success" || events[0].TransitionRunID != "transition-snapshot" {
		t.Fatalf("events mismatch: %+v", events)
	}

	var operations []ReplicationGuestOperation
	destDB.Find(&operations)
	if len(operations) != 1 || operations[0].Token != "migration:node-1:42" ||
		operations[0].State != ReplicationGuestOperationPreCutover {
		t.Fatalf("guest operations mismatch: %+v", operations)
	}

	var operationReceipts []ReplicationGuestOperationReceipt
	destDB.Order("token ASC").Find(&operationReceipts)
	if len(operationReceipts) != 1 || operationReceipts[0].Token != "migration:node-1:completed-41" ||
		operationReceipts[0].Operation != ReplicationGuestOperationMigration ||
		operationReceipts[0].GuestType != ReplicationGuestTypeVM || operationReceipts[0].GuestID != 41 ||
		operationReceipts[0].OwnerNodeID != "node-1" || operationReceipts[0].TargetNodeID != "node-2" ||
		operationReceipts[0].TaskID != 41 || !operationReceipts[0].CompletedAt.Equal(completedAt) {
		t.Fatalf("guest operation receipts mismatch: %+v", operationReceipts)
	}

	var sshIds []ClusterSSHIdentity
	destDB.Find(&sshIds)
	if len(sshIds) != 1 || sshIds[0].NodeUUID != "node-1" {
		t.Fatalf("ssh identities mismatch: %+v", sshIds)
	}

	var keys []EncryptionKey
	destDB.Find(&keys)
	if len(keys) != 1 || keys[0].UUID != "key-1" {
		t.Fatalf("encryption keys mismatch: %+v", keys)
	}
}

func TestClusterLegacySnapshotWithoutTargetRestoreOperationsClearsReservations(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, allSnapshotModels()...)
	target := BackupTarget{
		ID: 901, Name: "legacy-target", SSHHost: "root@backup", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}
	if err := database.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	now := time.Date(2026, time.May, 6, 7, 8, 9, 0, time.UTC)
	readyUntil := now.Add(time.Hour)
	if err := database.Create(&BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "local", TargetFingerprint: BackupTargetConnectivityFingerprint(&target),
		ValidationSucceeded: true, LastVerifiedAt: now, ReadyUntil: &readyUntil, Revision: 1, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed stale readiness: %v", err)
	}
	if err := database.Create(&BackupTargetRestoreOperation{
		Token: "target-restore:local:stale", TargetID: target.ID, HolderNodeID: "local",
		DestinationDataset: "zroot/stale", RequestPayload: `{"snapshot":"@stale"}`,
		State: BackupTargetRestoreOperationQueued, Revision: 1, AcquiredAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed stale operation: %v", err)
	}

	legacySnapshot := ClusterSnapshot{
		BackupTargets: []BackupTargetReplicationPayload{BackupTargetToReplicationPayload(target)},
	}
	encoded, err := json.Marshal(legacySnapshot)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	if bytes.Contains(encoded, []byte("backupTargetRestoreOperations")) {
		t.Fatalf("legacy snapshot unexpectedly contains target restore operations: %s", encoded)
	}
	fsm := NewFSMDispatcher(database)
	RegisterDefaultHandlers(fsm)
	if err := fsm.Restore(io.NopCloser(bytes.NewReader(encoded))); err != nil {
		t.Fatalf("restore legacy snapshot: %v", err)
	}
	var count int64
	if err := database.Model(&BackupTargetRestoreOperation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("target restore operations after legacy restore = %d err=%v", count, err)
	}
	if err := database.Model(&BackupTargetNodeReadiness{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("target readiness after legacy restore = %d err=%v", count, err)
	}
}

type writerSnapSink struct {
	buf *bytes.Buffer
}

func (w *writerSnapSink) Close() error                { return nil }
func (w *writerSnapSink) Cancel() error               { return nil }
func (w *writerSnapSink) ID() string                  { return "test" }
func (w *writerSnapSink) Write(p []byte) (int, error) { return w.buf.Write(p) }
