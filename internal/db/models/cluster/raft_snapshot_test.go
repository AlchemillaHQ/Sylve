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
	"io"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/hashicorp/raft"
)

func allSnapshotModels() []any {
	return []any{
		&ClusterNote{},
		&ClusterOption{},
		&BackupTarget{},
		&BackupJob{},
		&ReplicationPolicy{},
		&ReplicationPolicyTarget{},
		&ReplicationLease{},
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
		ID: 100, Name: "t1", SSHHost: "localhost", SSHPort: 22, BackupRoot: "/backup",
	}
	if err := sourceDB.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed backup target: %v", err)
	}

	if err := sourceDB.Create(&BackupJob{
		ID: 200, Name: "job1", TargetID: target.ID, Mode: BackupJobModeDataset,
		CronExpr: "0 0 * * *",
	}).Error; err != nil {
		t.Fatalf("failed to seed backup job: %v", err)
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

	if err := sourceDB.Create(&ReplicationEvent{
		ID: 600, EventType: "incremental", Status: "success",
		SourceNodeID: "node-1", TargetNodeID: "node-2",
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
	if len(targets) != 1 || targets[0].Name != "t1" {
		t.Fatalf("targets mismatch: %+v", targets)
	}

	var jobs []BackupJob
	destDB.Find(&jobs)
	if len(jobs) != 1 || jobs[0].Name != "job1" {
		t.Fatalf("jobs mismatch: %+v", jobs)
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
	if len(events) != 1 || events[0].Status != "success" {
		t.Fatalf("events mismatch: %+v", events)
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

type writerSnapSink struct {
	buf *bytes.Buffer
}

func (w *writerSnapSink) Close() error         { return nil }
func (w *writerSnapSink) Cancel() error        { return nil }
func (w *writerSnapSink) ID() string           { return "test" }
func (w *writerSnapSink) Write(p []byte) (int, error) { return w.buf.Write(p) }
