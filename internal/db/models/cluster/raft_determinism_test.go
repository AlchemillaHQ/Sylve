// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package clusterModels

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

func newSkewedDeterminismFSM(t *testing.T, localTime time.Time) (*gorm.DB, *FSMDispatcher) {
	t.Helper()

	db := newClusterModelTestDB(t, allSnapshotModels()...)
	db = db.Session(&gorm.Session{
		NowFunc: func() time.Time { return localTime },
	})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)
	return db, fsm
}

func deterministicCommandBytes(
	t *testing.T,
	decidedAt time.Time,
	commandType string,
	action string,
	data any,
) []byte {
	t.Helper()

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal command data: %v", err)
	}
	command := Command{Type: commandType, Action: action, Data: raw}
	if err := PrepareCommand(&command, decidedAt); err != nil {
		t.Fatalf("prepare command: %v", err)
	}
	payload, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	return payload
}

func applyDeterminismLog(
	t *testing.T,
	fsm *FSMDispatcher,
	payload []byte,
	followerLogTime time.Time,
) {
	t.Helper()

	response := fsm.Apply(&raft.Log{
		Type:       raft.LogCommand,
		Data:       payload,
		AppendedAt: followerLogTime,
	})
	if response == nil {
		return
	}
	if err, ok := response.(error); ok {
		t.Fatalf("apply deterministic command: %v", err)
	}
	t.Fatalf("unexpected apply response: %T", response)
}

func assertCompleteRowsEqual[T any](
	t *testing.T,
	leftDB *gorm.DB,
	rightDB *gorm.DB,
	query string,
	args ...any,
) {
	t.Helper()

	var left T
	if err := leftDB.Where(query, args...).First(&left).Error; err != nil {
		t.Fatalf("load left row: %v", err)
	}
	var right T
	if err := rightDB.Where(query, args...).First(&right).Error; err != nil {
		t.Fatalf("load right row: %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("replicated rows differ:\nleft:  %#v\nright: %#v", left, right)
	}
}

func snapshotDigest(t *testing.T, fsm *FSMDispatcher) ([sha256.Size]byte, []byte) {
	t.Helper()

	rawSnapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("take snapshot: %v", err)
	}
	snapshot, ok := rawSnapshot.(*ClusterSnapshot)
	if !ok {
		t.Fatalf("unexpected snapshot type %T", rawSnapshot)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return sha256.Sum256(payload), payload
}

func TestFSMVersionedCommandsAreDeterministicAcrossSkewedClocks(t *testing.T) {
	leftDB, leftFSM := newSkewedDeterminismFSM(
		t,
		time.Date(2035, time.January, 1, 0, 0, 0, 0, time.FixedZone("left", -8*60*60)),
	)
	rightDB, rightFSM := newSkewedDeterminismFSM(
		t,
		time.Date(2045, time.June, 1, 0, 0, 0, 0, time.FixedZone("right", 9*60*60)),
	)
	fsms := []*FSMDispatcher{leftFSM, rightFSM}
	followerTimes := []time.Time{
		time.Date(2050, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2060, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	applyBoth := func(payload []byte) {
		for index, fsm := range fsms {
			applyDeterminismLog(t, fsm, payload, followerTimes[index])
		}
	}

	createAt := time.Date(2026, time.July, 30, 10, 0, 0, 987654321, time.FixedZone("leader", 5*60*60+30*60))
	updateAt := createAt.Add(time.Minute)
	leaseAt := updateAt.Add(time.Minute)
	transitionAt := leaseAt.Add(time.Minute)

	applyBoth(deterministicCommandBytes(t, createAt, "note", "create", ClusterNote{
		ID: 1, Title: "deterministic", Content: "created",
	}))
	applyBoth(deterministicCommandBytes(t, updateAt, "note", "update", ClusterNote{
		ID: 1, Title: "deterministic", Content: "updated",
	}))
	applyBoth(deterministicCommandBytes(t, createAt, "backup_target", "create", BackupTargetReplicationPayload{
		ID: 10, Name: "target", SSHHost: "root@target", SSHPort: 22,
		BackupRoot: "tank/backups", Enabled: true,
	}))

	policy := ReplicationPolicy{
		ID: 20, Name: "policy", GuestType: ReplicationGuestTypeVM, GuestID: 99,
		SourceNodeID: "node-a", ActiveNodeID: "node-a", OwnerEpoch: 1,
		SourceMode:   ReplicationSourceModeFollowActive,
		FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual,
		CronExpr:     "*/5 * * * *", Enabled: true,
		ProtectionState: ReplicationProtectionStateArmed,
		TransitionState: ReplicationTransitionStateNone,
	}
	applyBoth(deterministicCommandBytes(t, createAt, "replication_policy", "create", ReplicationPolicyPayload{
		Policy: policy,
		Targets: []ReplicationPolicyTarget{{
			ID: 21, PolicyID: policy.ID, NodeID: "node-b", Weight: 100,
		}},
	}))
	leaseExpiry := leaseAt.Add(30 * time.Second)
	applyBoth(deterministicCommandBytes(t, leaseAt, "replication_lease", "upsert", ReplicationLease{
		ID: 30, PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
		OwnerNodeID: "node-a", OwnerEpoch: 1, ExpiresAt: leaseExpiry,
		Version: 1, LastReason: "test", LastActor: "leader",
	}))

	originalRunning := true
	requestedAt := transitionAt
	transition := ReplicationPolicyTransition{
		State: ReplicationTransitionStateDemoting, RunID: "transition-1",
		Reason: "manual_failover", SourceNodeID: "node-a", TargetNodeID: "node-b",
		OwnerEpoch: 1, RequestedAt: &requestedAt, OriginalRunning: &originalRunning,
		OriginalSourceNodeID: "node-a",
	}
	applyBoth(deterministicCommandBytes(t, transitionAt, "replication_policy_transition", "begin", ReplicationPolicyTransitionBegin{
		PolicyID: policy.ID, ExpectedOwnerEpoch: 1, Transition: transition,
	}))
	demotedAt := transitionAt.Add(time.Minute)
	transition.State = ReplicationTransitionStateCatchup
	transition.DemotedAt = &demotedAt
	applyBoth(deterministicCommandBytes(t, demotedAt, "replication_policy_transition", "update", struct {
		PolicyID   uint                        `json:"policyId"`
		Transition ReplicationPolicyTransition `json:"transition"`
	}{PolicyID: policy.ID, Transition: transition}))

	assertCompleteRowsEqual[ClusterNote](t, leftDB, rightDB, "id = ?", 1)
	assertCompleteRowsEqual[BackupTarget](t, leftDB, rightDB, "id = ?", 10)
	assertCompleteRowsEqual[ReplicationPolicy](t, leftDB, rightDB, "id = ?", policy.ID)
	assertCompleteRowsEqual[ReplicationPolicyTarget](
		t,
		leftDB,
		rightDB,
		"policy_id = ? AND node_id = ?",
		policy.ID,
		"node-b",
	)
	assertCompleteRowsEqual[ReplicationLease](t, leftDB, rightDB, "id = ?", 30)

	var storedNote ClusterNote
	if err := leftDB.First(&storedNote, 1).Error; err != nil {
		t.Fatalf("load note timestamps: %v", err)
	}
	if !storedNote.CreatedAt.Equal(NormalizeCommandTime(createAt)) ||
		!storedNote.UpdatedAt.Equal(NormalizeCommandTime(updateAt)) {
		t.Fatalf("note did not use leader decisions: %+v", storedNote)
	}

	leftDigest, snapshotPayload := snapshotDigest(t, leftFSM)
	rightDigest, _ := snapshotDigest(t, rightFSM)
	if leftDigest != rightDigest {
		t.Fatalf("replayed state digests differ: left=%x right=%x", leftDigest, rightDigest)
	}

	restoreDB, restoreFSM := newSkewedDeterminismFSM(t, time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err := restoreFSM.Restore(io.NopCloser(bytes.NewReader(snapshotPayload))); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	restoreDigest, _ := snapshotDigest(t, restoreFSM)
	if leftDigest != restoreDigest {
		t.Fatalf("restored state digest differs: source=%x restored=%x", leftDigest, restoreDigest)
	}
	assertCompleteRowsEqual[BackupTarget](t, leftDB, restoreDB, "id = ?", 10)
}

func TestFSMTransitionRecoveryDeadlineSurvivesReplay(t *testing.T) {
	leftDB, leftFSM := newSkewedDeterminismFSM(t, time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC))
	rightDB, rightFSM := newSkewedDeterminismFSM(t, time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC))
	startedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	originalRunning := true
	policy := ReplicationPolicy{
		ID: 40, Name: "recovering", GuestType: ReplicationGuestTypeJail, GuestID: 100,
		SourceNodeID: "node-a", ActiveNodeID: "node-b", OwnerEpoch: 2,
		SourceMode:   ReplicationSourceModeFollowActive,
		FailbackMode: ReplicationFailbackManual,
		FailoverMode: ReplicationFailoverManual,
		CronExpr:     "*/5 * * * *", Enabled: true,
		ProtectionState: ReplicationProtectionStateDegraded,
		TransitionState: ReplicationTransitionStatePromoting,
		TransitionRunID: "recover-1", TransitionReason: "auto_failover",
		TransitionSourceNodeID: "node-a", TransitionTargetNodeID: "node-b",
		TransitionOwnerEpoch: 2, TransitionRequestedAt: &startedAt,
		TransitionOriginalRunning:      &originalRunning,
		TransitionOriginalSourceNodeID: "node-a",
	}
	create := deterministicCommandBytes(t, startedAt, "replication_policy", "create", ReplicationPolicyPayload{
		Policy: policy,
	})
	deadline := startedAt.Add(2 * time.Minute)
	transition := ReplicationPolicyTransition{
		State: ReplicationTransitionStatePromoting, RunID: policy.TransitionRunID,
		Reason: policy.TransitionReason, SourceNodeID: policy.TransitionSourceNodeID,
		TargetNodeID: policy.TransitionTargetNodeID, OwnerEpoch: policy.TransitionOwnerEpoch,
		RequestedAt: &startedAt, RecoveryDeadlineAt: &deadline,
		OriginalRunning: &originalRunning, OriginalSourceNodeID: "node-a",
	}
	update := deterministicCommandBytes(t, startedAt.Add(time.Second), "replication_policy_transition", "update", struct {
		PolicyID   uint                        `json:"policyId"`
		Transition ReplicationPolicyTransition `json:"transition"`
	}{PolicyID: policy.ID, Transition: transition})

	for index, fsm := range []*FSMDispatcher{leftFSM, rightFSM} {
		applyDeterminismLog(t, fsm, create, startedAt.Add(time.Duration(index)*time.Hour))
		applyDeterminismLog(t, fsm, update, startedAt.Add(time.Duration(index+10)*time.Hour))
	}

	assertCompleteRowsEqual[ReplicationPolicy](t, leftDB, rightDB, "id = ?", policy.ID)
	var stored ReplicationPolicy
	if err := leftDB.First(&stored, policy.ID).Error; err != nil {
		t.Fatalf("load recovery policy: %v", err)
	}
	if stored.TransitionRecoveryDeadlineAt == nil ||
		!stored.TransitionRecoveryDeadlineAt.Equal(deadline) {
		t.Fatalf("recovery deadline was not durably replayed: %+v", stored.TransitionRecoveryDeadlineAt)
	}
}
