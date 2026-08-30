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
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
)

const clusterVersionProbeTimeout = 5 * time.Second

type ClusterVersionError struct {
	Code     string
	NodeID   string
	Expected string
	Actual   string
	Cause    error
}

func (e *ClusterVersionError) Error() string {
	if e == nil {
		return "cluster_version_check_failed"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: node_id=%s: %v", e.Code, e.NodeID, e.Cause)
	}
	return fmt.Sprintf(
		"%s: node_id=%s expected=%s actual=%s",
		e.Code,
		e.NodeID,
		e.Expected,
		e.Actual,
	)
}

func (e *ClusterVersionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (s *Service) CheckUniformVersions(ctx context.Context, candidate *raft.Server, exemptNodeID string) error {
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	return s.checkUniformVersionsLocked(ctx, candidate, exemptNodeID)
}

func (s *Service) checkUniformVersionsLocked(ctx context.Context, candidate *raft.Server, exemptNodeID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: fmt.Errorf("raft_unavailable")}
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: err}
	}
	servers := append([]raft.Server(nil), future.Configuration().Servers...)
	if candidate != nil {
		candidateID := strings.TrimSpace(string(candidate.ID))
		found := false
		for _, server := range servers {
			if strings.TrimSpace(string(server.ID)) == candidateID {
				found = true
				break
			}
		}
		if !found {
			servers = append(servers, *candidate)
		}
	}
	sort.Slice(servers, func(i, j int) bool {
		return strings.TrimSpace(string(servers[i].ID)) < strings.TrimSpace(string(servers[j].ID))
	})

	expected := strings.TrimSpace(cmd.Version)
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	if expected == "" {
		return &ClusterVersionError{
			Code:   "cluster_version_mismatch",
			NodeID: localNodeID,
			Cause:  fmt.Errorf("local_version_unavailable"),
		}
	}
	seen := make(map[string]struct{}, len(servers))
	remote := make([]raft.Server, 0, len(servers))
	for _, server := range servers {
		nodeID := strings.TrimSpace(string(server.ID))
		if nodeID == "" {
			return &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: fmt.Errorf("raft_member_id_empty")}
		}
		if _, exists := seen[nodeID]; exists {
			return &ClusterVersionError{
				Code:   "cluster_version_check_unavailable",
				NodeID: nodeID,
				Cause:  fmt.Errorf("duplicate_raft_member"),
			}
		}
		seen[nodeID] = struct{}{}
		if nodeID == strings.TrimSpace(exemptNodeID) || nodeID == localNodeID {
			continue
		}
		remote = append(remote, server)
	}
	if len(remote) == 0 {
		return nil
	}

	clusterKey := ""
	if s.DB != nil {
		var record clusterModels.Cluster
		if err := s.DB.First(&record).Error; err == nil {
			clusterKey = strings.TrimSpace(record.Key)
		} else if s.joinVersionForNode == nil {
			return &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: err}
		}
	}
	if clusterKey == "" && s.joinVersionForNode == nil {
		return &ClusterVersionError{
			Code:  "cluster_version_check_unavailable",
			Cause: fmt.Errorf("cluster_key_unavailable"),
		}
	}

	type result struct {
		nodeID  string
		version string
		err     error
	}
	results := make([]result, len(remote))
	var wg sync.WaitGroup
	for index, server := range remote {
		index, server := index, server
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, clusterVersionProbeTimeout)
			defer cancel()
			results[index].nodeID = strings.TrimSpace(string(server.ID))
			results[index].version, results[index].err = s.fetchJoinerVersion(probeCtx, server, clusterKey)
		}()
	}
	wg.Wait()

	for _, result := range results {
		if result.err != nil {
			return &ClusterVersionError{
				Code:   "cluster_version_check_unavailable",
				NodeID: result.nodeID,
				Cause:  result.err,
			}
		}
		actual := strings.TrimSpace(result.version)
		if actual == "" || actual != expected {
			return &ClusterVersionError{
				Code:     "cluster_version_mismatch",
				NodeID:   result.nodeID,
				Expected: expected,
				Actual:   actual,
			}
		}
	}
	return nil
}
