// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
)

const (
	joinReconcileInterval = 3 * time.Second
	nonvoterAbandonAfter  = 10 * time.Minute
)

type nonvoterProgress struct {
	appliedIndex uint64
	since        time.Time
}

func (s *Service) reconcileJoins(ctx context.Context, now time.Time) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return
	}
	defer release()
	s.membershipLifecycleMu.Lock()
	err = s.advanceJoin(ctx)
	s.membershipLifecycleMu.Unlock()
	if err != nil {
		logger.L.Debug().Err(err).Msg("cluster_join_retry_deferred")
	}
	s.promoteNonvoters(ctx, now)
}

func (s *Service) promoteNonvoters(ctx context.Context, now time.Time) {
	r := s.Raft()
	if r == nil || r.State() != raft.Leader {
		s.joinMu.Lock()
		s.nonvoterProgress = nil
		s.joinMu.Unlock()
		return
	}
	future := r.GetConfiguration()
	if err := future.Error(); err != nil {
		return
	}
	var nonvoters []raft.Server
	for _, server := range future.Configuration().Servers {
		if server.Suffrage == raft.Nonvoter {
			nonvoters = append(nonvoters, server)
		}
	}
	s.joinMu.Lock()
	tracked := make(map[string]nonvoterProgress, len(nonvoters))
	for _, server := range nonvoters {
		if progress, seen := s.nonvoterProgress[string(server.ID)]; seen {
			tracked[string(server.ID)] = progress
		}
	}
	s.nonvoterProgress = tracked
	s.joinMu.Unlock()

	for _, server := range nonvoters {
		nodeID := strings.TrimSpace(string(server.ID))
		target := r.CommitIndex()
		progress, err := s.fetchJoinProgress(ctx, server)
		if err == nil && progress.AppliedIndex >= target {
			if err := s.promoteNonvoter(server); err != nil {
				logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_join_promotion_failed")
				continue
			}
			logger.L.Info().Str("node_id", nodeID).Uint64("applied_index", progress.AppliedIndex).Msg("cluster_join_promoted_voter")
			if err := s.PopulateClusterNodes(); err != nil {
				logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_join")
			}
			continue
		}
		s.joinMu.Lock()
		last, seen := s.nonvoterProgress[nodeID]
		if !seen || err == nil && progress.AppliedIndex > last.appliedIndex {
			last = nonvoterProgress{appliedIndex: progress.AppliedIndex, since: now}
			s.nonvoterProgress[nodeID] = last
		}
		s.joinMu.Unlock()
		if now.Sub(last.since) < nonvoterAbandonAfter {
			continue
		}
		logger.L.Warn().Err(err).Str("node_id", nodeID).Uint64("applied_index", last.appliedIndex).Msg("cluster_join_abandoned")
		if err := s.RemoveMember(ctx, nodeID, true); err != nil {
			logger.L.Warn().Err(err).Str("node_id", nodeID).Msg("cluster_join_abandoned_removal_failed")
		}
	}
}

func (s *Service) promoteNonvoter(server raft.Server) error {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	r := s.Raft()
	if r == nil || r.State() != raft.Leader {
		return raft.ErrNotLeader
	}
	return r.AddVoter(server.ID, server.Address, 0, raftApplyTimeout).Error()
}

func (s *Service) startJoinReconciler(ctx context.Context) {
	go func() {
		for {
			s.reconcileJoins(ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-time.After(joinReconcileInterval):
			}
		}
	}()
}
