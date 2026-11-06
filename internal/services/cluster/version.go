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
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/pkg/utils"
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

// checkUniformVersionsLocked compares every member's version with ours.
func (s *Service) checkUniformVersionsLocked(ctx context.Context, candidate *raft.Server, exemptNodeID string) error {
	expected, results, err := s.probeClusterVersions(ctx, candidate, exemptNodeID)
	if err != nil {
		return err
	}
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

type clusterVersionProbeResult struct {
	nodeID  string
	version string
	err     error
}

func (s *Service) probeClusterVersions(
	ctx context.Context,
	candidate *raft.Server,
	exemptNodeID string,
) (string, []clusterVersionProbeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.Raft() == nil || s.Raft().State() == raft.Shutdown {
		return "", nil, &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: fmt.Errorf("raft_unavailable")}
	}
	future := s.Raft().GetConfiguration()
	if err := future.Error(); err != nil {
		return "", nil, &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: err}
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
		return "", nil, &ClusterVersionError{
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
			return "", nil, &ClusterVersionError{Code: "cluster_version_check_unavailable", Cause: fmt.Errorf("raft_member_id_empty")}
		}
		if _, exists := seen[nodeID]; exists {
			return "", nil, &ClusterVersionError{
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
		return expected, nil, nil
	}

	results := make([]clusterVersionProbeResult, len(remote))
	var wg sync.WaitGroup
	for index, server := range remote {
		results[index].nodeID = strings.TrimSpace(string(server.ID))
		index, server := index, server
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, clusterVersionProbeTimeout)
			defer cancel()
			results[index].version, results[index].err = s.fetchMemberVersion(probeCtx, server)
		}()
	}
	wg.Wait()
	return expected, results, nil
}

func (s *Service) fetchMemberVersion(ctx context.Context, server raft.Server) (string, error) {
	if s.joinVersionForNode != nil {
		return s.joinVersionForNode(ctx, server)
	}
	host := strings.TrimSpace(raftAddressHost(string(server.Address)))
	if host == "" {
		return "", fmt.Errorf("member_address_invalid")
	}
	body, statusCode, err := utils.HTTPGetJSONReadContext(
		ctx,
		fmt.Sprintf("https://%s/api/health/basic", ClusterAPIHost(host)),
		map[string]string{"Accept": "application/json"},
	)
	if err != nil {
		return "", fmt.Errorf("member_health_failed: node_id=%s status=%d: %w", strings.TrimSpace(string(server.ID)), statusCode, err)
	}
	var response internal.APIResponse[struct {
		SylveVersion string `json:"sylveVersion"`
	}]
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("member_health_decode_failed: %w", err)
	}
	version := strings.TrimSpace(response.Data.SylveVersion)
	if response.Status != "success" || version == "" {
		return "", fmt.Errorf("member_version_unavailable: message=%s error=%s", response.Message, response.Error)
	}
	return version, nil
}
