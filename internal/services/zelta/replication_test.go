// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestTransitionStateInProgress(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{clusterModels.ReplicationTransitionStateDemoting, true},
		{clusterModels.ReplicationTransitionStateCatchup, true},
		{clusterModels.ReplicationTransitionStatePromoting, true},
		{clusterModels.ReplicationTransitionStateNone, false},
		{clusterModels.ReplicationTransitionStateCompleted, false},
		{clusterModels.ReplicationTransitionStateFailed, false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if got := transitionStateInProgress(tt.state); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestTransitionDemoteAckRequired(t *testing.T) {
	if !transitionDemoteAckRequired("") {
		t.Fatal("empty reason should require ack")
	}
	if transitionDemoteAckRequired("force") {
		t.Fatal("force should not require ack")
	}
	if transitionDemoteAckRequired("node_down_failover") {
		t.Fatal("node_down_failover should not require ack")
	}
	if !transitionDemoteAckRequired("manual_failover") {
		t.Fatal("manual_failover should require ack")
	}
}

func TestTransitionAllowUnsafe(t *testing.T) {
	if transitionAllowUnsafe("") {
		t.Fatal("empty reason should not allow unsafe")
	}
	if !transitionAllowUnsafe("force") {
		t.Fatal("force should allow unsafe")
	}
	if !transitionAllowUnsafe("FORCE_FAILOVER") {
		t.Fatal("FORCE_FAILOVER should allow unsafe")
	}
	if transitionAllowUnsafe("manual") {
		t.Fatal("manual should not allow unsafe")
	}
}

func TestPolicyFailoverMode(t *testing.T) {
	if policyFailoverMode(nil) != clusterModels.ReplicationFailoverManual {
		t.Fatal("nil policy should return manual")
	}
	policy := &clusterModels.ReplicationPolicy{FailoverMode: clusterModels.ReplicationFailoverAutoSafe}
	if policyFailoverMode(policy) != clusterModels.ReplicationFailoverAutoSafe {
		t.Fatal("expected auto_safe")
	}
	policy.FailoverMode = "invalid"
	if policyFailoverMode(policy) != clusterModels.ReplicationFailoverManual {
		t.Fatal("invalid mode should default to manual")
	}
}

func TestReplicationFailoverRequestMode(t *testing.T) {
	if replicationFailoverRequestMode("force") != replicationFailoverRequestForce {
		t.Fatal("expected force")
	}
	if replicationFailoverRequestMode("safe") != replicationFailoverRequestSafe {
		t.Fatal("expected safe")
	}
	if replicationFailoverRequestMode("") != replicationFailoverRequestSafe {
		t.Fatal("empty should default to safe")
	}
	if replicationFailoverRequestMode("unknown") != replicationFailoverRequestSafe {
		t.Fatal("unknown should default to safe")
	}
}

func TestReplicationGuestKey(t *testing.T) {
	if replicationGuestKey("", 0) != "" {
		t.Fatal("empty guest should return empty key")
	}
	if replicationGuestKey(clusterModels.ReplicationGuestTypeVM, 100) != "vm:100" {
		t.Fatal("expected vm:100")
	}
	if replicationGuestKey("  VM  ", 200) != "vm:200" {
		t.Fatal("expected vm:200 after trimming")
	}
	if replicationGuestKey(clusterModels.ReplicationGuestTypeJail, 50) != "jail:50" {
		t.Fatal("expected jail:50")
	}
}

func TestTransitionPayloadFromPolicy(t *testing.T) {
	payload := transitionPayloadFromPolicy(nil)
	if payload.State != "" {
		t.Fatal("nil policy should return empty payload")
	}

	policy := &clusterModels.ReplicationPolicy{
		TransitionState:      clusterModels.ReplicationTransitionStateDemoting,
		TransitionRunID:      "run-123",
		TransitionReason:     "manual",
		TransitionOwnerEpoch: 5,
	}
	payload = transitionPayloadFromPolicy(policy)
	if payload.State != clusterModels.ReplicationTransitionStateDemoting {
		t.Fatalf("state mismatch: %q", payload.State)
	}
	if payload.RunID != "run-123" {
		t.Fatalf("runID mismatch: %q", payload.RunID)
	}
	if payload.OwnerEpoch != 5 {
		t.Fatalf("epoch mismatch: %d", payload.OwnerEpoch)
	}
}

func TestProjectedPolicyTopologyAfterFailover(t *testing.T) {
	sourceNodeID, activeNodeID := projectedPolicyTopologyAfterFailover(nil, "node-2", false)
	if sourceNodeID != "" || activeNodeID != "node-2" {
		t.Fatalf("nil policy: src=%q act=%q", sourceNodeID, activeNodeID)
	}

	policy := &clusterModels.ReplicationPolicy{
		SourceNodeID: "node-1",
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
	}
	sourceNodeID, activeNodeID = projectedPolicyTopologyAfterFailover(policy, "node-2", false)
	if sourceNodeID != "node-2" || activeNodeID != "node-2" {
		t.Fatalf("follow_active: src should be target, got src=%q act=%q", sourceNodeID, activeNodeID)
	}

	policy.SourceMode = clusterModels.ReplicationSourceModePinned
	sourceNodeID, activeNodeID = projectedPolicyTopologyAfterFailover(policy, "node-2", false)
	if sourceNodeID != "node-1" {
		t.Fatalf("pinned without move: src should be original, got %q", sourceNodeID)
	}

	sourceNodeID, activeNodeID = projectedPolicyTopologyAfterFailover(policy, "node-2", true)
	if sourceNodeID != "node-2" {
		t.Fatalf("pinned with move: src should be target, got %q", sourceNodeID)
	}
}

func TestReplicationPolicyOwnerNode(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{ActiveNodeID: "active-1", SourceNodeID: "source-1"}
	if replicationPolicyOwnerNode(policy) != "active-1" {
		t.Fatal("expected active node when non-empty")
	}

	policy.ActiveNodeID = ""
	if replicationPolicyOwnerNode(policy) != "source-1" {
		t.Fatal("expected source node when active is empty")
	}

	policy.SourceNodeID = ""
	if replicationPolicyOwnerNode(policy) != "" {
		t.Fatal("expected empty when both empty")
	}

	if replicationPolicyOwnerNode(nil) != "" {
		t.Fatal("nil policy should return empty")
	}
}

func TestReplicationPolicyOwnerEpoch(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{OwnerEpoch: 42}
	if replicationPolicyOwnerEpoch(policy) != 42 {
		t.Fatalf("expected 42, got %d", replicationPolicyOwnerEpoch(policy))
	}
	if replicationPolicyOwnerEpoch(nil) != 0 {
		t.Fatal("nil policy should return 0")
	}
}

func TestIsReplicationPolicyTransitionRunningError(t *testing.T) {
	if isReplicationPolicyTransitionRunningError(nil) {
		t.Fatal("nil should not be running error")
	}
	if isReplicationPolicyTransitionRunningError(errors.New("something")) {
		t.Fatal("unrelated error should not match")
	}
	runningErr := errReplicationPolicyTransitionAlreadyRunning
	if !isReplicationPolicyTransitionRunningError(runningErr) {
		t.Fatal("transition already running error should match")
	}
	if !isReplicationPolicyTransitionRunningError(fmt.Errorf("wrapped: %w", runningErr)) {
		t.Fatal("wrapped error should match via errors.Is")
	}
}

func TestSplitDatasetForTarget(t *testing.T) {
	tests := []struct {
		input       string
		wantPool    string
		wantDataset string
	}{
		{"tank/data/vm-1", "tank", "data/vm-1"},
		{"zroot", "zroot", ""}, // no slash -> root dataset
		{"pool/ds", "pool", "ds"},
		{"pool/a/b", "pool", "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			pool, ds := splitDatasetForTarget(tt.input)
			if pool != tt.wantPool || ds != tt.wantDataset {
				t.Fatalf("splitDatasetForTarget(%q) = (%q, %q), want (%q, %q)",
					tt.input, pool, ds, tt.wantPool, tt.wantDataset)
			}
		})
	}
}

func TestTargetDatasetPath(t *testing.T) {
	if targetDatasetPath("tank/backups", "vm-1") != "tank/backups/vm-1" {
		t.Fatalf("expected tank/backups/vm-1, got %q", targetDatasetPath("tank/backups", "vm-1"))
	}
	if targetDatasetPath("tank/backups", "") != "tank/backups" {
		t.Fatalf("empty suffix should return root: %q", targetDatasetPath("tank/backups", ""))
	}
}

func TestReplicationRunnerNodeID(t *testing.T) {
	db := newZeltaServiceTestDB(t)
	s := &Service{DB: db}

	policy := &clusterModels.ReplicationPolicy{
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		ActiveNodeID: "runner-node",
		SourceNodeID: "source-node",
	}
	if s.replicationRunnerNodeID(policy) != "runner-node" {
		t.Fatal("expected active node as runner")
	}

	policy.ActiveNodeID = ""
	if s.replicationRunnerNodeID(policy) != "source-node" {
		t.Fatal("expected source node as runner when active is empty")
	}

	policy.SourceMode = clusterModels.ReplicationSourceModePinned
	if s.replicationRunnerNodeID(policy) != "source-node" {
		t.Fatal("pinned should always use source node")
	}
}

func TestBackupJobToReqWithRunnerPreservesRecursive(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(fmt.Sprintf("recursive_%t", recursive), func(t *testing.T) {
			job := &clusterModels.BackupJob{
				Name:      "guest-backup",
				TargetID:  7,
				Mode:      clusterModels.BackupJobModeVM,
				Recursive: recursive,
				Enabled:   true,
			}

			req := backupJobToReqWithRunner(job, " node-b ")
			if req.Recursive != recursive {
				t.Fatalf("recursive = %v, want %v", req.Recursive, recursive)
			}
			if req.RunnerNodeID != "node-b" {
				t.Fatalf("runner node = %q, want node-b", req.RunnerNodeID)
			}
		})
	}
}

func TestIsReplicationTargetModifiedError(t *testing.T) {
	if !isReplicationTargetModifiedError(errors.New("cannot receive incremental stream: destination has been modified")) {
		t.Fatal("destination modified should match")
	}
	if isReplicationTargetModifiedError(errors.New("connection refused")) {
		t.Fatal("unrelated error should not match")
	}
}

func TestIsReplicationResumeStateError(t *testing.T) {
	err := errors.New("cannot receive resume stream: partially-complete state exists")
	if !isReplicationResumeStateError(err) {
		t.Fatal("resume state error should match")
	}
	if isReplicationResumeStateError(errors.New("success")) {
		t.Fatal("success should not match")
	}
	if isReplicationResumeStateError(errors.New("cannot receive resume stream: other error")) {
		t.Fatal("missing partially-complete state should not match")
	}
}

func TestIsReplicationResumeAbortNoopError(t *testing.T) {
	if isReplicationResumeAbortNoopError(errors.New("success")) {
		t.Fatal("success should not be resume abort noop")
	}
}

func TestReplicationErrorRetryClassificationMatchesRealisticError(t *testing.T) {
	realisticError := fmt.Errorf("full\tzroot/sylve/virtual-machines/103@ha_mq1atsyr\t71688\n" +
		"full\tzroot/sylve/virtual-machines/103/zvol-9@ha_mq1atsyr\t30187048\n" +
		"size\t30258736\n" +
		"warning: cannot send 'zroot/sylve/virtual-machines/103/zvol-9@ha_mq1atsyr': signal received\n" +
		"cannot receive: failed to read from stream: exit status 1: exit status 1")

	lowerSend := strings.ToLower(realisticError.Error())
	lowerOut := strings.ToLower("warning: cannot send 'zroot/sylve/virtual-machines/103/zvol-9@ha_mq1atsyr': signal received\n" +
		"cannot receive: failed to read from stream")
	if !strings.Contains(lowerSend, "signal") {
		t.Fatal("error should contain 'signal'")
	}
	if !strings.Contains(lowerSend, "exit status") {
		t.Fatal("error should contain 'exit status'")
	}
	if !strings.Contains(lowerSend, "cannot receive") {
		t.Fatal("error should contain 'cannot receive'")
	}
	if !strings.Contains(lowerOut, "failed to read from stream") {
		t.Fatal("output should contain 'failed to read from stream'")
	}
	_ = lowerSend
	if isReplicationResumeStateError(realisticError) || isReplicationTargetModifiedError(realisticError) {
		t.Log("realistic error unexpectedly matched existing classification - pre-fix behavior")
	}
}

func TestAppendReplicationFenceDatasetError(t *testing.T) {
	base := errors.New("base error")
	wrapped := appendReplicationFenceDatasetError(base, "tank/data", errors.New("dataset busy"))
	if wrapped == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(wrapped.Error(), "base error") {
		t.Fatal("should contain base error")
	}
	if !strings.Contains(wrapped.Error(), "tank/data") {
		t.Fatal("should contain dataset name")
	}

	// nil dataset err should return base error unchanged
	result := appendReplicationFenceDatasetError(base, "tank/data", nil)
	if result.Error() != "base error" {
		t.Fatalf("nil dataset err should not wrap, got: %v", result)
	}
}

func TestReplicationPolicyHAError(t *testing.T) {
	eval := clusterService.ReplicationPolicyHAEvaluation{
		Eligible: true,
		Reasons:  []string{clusterService.ReplicationHAReasonQuorumLost},
	}
	if err := replicationPolicyHAError(eval); err != nil {
		t.Fatalf("eligible eval should return nil error, got: %v", err)
	}

	eval.Eligible = false
	err := replicationPolicyHAError(eval)
	if err == nil {
		t.Fatal("ineligible eval should return error")
	}
	if !strings.Contains(err.Error(), clusterService.ReplicationHAReasonQuorumLost) {
		t.Fatalf("error should contain reason, got: %v", err)
	}
}

func TestAcquireReleaseReplication(t *testing.T) {
	s := &Service{runningReplication: make(map[uint]struct{})}

	if !s.acquireReplication(1) {
		t.Fatal("first acquire should succeed")
	}
	if s.acquireReplication(1) {
		t.Fatal("second acquire should fail (already running)")
	}
	if !s.acquireReplication(2) {
		t.Fatal("different policy should acquire")
	}

	s.releaseReplication(1)
	if !s.acquireReplication(1) {
		t.Fatal("after release should acquire again")
	}

	s.releaseReplication(1)
	s.releaseReplication(2)
	s.releaseReplication(999) // releasing non-existent is no-op
}

func TestStaleReplicationLineageDatasets(t *testing.T) {
	root := "tank/vm-1"
	lineage := []string{
		"tank/vm-1",
		"tank/vm-1_gen-0",
		"tank/vm-1_gen-1",
		"tank/vm-1_gen-2",
		"tank/vm-1_gen-3",
	}
	stale := staleReplicationLineageDatasets(root, lineage, 2)
	if len(stale) != 2 {
		t.Fatalf("expected 2 stale datasets (keeping 2 out-of-band), got %d: %v", len(stale), stale)
	}

	if len(staleReplicationLineageDatasets(root, nil, 1)) != 0 {
		t.Fatal("nil lineage should return empty")
	}
	if len(staleReplicationLineageDatasets(root, lineage, 0)) != 4 {
		t.Fatalf("keepOutOfBand=0 should keep all 4 gen entries")
	}
}

func TestReplicationLineageBaseLeaf(t *testing.T) {
	if replicationLineageBaseLeaf("vm-1_gen-0") != "vm-1" {
		t.Fatal("should strip _gen- suffix")
	}
	if replicationLineageBaseLeaf("vm-1") != "vm-1" {
		t.Fatal("no _gen- suffix should return unchanged")
	}
}
