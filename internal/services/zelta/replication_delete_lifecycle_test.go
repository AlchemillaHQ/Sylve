// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

func TestReplicationPolicyDeleteAuthority(t *testing.T) {
	base := clusterModels.ReplicationPolicy{
		ID:              41,
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         9,
		ActiveNodeID:    "node-a",
		OwnerEpoch:      7,
		Enabled:         true,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}

	tests := []struct {
		name          string
		mutate        func(*clusterModels.ReplicationPolicy)
		expectedEpoch uint64
		wantError     string
	}{
		{name: "valid", expectedEpoch: 7},
		{name: "stale epoch", expectedEpoch: 6, wantError: "epoch_mismatch"},
		{
			name:          "not deleting",
			expectedEpoch: 7,
			mutate: func(policy *clusterModels.ReplicationPolicy) {
				policy.ProtectionState = clusterModels.ReplicationProtectionStateArmed
			},
			wantError: "policy_not_deleting",
		},
		{
			name:          "transition active",
			expectedEpoch: 7,
			mutate: func(policy *clusterModels.ReplicationPolicy) {
				policy.TransitionState = clusterModels.ReplicationTransitionStatePromoting
			},
			wantError: "transition_in_progress",
		},
		{
			name:          "owner missing",
			expectedEpoch: 7,
			mutate: func(policy *clusterModels.ReplicationPolicy) {
				policy.ActiveNodeID = ""
				policy.SourceNodeID = ""
			},
			wantError: "owner_missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := base
			if tt.mutate != nil {
				tt.mutate(&policy)
			}
			err := validateReplicationPolicyDeleteAuthority(&policy, tt.expectedEpoch)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestDeletingPolicyCancelsTransferAuthority(t *testing.T) {
	db := newZeltaServiceTestDB(
		t,
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationPolicyTarget{},
	)
	policy := clusterModels.ReplicationPolicy{
		ID:              42,
		Name:            "deleting",
		GuestType:       clusterModels.ReplicationGuestTypeVM,
		GuestID:         10,
		ActiveNodeID:    "node-a",
		OwnerEpoch:      8,
		Enabled:         true,
		ProtectionState: clusterModels.ReplicationProtectionStateDeleting,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	s := newTestZeltaService(db)
	s.Cluster = &clusterService.Service{DB: db}
	_, err := s.validateReplicationTransferAuthority(policy.ID, policy.OwnerEpoch, "")
	if err == nil || !strings.Contains(err.Error(), "replication_policy_deleting") {
		t.Fatalf("deleting did not revoke transfer authority: %v", err)
	}
}

func TestReplicationDeleteCleanupGuardsQuiesceAndRelease(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{
		ID:        43,
		GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID:   11,
	}
	s := newTestZeltaService(nil)

	if !s.acquireReplication(policy.ID) {
		t.Fatal("failed to seed active replication guard")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := s.acquireReplicationDeleteCleanupGuards(cancelled, policy); err == nil ||
		release != nil || !strings.Contains(err.Error(), "delete_cleanup_quiescing") {
		t.Fatalf("expected retriable quiescence error, release=%v err=%v", release != nil, err)
	}
	s.releaseReplication(policy.ID)

	release, err := s.acquireReplicationDeleteCleanupGuards(context.Background(), policy)
	if err != nil {
		t.Fatalf("acquire cleanup guards: %v", err)
	}
	if s.acquireReplication(policy.ID) {
		t.Fatal("cleanup did not hold the replication guard")
	}
	if ok, _ := s.acquireWorkloadOperation(policy.GuestType, policy.GuestID, "competing"); ok {
		t.Fatal("cleanup did not hold the workload guard")
	}
	release()

	if !s.acquireReplication(policy.ID) {
		t.Fatal("replication guard was not released")
	}
	s.releaseReplication(policy.ID)
	if ok, _ := s.acquireWorkloadOperation(policy.GuestType, policy.GuestID, "after_cleanup"); !ok {
		t.Fatal("workload guard was not released")
	}
	s.releaseWorkloadOperation(policy.GuestType, policy.GuestID)
}
