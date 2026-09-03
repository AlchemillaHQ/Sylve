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
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	JoinPhaseNotStarted  = "not_started"
	JoinPhaseIntentSaved = "intent_saved"
	JoinPhaseStarting    = "starting"
	JoinPhaseSubmitting  = "submitting"
	JoinPhaseStaged      = "staged_nonvoter"
	JoinPhaseCatchingUp  = "catching_up"
	JoinPhaseComplete    = clusterModels.JoinPhaseComplete
	JoinPhaseStalled     = "stalled"
	JoinPhaseFailed      = "failed"

	joinAdmissionRequestTimeout = 6 * time.Second
	joinAdmissionResponseLimit  = 1 << 20
)

type JoinAdmissionRequest struct {
	NodeID      string                       `json:"nodeId" binding:"required"`
	NodeIP      string                       `json:"nodeIp" binding:"required,ip"`
	NodeVersion string                       `json:"nodeVersion" binding:"required"`
	Preflight   bool                         `json:"preflight"`
	Inventory   GuestIdentityInventoryReport `json:"inventory"`
}

type ClusterJoinProgress struct {
	NodeID        string `json:"nodeId"`
	RaftState     string `json:"raftState"`
	LeaderID      string `json:"leaderId,omitempty"`
	LeaderAddress string `json:"leaderAddress,omitempty"`
	AppliedIndex  uint64 `json:"appliedIndex"`
	LastIndex     uint64 `json:"lastIndex"`
	RepairFenced  bool   `json:"repairFenced"`
}

type ClusterJoinStatus struct {
	NodeID        string `json:"nodeId"`
	NodeIP        string `json:"nodeIp,omitempty"`
	LeaderIP      string `json:"leaderIp,omitempty"`
	LeaderID      string `json:"leaderId,omitempty"`
	LeaderAddress string `json:"leaderAddress,omitempty"`
	Phase         string `json:"phase"`
	Suffrage      string `json:"suffrage,omitempty"`
	RaftState     string `json:"raftState,omitempty"`
	AppliedIndex  uint64 `json:"appliedIndex"`
	TargetIndex   uint64 `json:"targetIndex,omitempty"`
	Attempts      uint   `json:"attempts"`
	Retrying      bool   `json:"retrying"`
	LastError     string `json:"lastError,omitempty"`
}

type JoinIntentSubmissionResult struct {
	Status     ClusterJoinStatus
	Response   internal.APIResponse[GuestIdentityInventoryReport]
	StatusCode int
	Retryable  bool
	Err        error
}

func (s *Service) SaveJoinIntent(
	leaderIP string,
	clusterKey string,
	request JoinAdmissionRequest,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	var err error
	leaderIP, err = normalizeClusterIPv4(leaderIP, "invalid_leader_ip")
	if err != nil {
		return err
	}
	clusterKey = strings.TrimSpace(clusterKey)
	if clusterKey == "" {
		return fmt.Errorf("invalid_cluster_key")
	}
	request.NodeID = strings.TrimSpace(request.NodeID)
	request.NodeIP, err = normalizeClusterIPv4(request.NodeIP, "invalid_joining_node_ip")
	if err != nil {
		return err
	}
	request.NodeVersion = strings.TrimSpace(request.NodeVersion)
	canonical, err := canonicalSubmittedGuestIdentityInventory(request.NodeID, request.Inventory)
	if err != nil {
		return err
	}
	inventoryJSON, err := json.Marshal(canonical)
	if err != nil {
		return fmt.Errorf("marshal_join_inventory: %w", err)
	}

	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()
	if s.guestIdentityClusterFormation || len(s.guestIdentityLocalReservations) != 0 {
		return fmt.Errorf("guest_identity_mutation_in_progress")
	}
	latest, err := ScanLocalGuestIdentityInventory(s.DB, request.NodeID)
	if err != nil {
		return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
	}
	if err := requireCleanGuestIdentityInventory(latest); err != nil {
		return err
	}
	if latest.Digest != canonical.Digest {
		return fmt.Errorf("joining_inventory_changed_before_start")
	}

	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return err
	}
	if err := s.DB.Model(&record).Updates(map[string]any{
		"key":               clusterKey,
		"join_leader_ip":    leaderIP,
		"join_node_id":      request.NodeID,
		"join_node_ip":      request.NodeIP,
		"join_node_version": request.NodeVersion,
		"join_inventory":    inventoryJSON,
		"join_phase":        JoinPhaseIntentSaved,
		"join_last_error":   "",
		"join_attempts":     0,
	}).Error; err != nil {
		return err
	}
	s.joinComplete.Store(false)
	return nil
}

func (s *Service) updateJoinIntent(
	phase, lastError string,
	incrementAttempt bool,
	leaderIP string,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return err
	}
	updates := map[string]any{
		"join_phase":      phase,
		"join_last_error": strings.TrimSpace(lastError),
	}
	if incrementAttempt {
		updates["join_attempts"] = gorm.Expr("join_attempts + 1")
	}
	if strings.TrimSpace(leaderIP) != "" {
		updates["join_leader_ip"] = strings.TrimSpace(leaderIP)
	}
	return s.DB.Model(&record).Updates(updates).Error
}

func (s *Service) MarkJoinIntentPhase(phase string, err error) error {
	lastError := ""
	if err != nil {
		lastError = err.Error()
	}
	return s.updateJoinIntent(phase, lastError, false, "")
}

func (s *Service) joinServer(nodeID string) (*raft.Server, error) {
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return nil, nil
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return nil, err
	}
	nodeID = strings.TrimSpace(nodeID)
	for _, server := range future.Configuration().Servers {
		if strings.TrimSpace(string(server.ID)) == nodeID {
			copy := server
			return &copy, nil
		}
	}
	return nil, nil
}

func joinPhaseIsRetrying(phase string) bool {
	switch phase {
	case JoinPhaseIntentSaved, JoinPhaseStarting, JoinPhaseSubmitting,
		JoinPhaseStaged, JoinPhaseCatchingUp, JoinPhaseStalled:
		return true
	default:
		return false
	}
}

func (s *Service) JoinStatus() (ClusterJoinStatus, error) {
	if s == nil || s.DB == nil {
		return ClusterJoinStatus{}, fmt.Errorf("cluster_service_not_initialized")
	}
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		return ClusterJoinStatus{}, err
	}
	localNodeID := strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	nodeID := strings.TrimSpace(record.JoinNodeID)
	if nodeID == "" {
		nodeID = localNodeID
	}

	status := ClusterJoinStatus{
		NodeID:    nodeID,
		NodeIP:    strings.TrimSpace(record.JoinNodeIP),
		LeaderIP:  strings.TrimSpace(record.JoinLeaderIP),
		Phase:     strings.TrimSpace(record.JoinPhase),
		Attempts:  record.JoinAttempts,
		LastError: strings.TrimSpace(record.JoinLastError),
	}
	if status.Phase == "" {
		status.Phase = JoinPhaseNotStarted
	}
	if status.NodeIP == "" && nodeID == localNodeID {
		status.NodeIP = strings.TrimSpace(record.RaftIP)
	}

	if s.Raft != nil && s.Raft.State() != raft.Shutdown {
		status.RaftState = s.Raft.State().String()
		status.AppliedIndex = s.Raft.AppliedIndex()
		status.TargetIndex = s.Raft.LastIndex()
		leaderAddress, leaderID := s.Raft.LeaderWithID()
		status.LeaderID = strings.TrimSpace(string(leaderID))
		status.LeaderAddress = strings.TrimSpace(string(leaderAddress))
		server, err := s.joinServer(nodeID)
		if err == nil && server != nil {
			status.Suffrage = raftSuffrageName(server.Suffrage)
			switch server.Suffrage {
			case raft.Voter:
				status.Phase = JoinPhaseComplete
				status.LastError = ""
				if nodeID == localNodeID && strings.TrimSpace(record.JoinNodeID) == localNodeID &&
					record.JoinPhase != JoinPhaseComplete {
					_ = s.updateJoinIntent(JoinPhaseComplete, "", false, "")
				}
			case raft.Nonvoter, raft.Staging:
				status.Phase = JoinPhaseCatchingUp
				status.LastError = ""
				if nodeID == localNodeID && strings.TrimSpace(record.JoinNodeID) == localNodeID &&
					record.JoinPhase != JoinPhaseCatchingUp {
					_ = s.updateJoinIntent(JoinPhaseCatchingUp, "", false, "")
				}
			}
		}
	}
	status.Retrying = joinPhaseIsRetrying(status.Phase)
	return status, nil
}

func (s *Service) LocalJoinProgress(expectedNodeID string) (ClusterJoinProgress, error) {
	progress := ClusterJoinProgress{}
	if s == nil || s.Raft == nil || s.Raft.State() == raft.Shutdown {
		return progress, fmt.Errorf("join_progress_raft_unavailable")
	}
	progress.NodeID = strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	expectedNodeID = strings.TrimSpace(expectedNodeID)
	if progress.NodeID == "" || (expectedNodeID != "" && expectedNodeID != progress.NodeID) {
		return progress, fmt.Errorf(
			"join_progress_node_id_mismatch: expected=%s actual=%s",
			expectedNodeID,
			progress.NodeID,
		)
	}
	progress.RaftState = s.Raft.State().String()
	progress.AppliedIndex = s.Raft.AppliedIndex()
	progress.LastIndex = s.Raft.LastIndex()
	progress.RepairFenced = s.stateRepair.Load()
	leaderAddress, leaderID := s.Raft.LeaderWithID()
	progress.LeaderID = strings.TrimSpace(string(leaderID))
	progress.LeaderAddress = strings.TrimSpace(string(leaderAddress))
	return progress, nil
}

func (s *Service) fetchJoinProgress(
	ctx context.Context,
	nodeID string,
	address raft.ServerAddress,
	minimumIndex uint64,
) (ClusterJoinProgress, error) {
	if s.joinProgressForNode != nil {
		return s.joinProgressForNode(ctx, nodeID, address, minimumIndex)
	}
	if s.stateDigestForNode != nil {
		digest, err := s.fetchReplicatedStateDigest(ctx, nodeID, address, minimumIndex)
		return ClusterJoinProgress{
			NodeID:       digest.NodeID,
			AppliedIndex: digest.AppliedIndex,
			LastIndex:    digest.AppliedIndex,
			RepairFenced: digest.RepairFenced,
		}, err
	}
	endpoint, err := s.replicatedStateRemoteAPI(nodeID, address)
	if err != nil {
		return ClusterJoinProgress{}, err
	}
	token, err := s.replicatedStateInternalToken()
	if err != nil {
		return ClusterJoinProgress{}, err
	}
	requestURL := fmt.Sprintf(
		"https://%s/api/intra-cluster/join-progress?expectedNodeId=%s",
		endpoint,
		nodeID,
	)
	body, statusCode, err := utils.HTTPGetJSONReadContext(ctx, requestURL, map[string]string{
		"Accept":                "application/json",
		auth.ClusterTokenHeader: fmt.Sprintf("Bearer %s", token),
	})
	if err != nil {
		return ClusterJoinProgress{}, fmt.Errorf(
			"join_progress_remote_request_failed: node_id=%s status=%d: %w",
			nodeID,
			statusCode,
			err,
		)
	}
	var response internal.APIResponse[ClusterJoinProgress]
	if err := json.Unmarshal(body, &response); err != nil {
		return ClusterJoinProgress{}, fmt.Errorf("join_progress_remote_decode_failed: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(response.Status)) != "success" {
		return ClusterJoinProgress{}, fmt.Errorf(
			"join_progress_remote_non_success: message=%s error=%s",
			response.Message,
			response.Error,
		)
	}
	if strings.TrimSpace(response.Data.NodeID) != strings.TrimSpace(nodeID) {
		return ClusterJoinProgress{}, fmt.Errorf(
			"join_progress_remote_node_id_mismatch: expected=%s actual=%s",
			nodeID,
			response.Data.NodeID,
		)
	}
	return response.Data, nil
}

func leaderIPFromNotLeaderError(value string) string {
	const marker = "leader_addr="
	index := strings.Index(value, marker)
	if index < 0 {
		return ""
	}
	address := strings.TrimSpace(value[index+len(marker):])
	if end := strings.IndexByte(address, ';'); end >= 0 {
		address = strings.TrimSpace(address[:end])
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(strings.TrimSpace(host)) == nil {
		return ""
	}
	return strings.TrimSpace(host)
}

func retryableJoinAdmissionStatus(statusCode int, response internal.APIResponse[GuestIdentityInventoryReport]) bool {
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		return true
	}
	switch strings.TrimSpace(response.Message) {
	case "not_leader", "cluster_version_mismatch", "cluster_join_failed":
		return true
	default:
		return false
	}
}

func (s *Service) SubmitJoinIntent(ctx context.Context) JoinIntentSubmissionResult {
	result := JoinIntentSubmissionResult{}
	if s == nil || s.DB == nil {
		result.Err = fmt.Errorf("cluster_service_not_initialized")
		return result
	}
	admittedCtx, release, err := s.EnterMutation(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	defer release()
	ctx = admittedCtx
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	var record clusterModels.Cluster
	if err := s.DB.First(&record).Error; err != nil {
		result.Err = err
		return result
	}
	if strings.TrimSpace(record.JoinNodeID) == "" || len(record.JoinInventory) == 0 {
		result.Err = fmt.Errorf("cluster_join_intent_not_found")
		return result
	}
	if status, err := s.JoinStatus(); err == nil {
		result.Status = status
		if status.Phase == JoinPhaseComplete {
			return result
		}
	}

	var inventory GuestIdentityInventoryReport
	if err := json.Unmarshal(record.JoinInventory, &inventory); err != nil {
		result.Err = fmt.Errorf("decode_join_inventory: %w", err)
		_ = s.updateJoinIntent(JoinPhaseFailed, result.Err.Error(), false, "")
		return result
	}
	request := JoinAdmissionRequest{
		NodeID:      strings.TrimSpace(record.JoinNodeID),
		NodeIP:      strings.TrimSpace(record.JoinNodeIP),
		NodeVersion: strings.TrimSpace(record.JoinNodeVersion),
		Preflight:   false,
		Inventory:   inventory,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		result.Err = fmt.Errorf("marshal_join_admission: %w", err)
		return result
	}
	if err := s.updateJoinIntent(JoinPhaseSubmitting, "", true, ""); err != nil {
		result.Err = err
		return result
	}

	requestContext := ctx
	if requestContext == nil {
		requestContext = context.Background()
	}
	response, requestErr := utils.HTTPRequestReadContext(
		requestContext,
		http.MethodPost,
		fmt.Sprintf(
			"https://%s/api/cluster/accept-join",
			ClusterAPIHost(strings.TrimSpace(record.JoinLeaderIP)),
		),
		payload,
		map[string]string{
			"Accept":              "application/json",
			"Content-Type":        "application/json",
			auth.ClusterKeyHeader: strings.TrimSpace(record.Key),
		},
		joinAdmissionRequestTimeout,
		joinAdmissionResponseLimit,
	)
	if requestErr != nil {
		result.Err = requestErr
		result.Retryable = true
		_ = s.updateJoinIntent(JoinPhaseStalled, requestErr.Error(), false, "")
		result.Status, _ = s.JoinStatus()
		return result
	}
	result.StatusCode = response.StatusCode
	if len(response.Body) != 0 {
		if err := json.Unmarshal(response.Body, &result.Response); err != nil {
			result.Err = fmt.Errorf("decode_join_admission_response_failed: %w", err)
			result.Retryable = true
			_ = s.updateJoinIntent(JoinPhaseStalled, result.Err.Error(), false, "")
			result.Status, _ = s.JoinStatus()
			return result
		}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 &&
		strings.ToLower(strings.TrimSpace(result.Response.Status)) == "success" {
		_ = s.updateJoinIntent(JoinPhaseStaged, "", false, "")
		result.Status, _ = s.JoinStatus()
		return result
	}

	errorText := strings.TrimSpace(result.Response.Error)
	if errorText == "" {
		errorText = fmt.Sprintf("join_admission_http_status_%d", response.StatusCode)
	}
	result.Err = errors.New(errorText)
	result.Retryable = retryableJoinAdmissionStatus(response.StatusCode, result.Response)
	if result.Retryable {
		newLeaderIP := leaderIPFromNotLeaderError(errorText)
		_ = s.updateJoinIntent(JoinPhaseStalled, errorText, false, newLeaderIP)
	} else {
		_ = s.updateJoinIntent(JoinPhaseFailed, errorText, false, "")
	}
	result.Status, _ = s.JoinStatus()
	return result
}
