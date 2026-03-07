// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

const (
	ReplicationHAReasonMinThreeVoters            = "cluster_requires_min_three_voters"
	ReplicationHAReasonMinTwoTargets             = "policy_requires_min_two_targets"
	ReplicationHAReasonRemoteTarget              = "policy_requires_remote_target"
	ReplicationHAReasonTransitionRemoteTarget    = "transition_would_remove_remote_target"
	ReplicationHAReasonReducedRedundancy         = "reduced_redundancy_one_voter_down"
	ReplicationHAReasonQuorumLost                = "quorum_lost"
	replicationHAIneligibleErrorPrefix           = "replication_policy_ha_ineligible"
	replicationHAStaticReasonsKeyTransition      = ReplicationHAReasonTransitionRemoteTarget
	replicationHARemoteReasonDefault             = ReplicationHAReasonRemoteTarget
	replicationHARemoteReasonTransitionProjected = ReplicationHAReasonTransitionRemoteTarget
)

var replicationHAStaticReasonSet = map[string]struct{}{
	ReplicationHAReasonMinThreeVoters:       {},
	ReplicationHAReasonMinTwoTargets:        {},
	ReplicationHAReasonRemoteTarget:         {},
	replicationHAStaticReasonsKeyTransition: {},
}

type ReplicationHAIneligibleError struct {
	Reasons []string
}

func (e *ReplicationHAIneligibleError) Error() string {
	reasons := normalizeReplicationHAReasons(e.Reasons)
	if len(reasons) == 0 {
		return replicationHAIneligibleErrorPrefix
	}
	return fmt.Sprintf("%s: %s", replicationHAIneligibleErrorPrefix, strings.Join(reasons, ","))
}

type ReplicationPolicyHAEvaluation struct {
	Eligible            bool
	Degraded            bool
	Reasons             []string
	EffectiveRunner     string
	DistinctTargetCount int
	RemoteTargetCount   int
	TotalVoters         int
	OnlineVoters        int
	QuorumRequired      int
}

type ReplicationPolicyHAEvalOptions struct {
	TargetsOverride      []clusterModels.ReplicationPolicyTarget
	SourceNodeIDOverride string
	ActiveNodeIDOverride string
	RemoteTargetReason   string
	SkipRuntimeChecks    bool
	RuntimeSnapshot      *replicationHARuntimeSnapshot
}

type replicationHARuntimeSnapshot struct {
	TotalVoters     int
	OnlineVoters    int
	QuorumRequired  int
	LeaderHealthy   bool
	QuorumAvailable bool
}

func NewReplicationHAIneligibleError(reasons []string) error {
	normalized := normalizeReplicationHAReasons(reasons)
	if len(normalized) == 0 {
		return nil
	}
	return &ReplicationHAIneligibleError{Reasons: normalized}
}

func ParseReplicationHAIneligibleReasons(err error) []string {
	if err == nil {
		return nil
	}

	var typedErr *ReplicationHAIneligibleError
	if errors.As(err, &typedErr) {
		return normalizeReplicationHAReasons(typedErr.Reasons)
	}

	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return nil
	}
	lowerMsg := strings.ToLower(msg)
	prefix := replicationHAIneligibleErrorPrefix + ":"
	idx := strings.Index(lowerMsg, prefix)
	if idx < 0 {
		if lowerMsg == replicationHAIneligibleErrorPrefix {
			return nil
		}
		return nil
	}

	reasonPart := strings.TrimSpace(msg[idx+len(prefix):])
	if reasonPart == "" {
		return nil
	}
	return normalizeReplicationHAReasons(strings.Split(reasonPart, ","))
}

func IsReplicationHAStaticIneligibleError(err error) bool {
	reasons := ParseReplicationHAIneligibleReasons(err)
	if len(reasons) == 0 {
		return false
	}
	for _, reason := range reasons {
		if _, ok := replicationHAStaticReasonSet[reason]; ok {
			return true
		}
	}
	return false
}

func ReplicationHAReasonSetIncludes(reasons []string, reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	for _, candidate := range reasons {
		if strings.TrimSpace(candidate) == reason {
			return true
		}
	}
	return false
}

func (s *Service) ApplyReplicationPolicyHAState(policy *clusterModels.ReplicationPolicy, eval ReplicationPolicyHAEvaluation) {
	if policy == nil {
		return
	}
	policy.HAEligible = eval.Eligible
	policy.HADegraded = eval.Degraded
	policy.HAReasons = append([]string{}, eval.Reasons...)
	if policy.HAReasons == nil {
		policy.HAReasons = []string{}
	}
}

func (s *Service) EvaluateReplicationPolicyHA(policy *clusterModels.ReplicationPolicy) ReplicationPolicyHAEvaluation {
	return s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{})
}

func (s *Service) EvaluateReplicationPolicyHAWithTargets(
	policy *clusterModels.ReplicationPolicy,
	targets []clusterModels.ReplicationPolicyTarget,
) ReplicationPolicyHAEvaluation {
	return s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
		TargetsOverride: targets,
	})
}

func (s *Service) EvaluateReplicationPolicyTransitionHA(
	policy *clusterModels.ReplicationPolicy,
	projectedSourceNodeID string,
	projectedActiveNodeID string,
) ReplicationPolicyHAEvaluation {
	return s.evaluateReplicationPolicyHA(policy, ReplicationPolicyHAEvalOptions{
		SourceNodeIDOverride: strings.TrimSpace(projectedSourceNodeID),
		ActiveNodeIDOverride: strings.TrimSpace(projectedActiveNodeID),
		RemoteTargetReason:   replicationHARemoteReasonTransitionProjected,
	})
}

func (s *Service) evaluateReplicationPolicyHA(
	policy *clusterModels.ReplicationPolicy,
	opts ReplicationPolicyHAEvalOptions,
) ReplicationPolicyHAEvaluation {
	eval := ReplicationPolicyHAEvaluation{
		Eligible: true,
		Reasons:  []string{},
	}
	if policy == nil {
		eval.Eligible = false
		eval.Reasons = []string{ReplicationHAReasonRemoteTarget}
		return eval
	}

	targets := opts.TargetsOverride
	if len(targets) == 0 {
		targets = policy.Targets
	}

	sourceNodeID := strings.TrimSpace(policy.SourceNodeID)
	if strings.TrimSpace(opts.SourceNodeIDOverride) != "" {
		sourceNodeID = strings.TrimSpace(opts.SourceNodeIDOverride)
	}
	activeNodeID := strings.TrimSpace(policy.ActiveNodeID)
	if strings.TrimSpace(opts.ActiveNodeIDOverride) != "" {
		activeNodeID = strings.TrimSpace(opts.ActiveNodeIDOverride)
	}

	effectiveRunner := replicationPolicyEffectiveRunner(strings.TrimSpace(policy.SourceMode), sourceNodeID, activeNodeID)
	eval.EffectiveRunner = effectiveRunner

	remoteReason := strings.TrimSpace(opts.RemoteTargetReason)
	if remoteReason == "" {
		remoteReason = replicationHARemoteReasonDefault
	}

	targetSet := make(map[string]struct{}, len(targets))
	remoteTargetCount := 0
	for _, target := range targets {
		nodeID := strings.TrimSpace(target.NodeID)
		if nodeID == "" {
			continue
		}
		targetSet[nodeID] = struct{}{}
		if effectiveRunner == "" || nodeID != effectiveRunner {
			remoteTargetCount++
		}
	}

	eval.DistinctTargetCount = len(targetSet)
	eval.RemoteTargetCount = remoteTargetCount

	blockingReasons := make([]string, 0, 4)
	informationalReasons := make([]string, 0, 1)

	runtime := opts.RuntimeSnapshot
	if runtime == nil {
		snapshot := s.buildReplicationHARuntimeSnapshot()
		runtime = &snapshot
	}

	eval.TotalVoters = runtime.TotalVoters
	eval.OnlineVoters = runtime.OnlineVoters
	eval.QuorumRequired = runtime.QuorumRequired

	if runtime.TotalVoters < 3 {
		blockingReasons = appendReasonIfMissing(blockingReasons, ReplicationHAReasonMinThreeVoters)
	}
	if eval.DistinctTargetCount < 2 {
		blockingReasons = appendReasonIfMissing(blockingReasons, ReplicationHAReasonMinTwoTargets)
	}
	if eval.RemoteTargetCount < 1 {
		blockingReasons = appendReasonIfMissing(blockingReasons, remoteReason)
	}

	if !opts.SkipRuntimeChecks {
		if !runtime.QuorumAvailable {
			blockingReasons = appendReasonIfMissing(blockingReasons, ReplicationHAReasonQuorumLost)
		} else if runtime.OnlineVoters < runtime.TotalVoters {
			eval.Degraded = true
			informationalReasons = appendReasonIfMissing(informationalReasons, ReplicationHAReasonReducedRedundancy)
		}
	}

	eval.Reasons = append(eval.Reasons, blockingReasons...)
	eval.Reasons = append(eval.Reasons, informationalReasons...)
	eval.Eligible = len(blockingReasons) == 0
	return eval
}

func (s *Service) buildReplicationHARuntimeSnapshot() replicationHARuntimeSnapshot {
	snapshot := replicationHARuntimeSnapshot{
		TotalVoters:     0,
		OnlineVoters:    0,
		QuorumRequired:  0,
		LeaderHealthy:   false,
		QuorumAvailable: false,
	}

	if s == nil || s.Raft == nil {
		return snapshot
	}

	cfgFuture := s.Raft.GetConfiguration()
	if err := cfgFuture.Error(); err != nil {
		return snapshot
	}

	voterIDs := make([]string, 0)
	for _, server := range cfgFuture.Configuration().Servers {
		if server.Suffrage != raft.Voter {
			continue
		}
		nodeID := strings.TrimSpace(string(server.ID))
		if nodeID == "" {
			continue
		}
		voterIDs = append(voterIDs, nodeID)
	}

	sort.Strings(voterIDs)
	snapshot.TotalVoters = len(voterIDs)
	if snapshot.TotalVoters == 0 {
		return snapshot
	}

	statusByNodeID := make(map[string]string, snapshot.TotalVoters)
	nodes, nodesErr := s.Nodes()
	if nodesErr == nil {
		for _, node := range nodes {
			statusByNodeID[strings.TrimSpace(node.NodeUUID)] = strings.ToLower(strings.TrimSpace(node.Status))
		}
	}

	localNodeID := strings.TrimSpace(s.LocalNodeID())
	for _, voterID := range voterIDs {
		if voterID == localNodeID {
			snapshot.OnlineVoters++
			continue
		}
		if statusByNodeID[voterID] == "online" {
			snapshot.OnlineVoters++
		}
	}

	snapshot.QuorumRequired = (snapshot.TotalVoters / 2) + 1
	if snapshot.QuorumRequired <= 0 {
		snapshot.QuorumRequired = 1
	}

	leaderAddress, leaderID := s.Raft.LeaderWithID()
	leaderNodeID := strings.TrimSpace(string(leaderID))
	leaderAddressStr := strings.TrimSpace(string(leaderAddress))
	snapshot.LeaderHealthy = leaderNodeID != "" || leaderAddressStr != ""
	if snapshot.LeaderHealthy {
		if s.Raft.State() == raft.Leader {
			if err := s.Raft.VerifyLeader().Error(); err != nil {
				snapshot.LeaderHealthy = false
			}
		}
	}

	if snapshot.LeaderHealthy && leaderNodeID != "" && leaderNodeID != localNodeID {
		leaderStatus, ok := statusByNodeID[leaderNodeID]
		if !ok || leaderStatus != "online" {
			snapshot.LeaderHealthy = false
		}
	}

	snapshot.QuorumAvailable = snapshot.LeaderHealthy && snapshot.OnlineVoters >= snapshot.QuorumRequired
	return snapshot
}

func replicationPolicyEffectiveRunner(sourceMode, sourceNodeID, activeNodeID string) string {
	mode := strings.ToLower(strings.TrimSpace(sourceMode))
	sourceNodeID = strings.TrimSpace(sourceNodeID)
	activeNodeID = strings.TrimSpace(activeNodeID)
	if mode == clusterModels.ReplicationSourceModePinned {
		return sourceNodeID
	}
	if activeNodeID != "" {
		return activeNodeID
	}
	return sourceNodeID
}

func appendReasonIfMissing(reasons []string, reason string) []string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reasons
	}
	for _, existing := range reasons {
		if strings.TrimSpace(existing) == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func normalizeReplicationHAReasons(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	seen := map[string]struct{}{}
	for _, reason := range reasons {
		value := strings.TrimSpace(reason)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
