// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestTransitionCallsRequireTheirFields(t *testing.T) {
	s := newTestZeltaService(newZeltaServiceTestDB(t))
	ctx := t.Context()
	calls := map[string]func(transitionRequest) error{
		"activate": func(r transitionRequest) error { _, err := s.activateCall(ctx, "", r); return err },
		"demote":   func(r transitionRequest) error { _, err := s.demoteCall(ctx, "", r); return err },
		"catchup":  func(r transitionRequest) error { _, err := s.catchupCall(ctx, "", r); return err },
		"runtime":  func(r transitionRequest) error { _, err := s.runtimeStateCall(ctx, "", r); return err },
	}
	for name, call := range calls {
		for _, request := range []transitionRequest{
			{OwnerEpoch: 2, TransitionRunID: "run-1"},
			{PolicyID: 1, TransitionRunID: "run-1"},
			{PolicyID: 1, OwnerEpoch: 2, TransitionRunID: " "},
		} {
			if err := call(request); err == nil || !strings.Contains(err.Error(), "invalid_request") {
				t.Fatalf("%s accepted %+v: %v", name, request, err)
			}
		}
	}
	complete := transitionRequest{PolicyID: 1, OwnerEpoch: 2, TransitionRunID: "run-1", TargetNodeID: "node-2"}
	if _, err := s.activateCall(ctx, "", complete); err == nil ||
		!strings.Contains(err.Error(), "replication_desired_running_required") {
		t.Fatalf("activate without a desired state: %v", err)
	}
	if _, err := s.catchupCall(ctx, "", complete); err == nil ||
		!strings.Contains(err.Error(), "replication_generation_id_required") {
		t.Fatalf("catchup without a generation: %v", err)
	}
	for _, request := range []transitionRequest{{PolicyID: 1}, {OwnerEpoch: 2}} {
		if _, err := s.deleteCleanupCall(ctx, "", request); err == nil || !strings.Contains(err.Error(), "invalid_request") {
			t.Fatalf("delete cleanup accepted %+v: %v", request, err)
		}
	}
}

func newCallsFixture(t *testing.T) (fx *ZeltaClusterFixture, leader, follower *zeltaRaftNode) {
	t.Helper()
	fx = SetupZeltaClusterFixture(t, 2)
	leader = fx.LeaderNode()
	for _, node := range fx.Nodes {
		(&Service{DB: node.db, Cluster: node.cService}).registerCalls(node.cService)
		if node != leader {
			follower = node
		}
	}
	if leader == nil || follower == nil {
		t.Fatal("fixture lacks a leader and a follower")
	}
	return fx, leader, follower
}

func TestIntegrationMigrationInterlockBarrierAsksTheTarget(t *testing.T) {
	fx, leader, target := newCallsFixture(t)
	asked := 0
	fx.routeCalls(func(_, to *zeltaRaftNode, name string, _ []byte) error {
		if name != "migration-interlock-applied" || to != target {
			t.Fatalf("unexpected call %s to %s", name, to.id)
		}
		asked++
		return nil
	})
	now := time.Now().UTC()
	operation := clusterModels.ReplicationGuestOperation{
		GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 702,
		Operation: clusterModels.ReplicationGuestOperationMigration,
		State:     clusterModels.ReplicationGuestOperationCutover,
		Token:     "migration:source:702", OwnerNodeID: leader.id, TargetNodeID: target.id,
		TaskID: 702, AcquiredAt: now, SealedAt: &now,
	}
	if err := leader.db.Create(&operation).Error; err != nil {
		t.Fatalf("seed leader interlock: %v", err)
	}
	service := &Service{DB: leader.db, Cluster: leader.cService}
	wait := func(timeout time.Duration) error {
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		defer cancel()
		return service.WaitGuestMigrationInterlockApplied(ctx, operation.GuestType, operation.GuestID, target.id, operation.Token)
	}

	if err := wait(600 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "migration_interlock_apply_barrier_timeout") {
		t.Fatalf("barrier passed before the target had the interlock: %v", err)
	}
	if asked == 0 {
		t.Fatal("barrier never asked the target")
	}
	if err := target.db.Create(&operation).Error; err != nil {
		t.Fatalf("seed target interlock: %v", err)
	}
	if err := wait(5 * time.Second); err != nil {
		t.Fatalf("barrier with the target's interlock: %v", err)
	}
}

func TestIntegrationDeleteCleanupRetriesOnlyWhatCanPass(t *testing.T) {
	fx, leader, follower := newCallsFixture(t)
	policy := clusterModels.ReplicationPolicy{
		ID: 181, Name: "deleting", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 181,
		SourceNodeID: leader.id, ActiveNodeID: leader.id, OwnerEpoch: 4,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
	}
	for _, node := range fx.Nodes {
		if err := node.db.Create(&policy).Error; err != nil {
			t.Fatalf("seed policy on %s: %v", node.id, err)
		}
	}
	attempts := 0
	fx.routeCalls(func(_, to *zeltaRaftNode, name string, _ []byte) error {
		if name != "replication-delete-cleanup" || to != follower {
			t.Fatalf("unexpected call %s to %s", name, to.id)
		}
		if attempts++; attempts == 1 {
			return errors.New("replication_policy_delete_cleanup_quiescing")
		}
		return nil
	})

	service := &Service{DB: leader.db, Cluster: leader.cService}
	err := service.cleanupReplicationPolicyDeleteOn(t.Context(), follower.id, policy.ID, policy.OwnerEpoch-1)
	if err == nil || !strings.Contains(err.Error(), "replication_policy_delete_authority_epoch_mismatch") {
		t.Fatalf("stale cleanup error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("cleanup attempts = %d, want a retry after quiescing and none after the refusal", attempts)
	}
	var retained clusterModels.ReplicationPolicy
	if err := follower.db.First(&retained, policy.ID).Error; err != nil {
		t.Fatalf("stale cleanup removed the policy: %v", err)
	}
}

func TestIntegrationDeleteCleanupCleansTheOwnerFirst(t *testing.T) {
	for _, ownerRefuses := range []bool{false, true} {
		t.Run(fmt.Sprintf("owner refuses=%t", ownerRefuses), func(t *testing.T) {
			fx := SetupZeltaClusterFixture(t, 3)
			leader := fx.LeaderNode()
			var owner, target *zeltaRaftNode
			for _, node := range fx.Nodes {
				switch {
				case node == leader:
				case owner == nil:
					owner = node
				default:
					target = node
				}
			}
			var mu sync.Mutex
			var steps []string
			step := func(s string) {
				mu.Lock()
				defer mu.Unlock()
				steps = append(steps, s)
			}
			for _, node := range fx.Nodes {
				cluster.OnNode(node.cService, "replication-delete-cleanup",
					func(context.Context, string, transitionRequest) (struct{}, error) {
						step("start " + node.id)
						if node == owner {
							// Long enough for a concurrent cleanup to start elsewhere.
							time.Sleep(200 * time.Millisecond)
							if ownerRefuses {
								return struct{}{}, errors.New("replication_policy_delete_authority_epoch_mismatch")
							}
						}
						step("end " + node.id)
						return struct{}{}, nil
					})
			}
			fx.routeCalls(nil)
			policy := clusterModels.ReplicationPolicy{
				ID: 191, Name: "deleting", GuestType: clusterModels.ReplicationGuestTypeVM, GuestID: 191,
				SourceNodeID: owner.id, ActiveNodeID: owner.id, OwnerEpoch: 2,
				ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
				Targets:         []clusterModels.ReplicationPolicyTarget{{NodeID: target.id}},
			}
			if err := leader.db.Create(&policy).Error; err != nil {
				t.Fatalf("seed policy: %v", err)
			}

			service := &Service{DB: leader.db, Cluster: leader.cService}
			err := service.CleanupReplicationPolicyDeleteBestEffort(t.Context(), policy.ID)
			if ownerRefuses {
				if err == nil || !strings.Contains(err.Error(), owner.id) || len(steps) != 1 {
					t.Fatalf("owner refusal: error %v, steps %v", err, steps)
				}
				return
			}
			if err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			if len(steps) != 6 || steps[0] != "start "+owner.id || steps[1] != "end "+owner.id {
				t.Fatalf("steps %v, want the owner's cleanup done before any other starts", steps)
			}
			for _, node := range []*zeltaRaftNode{leader, target} {
				if !slices.Contains(steps, "end "+node.id) {
					t.Fatalf("%s not cleaned: steps %v", node.id, steps)
				}
			}
		})
	}
}

func TestIntegrationTransitionCallsReachTheTarget(t *testing.T) {
	fx, leader, follower := newCallsFixture(t)
	var called []string
	fx.routeCalls(func(_, _ *zeltaRaftNode, name string, _ []byte) error {
		called = append(called, name)
		return nil
	})
	service := &Service{DB: leader.db, Cluster: leader.cService}
	for name, call := range map[string]func() error{
		"replication-run": func() error {
			return service.enqueueReplicationValidationRun(t.Context(), 999, follower.id)
		},
		"replication-activate": func() error {
			return service.forwardActivateReplicationPolicy(follower.id, 999, 1, "run-1", nil)
		},
		"replication-demote": func() error {
			return service.forwardDemoteReplicationPolicy(follower.id, 999, 1, "run-1")
		},
		"replication-catchup": func() error {
			return service.forwardCatchupReplicationPolicy(t.Context(), follower.id, 999, leader.id, 1, "run-1", "gen-1")
		},
		"replication-runtime-state": func() error {
			_, err := service.forwardReplicationPolicyRuntimeState(follower.id, 999, 1, "run-1")
			return err
		},
	} {
		called = nil
		err := call()
		if err == nil || strings.Contains(err.Error(), "call_unknown") || len(called) == 0 || called[0] != name {
			t.Fatalf("%s: calls %v, error %v", name, called, err)
		}
	}
}
