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
	"net/http"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	JoinPhaseNotStarted = "not_started"
	JoinPhaseSubmitting = "submitting"
	JoinPhaseCatchingUp = "catching_up"
	JoinPhaseComplete   = clusterModels.JoinPhaseComplete

	joinAdmissionRequestTimeout = 30 * time.Second
	joinAdmissionResponseLimit  = 1 << 20
	joinProgressTimeout         = 5 * time.Second
)

type JoinAdmissionRequest struct {
	NodeID      string `json:"nodeId" binding:"required"`
	NodeIP      string `json:"nodeIp" binding:"required,ip"`
	NodeVersion string `json:"nodeVersion" binding:"required"`
	// NodeHostname names the node in requests, so no two members share one.
	NodeHostname string                       `json:"nodeHostname" binding:"required"`
	Inventory    GuestIdentityInventoryReport `json:"inventory"`
	// SSHPublicKey is the joiner's cluster SSH key, recorded with its pin.
	SSHPublicKey string   `json:"sshPublicKey" binding:"required"`
	LocalUsers   []string `json:"localUsers"`
	// UserClusters identifies retained copies, including users since renamed or deleted there.
	UserClusters map[string]string `json:"userClusters,omitempty"`
}

type ClusterJoinProgress struct {
	NodeID       string `json:"nodeId"`
	AppliedIndex uint64 `json:"appliedIndex"`
}

type ClusterJoinStatus struct {
	ClusterID         string `json:"clusterId,omitempty"`
	NodeID            string `json:"nodeId"`
	NodeIP            string `json:"nodeIp,omitempty"`
	LeaderIP          string `json:"leaderIp,omitempty"`
	LeaderID          string `json:"leaderId,omitempty"`
	LeaderAddress     string `json:"leaderAddress,omitempty"`
	Phase             string `json:"phase"`
	Suffrage          string `json:"suffrage,omitempty"`
	RaftState         string `json:"raftState,omitempty"`
	AppliedIndex      uint64 `json:"appliedIndex"`
	TargetIndex       uint64 `json:"targetIndex,omitempty"`
	Attempts          uint   `json:"attempts"`
	Retrying          bool   `json:"retrying"`
	AwaitingPromotion bool   `json:"awaitingPromotion,omitempty"`
	LastError         string `json:"lastError,omitempty"`
	// Pins are the members' cert pins by node ID, which an admitted node trusts
	// until it catches up.
	Pins map[string]string `json:"pins,omitempty"`
}

type JoinAdmissionError struct {
	StatusCode int
	Message    string
	Detail     string
	Data       json.RawMessage
}

func (e *JoinAdmissionError) Error() string {
	return fmt.Sprintf("%s: %s", e.Message, e.Detail)
}

func (s *Service) Join(ctx context.Context, leaderIP, nodeIP, clusterKey string) (ClusterJoinStatus, error) {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return ClusterJoinStatus{}, err
	}
	defer release()
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	if err := s.saveJoinIntent(leaderIP, nodeIP, clusterKey); err != nil {
		return ClusterJoinStatus{}, err
	}
	var refused *JoinAdmissionError
	if err := s.advanceJoin(ctx); errors.As(err, &refused) {
		return ClusterJoinStatus{}, err
	}
	return s.JoinStatus()
}

// No guest may change while a join is open: the leader claims this node's guest
// IDs from the inventory sent with the admission.
func (s *Service) saveJoinIntent(leaderIP, nodeIP, clusterKey string) error {
	leaderIP, err := normalizeClusterIPv4(leaderIP, "invalid_leader_ip")
	if err != nil {
		return err
	}
	nodeIP, err = normalizeClusterIPv4(nodeIP, "invalid_joining_node_ip")
	if err != nil {
		return err
	}
	clusterKey = strings.TrimSpace(clusterKey)
	if _, err := ParseJoinKey(clusterKey); err != nil {
		return err
	}
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	if len(s.guestIdentityLocalReservations) != 0 {
		return fmt.Errorf("guest_identity_mutation_in_progress")
	}
	if err := s.requireNoDurableGuestIdentityOperations(context.Background()); err != nil {
		return err
	}
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	if record.Enabled {
		// A clustered node may only go on with its own unfinished join.
		if record.HasIncompleteJoin() && auth.ClusterKeyMatches(clusterKey, strings.TrimSpace(record.Key)) {
			return nil
		}
		return fmt.Errorf("clustered_already")
	}
	s.joinMu.Lock()
	s.joinAttempts, s.joinLastError = 0, ""
	s.joinMu.Unlock()
	s.joinComplete.Store(false)
	return s.DB.Model(&record).Updates(map[string]any{
		"key": clusterKey, "join_leader_ip": leaderIP, "raft_ip": nodeIP,
	}).Error
}

func (s *Service) advanceJoin(ctx context.Context) error {
	record, err := s.loadClusterRecord()
	if err != nil || !record.HasIncompleteJoin() {
		return err
	}
	if record.Enabled {
		r := s.Raft()
		if r == nil || r.State() == raft.Shutdown {
			return nil
		}
		if server, err := s.joinServer(s.LocalNodeID()); err == nil && server != nil && server.Suffrage == raft.Voter {
			return s.finishJoin(record)
		}
		if leaderAddress, _ := r.LeaderWithID(); leaderAddress != "" {
			return nil
		}
	}
	err = s.requestAdmission(ctx, record)
	s.recordJoinError(err)
	var refused *JoinAdmissionError
	switch {
	case errors.As(err, &refused) && !record.Enabled:
		return errors.Join(err, s.dropJoinIntent(record))
	case errors.As(err, &refused):
		// Refused after it started Raft: the cluster gave the join up. Drop cluster
		// state only once a leader confirms it isn't listed.
		if removed, checkErr := s.removedFromCluster(ctx); checkErr == nil && removed {
			logger.L.Warn().Err(err).Msg("cluster_join_given_up_by_cluster")
			return errors.Join(err, s.finishLeave())
		}
		return err
	case err != nil:
		return err
	case !record.Enabled:
		err = s.startJoinerRaft(record.RaftIP, record.Key)
		s.recordJoinError(err)
		return err
	}
	return nil
}

func (s *Service) recordJoinError(err error) {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()
	s.joinLastError = ""
	if err != nil {
		s.joinLastError = err.Error()
	}
}

func (s *Service) dropJoinIntent(record clusterModels.Cluster) error {
	return s.DB.Model(&record).Updates(map[string]any{"key": "", "join_leader_ip": "", "join_cluster_id": "", "raft_ip": ""}).Error
}

func (s *Service) finishJoin(record clusterModels.Cluster) error {
	if err := s.DB.Model(&record).Updates(map[string]any{"join_leader_ip": "", "join_cluster_id": "", "key": ""}).Error; err != nil {
		return err
	}
	s.recordJoinError(nil)
	s.notifyJoinComplete()
	if err := s.PopulateClusterNodes(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_node_population_deferred_after_join")
	}
	return nil
}

func (s *Service) startJoinerRaft(ip, clusterKey string) error {
	if s.joinRaftStarter != nil {
		return s.joinRaftStarter(ip, clusterKey)
	}
	return s.StartAsJoiner(ip, clusterKey)
}

func (s *Service) requestAdmission(ctx context.Context, record clusterModels.Cluster) error {
	localNodeID := s.LocalNodeID()
	inventory, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), localNodeID)
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	_, _, sshPublicKey, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		return fmt.Errorf("cluster_join_ssh_key_unavailable: %w", err)
	}
	hostname, err := utils.GetSystemHostname()
	if err != nil {
		return fmt.Errorf("cluster_join_hostname_unavailable: %w", err)
	}
	var users []models.User
	if err := s.DB.WithContext(ctx).Select("username", "shared_cluster_id", "shared_user_id").
		Where("source = ? AND username <> ?", "local", "admin").Find(&users).Error; err != nil {
		return fmt.Errorf("cluster_join_users_unavailable: %w", err)
	}
	localUsers := make([]string, 0, len(users))
	userClusters := make(map[string]string)
	for _, user := range users {
		localUsers = append(localUsers, user.Username)
		if user.SharedClusterID != "" && user.SharedUserID != 0 {
			userClusters[user.Username] = user.SharedClusterID
		}
	}
	payload, err := json.Marshal(JoinAdmissionRequest{
		NodeID: localNodeID, NodeIP: record.RaftIP, NodeVersion: cmd.Version, NodeHostname: hostname,
		Inventory: inventory, SSHPublicKey: sshPublicKey, LocalUsers: localUsers, UserClusters: userClusters,
	})
	if err != nil {
		return err
	}
	s.joinMu.Lock()
	s.joinAttempts++
	s.joinMu.Unlock()
	key, err := ParseJoinKey(record.Key)
	if err != nil {
		return &JoinAdmissionError{StatusCode: http.StatusBadRequest, Message: "cluster_join_key_invalid", Detail: err.Error()}
	}
	// Only the member that issued the key gets the request, which it forwards
	// to the leader.
	response, err := s.peerHTTP(
		utils.ForJoin(ctx, key.VerifyPin),
		http.MethodPost,
		fmt.Sprintf("https://%s/api/cluster/accept-join", ClusterAPIHost(strings.TrimSpace(record.JoinLeaderIP))),
		payload,
		map[string]string{
			"Accept": "application/json", "Content-Type": "application/json", auth.ClusterKeyHeader: key.Secret,
		},
		joinAdmissionRequestTimeout,
		joinAdmissionResponseLimit,
	)
	if err != nil {
		return fmt.Errorf("cluster_join_admission_request_failed: %w", err)
	}
	var apiResponse internal.APIResponse[json.RawMessage]
	if err := json.Unmarshal(response.Body, &apiResponse); err != nil {
		return fmt.Errorf("cluster_join_admission_response_invalid: status=%d: %w", response.StatusCode, err)
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300 && apiResponse.Status == "success":
		var status ClusterJoinStatus
		if err := json.Unmarshal(apiResponse.Data, &status); err != nil {
			return fmt.Errorf("cluster_join_admission_response_invalid: %w", err)
		}
		if err := s.DB.Model(&record).Update("join_cluster_id", status.ClusterID).Error; err != nil {
			return err
		}
		s.seedJoinPins(status.Pins)
		return nil
	// A fenced member is leaving or changing its address, or no leader is known;
	// ask again later.
	case response.StatusCode >= http.StatusInternalServerError || response.StatusCode == http.StatusLocked ||
		response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf(
			"cluster_join_admission_unavailable: status=%d message=%s error=%s",
			response.StatusCode, apiResponse.Message, apiResponse.Error,
		)
	default:
		return &JoinAdmissionError{
			StatusCode: response.StatusCode, Message: apiResponse.Message, Detail: apiResponse.Error, Data: apiResponse.Data,
		}
	}
}

func (s *Service) joinServer(nodeID string) (*raft.Server, error) {
	if s.Raft() == nil || s.Raft().State() == raft.Shutdown {
		return nil, nil
	}
	future := s.Raft().GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, err
	}
	server, present, err := resolveRaftMember(future.Configuration(), nodeID)
	if err != nil || !present {
		return nil, err
	}
	return &server, nil
}

func (s *Service) JoinStatus() (ClusterJoinStatus, error) {
	record, err := s.loadClusterRecord()
	if err != nil {
		return ClusterJoinStatus{}, err
	}
	status := ClusterJoinStatus{
		NodeID:   strings.TrimSpace(s.LocalNodeID()),
		NodeIP:   strings.TrimSpace(record.RaftIP),
		LeaderIP: strings.TrimSpace(record.JoinLeaderIP),
		Phase:    JoinPhaseNotStarted,
	}
	s.joinMu.Lock()
	status.Attempts, status.LastError = s.joinAttempts, s.joinLastError
	s.joinMu.Unlock()
	if record.HasIncompleteJoin() {
		status.Phase, status.Retrying = JoinPhaseSubmitting, true
	}
	r := s.Raft()
	if !record.Enabled || r == nil || r.State() == raft.Shutdown {
		return status, nil
	}
	status.RaftState = r.State().String()
	status.AppliedIndex = r.AppliedIndex()
	status.TargetIndex = r.CommitIndex()
	leaderAddress, leaderID := r.LeaderWithID()
	status.LeaderID = strings.TrimSpace(string(leaderID))
	status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
	server, err := s.joinServer(status.NodeID)
	if err == nil && server != nil {
		status.Suffrage = raftSuffrageName(server.Suffrage)
	}
	switch {
	case server != nil && server.Suffrage == raft.Voter:
		status.Phase, status.Retrying, status.LastError = JoinPhaseComplete, false, ""
	case record.HasIncompleteJoin():
		status.Phase = JoinPhaseCatchingUp
		status.AwaitingPromotion = status.TargetIndex > 0 && status.AppliedIndex >= status.TargetIndex
	}
	return status, nil
}

func (s *Service) LocalJoinProgress() (ClusterJoinProgress, error) {
	r := s.Raft()
	if r == nil || r.State() == raft.Shutdown {
		return ClusterJoinProgress{}, fmt.Errorf("join_progress_raft_unavailable")
	}
	return ClusterJoinProgress{NodeID: s.LocalNodeID(), AppliedIndex: r.AppliedIndex()}, nil
}

// The nonvoter answers at once: waiting until it reflects the leader would
// defeat the question.
func (s *Service) fetchJoinProgress(ctx context.Context, server raft.Server) (ClusterJoinProgress, error) {
	ctx, cancel := context.WithTimeout(ctx, joinProgressTimeout)
	defer cancel()
	return callNodeAt[ClusterJoinProgress](
		ctx, s, raftAddressHost(string(server.Address)), string(server.ID), "join-progress", []byte("{}"), 0,
	)
}

// peerHTTP sends a request to another node's cluster API.
func (s *Service) peerHTTP(
	ctx context.Context,
	method string,
	url string,
	payload []byte,
	headers map[string]string,
	timeout time.Duration,
	responseLimit int64,
) (utils.HTTPReadResponse, error) {
	if s.peerRequest != nil {
		statusCode, body, err := s.peerRequest(ctx, method, url, payload, headers)
		return utils.HTTPReadResponse{StatusCode: statusCode, Body: body}, err
	}
	return utils.HTTPRequestReadContext(ctx, method, url, payload, headers, timeout, responseLimit)
}
