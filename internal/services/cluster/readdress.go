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
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/network"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	ReaddressPhasePrepared            = "prepared"
	ReaddressPhaseLocalRebound        = "local_rebound"
	ReaddressPhaseMembershipCommitted = "membership_committed"
)

const (
	readdressControlTimeout = 30 * time.Second
	readdressDrainTimeout   = 10 * time.Second
	readdressLeaderTimeout  = 20 * time.Second
)

type ReaddressRequest struct {
	NewIP           string `json:"newIp"`
	AllowDisruption bool   `json:"allowDisruption"`
}

type RepairAddressRequest struct {
	NodeID          string `json:"nodeId"`
	NewIP           string `json:"newIp"`
	AllowDisruption bool   `json:"allowDisruption"`
}

type MemberAddressChangeRequest struct {
	NodeID          string `json:"nodeId"`
	OldIP           string `json:"oldIp"`
	NewIP           string `json:"newIp"`
	Recovery        bool   `json:"recovery"`
	AllowDisruption bool   `json:"allowDisruption"`
}

type ReaddressResult struct {
	NodeID              string `json:"nodeId"`
	OldIP               string `json:"oldIp"`
	NewIP               string `json:"newIp"`
	Phase               string `json:"phase"`
	MembershipCommitted bool   `json:"membershipCommitted"`
	RestartRequested    bool   `json:"restartRequested"`
}

func validReaddressPhase(phase string) bool {
	switch strings.TrimSpace(phase) {
	case "", ReaddressPhasePrepared, ReaddressPhaseLocalRebound, ReaddressPhaseMembershipCommitted:
		return true
	default:
		return false
	}
}

func normalizeReaddressIP(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return "", fmt.Errorf("cluster_readdress_ip_invalid")
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return "", fmt.Errorf("cluster_readdress_ipv6_unsupported")
	}
	return ipv4.String(), nil
}

func sameReaddressIP(left string, right string) bool {
	leftIP := net.ParseIP(strings.TrimSpace(left))
	rightIP := net.ParseIP(strings.TrimSpace(right))
	return leftIP != nil && rightIP != nil && leftIP.Equal(rightIP)
}

func validateReaddressBind(newIP string) error {
	if !utils.IsLocalIP(newIP) {
		return fmt.Errorf("cluster_readdress_ip_not_local")
	}
	for _, port := range []int{ClusterRaftPort, ClusterEmbeddedSSHPort, ClusterEmbeddedHTTPSPort} {
		if err := network.TryBindToPort(newIP, port, "tcp"); err != nil {
			return fmt.Errorf("cluster_readdress_port_unavailable: address=%s: %w", net.JoinHostPort(newIP, fmt.Sprint(port)), err)
		}
	}
	return nil
}

func (s *Service) InitializeReaddressRuntime() error {
	if s == nil || s.DB == nil || s.mutationGate == nil {
		return fmt.Errorf("cluster_readdress_runtime_unavailable")
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return fmt.Errorf("cluster_readdress_state_load_failed: %w", err)
	}
	phase := strings.TrimSpace(record.ReaddressPhase)
	if !validReaddressPhase(phase) {
		s.mutationGate.Close()
		return fmt.Errorf("cluster_readdress_phase_invalid: %s", phase)
	}
	if phase == "" {
		return nil
	}
	s.mutationGate.Close()
	if strings.TrimSpace(record.LeavePhase) != "" {
		return fmt.Errorf("cluster_readdress_leave_conflict")
	}
	newIP, err := normalizeReaddressIP(record.ReaddressNewIP)
	if err != nil {
		return err
	}
	if phase == ReaddressPhaseMembershipCommitted {
		return s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
			"raft_ip":         newIP,
			"readdress_phase": ReaddressPhaseLocalRebound,
		}).Error
	}
	if phase == ReaddressPhaseLocalRebound && !sameReaddressIP(record.RaftIP, newIP) {
		return fmt.Errorf("cluster_readdress_local_bind_mismatch")
	}
	return nil
}

func (s *Service) ReaddressLocal(ctx context.Context, request ReaddressRequest) (ReaddressResult, error) {
	if !request.AllowDisruption {
		return ReaddressResult{}, fmt.Errorf("cluster_readdress_disruption_acknowledgement_required")
	}
	newIP, err := normalizeReaddressIP(request.NewIP)
	if err != nil {
		return ReaddressResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	record, err := s.loadClusterRecord()
	if err != nil {
		return ReaddressResult{}, err
	}
	if strings.TrimSpace(record.ReaddressPhase) == "" {
		if err := s.preflightLocalReaddress(ctx, record, newIP); err != nil {
			return ReaddressResult{}, err
		}
		drainCtx, cancel := context.WithTimeout(ctx, readdressDrainTimeout)
		err = s.DrainMutations(drainCtx)
		cancel()
		if err != nil {
			_ = s.ReopenMutations()
			return ReaddressResult{}, fmt.Errorf("cluster_readdress_active_mutations: %w", err)
		}
	}

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	record, err = s.loadClusterRecord()
	if err != nil {
		return ReaddressResult{}, err
	}
	if phase := strings.TrimSpace(record.ReaddressPhase); phase != "" {
		if !sameReaddressIP(record.ReaddressNewIP, newIP) {
			return ReaddressResult{}, fmt.Errorf(
				"cluster_readdress_already_active: old_ip=%s new_ip=%s phase=%s",
				record.ReaddressOldIP,
				record.ReaddressNewIP,
				phase,
			)
		}
		s.mutationGate.Close()
		if phase == ReaddressPhasePrepared && !s.localMembershipHasAddress(s.LocalNodeID(), newIP) {
			if err := s.preflightLocalReaddress(ctx, record, newIP); err != nil {
				s.persistReaddressError(err)
				return ReaddressResult{}, err
			}
		}
		return s.advanceLocalReaddress(ctx, record, request.AllowDisruption)
	}
	if err := s.preflightLocalReaddress(ctx, record, newIP); err != nil {
		if strings.TrimSpace(record.LeavePhase) == "" {
			_ = s.ReopenMutations()
		}
		return ReaddressResult{}, err
	}
	if err := s.persistReaddressPrepared(record, newIP); err != nil {
		_ = s.ReopenMutations()
		return ReaddressResult{}, err
	}
	s.readdressRestart.Store(false)
	record, err = s.loadClusterRecord()
	if err != nil {
		return ReaddressResult{}, err
	}
	return s.advanceLocalReaddress(ctx, record, request.AllowDisruption)
}

func (s *Service) preflightLocalReaddress(ctx context.Context, record clusterModels.Cluster, newIP string) error {
	if !record.Enabled {
		return fmt.Errorf("cluster_not_enabled")
	}
	if sameReaddressIP(record.RaftIP, newIP) {
		return fmt.Errorf("cluster_readdress_ip_unchanged")
	}
	if strings.TrimSpace(record.JoinPhase) != "" || strings.TrimSpace(record.JoinNodeID) != "" {
		return fmt.Errorf("cluster_readdress_join_conflict")
	}
	if strings.TrimSpace(record.LeavePhase) != "" {
		return fmt.Errorf("cluster_readdress_leave_conflict")
	}
	if s.stateRepair.Load() {
		return fmt.Errorf("cluster_readdress_state_repair_conflict")
	}
	if err := validateReaddressBind(newIP); err != nil {
		return err
	}
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return fmt.Errorf("cluster_readdress_raft_unavailable")
	}
	if strings.TrimSpace(string(s.Raft.Leader())) == "" {
		return fmt.Errorf("cluster_consensus_unavailable")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	member, present, err := resolveRaftMember(future.Configuration(), s.LocalNodeID())
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("cluster_readdress_local_member_absent")
	}
	if member.Suffrage != raft.Voter {
		return fmt.Errorf("cluster_readdress_local_member_not_voter")
	}
	if strings.TrimSpace(string(member.Address)) != RaftServerAddress(record.RaftIP) {
		return fmt.Errorf("cluster_readdress_local_address_mismatch")
	}
	return s.CheckUniformVersions(ctx, nil, "")
}

func (s *Service) persistReaddressPrepared(record clusterModels.Cluster, newIP string) error {
	return s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"readdress_old_ip":     strings.TrimSpace(record.RaftIP),
		"readdress_new_ip":     newIP,
		"readdress_phase":      ReaddressPhasePrepared,
		"readdress_last_error": "",
	}).Error
}

func (s *Service) advanceLocalReaddress(
	ctx context.Context,
	record clusterModels.Cluster,
	allowDisruption bool,
) (ReaddressResult, error) {
	result := ReaddressResult{
		NodeID: s.LocalNodeID(),
		OldIP:  strings.TrimSpace(record.ReaddressOldIP),
		NewIP:  strings.TrimSpace(record.ReaddressNewIP),
		Phase:  strings.TrimSpace(record.ReaddressPhase),
	}
	switch result.Phase {
	case ReaddressPhaseLocalRebound:
		committed, err := s.completeLocalReaddressIfCommitted()
		result.MembershipCommitted = committed
		if committed {
			result.Phase = ""
		}
		return result, err
	case ReaddressPhaseMembershipCommitted:
		if err := s.persistLocalRebound(record); err != nil {
			return result, err
		}
		result.Phase = ReaddressPhaseLocalRebound
		result.MembershipCommitted = true
		result.RestartRequested = true
		s.notifyReaddressRestart()
		return result, nil
	case ReaddressPhasePrepared:
	default:
		return result, fmt.Errorf("cluster_readdress_phase_invalid: %s", result.Phase)
	}

	if err := s.transferLeadershipForReaddress(ctx); err != nil {
		s.persistReaddressError(err)
		return result, err
	}
	request := MemberAddressChangeRequest{
		NodeID:          result.NodeID,
		OldIP:           result.OldIP,
		NewIP:           result.NewIP,
		AllowDisruption: allowDisruption,
	}
	if err := s.submitMemberAddressChange(ctx, request); err != nil {
		if !s.localMembershipHasAddress(result.NodeID, result.NewIP) {
			s.persistReaddressError(err)
			return result, fmt.Errorf("cluster_readdress_membership_update_uncertain: %w", err)
		}
	}
	if err := s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"readdress_phase":      ReaddressPhaseMembershipCommitted,
		"readdress_last_error": "",
	}).Error; err != nil {
		return result, err
	}
	if err := s.persistLocalRebound(record); err != nil {
		return result, err
	}
	result.Phase = ReaddressPhaseLocalRebound
	result.MembershipCommitted = true
	result.RestartRequested = true
	s.notifyReaddressRestart()
	return result, nil
}

func (s *Service) persistLocalRebound(record clusterModels.Cluster) error {
	newIP, err := normalizeReaddressIP(record.ReaddressNewIP)
	if err != nil {
		return err
	}
	return s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"raft_ip":              newIP,
		"readdress_phase":      ReaddressPhaseLocalRebound,
		"readdress_last_error": "",
	}).Error
}

func (s *Service) persistReaddressError(err error) {
	if s == nil || s.DB == nil || err == nil {
		return
	}
	_ = s.DB.Model(&clusterModels.Cluster{}).Where("readdress_phase <> ''").Update("readdress_last_error", err.Error()).Error
}

func (s *Service) transferLeadershipForReaddress(ctx context.Context) error {
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return nil
	}
	s.clusterJoinMu.Lock()
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		s.clusterJoinMu.Unlock()
		return err
	}
	localID := raft.ServerID(s.LocalNodeID())
	hasCandidate := false
	for _, server := range future.Configuration().Servers {
		if server.ID != localID && server.Suffrage == raft.Voter {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		s.clusterJoinMu.Unlock()
		return nil
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		s.clusterJoinMu.Unlock()
		return fmt.Errorf("cluster_readdress_leader_barrier_failed: %w", err)
	}
	err := s.Raft.LeadershipTransfer().Error()
	s.clusterJoinMu.Unlock()
	if err != nil {
		return fmt.Errorf("cluster_readdress_leadership_transfer_failed: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, raftLeaderWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(raftLeaderPollInterval)
	defer ticker.Stop()
	for {
		_, leaderID := s.Raft.LeaderWithID()
		if leaderID != "" && leaderID != localID {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("cluster_readdress_leadership_transfer_unconfirmed: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) submitMemberAddressChange(ctx context.Context, request MemberAddressChangeRequest) error {
	if s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return fmt.Errorf("cluster_readdress_raft_unavailable")
	}
	if s.Raft.State() == raft.Leader {
		return s.commitMemberAddress(ctx, request, s.LocalNodeID(), false)
	}
	leaderAddress, _ := s.Raft.LeaderWithID()
	leaderIP := raftAddressHost(string(leaderAddress))
	if strings.TrimSpace(leaderIP) == "" {
		return fmt.Errorf("cluster_consensus_unavailable")
	}
	return s.forwardMemberAddressChange(ctx, leaderIP, request)
}

func (s *Service) localMembershipHasAddress(nodeID string, ip string) bool {
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return false
	}
	future := s.Raft.GetConfiguration()
	if future.Error() != nil {
		return false
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	return err == nil && present && strings.TrimSpace(string(server.Address)) == RaftServerAddress(ip)
}

func (s *Service) completeLocalReaddressIfCommitted() (bool, error) {
	record, err := s.loadClusterRecord()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(record.ReaddressPhase) != ReaddressPhaseLocalRebound {
		return strings.TrimSpace(record.ReaddressPhase) == "", nil
	}
	if strings.TrimSpace(record.LeavePhase) != "" {
		return false, fmt.Errorf("cluster_readdress_leave_conflict")
	}
	if !s.localMembershipHasAddress(s.LocalNodeID(), record.ReaddressNewIP) {
		return false, nil
	}
	err = s.DB.Model(&clusterModels.Cluster{}).Where("id = ?", record.ID).Updates(map[string]any{
		"readdress_old_ip":     "",
		"readdress_new_ip":     "",
		"readdress_phase":      "",
		"readdress_last_error": "",
	}).Error
	if err != nil {
		return false, err
	}
	if err := s.ReopenMutations(); err != nil {
		return false, err
	}
	s.EmitLeftPanelRefreshClusterWide("cluster_membership_changed")
	return true, nil
}

func (s *Service) StartReaddressReconciler(ctx context.Context) {
	if s == nil {
		return
	}
	record, err := s.loadClusterRecord()
	if err != nil || strings.TrimSpace(record.ReaddressPhase) == "" {
		return
	}
	s.readdressOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				if !s.reconcileReaddress(ctx) {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (s *Service) reconcileReaddress(ctx context.Context) bool {
	record, err := s.loadClusterRecord()
	if err != nil {
		return true
	}
	switch strings.TrimSpace(record.ReaddressPhase) {
	case ReaddressPhasePrepared:
		_, err = s.ReaddressLocal(ctx, ReaddressRequest{NewIP: record.ReaddressNewIP, AllowDisruption: true})
	case ReaddressPhaseLocalRebound:
		_, err = s.completeLocalReaddressIfCommitted()
	default:
		return false
	}
	if err != nil {
		s.persistReaddressError(err)
	}
	return true
}

func (s *Service) LocalReaddressIdentity() (ReaddressIdentity, error) {
	if s == nil || s.DB == nil {
		return ReaddressIdentity{}, fmt.Errorf("cluster_service_not_initialized")
	}
	record, err := s.loadClusterRecord()
	if err != nil {
		return ReaddressIdentity{}, err
	}
	return ReaddressIdentity{
		NodeID:       strings.TrimSpace(s.LocalNodeID()),
		Enabled:      record.Enabled,
		OldIP:        strings.TrimSpace(record.ReaddressOldIP),
		NewIP:        strings.TrimSpace(record.ReaddressNewIP),
		RaftIP:       strings.TrimSpace(record.RaftIP),
		Phase:        strings.TrimSpace(record.ReaddressPhase),
		SylveVersion: cmd.Version,
	}, nil
}

func (s *Service) fetchReaddressIdentity(
	ctx context.Context,
	nodeID string,
	address raft.ServerAddress,
) (ReaddressIdentity, error) {
	if s.readdressIdentityForNode != nil {
		return s.readdressIdentityForNode(ctx, nodeID, address)
	}
	token, err := s.AuthService.CreateInternalClusterJWT(s.LocalNodeID())
	if err != nil {
		return ReaddressIdentity{}, fmt.Errorf("cluster_readdress_token_failed: %w", err)
	}
	host := raftAddressHost(string(address))
	response, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("https://%s/api/intra-cluster/readdress-identity", ClusterAPIHost(host)),
		nil,
		map[string]string{"Accept": "application/json", "X-Cluster-Token": "Bearer " + token},
		leaveControlTimeout,
		1<<20,
	)
	if err != nil {
		return ReaddressIdentity{}, fmt.Errorf("cluster_readdress_identity_unavailable: %w", err)
	}
	var apiResponse internal.APIResponse[ReaddressIdentity]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return ReaddressIdentity{}, fmt.Errorf("cluster_readdress_identity_invalid: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || apiResponse.Status != "success" {
		return ReaddressIdentity{}, fmt.Errorf("cluster_readdress_identity_rejected: %s", apiResponse.Error)
	}
	return apiResponse.Data, nil
}

func validateRecoveryIdentity(
	identity ReaddressIdentity,
	nodeID string,
	oldIP string,
	newIP string,
) error {
	if !identity.Enabled || strings.TrimSpace(identity.NodeID) != strings.TrimSpace(nodeID) {
		return fmt.Errorf("cluster_readdress_target_identity_mismatch")
	}
	if identity.Phase != ReaddressPhaseLocalRebound ||
		!sameReaddressIP(identity.OldIP, oldIP) ||
		!sameReaddressIP(identity.NewIP, newIP) ||
		!sameReaddressIP(identity.RaftIP, newIP) {
		return fmt.Errorf("cluster_readdress_target_intent_mismatch")
	}
	if strings.TrimSpace(identity.SylveVersion) != cmd.Version {
		return &ClusterVersionError{
			Code: "cluster_version_mismatch", NodeID: nodeID,
			Expected: cmd.Version, Actual: strings.TrimSpace(identity.SylveVersion),
		}
	}
	return nil
}

func (s *Service) RepairMemberAddress(
	ctx context.Context,
	request RepairAddressRequest,
) (ReaddressResult, error) {
	if !request.AllowDisruption {
		return ReaddressResult{}, fmt.Errorf("cluster_readdress_disruption_acknowledgement_required")
	}
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return ReaddressResult{}, err
	}
	defer release()
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()

	nodeID := strings.TrimSpace(request.NodeID)
	if nodeID == "" {
		return ReaddressResult{}, fmt.Errorf("cluster_readdress_node_id_required")
	}
	newIP, err := normalizeReaddressIP(request.NewIP)
	if err != nil {
		return ReaddressResult{}, err
	}
	member, err := s.ResolveCurrentRaftMember(nodeID)
	if err != nil {
		return ReaddressResult{}, err
	}
	oldIP := raftAddressHost(member.Address)
	if sameReaddressIP(oldIP, newIP) {
		return ReaddressResult{
			NodeID: nodeID, OldIP: oldIP, NewIP: newIP, MembershipCommitted: true,
		}, nil
	}
	identity, err := s.fetchReaddressIdentity(ctx, nodeID, raft.ServerAddress(RaftServerAddress(newIP)))
	if err != nil {
		return ReaddressResult{}, err
	}
	if err := validateRecoveryIdentity(identity, nodeID, oldIP, newIP); err != nil {
		return ReaddressResult{}, err
	}
	address := raft.ServerAddress(RaftServerAddress(newIP))
	if err := s.installRaftAddressOverride(raft.ServerID(nodeID), address, request.AllowDisruption); err != nil {
		return ReaddressResult{}, err
	}
	defer s.clearRaftAddressOverride(raft.ServerID(nodeID))

	change := MemberAddressChangeRequest{
		NodeID: nodeID, OldIP: oldIP, NewIP: newIP, Recovery: true, AllowDisruption: request.AllowDisruption,
	}
	leaderID, leaderIP, err := s.waitForReaddressLeader(ctx, nodeID, newIP)
	if err != nil {
		return ReaddressResult{}, err
	}
	if leaderID == s.LocalNodeID() {
		err = s.commitMemberAddress(ctx, change, s.LocalNodeID(), false)
	} else {
		err = s.forwardMemberAddressChange(ctx, leaderIP, change)
	}
	if err != nil {
		return ReaddressResult{}, err
	}
	return ReaddressResult{
		NodeID: nodeID, OldIP: oldIP, NewIP: newIP,
		Phase: ReaddressPhaseMembershipCommitted, MembershipCommitted: true,
	}, nil
}

func (s *Service) waitForReaddressLeader(
	ctx context.Context,
	targetNodeID string,
	targetNewIP string,
) (string, string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, readdressLeaderTimeout)
	defer cancel()
	ticker := time.NewTicker(raftLeaderPollInterval)
	defer ticker.Stop()
	for {
		if s.Raft != nil && s.Raft.State() != raft.Shutdown {
			leaderAddress, leaderID := s.Raft.LeaderWithID()
			id := strings.TrimSpace(string(leaderID))
			if id != "" {
				if id == strings.TrimSpace(targetNodeID) {
					return id, targetNewIP, nil
				}
				if host := strings.TrimSpace(raftAddressHost(string(leaderAddress))); host != "" {
					return id, host, nil
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return "", "", fmt.Errorf("cluster_readdress_leader_unavailable: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) CommitMemberAddress(
	ctx context.Context,
	request MemberAddressChangeRequest,
	issuerNodeID string,
) error {
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	return s.commitMemberAddress(ctx, request, issuerNodeID, request.Recovery)
}

func (s *Service) commitMemberAddress(
	ctx context.Context,
	request MemberAddressChangeRequest,
	issuerNodeID string,
	installOverride bool,
) error {
	if !request.AllowDisruption {
		return fmt.Errorf("cluster_readdress_disruption_acknowledgement_required")
	}
	nodeID := strings.TrimSpace(request.NodeID)
	oldIP, err := normalizeReaddressIP(request.OldIP)
	if err != nil {
		return err
	}
	newIP, err := normalizeReaddressIP(request.NewIP)
	if err != nil {
		return err
	}
	if !request.Recovery && strings.TrimSpace(issuerNodeID) != nodeID {
		return fmt.Errorf("cluster_readdress_issuer_mismatch")
	}
	if s.localMembershipHasAddress(nodeID, newIP) {
		return nil
	}
	if request.Recovery {
		identity, err := s.fetchReaddressIdentity(ctx, nodeID, raft.ServerAddress(RaftServerAddress(newIP)))
		if err != nil {
			return err
		}
		if err := validateRecoveryIdentity(identity, nodeID, oldIP, newIP); err != nil {
			return err
		}
		if installOverride && nodeID != strings.TrimSpace(s.LocalNodeID()) {
			if err := s.installRaftAddressOverride(raft.ServerID(nodeID), raft.ServerAddress(RaftServerAddress(newIP)), true); err != nil {
				return err
			}
			defer s.clearRaftAddressOverride(raft.ServerID(nodeID))
		}
	}

	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft_configuration_unavailable: %w", err)
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("cluster_readdress_member_absent")
	}
	newAddress := raft.ServerAddress(RaftServerAddress(newIP))
	if server.Address == newAddress {
		return nil
	}
	if server.Suffrage != raft.Voter || strings.TrimSpace(string(server.Address)) != RaftServerAddress(oldIP) {
		return fmt.Errorf("cluster_readdress_member_mismatch")
	}
	for _, current := range future.Configuration().Servers {
		if current.ID != server.ID && current.Address == newAddress {
			return fmt.Errorf("cluster_readdress_address_in_use")
		}
	}
	exemptNodeID := ""
	if request.Recovery {
		exemptNodeID = nodeID
	}
	if err := s.checkUniformVersionsLocked(ctx, nil, exemptNodeID); err != nil {
		return err
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("cluster_readdress_leader_barrier_failed: %w", err)
	}
	if err := s.Raft.AddVoter(server.ID, newAddress, 0, raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("cluster_readdress_membership_update_failed: %w", err)
	}
	confirmed := s.Raft.GetConfiguration()
	if err := confirmed.Error(); err != nil {
		return fmt.Errorf("cluster_readdress_membership_confirmation_failed: %w", err)
	}
	updated, present, err := resolveRaftMember(confirmed.Configuration(), nodeID)
	if err != nil || !present || updated.Address != newAddress || updated.Suffrage != raft.Voter {
		return fmt.Errorf("cluster_readdress_membership_unconfirmed")
	}
	s.EmitLeftPanelRefreshClusterWide("cluster_membership_changed")
	return nil
}

func (s *Service) forwardMemberAddressChange(
	ctx context.Context,
	leaderIP string,
	request MemberAddressChangeRequest,
) error {
	token, err := s.AuthService.CreateInternalClusterJWT(s.LocalNodeID())
	if err != nil {
		return fmt.Errorf("cluster_readdress_token_failed: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := utils.HTTPRequestReadContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/intra-cluster/readdress-member", ClusterAPIHost(leaderIP)),
		payload,
		map[string]string{
			"Accept": "application/json", "Content-Type": "application/json",
			"X-Cluster-Token": "Bearer " + token,
		},
		readdressControlTimeout,
		1<<20,
	)
	if err != nil {
		return err
	}
	var apiResponse internal.APIResponse[any]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return fmt.Errorf("cluster_readdress_response_invalid: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && apiResponse.Status == "success" {
		return nil
	}
	if strings.TrimSpace(apiResponse.Error) != "" {
		return errors.New(apiResponse.Error)
	}
	return fmt.Errorf("cluster_readdress_request_rejected: %s", apiResponse.Message)
}
