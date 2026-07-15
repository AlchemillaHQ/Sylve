// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func makeRuntimeSnapshot(totalVoters, onlineVoters int) replicationHARuntimeSnapshot {
	return replicationHARuntimeSnapshot{
		TotalVoters:     totalVoters,
		OnlineVoters:    onlineVoters,
		QuorumRequired:  (totalVoters / 2) + 1,
		QuorumAvailable: onlineVoters >= ((totalVoters / 2) + 1),
	}
}

func TestReplicationPolicyEffectiveRunner(t *testing.T) {
	tests := []struct {
		name, mode, sourceNodeID, activeNodeID, want string
	}{
		{"pinned uses active owner", clusterModels.ReplicationSourceModePinned, "node-1", "node-2", "node-2"},
		{"follow_active prefers active", clusterModels.ReplicationSourceModeFollowActive, "node-1", "node-2", "node-2"},
		{"follow_active no active falls back to source", clusterModels.ReplicationSourceModeFollowActive, "node-1", "", "node-1"},
		{"follow_active empty both returns empty", clusterModels.ReplicationSourceModeFollowActive, "", "", ""},
		{"pinned empty source still uses active owner", clusterModels.ReplicationSourceModePinned, "", "node-2", "node-2"},
		{"pinned without active falls back to preferred source", clusterModels.ReplicationSourceModePinned, "node-1", "", "node-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replicationPolicyEffectiveRunner(tt.mode, tt.sourceNodeID, tt.activeNodeID)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNewReplicationHAIneligibleError(t *testing.T) {
	err := NewReplicationHAIneligibleError([]string{ReplicationHAReasonMinThreeVoters})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), ReplicationHAReasonMinThreeVoters) {
		t.Fatalf("expected reason in error string: %v", err)
	}

	err = NewReplicationHAIneligibleError([]string{})
	if err != nil {
		t.Fatal("expected nil for empty reasons")
	}

	err = NewReplicationHAIneligibleError([]string{ReplicationHAReasonMinThreeVoters, ReplicationHAReasonQuorumLost})
	if err == nil {
		t.Fatal("expected error for multiple reasons")
	}
	if !strings.Contains(err.Error(), ReplicationHAReasonMinThreeVoters) || !strings.Contains(err.Error(), ReplicationHAReasonQuorumLost) {
		t.Fatalf("expected both reasons: %v", err)
	}
}

func TestParseReplicationHAIneligibleReasons(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		orig := []string{ReplicationHAReasonMinThreeVoters, ReplicationHAReasonQuorumLost}
		err := NewReplicationHAIneligibleError(orig)
		parsed := ParseReplicationHAIneligibleReasons(err)
		if len(parsed) != 2 {
			t.Fatalf("expected 2 reasons, got %d", len(parsed))
		}
		for _, r := range orig {
			found := false
			for _, p := range parsed {
				if p == r {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("reason %q not found in parsed", r)
			}
		}
	})

	t.Run("nil error returns nil", func(t *testing.T) {
		if got := ParseReplicationHAIneligibleReasons(nil); got != nil {
			t.Fatalf("expected nil for nil error, got %v", got)
		}
	})
}

func TestIsReplicationHAStaticIneligibleError(t *testing.T) {
	tests := []struct {
		name    string
		reasons []string
		want    bool
	}{
		{"min 3 voters is static", []string{ReplicationHAReasonMinThreeVoters}, true},
		{"quorum lost is static", []string{ReplicationHAReasonQuorumLost}, false},
		{"min 1 target is static", []string{ReplicationHAReasonMinOneTarget}, true},
		{"remote target is static", []string{ReplicationHAReasonRemoteTarget}, true},
		{"reduced redundancy is NOT static", []string{ReplicationHAReasonReducedRedundancy}, false},
		{"mixed static+dynamic has static", []string{ReplicationHAReasonReducedRedundancy, ReplicationHAReasonMinThreeVoters}, true},
		{"unknown is assumed false", []string{"unknown_reason"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewReplicationHAIneligibleError(tt.reasons)
			got := IsReplicationHAStaticIneligibleError(err)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestReplicationHAReasonSetIncludes(t *testing.T) {
	reasons := []string{ReplicationHAReasonMinThreeVoters, ReplicationHAReasonQuorumLost}
	if !ReplicationHAReasonSetIncludes(reasons, ReplicationHAReasonMinThreeVoters) {
		t.Fatal("expected to include min_three_voters")
	}
	if ReplicationHAReasonSetIncludes(reasons, ReplicationHAReasonRemoteTarget) {
		t.Fatal("should not include remote_target")
	}
	if ReplicationHAReasonSetIncludes(reasons, "") {
		t.Fatal("empty reason should return false")
	}
}

func TestApplyReplicationPolicyHAState(t *testing.T) {
	s := &Service{}
	policy := &clusterModels.ReplicationPolicy{}
	eval := ReplicationPolicyHAEvaluation{
		Eligible: true,
		Degraded: true,
		Reasons:  []string{ReplicationHAReasonReducedRedundancy},
	}
	s.ApplyReplicationPolicyHAState(policy, eval)
	if !policy.HAEligible || !policy.HADegraded {
		t.Fatal("expected HA state to be applied")
	}
	if len(policy.HAReasons) != 1 || policy.HAReasons[0] != ReplicationHAReasonReducedRedundancy {
		t.Fatalf("reasons mismatch: %v", policy.HAReasons)
	}

	s.ApplyReplicationPolicyHAState(nil, eval) // nil policy, no panic
}

func TestEvaluateReplicationPolicyHANilPolicy(t *testing.T) {
	s := &Service{}
	eval := s.EvaluateReplicationPolicyHA(nil)
	if eval.Eligible {
		t.Fatal("expected nil policy to be ineligible")
	}
}

func TestEvaluateReplicationPolicyHAWithRuntimeSnapshot(t *testing.T) {
	db := newClusterServiceTestDB(t)
	s := &Service{DB: db}

	policy := &clusterModels.ReplicationPolicy{
		ID: 1, Name: "ha-test", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "node-1",
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: "node-2", Weight: 100},
		},
	}

	t.Run("3 voters 2 targets all healthy", func(t *testing.T) {
		snapshot := makeRuntimeSnapshot(3, 3)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if !eval.Eligible {
			t.Fatalf("expected eligible, got: %v", eval.Reasons)
		}
		if eval.TotalVoters != 3 || eval.OnlineVoters != 3 {
			t.Fatalf("voter counts wrong: total=%d online=%d", eval.TotalVoters, eval.OnlineVoters)
		}
	})

	t.Run("2 voters ineligible", func(t *testing.T) {
		snapshot := makeRuntimeSnapshot(2, 2)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if eval.Eligible {
			t.Fatal("expected ineligible with 2 voters")
		}
		if !ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonMinThreeVoters) {
			t.Fatalf("expected min_3_voters reason, got: %v", eval.Reasons)
		}
	})

	t.Run("0 targets ineligible", func(t *testing.T) {
		policyNoTargets := &clusterModels.ReplicationPolicy{
			Name: "no-targets", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 200, SourceNodeID: "node-1",
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
		}
		snapshot := makeRuntimeSnapshot(3, 3)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policyNoTargets, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if eval.Eligible {
			t.Fatal("expected ineligible with 0 targets")
		}
		if !ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonMinOneTarget) {
			t.Fatalf("expected min_1_target, got: %v", eval.Reasons)
		}
	})

	t.Run("only local target ineligible", func(t *testing.T) {
		policySelfTarget := &clusterModels.ReplicationPolicy{
			Name: "self-target", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 300, SourceNodeID: "node-1",
			SourceMode:   clusterModels.ReplicationSourceModePinned,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
			Targets: []clusterModels.ReplicationPolicyTarget{
				{NodeID: "node-1", Weight: 100}, // same as source
			},
		}
		snapshot := makeRuntimeSnapshot(3, 3)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policySelfTarget, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if eval.Eligible {
			t.Fatal("expected ineligible with only local target")
		}
		if !ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonRemoteTarget) {
			t.Fatalf("expected remote_target reason, got: %v", eval.Reasons)
		}
	})

	t.Run("quorum lost ineligible", func(t *testing.T) {
		snapshot := makeRuntimeSnapshot(3, 1)
		snapshot.QuorumAvailable = false
		eval := s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if eval.Eligible {
			t.Fatal("expected ineligible with quorum lost")
		}
		if !ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonQuorumLost) {
			t.Fatalf("expected quorum_lost, got: %v", eval.Reasons)
		}
	})

	t.Run("follow_active identifies effective runner", func(t *testing.T) {
		policyFA := &clusterModels.ReplicationPolicy{
			Name: "fa", GuestType: clusterModels.ReplicationGuestTypeVM,
			GuestID: 400, ActiveNodeID: "node-3",
			SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
			FailbackMode: clusterModels.ReplicationFailbackManual,
			FailoverMode: clusterModels.ReplicationFailoverManual,
			CronExpr:     "* * * * *", OwnerEpoch: 1,
			Targets: []clusterModels.ReplicationPolicyTarget{
				{NodeID: "node-2", Weight: 100},
				{NodeID: "node-3", Weight: 50},
			},
		}
		snapshot := makeRuntimeSnapshot(3, 3)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policyFA, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if !eval.Eligible {
			t.Fatalf("expected eligible, got: %v", eval.Reasons)
		}
		if eval.EffectiveRunner != "node-3" {
			t.Fatalf("expected effective runner node-3, got %q", eval.EffectiveRunner)
		}
	})

	t.Run("reduced redundancy degraded but eligible", func(t *testing.T) {
		snapshot := makeRuntimeSnapshot(3, 2)
		snapshot.QuorumAvailable = true
		eval := s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &snapshot,
		})
		if !eval.Eligible {
			t.Fatalf("expected eligible (degraded), got: %v", eval.Reasons)
		}
		if !eval.Degraded {
			t.Fatal("expected degraded=true when online < total")
		}
		if !ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonReducedRedundancy) {
			t.Fatalf("expected reduced_redundancy, got: %v", eval.Reasons)
		}
	})

	t.Run("SkipRuntimeChecks bypasses quorum checks", func(t *testing.T) {
		snapshot := makeRuntimeSnapshot(3, 1)
		snapshot.QuorumAvailable = false
		eval := s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot:   &snapshot,
			SkipRuntimeChecks: true,
		})
		if !eval.Eligible {
			t.Fatalf("expected eligible when skipping runtime checks (quorum bypassed): %v", eval.Reasons)
		}
		if ReplicationHAReasonSetIncludes(eval.Reasons, ReplicationHAReasonQuorumLost) {
			t.Fatal("quorum_lost should be absent when SkipRuntimeChecks=true")
		}
	})
}

func TestEvaluateReplicationPolicyHAWithTargetsOverride(t *testing.T) {
	db := newClusterServiceTestDB(t)
	s := &Service{DB: db}

	policy := &clusterModels.ReplicationPolicy{
		Name: "override-test", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "node-1",
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
		Targets: []clusterModels.ReplicationPolicyTarget{}, // policy has no targets
	}

	// override with valid targets
	eval := s.EvaluateReplicationPolicyHAWithTargets(policy, []clusterModels.ReplicationPolicyTarget{
		{NodeID: "node-2", Weight: 100},
		{NodeID: "node-3", Weight: 50},
	})
	if eval.DistinctTargetCount != 2 {
		t.Fatalf("expected 2 distinct targets, got %d", eval.DistinctTargetCount)
	}
}

func TestEvaluateReplicationPolicyTransitionHA(t *testing.T) {
	db := newClusterServiceTestDB(t)
	s := &Service{DB: db}

	policy := &clusterModels.ReplicationPolicy{
		Name: "transition-test", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 100, SourceNodeID: "node-1", ActiveNodeID: "node-1",
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode: clusterModels.ReplicationFailbackManual,
		FailoverMode: clusterModels.ReplicationFailoverManual,
		CronExpr:     "* * * * *", OwnerEpoch: 1,
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: "node-2", Weight: 100},
			{NodeID: "node-3", Weight: 50},
		},
	}

	eval := s.EvaluateReplicationPolicyTransitionHA(policy, "node-3", "node-3")
	if eval.EffectiveRunner != "node-3" {
		t.Fatalf("expected effective runner to switch to node-3, got %q", eval.EffectiveRunner)
	}
}

func TestBuildReplicationHARuntimeSnapshot(t *testing.T) {
	t.Run("nil service returns zero snapshot", func(t *testing.T) {
		var s *Service
		snapshot := s.buildReplicationHARuntimeSnapshot()
		if snapshot.TotalVoters != 0 || snapshot.OnlineVoters != 0 {
			t.Fatalf("expected zero snapshot, got %+v", snapshot)
		}
	})

	t.Run("nil Raft returns zero snapshot", func(t *testing.T) {
		db := newClusterServiceTestDB(t)
		s := &Service{DB: db, Raft: nil}
		snapshot := s.buildReplicationHARuntimeSnapshot()
		if snapshot.TotalVoters != 0 {
			t.Fatalf("expected zero voters with nil Raft, got %d", snapshot.TotalVoters)
		}
	})

	t.Run("in-memory Raft with known voters", func(t *testing.T) {
		nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNode{})
		defer cleanupClusterRaftTestNodes(t, nodes)

		leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

		// seed online cluster nodes matching raft voter IDs
		for _, n := range nodes {
			leader.service.DB.Create(&clusterModels.ClusterNode{
				NodeUUID: n.id, Status: "online",
			})
		}

		snapshot := leader.service.buildReplicationHARuntimeSnapshot()
		if snapshot.TotalVoters != 3 {
			t.Fatalf("expected 3 voters, got %d", snapshot.TotalVoters)
		}
		if snapshot.QuorumRequired != 2 {
			t.Fatalf("expected quorum=2, got %d", snapshot.QuorumRequired)
		}
		if !snapshot.QuorumAvailable {
			t.Fatalf("expected quorum available with 3 online voters, got online=%d leader_healthy=%v",
				snapshot.OnlineVoters, snapshot.LeaderHealthy)
		}
	})

	t.Run("3 voters 2 online yields quorum", func(t *testing.T) {
		nodes := setupClusterRaftTestNodes(t, 3, &clusterModels.ClusterNode{})
		defer cleanupClusterRaftTestNodes(t, nodes)

		leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

		// seed online status for nodes
		for _, n := range nodes {
			leader.service.DB.Create(&clusterModels.ClusterNode{
				NodeUUID: n.id, Status: "online",
			})
		}

		snapshot := leader.service.buildReplicationHARuntimeSnapshot()
		if snapshot.TotalVoters != 3 {
			t.Fatalf("expected 3 voters, got %d", snapshot.TotalVoters)
		}
		if snapshot.OnlineVoters < 2 {
			t.Fatalf("expected at least 2 online, got %d", snapshot.OnlineVoters)
		}
		if !snapshot.QuorumAvailable {
			t.Fatal("expected quorum available")
		}
	})

	t.Run("single node has quorum=1", func(t *testing.T) {
		nodes := setupClusterRaftTestNodes(t, 1, &clusterModels.ClusterNode{})
		defer cleanupClusterRaftTestNodes(t, nodes)

		leader := waitForClusterRaftLeader(t, nodes, 8*time.Second)

		// seed online cluster node
		leader.service.DB.Create(&clusterModels.ClusterNode{
			NodeUUID: "node-1", Status: "online",
		})

		snapshot := leader.service.buildReplicationHARuntimeSnapshot()
		if snapshot.TotalVoters != 1 {
			t.Fatalf("expected 1 voter, got %d", snapshot.TotalVoters)
		}
		if snapshot.QuorumRequired != 1 {
			t.Fatalf("expected quorum=1, got %d", snapshot.QuorumRequired)
		}
		if !snapshot.QuorumAvailable {
			t.Fatalf("expected quorum available for single node, online=%d leader_healthy=%v",
				snapshot.OnlineVoters, snapshot.LeaderHealthy)
		}
	})
}
