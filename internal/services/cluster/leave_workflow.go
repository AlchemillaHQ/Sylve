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
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"github.com/hashicorp/raft"
)

const (
	leaveRequestTimeout    = 45 * time.Second
	leaveControlTimeout    = 6 * time.Second
	leaveLeaderWaitTimeout = 10 * time.Second
	leaveDrainTimeout      = 10 * time.Second
)

type StartLeaveRequest struct {
	LeaveID        string `json:"leaveId"`
	ExpectedNodeID string `json:"expectedNodeId"`
	LeaderIP       string `json:"leaderIp"`
}

type ClusterLeaveResult struct {
	Status              ClusterLeaveStatus `json:"status"`
	MembershipRemoved   bool               `json:"membershipRemoved"`
	CleanupAcknowledged bool               `json:"cleanupAcknowledged"`
}

type ClusterLeaveError struct {
	Code     string
	Conflict *PeerRemovalConflict
	Cause    error
}

func (e *ClusterLeaveError) Error() string {
	if e == nil {
		return "cluster_leave_failed"
	}
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *ClusterLeaveError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (s *Service) prepareLocalLeave(
	ctx context.Context,
	leaveID string,
	leaderIP string,
	allowGuests bool,
	requireEnabled bool,
) (GuestIdentityInventoryReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := uuid.Parse(strings.TrimSpace(leaveID)); err != nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("cluster_leave_id_invalid")
	}
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	status, err := s.LeaveStatus()
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	if status.Phase != "" {
		s.mutationGate.Close()
		if status.LeaveID != strings.TrimSpace(leaveID) {
			return GuestIdentityInventoryReport{}, fmt.Errorf("cluster_leave_already_in_progress")
		}
		return s.LocalLeavePreflight(ctx, allowGuests)
	}
	if !status.Enabled {
		if requireEnabled {
			return GuestIdentityInventoryReport{}, fmt.Errorf("cluster_not_enabled")
		}
		record, err := s.loadClusterRecord()
		if err != nil {
			return GuestIdentityInventoryReport{}, err
		}
		pendingJoin := strings.TrimSpace(record.JoinNodeID) != "" || strings.TrimSpace(record.JoinPhase) != ""
		if !pendingJoin {
			return GuestIdentityInventoryReport{}, fmt.Errorf("cluster_not_enabled")
		}
	}
	drainCtx, cancel := context.WithTimeout(ctx, leaveDrainTimeout)
	err = s.DrainMutations(drainCtx)
	cancel()
	if err != nil {
		_ = s.ReopenMutations()
		return GuestIdentityInventoryReport{}, &ClusterLeaveError{
			Code:  "cluster_leave_active_mutations",
			Cause: err,
		}
	}

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	reopen := true
	defer func() {
		if reopen {
			_ = s.ReopenMutations()
		}
	}()

	status, err = s.LeaveStatus()
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	if status.Phase != "" {
		if status.LeaveID != strings.TrimSpace(leaveID) {
			reopen = false
			s.mutationGate.Close()
			return GuestIdentityInventoryReport{}, fmt.Errorf("cluster_leave_already_in_progress")
		}
		reopen = false
		return s.LocalLeavePreflight(ctx, allowGuests)
	}
	report, err := s.LocalLeavePreflight(ctx, allowGuests)
	if err != nil {
		return report, err
	}
	peerAddresses, err := s.captureLeavePeerAddresses()
	if err != nil {
		return report, err
	}
	if err := s.persistLeaveIntent(leaveID, leaderIP, LeavePhaseFenced, peerAddresses); err != nil {
		return report, err
	}
	s.leaveComplete.Store(false)
	reopen = false
	return report, nil
}

func (s *Service) StartCooperativeLeave(
	ctx context.Context,
	request StartLeaveRequest,
	issuerNodeID string,
) (ClusterLeaveResult, error) {
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	if strings.TrimSpace(request.ExpectedNodeID) != localNodeID {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_leave_target_mismatch")
	}
	issuerNodeID = strings.TrimSpace(issuerNodeID)
	if issuerNodeID == "" || issuerNodeID == localNodeID {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_leave_issuer_invalid")
	}
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_leave_raft_unavailable")
	}
	leaderAddress, leaderID := s.Raft.LeaderWithID()
	if strings.TrimSpace(string(leaderID)) != issuerNodeID {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_leave_issuer_not_leader")
	}
	membership, err := s.ResolveCurrentRaftMember(issuerNodeID)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	leaderIP := strings.TrimSpace(request.LeaderIP)
	if leaderIP == "" || leaderIP != strings.TrimSpace(raftAddressHost(membership.Address)) ||
		leaderIP != strings.TrimSpace(raftAddressHost(string(leaderAddress))) {
		return ClusterLeaveResult{}, fmt.Errorf("cluster_leave_leader_address_mismatch")
	}

	status, err := s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if status.Phase == "" {
		if err := s.CheckUniformVersions(ctx, nil, ""); err != nil {
			return ClusterLeaveResult{}, err
		}
		if _, err := s.LocalLeavePreflight(ctx, false); err != nil {
			return ClusterLeaveResult{}, err
		}
	}
	if _, err := s.prepareLocalLeave(ctx, request.LeaveID, leaderIP, false, true); err != nil {
		return ClusterLeaveResult{}, err
	}
	return s.advanceLocalLeave(ctx, true)
}

func (s *Service) LeaveCluster(ctx context.Context) (ClusterLeaveResult, error) {
	status, err := s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if status.Phase != "" {
		return s.advanceLocalLeave(ctx, true)
	}
	record, err := s.loadClusterRecord()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	pendingJoin := strings.TrimSpace(record.JoinNodeID) != "" || strings.TrimSpace(record.JoinPhase) != ""
	if !status.Enabled && !pendingJoin {
		return ClusterLeaveResult{Status: status, CleanupAcknowledged: true}, nil
	}
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	leaveID := uuid.NewString()

	if s.Raft == nil || s.Raft.State() == raft.Shutdown || s.Raft.Leader() == "" {
		if _, err := s.LocalLeavePreflight(ctx, false); err != nil {
			return ClusterLeaveResult{}, err
		}
		leaderIP := strings.TrimSpace(record.JoinLeaderIP)
		if _, err := s.prepareLocalLeave(ctx, leaveID, leaderIP, false, false); err != nil {
			return ClusterLeaveResult{}, err
		}
		return s.advanceLocalLeave(ctx, true)
	}

	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return ClusterLeaveResult{}, fmt.Errorf("failed_to_get_raft_configuration: %w", err)
	}
	configuration := future.Configuration()
	if s.Raft.State() == raft.Leader {
		if len(configuration.Servers) == 1 && strings.TrimSpace(string(configuration.Servers[0].ID)) == localNodeID {
			if _, err := s.LocalLeavePreflight(ctx, true); err != nil {
				return ClusterLeaveResult{}, err
			}
			if _, err := s.prepareLocalLeave(ctx, leaveID, "", true, false); err != nil {
				return ClusterLeaveResult{}, err
			}
			if err := s.updateLeavePhase(LeavePhaseCleaning, nil); err != nil {
				return ClusterLeaveResult{}, err
			}
			return s.advanceLocalLeave(ctx, true)
		}
		if _, err := s.LocalLeavePreflight(ctx, false); err != nil {
			return ClusterLeaveResult{}, err
		}
		leaderIP, err := s.transferLeadershipForLeave(ctx, configuration, localNodeID)
		if err != nil {
			return ClusterLeaveResult{}, err
		}
		if _, err := s.LocalLeavePreflight(ctx, false); err != nil {
			return ClusterLeaveResult{}, err
		}
		if _, err := s.prepareLocalLeave(ctx, leaveID, leaderIP, false, false); err != nil {
			return ClusterLeaveResult{}, err
		}
		return s.advanceLocalLeave(ctx, true)
	}

	if err := s.CheckUniformVersions(ctx, nil, ""); err != nil {
		return ClusterLeaveResult{}, err
	}
	if _, err := s.LocalLeavePreflight(ctx, false); err != nil {
		return ClusterLeaveResult{}, err
	}
	leaderIP := strings.TrimSpace(raftAddressHost(string(s.Raft.Leader())))
	if _, err := s.prepareLocalLeave(ctx, leaveID, leaderIP, false, false); err != nil {
		return ClusterLeaveResult{}, err
	}
	return s.advanceLocalLeave(ctx, true)
}

func (s *Service) transferLeadershipForLeave(
	ctx context.Context,
	configuration raft.Configuration,
	localNodeID string,
) (string, error) {
	candidates := make([]raft.Server, 0)
	for _, server := range configuration.Servers {
		if strings.TrimSpace(string(server.ID)) == localNodeID {
			continue
		}
		if server.Suffrage == raft.Voter {
			candidates = append(candidates, server)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("cluster_leave_leadership_transfer_unavailable")
	}
	sort.Slice(candidates, func(i, j int) bool { return string(candidates[i].ID) < string(candidates[j].ID) })
	target := candidates[0]

	s.clusterJoinMu.Lock()
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		s.clusterJoinMu.Unlock()
		return "", fmt.Errorf("not_leader")
	}
	if err := s.checkUniformVersionsLocked(ctx, nil, ""); err != nil {
		s.clusterJoinMu.Unlock()
		return "", err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		s.clusterJoinMu.Unlock()
		return "", fmt.Errorf("cluster_leave_leader_barrier_failed: %w", err)
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(localNodeID)
	if err != nil {
		s.clusterJoinMu.Unlock()
		return "", err
	}
	if len(dependencies) != 0 {
		s.clusterJoinMu.Unlock()
		return "", &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       localNodeID,
			Dependencies: dependencies,
		}}
	}
	err = s.Raft.LeadershipTransferToServer(target.ID, target.Address).Error()
	s.clusterJoinMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("cluster_leave_leadership_transfer_failed: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, leaveLeaderWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		leaderAddress, leaderID := s.Raft.LeaderWithID()
		if leaderID == target.ID && leaderAddress == target.Address {
			return strings.TrimSpace(raftAddressHost(string(target.Address))), nil
		}
		select {
		case <-waitCtx.Done():
			return "", fmt.Errorf("cluster_leave_leadership_transfer_unconfirmed: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) advanceLocalLeave(ctx context.Context, notify bool) (ClusterLeaveResult, error) {
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	status, err := s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if status.Phase == "" {
		return ClusterLeaveResult{Status: status, CleanupAcknowledged: !status.Enabled}, nil
	}
	if status.Phase == LeavePhaseCleaning {
		return s.finishLocalLeave(notify)
	}
	if status.Phase == LeavePhaseFenced {
		if err := s.updateLeavePhase(LeavePhaseRemoving, nil); err != nil {
			return ClusterLeaveResult{Status: status}, err
		}
		status.Phase = LeavePhaseRemoving
	}

	record, err := s.loadClusterRecord()
	if err != nil {
		return ClusterLeaveResult{Status: status}, err
	}
	if s.Raft != nil && s.Raft.State() == raft.Leader {
		future := s.Raft.GetConfiguration()
		if err := future.Error(); err != nil {
			_ = s.updateLeavePhase(LeavePhaseRemoving, err)
			return ClusterLeaveResult{Status: status}, err
		}
		leaderIP, err := s.transferLeadershipForLeave(ctx, future.Configuration(), status.LocalNodeID)
		if err != nil {
			_ = s.updateLeavePhase(LeavePhaseRemoving, err)
			return ClusterLeaveResult{Status: status}, err
		}
		record.LeaveLeaderIP = leaderIP
		if err := s.DB.Model(&clusterModels.Cluster{}).
			Where("id = ?", record.ID).
			Update("leave_leader_ip", leaderIP).Error; err != nil {
			_ = s.updateLeavePhase(LeavePhaseRemoving, err)
			return ClusterLeaveResult{Status: status}, err
		}
	}
	membership, authorityErr := s.leaveMembership(ctx, record, status.LocalNodeID)
	if authorityErr == nil && !membership.Present {
		if err := s.updateLeavePhase(LeavePhaseCleaning, nil); err != nil {
			return ClusterLeaveResult{Status: status, MembershipRemoved: true}, err
		}
		return s.finishLocalLeave(notify)
	}
	if authorityErr == nil {
		record.LeaveLeaderIP = strings.TrimSpace(raftAddressHost(membership.LeaderAddress))
	}

	report, preflightErr := s.LocalLeavePreflight(ctx, false)
	if preflightErr != nil {
		_ = s.updateLeavePhase(LeavePhaseRemoving, preflightErr)
		return ClusterLeaveResult{Status: status}, preflightErr
	}
	leaderIP := strings.TrimSpace(record.LeaveLeaderIP)
	if leaderIP == "" && authorityErr == nil {
		leaderIP = strings.TrimSpace(raftAddressHost(membership.LeaderAddress))
	}
	if leaderIP == "" {
		err := &ClusterLeaveError{Code: "cluster_leave_membership_unconfirmed", Cause: authorityErr}
		_ = s.updateLeavePhase(LeavePhaseRemoving, err)
		return ClusterLeaveResult{Status: status}, err
	}

	removeErr := s.removeLeaveMembership(ctx, leaderIP, RemoveMembershipRequest{
		LeaveID:   status.LeaveID,
		NodeID:    status.LocalNodeID,
		Inventory: report,
	})
	if removeErr == nil {
		if err := s.updateLeavePhase(LeavePhaseCleaning, nil); err != nil {
			return ClusterLeaveResult{Status: status, MembershipRemoved: true}, err
		}
		return s.finishLocalLeave(notify)
	}

	membership, confirmationErr := s.leaveMembership(ctx, record, status.LocalNodeID)
	if confirmationErr == nil && !membership.Present {
		if err := s.updateLeavePhase(LeavePhaseCleaning, nil); err != nil {
			return ClusterLeaveResult{Status: status, MembershipRemoved: true}, err
		}
		return s.finishLocalLeave(notify)
	}
	var blocked *PeerRemovalBlockedError
	var versionErr *ClusterVersionError
	if confirmationErr == nil && membership.Present &&
		(errors.As(removeErr, &blocked) || errors.As(removeErr, &versionErr)) {
		if err := s.clearLeaveIntentAndReopen(); err != nil {
			return ClusterLeaveResult{Status: status}, err
		}
		return ClusterLeaveResult{}, removeErr
	}
	if confirmationErr != nil {
		removeErr = errors.Join(removeErr, confirmationErr)
	}
	uncertain := &ClusterLeaveError{Code: "cluster_leave_membership_unconfirmed", Cause: removeErr}
	_ = s.updateLeavePhase(LeavePhaseRemoving, uncertain)
	status, _ = s.LeaveStatus()
	return ClusterLeaveResult{Status: status}, uncertain
}

func (s *Service) finishLocalLeave(notify bool) (ClusterLeaveResult, error) {
	if err := s.FinalizeLocalDecluster(); err != nil {
		_ = s.updateLeavePhase(LeavePhaseCleaning, err)
		status, _ := s.LeaveStatus()
		return ClusterLeaveResult{Status: status, MembershipRemoved: true}, err
	}
	status, err := s.LeaveStatus()
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	if notify {
		s.notifyLeaveComplete()
	}
	return ClusterLeaveResult{
		Status:              status,
		MembershipRemoved:   true,
		CleanupAcknowledged: true,
	}, nil
}

func (s *Service) loadClusterRecord() (clusterModels.Cluster, error) {
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return record, err
	}
	return record, nil
}

func (s *Service) submitSelfRemoval(
	ctx context.Context,
	leaderIP string,
	request RemoveMembershipRequest,
) error {
	token, err := s.AuthService.CreateInternalClusterJWT(request.NodeID)
	if err != nil {
		return fmt.Errorf("cluster_leave_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/remove-peer", ClusterAPIHost(leaderIP)),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": "Bearer " + token,
		},
		leaveControlTimeout,
		4<<20,
	)
	if err != nil {
		return fmt.Errorf("cluster_leave_remove_request_failed: %w", err)
	}
	var apiResponse internal.APIResponse[PeerRemovalConflict]
	if len(response.Body) != 0 {
		if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
			return fmt.Errorf("cluster_leave_remove_response_invalid: %w", err)
		}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && strings.EqualFold(apiResponse.Status, "success") {
		return nil
	}
	switch apiResponse.Message {
	case "peer_removal_blocked":
		return &PeerRemovalBlockedError{Conflict: apiResponse.Data}
	case "cluster_version_mismatch", "cluster_version_check_unavailable":
		return &ClusterVersionError{Code: apiResponse.Message, Cause: errors.New(apiResponse.Error)}
	default:
		return fmt.Errorf(
			"cluster_leave_remove_rejected: status=%d message=%s error=%s",
			response.StatusCode,
			apiResponse.Message,
			apiResponse.Error,
		)
	}
}

func (s *Service) leaveMembership(
	ctx context.Context,
	record clusterModels.Cluster,
	nodeID string,
) (MembershipStatus, error) {
	if s.leaveMembershipForNode != nil {
		return s.leaveMembershipForNode(ctx, record, nodeID)
	}
	return s.discoverAuthoritativeMembership(ctx, record, nodeID)
}

func (s *Service) removeLeaveMembership(
	ctx context.Context,
	leaderIP string,
	request RemoveMembershipRequest,
) error {
	if s.leaveRemovalForNode != nil {
		return s.leaveRemovalForNode(ctx, leaderIP, request)
	}
	return s.submitSelfRemoval(ctx, leaderIP, request)
}

func (s *Service) discoverAuthoritativeMembership(
	ctx context.Context,
	record clusterModels.Cluster,
	nodeID string,
) (MembershipStatus, error) {
	seeds := s.leaveDiscoverySeeds(record)
	if len(seeds) == 0 {
		return MembershipStatus{NodeID: nodeID}, fmt.Errorf("cluster_leave_no_discovery_peers")
	}
	seen := make(map[string]struct{}, len(seeds))
	var lastErr error
	for index := 0; index < len(seeds); index++ {
		host := normalizeLeaveSeed(seeds[index])
		if host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		status, err := s.queryMembershipStatus(ctx, host, record.Key, nodeID)
		if err == nil {
			return status, nil
		}
		lastErr = err
		if status.LeaderAddress != "" {
			seeds = append(seeds, status.LeaderAddress)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("cluster_leave_membership_unavailable")
	}
	return MembershipStatus{NodeID: nodeID}, lastErr
}

func (s *Service) leaveDiscoverySeeds(record clusterModels.Cluster) []string {
	seeds := make([]string, 0)
	if record.LeaveLeaderIP != "" {
		seeds = append(seeds, record.LeaveLeaderIP)
	}
	if record.JoinLeaderIP != "" {
		seeds = append(seeds, record.JoinLeaderIP)
	}
	if s.Raft != nil && s.Raft.State() != raft.Shutdown {
		if leader := strings.TrimSpace(string(s.Raft.Leader())); leader != "" {
			seeds = append(seeds, leader)
		}
		if future := s.Raft.GetConfiguration(); future.Error() == nil {
			for _, server := range future.Configuration().Servers {
				seeds = append(seeds, string(server.Address))
			}
		}
	}
	var persisted []string
	if json.Unmarshal(record.LeavePeerAddrs, &persisted) == nil {
		seeds = append(seeds, persisted...)
	}
	if s.DB.Migrator().HasTable(&clusterModels.ClusterNode{}) {
		var nodes []clusterModels.ClusterNode
		if s.DB.Select("api").Find(&nodes).Error == nil {
			for _, node := range nodes {
				seeds = append(seeds, node.API)
			}
		}
	}
	return seeds
}

func normalizeLeaveSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(seed); err == nil {
		return strings.TrimSpace(host)
	}
	return strings.Trim(seed, "[]")
}

func (s *Service) queryMembershipStatus(
	ctx context.Context,
	host string,
	clusterKey string,
	nodeID string,
) (MembershipStatus, error) {
	result := MembershipStatus{NodeID: nodeID}
	payload, err := json.Marshal(struct {
		NodeID string `json:"nodeId"`
	}{NodeID: nodeID})
	if err != nil {
		return result, err
	}
	response, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/cluster/membership-status", ClusterAPIHost(host)),
		payload,
		map[string]string{
			"Accept":              "application/json",
			"Content-Type":        "application/json",
			auth.ClusterKeyHeader: strings.TrimSpace(clusterKey),
		},
		leaveControlTimeout,
		1<<20,
	)
	if err != nil {
		return result, err
	}
	var apiResponse internal.APIResponse[MembershipStatus]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return result, fmt.Errorf("cluster_membership_status_decode_failed: %w", err)
	}
	result = apiResponse.Data
	if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.EqualFold(apiResponse.Status, "success") {
		return result, fmt.Errorf(
			"cluster_membership_status_rejected: status=%d message=%s error=%s",
			response.StatusCode,
			apiResponse.Message,
			apiResponse.Error,
		)
	}
	if strings.TrimSpace(result.NodeID) != strings.TrimSpace(nodeID) ||
		strings.TrimSpace(result.LeaderID) == "" || strings.TrimSpace(result.LeaderAddress) == "" {
		return result, fmt.Errorf("cluster_membership_status_invalid")
	}
	return result, nil
}

func (s *Service) OrchestratePeerRemoval(ctx context.Context, nodeID string) (ClusterLeaveResult, error) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	defer release()
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return ClusterLeaveResult{}, fmt.Errorf("peer_node_id_required")
	}
	localNodeID := strings.TrimSpace(s.LocalNodeID())
	if nodeID == localNodeID {
		return ClusterLeaveResult{}, fmt.Errorf("peer_removal_target_is_local")
	}

	s.clusterJoinMu.Lock()
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, fmt.Errorf("not_leader")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, err
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, err
	}
	if !present {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, fmt.Errorf("peer_not_found")
	}
	if err := s.checkUniformVersionsLocked(ctx, nil, ""); err != nil {
		s.clusterJoinMu.Unlock()
		var versionErr *ClusterVersionError
		if errors.As(err, &versionErr) && versionErr.NodeID == nodeID &&
			versionErr.Code == "cluster_version_check_unavailable" {
			return ClusterLeaveResult{}, &ClusterLeaveError{Code: "cluster_target_unreachable", Cause: err}
		}
		return ClusterLeaveResult{}, err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, err
	}
	dependencies, err := s.replicatedPeerRemovalDependencies(nodeID)
	if err != nil {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, err
	}
	if len(dependencies) != 0 {
		s.clusterJoinMu.Unlock()
		return ClusterLeaveResult{}, &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{
			NodeID:       nodeID,
			Dependencies: dependencies,
		}}
	}
	targetIP := strings.TrimSpace(raftAddressHost(string(server.Address)))
	leaderIP := strings.TrimSpace(raftAddressHost(string(s.Raft.Leader())))
	s.clusterJoinMu.Unlock()

	request := StartLeaveRequest{
		LeaveID:        uuid.NewString(),
		ExpectedNodeID: nodeID,
		LeaderIP:       leaderIP,
	}
	token, err := s.AuthService.CreateInternalClusterJWT(localNodeID)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return ClusterLeaveResult{}, err
	}
	response, requestErr := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/leave", ClusterAPIHost(targetIP)),
		payload,
		map[string]string{
			"Accept":          "application/json",
			"Content-Type":    "application/json",
			"X-Cluster-Token": "Bearer " + token,
		},
		leaveRequestTimeout,
		4<<20,
	)
	if requestErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		var apiResponse internal.APIResponse[ClusterLeaveResult]
		if err := json.Unmarshal(response.Body, &apiResponse); err == nil &&
			strings.EqualFold(apiResponse.Status, "success") {
			_ = s.ClearClusterNode(nodeID)
			return apiResponse.Data, nil
		}
		requestErr = fmt.Errorf("cluster_leave_target_response_invalid")
	}
	if requestErr == nil {
		var apiResponse internal.APIResponse[PeerRemovalConflict]
		if json.Unmarshal(response.Body, &apiResponse) == nil {
			switch apiResponse.Message {
			case "peer_removal_blocked":
				return ClusterLeaveResult{}, &PeerRemovalBlockedError{Conflict: apiResponse.Data}
			case "cluster_version_mismatch", "cluster_version_check_unavailable":
				return ClusterLeaveResult{}, &ClusterVersionError{Code: apiResponse.Message, Cause: errors.New(apiResponse.Error)}
			case "cluster_leave_active_mutations":
				return ClusterLeaveResult{}, &ClusterLeaveError{
					Code:  apiResponse.Message,
					Cause: errors.New(apiResponse.Error),
				}
			}
		}
		requestErr = fmt.Errorf("cluster_leave_target_rejected: status=%d", response.StatusCode)
	}

	membership, authorityErr := s.AuthoritativeMembershipStatus(nodeID)
	if authorityErr == nil && !membership.Present {
		_ = s.ClearClusterNode(nodeID)
		return ClusterLeaveResult{MembershipRemoved: true}, &ClusterLeaveError{
			Code:  "cluster_removal_cleanup_unconfirmed",
			Cause: requestErr,
		}
	}
	if authorityErr != nil {
		requestErr = errors.Join(requestErr, authorityErr)
	}
	return ClusterLeaveResult{}, &ClusterLeaveError{
		Code:  "cluster_removal_start_uncertain",
		Cause: requestErr,
	}
}
