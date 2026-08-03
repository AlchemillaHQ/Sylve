// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestForcedPromotionObservationRequiresContinuousLocalWait(t *testing.T) {
	start := time.Date(2045, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeReplicationRuntimeClock{now: start}
	service := &Service{}
	service.setReplicationRuntimeClock(clock)

	decision := service.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
	if decision.Ready || decision.Remaining != replicationForcedPromotionWait {
		t.Fatalf("first observation = %+v", decision)
	}
	for range 10 {
		clock.Sleep(replicationFailoverControllerInterval)
		decision = service.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
	}
	if decision.Ready || decision.Remaining != 2*time.Second {
		t.Fatalf("decision after 50 seconds = %+v", decision)
	}
	clock.Sleep(2 * time.Second)
	decision = service.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
	if !decision.Ready || decision.Remaining != 0 {
		t.Fatalf("decision after full wait = %+v", decision)
	}
}

func TestForcedPromotionObservationResetsUnsafeContinuity(t *testing.T) {
	tests := []struct {
		name         string
		advance      time.Duration
		owner        string
		epoch        uint64
		term         string
		leaseVersion uint64
		wantReason   string
	}{
		{name: "lease version", advance: 5 * time.Second, owner: "owner-a", epoch: 7, term: "term-1", leaseVersion: 12, wantReason: "lease_version_changed"},
		{name: "leader term", advance: 5 * time.Second, owner: "owner-a", epoch: 7, term: "term-2", leaseVersion: 11, wantReason: "leader_term_changed"},
		{name: "owner epoch", advance: 5 * time.Second, owner: "owner-a", epoch: 8, term: "term-1", leaseVersion: 11, wantReason: "owner_identity_changed"},
		{name: "observation gap", advance: replicationForcedPromotionMaxObservationGap + time.Second, owner: "owner-a", epoch: 7, term: "term-1", leaseVersion: 11, wantReason: "observation_gap"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fakeReplicationRuntimeClock{now: time.Date(2045, time.January, 1, 0, 0, 0, 0, time.UTC)}
			service := &Service{}
			service.setReplicationRuntimeClock(clock)
			service.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
			clock.Sleep(test.advance)

			decision := service.recordForcedPromotionObservation(
				1, test.owner, test.epoch, test.term, test.leaseVersion,
			)
			if decision.Ready || decision.Remaining != replicationForcedPromotionWait || decision.Reason != test.wantReason {
				t.Fatalf("reset decision = %+v", decision)
			}
		})
	}
}

func TestForcedPromotionObservationDoesNotSurviveServiceRestart(t *testing.T) {
	start := time.Date(2045, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeReplicationRuntimeClock{now: start}
	first := &Service{}
	first.setReplicationRuntimeClock(clock)
	first.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
	clock.Sleep(50 * time.Second)

	restarted := &Service{}
	restarted.setReplicationRuntimeClock(clock)
	decision := restarted.recordForcedPromotionObservation(1, "owner-a", 7, "term-1", 11)
	if decision.Ready || decision.Remaining != replicationForcedPromotionWait ||
		decision.Reason != "first_owner_unreachable_observation" {
		t.Fatalf("restarted service decision = %+v", decision)
	}
}

func TestAutoForceControllerResetsWaitAfterOwnerRecovery(t *testing.T) {
	fx := SetupZeltaClusterFixture(t, 3)
	defer fx.Cleanup()

	start := time.Date(2045, time.January, 1, 0, 0, 0, 0, time.UTC)
	verifiedAt := start
	readyUntil := start.Add(time.Hour)
	policy := &clusterModels.ReplicationPolicy{
		ID: 7301, Name: "auto-force-barrier", GuestType: clusterModels.ReplicationGuestTypeVM,
		GuestID: 7301, SourceNodeID: "node-2", ActiveNodeID: "node-2", OwnerEpoch: 1,
		SourceMode:   clusterModels.ReplicationSourceModeFollowActive,
		FailoverMode: clusterModels.ReplicationFailoverAutoForce,
		Enabled:      true, CronExpr: "*/5 * * * *",
		Targets: []clusterModels.ReplicationPolicyTarget{
			{NodeID: fx.LocalNodeID, Weight: 200, Ready: true, GenerationID: "generation-a", OwnerEpoch: 1, ManifestHash: "manifest-a", RequiredDatasetCount: 1, CompletedDatasetCount: 1, LastVerifiedAt: &verifiedAt, ReadyUntil: &readyUntil},
			{NodeID: "node-3", Weight: 100, Ready: true, GenerationID: "generation-b", OwnerEpoch: 1, ManifestHash: "manifest-b", RequiredDatasetCount: 1, CompletedDatasetCount: 1, LastVerifiedAt: &verifiedAt, ReadyUntil: &readyUntil},
		},
	}
	fx.SeedPolicy(policy)
	fx.SeedLease(&clusterModels.ReplicationLease{
		PolicyID: policy.ID, GuestType: policy.GuestType, GuestID: policy.GuestID,
		OwnerNodeID: "node-2", OwnerEpoch: 1, Version: 41, ExpiresAt: start.Add(24 * time.Hour),
	})
	service := fx.NewZeltaService()
	clock := &fakeReplicationRuntimeClock{now: start}
	service.setReplicationRuntimeClock(clock)
	defer service.replicationCountersDelete(policy.ID)

	fx.SetNodeStatus("node-2", "offline")
	for range 11 {
		if err := service.runFailoverControllerTick(t.Context()); err != nil {
			t.Fatal(err)
		}
		clock.Sleep(replicationFailoverControllerInterval)
	}
	var current clusterModels.ReplicationPolicy
	if err := fx.DB.First(&current, policy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if transitionStateInProgress(current.TransitionState) {
		t.Fatalf("auto_force transitioned before barrier: %q", current.TransitionState)
	}

	fx.SetNodeStatus("node-2", "online")
	if err := service.runFailoverControllerTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	service.forcedPromotionMu.Lock()
	_, tracked := service.forcedPromotions[policy.ID]
	service.forcedPromotionMu.Unlock()
	if tracked {
		t.Fatal("owner recovery did not reset forced-promotion wait")
	}

	fx.SetNodeStatus("node-2", "offline")
	if err := service.runFailoverControllerTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	service.forcedPromotionMu.Lock()
	observation := service.forcedPromotions[policy.ID]
	service.forcedPromotionMu.Unlock()
	if !observation.firstObservedAt.Equal(clock.Now()) {
		t.Fatalf("new outage started at %s, want %s", observation.firstObservedAt, clock.Now())
	}
	for range 10 {
		clock.Sleep(replicationFailoverControllerInterval)
		if err := service.runFailoverControllerTick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	clock.Sleep(2 * time.Second)
	if err := service.runFailoverControllerTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fx.DB.First(&current, policy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(current.TransitionRunID) == "" {
		t.Fatal("auto_force did not begin a transition after the full fencing wait")
	}
}
