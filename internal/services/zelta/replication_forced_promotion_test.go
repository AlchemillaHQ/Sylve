// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"testing"
	"time"
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
