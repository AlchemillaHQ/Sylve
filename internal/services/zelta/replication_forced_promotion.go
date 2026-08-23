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
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	replicationForcedPromotionWait              = replicationLeaseTTL + replicationSelfFenceInterval + replicationLeaseExpirySafetyMargin
	replicationForcedPromotionMaxObservationGap = 2 * replicationFailoverControllerInterval
)

type replicationForcedPromotionObservation struct {
	ownerNodeID     string
	ownerEpoch      uint64
	leaderTerm      string
	leaseVersion    uint64
	firstObservedAt time.Time
	lastObservedAt  time.Time
}

type replicationForcedPromotionDecision struct {
	Ready        bool
	LeaseVersion uint64
	Remaining    time.Duration
	Reason       string
}

func (s *Service) resetForcedPromotionObservation(policyID uint) {
	if s == nil || policyID == 0 {
		return
	}
	s.forcedPromotionMu.Lock()
	delete(s.forcedPromotions, policyID)
	s.forcedPromotionMu.Unlock()
}

func (s *Service) resetForcedPromotionObservations() {
	if s == nil {
		return
	}
	s.forcedPromotionMu.Lock()
	s.forcedPromotions = make(map[uint]replicationForcedPromotionObservation)
	s.forcedPromotionMu.Unlock()
}

func (s *Service) recordForcedPromotionObservation(
	policyID uint,
	ownerNodeID string,
	ownerEpoch uint64,
	leaderTerm string,
	leaseVersion uint64,
) replicationForcedPromotionDecision {
	now := s.now()
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	leaderTerm = strings.TrimSpace(leaderTerm)

	s.forcedPromotionMu.Lock()
	defer s.forcedPromotionMu.Unlock()
	if s.forcedPromotions == nil {
		s.forcedPromotions = make(map[uint]replicationForcedPromotionObservation)
	}

	observation, found := s.forcedPromotions[policyID]
	reason := "continuous_owner_unreachable"
	switch {
	case !found:
		reason = "first_owner_unreachable_observation"
	case observation.ownerNodeID != ownerNodeID || observation.ownerEpoch != ownerEpoch:
		reason = "owner_identity_changed"
	case observation.leaderTerm != leaderTerm:
		reason = "leader_term_changed"
	case observation.leaseVersion != leaseVersion:
		reason = "lease_version_changed"
	case now.Before(observation.lastObservedAt) || now.Sub(observation.lastObservedAt) > replicationForcedPromotionMaxObservationGap:
		reason = "observation_gap"
	default:
		observation.lastObservedAt = now
		s.forcedPromotions[policyID] = observation
		remaining := replicationForcedPromotionWait - now.Sub(observation.firstObservedAt)
		if remaining < 0 {
			remaining = 0
		}
		return replicationForcedPromotionDecision{
			Ready: remaining == 0, LeaseVersion: leaseVersion, Remaining: remaining, Reason: reason,
		}
	}

	observation = replicationForcedPromotionObservation{
		ownerNodeID: ownerNodeID, ownerEpoch: ownerEpoch, leaderTerm: leaderTerm,
		leaseVersion: leaseVersion, firstObservedAt: now, lastObservedAt: now,
	}
	s.forcedPromotions[policyID] = observation
	return replicationForcedPromotionDecision{
		LeaseVersion: leaseVersion, Remaining: replicationForcedPromotionWait, Reason: reason,
	}
}

func (s *Service) forcedPromotionLeaderTerm() (string, error) {
	if s == nil || s.Cluster == nil || s.Cluster.Raft == nil || s.Cluster.Raft.State() != raft.Leader {
		return "", fmt.Errorf("not_leader")
	}
	term := strings.TrimSpace(s.Cluster.Raft.Stats()["term"])
	if term == "" {
		return "", fmt.Errorf("raft_leader_term_unavailable")
	}
	return term, nil
}

func (s *Service) forcedPromotionLeaseVersion(policyID uint, ownerNodeID string, ownerEpoch uint64) (uint64, error) {
	lease, err := s.Cluster.GetReplicationLeaseByPolicyID(policyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("replication_previous_owner_lease_lookup_failed: %w", err)
	}
	if lease == nil {
		return 0, nil
	}
	leaseOwner := strings.TrimSpace(lease.OwnerNodeID)
	if lease.OwnerEpoch > ownerEpoch {
		return 0, fmt.Errorf("replication_previous_owner_lease_epoch_advanced")
	}
	if lease.OwnerEpoch == ownerEpoch && leaseOwner != strings.TrimSpace(ownerNodeID) {
		return 0, fmt.Errorf("replication_previous_owner_lease_owner_changed")
	}
	return lease.Version, nil
}

func (s *Service) observeForcedPromotion(
	policyID uint,
	ownerNodeID string,
	ownerEpoch uint64,
	nodeByID map[string]clusterModels.ClusterNode,
) (replicationForcedPromotionDecision, error) {
	if policyID == 0 || strings.TrimSpace(ownerNodeID) == "" || ownerEpoch == 0 {
		return replicationForcedPromotionDecision{}, fmt.Errorf("replication_force_promotion_identity_invalid")
	}
	if nodeOnlineByID(nodeByID, ownerNodeID) {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, fmt.Errorf("replication_force_promotion_owner_reachable")
	}
	leaderTerm, err := s.forcedPromotionLeaderTerm()
	if err != nil {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, err
	}
	leaseVersion, err := s.forcedPromotionLeaseVersion(policyID, ownerNodeID, ownerEpoch)
	if err != nil {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, err
	}
	return s.recordForcedPromotionObservation(policyID, ownerNodeID, ownerEpoch, leaderTerm, leaseVersion), nil
}

func (s *Service) logForcedPromotionDecision(
	policyID uint,
	ownerNodeID string,
	decision replicationForcedPromotionDecision,
) {
	logger.L.Debug().
		Uint("policy_id", policyID).
		Str("owner_node_id", strings.TrimSpace(ownerNodeID)).
		Uint64("lease_version", decision.LeaseVersion).
		Int64("remaining_authority_ms", decision.Remaining.Milliseconds()).
		Str("wait_reason", decision.Reason).
		Msg("replication_force_promotion_waiting_for_owner_fence")
}

func (s *Service) evaluateForcedPromotionBarrier(
	policyID uint,
	ownerNodeID string,
	ownerEpoch uint64,
) (replicationForcedPromotionDecision, map[string]clusterModels.ClusterNode, error) {
	nodes, err := s.Cluster.Nodes()
	if err != nil {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, nil,
			fmt.Errorf("replication_force_promotion_node_revalidation_failed: %w", err)
	}
	nodeByID := make(map[string]clusterModels.ClusterNode, len(nodes))
	for _, node := range nodes {
		nodeByID[strings.TrimSpace(node.NodeUUID)] = node
	}
	quorumOK, quorumErr := s.hasFailoverQuorum(nodeByID)
	if quorumErr != nil {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, nil,
			fmt.Errorf("replication_force_promotion_quorum_revalidation_failed: %w", quorumErr)
	}
	if !quorumOK {
		s.resetForcedPromotionObservation(policyID)
		return replicationForcedPromotionDecision{}, nil, fmt.Errorf("force_failover_requires_quorum")
	}
	decision, err := s.observeForcedPromotion(policyID, ownerNodeID, ownerEpoch, nodeByID)
	return decision, nodeByID, err
}

func (s *Service) revalidateForcedPromotion(
	policyID uint,
	previousOwner string,
	expectedOwnerEpoch uint64,
	targetNodeID string,
	transition *clusterModels.ReplicationPolicyTransition,
) (uint64, error) {
	decision, nodeByID, err := s.evaluateForcedPromotionBarrier(
		policyID, previousOwner, expectedOwnerEpoch,
	)
	if err != nil {
		return 0, err
	}
	if !decision.Ready {
		s.logForcedPromotionDecision(policyID, previousOwner, decision)
		return 0, fmt.Errorf("replication_force_promotion_barrier_not_elapsed")
	}
	if !nodeOnlineByID(nodeByID, targetNodeID) {
		return 0, fmt.Errorf("replication_target_node_offline")
	}

	policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
	if err != nil {
		return 0, err
	}
	if policy == nil || !policy.Enabled ||
		replicationPolicyOwnerNode(policy) != strings.TrimSpace(previousOwner) ||
		replicationPolicyOwnerEpoch(policy) != expectedOwnerEpoch {
		return 0, fmt.Errorf("replication_ownership_changed_before_force_cutover")
	}
	transitionState := strings.ToLower(strings.TrimSpace(policy.TransitionState))
	if transition == nil ||
		!transition.AllowUnsafe ||
		(transitionState != clusterModels.ReplicationTransitionStateDemoting &&
			transitionState != clusterModels.ReplicationTransitionStateCatchup) ||
		strings.TrimSpace(policy.TransitionRunID) != strings.TrimSpace(transition.RunID) ||
		!policy.TransitionAllowUnsafe ||
		strings.TrimSpace(transition.SourceNodeID) != strings.TrimSpace(previousOwner) ||
		strings.TrimSpace(transition.TargetNodeID) != strings.TrimSpace(targetNodeID) ||
		transition.OwnerEpoch != expectedOwnerEpoch ||
		strings.TrimSpace(policy.TransitionSourceNodeID) != strings.TrimSpace(previousOwner) ||
		strings.TrimSpace(policy.TransitionTargetNodeID) != strings.TrimSpace(targetNodeID) ||
		policy.TransitionOwnerEpoch != expectedOwnerEpoch {
		return 0, fmt.Errorf("replication_force_transition_identity_changed")
	}
	if strings.TrimSpace(policy.TransitionGenerationID) != strings.TrimSpace(transition.GenerationID) ||
		policy.TransitionGenerationOwnerEpoch != transition.GenerationOwnerEpoch ||
		strings.TrimSpace(policy.TransitionGenerationManifest) != strings.TrimSpace(transition.GenerationManifest) ||
		policy.TransitionGenerationRootCount != transition.GenerationRootCount {
		return 0, fmt.Errorf("replication_transition_target_generation_changed")
	}
	if err := s.validateUnsafeFailoverTargetGeneration(policy, targetNodeID); err != nil {
		return 0, err
	}
	if err := bindReplicationTransitionGenerationEvidence(policy, targetNodeID, transition, false); err != nil {
		return 0, err
	}

	logger.L.Info().
		Uint("policy_id", policyID).
		Str("owner_node_id", strings.TrimSpace(previousOwner)).
		Str("target_node_id", strings.TrimSpace(targetNodeID)).
		Uint64("owner_epoch", expectedOwnerEpoch).
		Uint64("lease_version", decision.LeaseVersion).
		Str("generation_id", strings.TrimSpace(transition.GenerationID)).
		Msg("replication_force_promotion_barrier_satisfied")
	return decision.LeaseVersion, nil
}
