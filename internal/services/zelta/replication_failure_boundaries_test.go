// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
)

type runtimeTruthJailStub struct {
	jailServiceInterfaces.JailServiceInterface
	runningStates []bool
	stateErr      error
	stopErr       error
	stateCalls    int
	stopCalls     int
}

func (s *runtimeTruthJailStub) IsJailRunning(_ uint) (bool, error) {
	s.stateCalls++
	if s.stateErr != nil {
		return false, s.stateErr
	}
	if len(s.runningStates) == 0 {
		return false, nil
	}
	state := s.runningStates[0]
	if len(s.runningStates) > 1 {
		s.runningStates = s.runningStates[1:]
	}
	return state, nil
}

func (s *runtimeTruthJailStub) ForceStopJail(_ uint) error {
	s.stopCalls++
	return s.stopErr
}

func TestStopLocalJailUsesLiveRuntimeState(t *testing.T) {
	jail := &runtimeTruthJailStub{runningStates: []bool{true, false}}
	cleanupCalls := 0
	service := &Service{
		Jail: jail,
		replicationJailMountCleaner: func(context.Context, uint) error {
			cleanupCalls++
			return nil
		},
	}

	if err := service.stopLocalJailIfPresent(t.Context(), 42); err != nil {
		t.Fatalf("stop live jail: %v", err)
	}
	if jail.stopCalls != 1 {
		t.Fatalf("force-stop calls = %d, want 1", jail.stopCalls)
	}
	if jail.stateCalls != 2 {
		t.Fatalf("runtime-state calls = %d, want 2", jail.stateCalls)
	}
	if cleanupCalls != 1 {
		t.Fatalf("mount cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestStopLocalJailCleansMountsWhenAlreadyStopped(t *testing.T) {
	jail := &runtimeTruthJailStub{runningStates: []bool{false}}
	cleanupCalls := 0
	service := &Service{
		Jail: jail,
		replicationJailMountCleaner: func(_ context.Context, ctID uint) error {
			cleanupCalls++
			if ctID != 42 {
				t.Fatalf("mount cleanup CTID = %d, want 42", ctID)
			}
			return nil
		},
	}

	if err := service.stopLocalJailIfPresent(t.Context(), 42); err != nil {
		t.Fatalf("clean mounts for stopped jail: %v", err)
	}
	if jail.stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", jail.stopCalls)
	}
	if jail.stateCalls != 1 {
		t.Fatalf("runtime-state calls = %d, want 1", jail.stateCalls)
	}
	if cleanupCalls != 1 {
		t.Fatalf("mount cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestStopLocalJailReturnsMountCleanupFailure(t *testing.T) {
	cleanupErr := errors.New("runtime mount remains busy")
	service := &Service{
		Jail: &runtimeTruthJailStub{runningStates: []bool{false}},
		replicationJailMountCleaner: func(context.Context, uint) error {
			return cleanupErr
		},
	}

	err := service.stopLocalJailIfPresent(t.Context(), 42)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("mount cleanup error = %v, want %v", err, cleanupErr)
	}
}

func TestStopLocalJailFailsWhenRuntimeCannotBeConfirmedStopped(t *testing.T) {
	jail := &runtimeTruthJailStub{runningStates: []bool{true, true}}
	service := &Service{
		Jail: jail,
		replicationJailMountCleaner: func(context.Context, uint) error {
			t.Fatal("mount cleanup ran before runtime stop was confirmed")
			return nil
		},
	}

	err := service.stopLocalJailIfPresent(t.Context(), 42)
	if err == nil || !strings.Contains(err.Error(), "local_jail_still_running_after_stop") {
		t.Fatalf("still-running jail returned %v", err)
	}
}

func TestSelfFenceSurfacesJailMountCleanupFailure(t *testing.T) {
	cleanupErr := errors.New("self-fence runtime mount cleanup failed")
	jail := &runtimeTruthJailStub{}
	cleanupCalls := 0
	service := &Service{
		Jail: jail,
		localFilesystemDatasetLister: func(context.Context) ([]string, error) {
			return nil, nil
		},
		replicationGuestDriverFactory: func(string) (replicationGuestDriver, error) {
			return &disposableReplicationGuestDriver{}, nil
		},
		replicationJailMountCleaner: func(context.Context, uint) error {
			cleanupCalls++
			return cleanupErr
		},
	}
	policy := &clusterModels.ReplicationPolicy{
		ID:        8,
		GuestType: clusterModels.ReplicationGuestTypeJail,
		GuestID:   42,
	}

	err := service.selfFenceReplicationPolicy(
		t.Context(), policy, "node-local", "node-owner", "test", false,
	)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("self-fence error = %v, want %v", err, cleanupErr)
	}
	if cleanupCalls != 1 {
		t.Fatalf("mount cleanup calls = %d, want 1", cleanupCalls)
	}
	if jail.stopCalls != 1 {
		t.Fatalf("emergency stop calls = %d, want 1", jail.stopCalls)
	}
}

func TestPrepareReplicationActivationFailsBeforeMutationWhenVolumeDiscoveryFails(t *testing.T) {
	volumeErr := errors.New("volume inventory unavailable")
	service := &Service{
		localFilesystemDatasetLister: func(context.Context) ([]string, error) {
			return []string{"zroot/sylve/virtual-machines/108"}, nil
		},
		localVolumeDatasetLister: func(context.Context) ([]string, error) {
			return nil, volumeErr
		},
	}

	err := service.prepareReplicatedDatasetForActivation(
		context.Background(),
		"zroot/sylve/virtual-machines/108",
	)
	if !errors.Is(err, volumeErr) ||
		!strings.Contains(err.Error(), "failed_to_list_volumes_for_replication_activation") {
		t.Fatalf("volume discovery failure returned %v", err)
	}
}

func TestReturnedForceFailoverSourceCandidate(t *testing.T) {
	policy := &clusterModels.ReplicationPolicy{
		ID:                     7,
		Enabled:                true,
		ActiveNodeID:           "node-new",
		OwnerEpoch:             3,
		TransitionAllowUnsafe:  true,
		TransitionSourceNodeID: "node-old",
		TransitionTargetNodeID: "node-new",
		TransitionOwnerEpoch:   3,
	}
	if !returnedForceFailoverSource(policy, "node-old", "node-new") {
		t.Fatal("returned force-failover source was not selected for standby adoption")
	}

	policy.TransitionAllowUnsafe = false
	if returnedForceFailoverSource(policy, "node-old", "node-new") {
		t.Fatal("safe transition source was selected for force-failover adoption")
	}
	policy.TransitionAllowUnsafe = true
	if returnedForceFailoverSource(policy, "node-third", "node-new") {
		t.Fatal("unrelated standby node was selected for source adoption")
	}
	if returnedForceFailoverSource(policy, "node-new", "node-new") {
		t.Fatal("current owner was selected for standby adoption")
	}
}

func TestCommittedPromotionKeepsTargetOwnerWhenTargetIsOffline(t *testing.T) {
	db := newZeltaServiceTestDB(t, &clusterModels.ClusterNode{})
	if err := db.Create(&clusterModels.ClusterNode{
		NodeUUID: "node-target",
		Status:   "offline",
	}).Error; err != nil {
		t.Fatalf("create offline target node: %v", err)
	}

	deadline := time.Now().UTC().Add(-time.Minute)
	desiredRunning := true
	policy := &clusterModels.ReplicationPolicy{
		ID:                           11,
		Enabled:                      true,
		GuestType:                    clusterModels.ReplicationGuestTypeVM,
		GuestID:                      108,
		ActiveNodeID:                 "node-target",
		OwnerEpoch:                   4,
		TransitionState:              clusterModels.ReplicationTransitionStatePromoting,
		TransitionRunID:              "run-11",
		TransitionSourceNodeID:       "node-old",
		TransitionTargetNodeID:       "node-target",
		TransitionOwnerEpoch:         4,
		TransitionOriginalRunning:    &desiredRunning,
		TransitionRecoveryDeadlineAt: &deadline,
	}
	service := &Service{Cluster: &clusterService.Service{DB: db, NodeID: "node-leader"}}

	err := service.resumePromotingTransition(context.Background(), policy)
	if err == nil || err.Error() != "replication_target_node_offline" {
		t.Fatalf("offline committed target returned %v", err)
	}
	if policy.ActiveNodeID != "node-target" ||
		policy.TransitionState != clusterModels.ReplicationTransitionStatePromoting ||
		policy.OwnerEpoch != 4 {
		t.Fatalf("committed target ownership changed during recovery: %+v", policy)
	}
}
