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
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const (
	LeavePhaseRemoving = "removing"
	LeavePhaseCleaning = "cleaning"
)

const (
	leaveControlTimeout     = 6 * time.Second
	leaveLeaderWaitTimeout  = 10 * time.Second
	leaveDrainTimeout       = 10 * time.Second
	leaveReconcileInterval  = 3 * time.Second
	membershipProbeInterval = 30 * time.Second
)

type ClusterLeaveStatus struct {
	Enabled     bool   `json:"enabled"`
	Phase       string `json:"phase"`
	LastError   string `json:"lastError"`
	LocalNodeID string `json:"localNodeId"`
}

type RemoveMembershipRequest struct {
	NodeID string `json:"nodeId"`
}

type ClusterLeaveError struct {
	Code  string
	Cause error
}

func (e *ClusterLeaveError) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *ClusterLeaveError) Unwrap() error {
	return e.Cause
}

var ErrLeaveAlreadyRemoved = &ClusterLeaveError{Code: "cluster_leave_already_removed"}

func (s *Service) InitializeLeaveRuntime() error {
	if s == nil || s.DB == nil || s.mutationGate == nil {
		return fmt.Errorf("cluster_leave_runtime_unavailable")
	}
	s.mutationGate.Close()
	record, err := s.loadClusterRecord()
	if err != nil {
		return fmt.Errorf("cluster_leave_state_load_failed: %w", err)
	}
	switch phase := strings.TrimSpace(record.LeavePhase); phase {
	case "":
		return s.mutationGate.Open()
	case LeavePhaseRemoving:
		return nil
	case LeavePhaseCleaning:
		if err := s.FinalizeLocalDecluster(); err != nil {
			// Stay fenced and skip Raft; the leave reconciler retries the
			// cleanup instead of the daemon crash-looping at boot.
			logger.L.Error().Err(err).Msg("cluster_leave_cleanup_deferred")
			return nil
		}
		return s.mutationGate.Open()
	default:
		return fmt.Errorf("cluster_leave_phase_invalid: %s", phase)
	}
}

func (s *Service) LeaveStatus() (ClusterLeaveStatus, error) {
	record, err := s.loadClusterRecord()
	if err != nil {
		return ClusterLeaveStatus{}, err
	}
	return ClusterLeaveStatus{
		Enabled:     record.Enabled,
		Phase:       strings.TrimSpace(record.LeavePhase),
		LastError:   strings.TrimSpace(record.LeaveLastError),
		LocalNodeID: strings.TrimSpace(s.LocalNodeID()),
	}, nil
}

func (s *Service) loadClusterRecord() (clusterModels.Cluster, error) {
	var record clusterModels.Cluster
	err := s.DB.First(&record).Error
	return record, err
}

func (s *Service) setLeavePhase(phase string, cause error) error {
	lastError := ""
	if cause != nil {
		lastError = cause.Error()
	}
	var record clusterModels.Cluster
	if err := s.DB.Select("id").First(&record).Error; err != nil {
		return err
	}
	return s.DB.Model(&record).Updates(map[string]any{"leave_phase": phase, "leave_last_error": lastError}).Error
}

func (s *Service) clearLeave() error {
	if err := s.setLeavePhase("", nil); err != nil {
		return err
	}
	return s.ReopenMutations()
}

func (s *Service) reopenMutations() {
	if err := s.ReopenMutations(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_leave_reopen_failed")
	}
}

func (s *Service) Leave(ctx context.Context, keepGuests bool) error {
	if err := s.startLeave(ctx, keepGuests); err != nil {
		return err
	}
	return s.advanceLeave(ctx)
}

func (s *Service) startLeave(ctx context.Context, keepGuests bool) error {
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	if strings.TrimSpace(record.LeavePhase) != "" || !record.Enabled && !record.HasIncompleteJoin() {
		return nil
	}
	// Before draining: a drain that fails reopens the gate, which a readdress
	// keeps closed.
	if strings.TrimSpace(record.ReaddressPhase) != "" {
		return &ClusterLeaveError{Code: "cluster_readdress_in_progress"}
	}
	// Clusters formed before user provenance was recorded bind their copies before leaving.
	if record.Enabled && !record.HasIncompleteJoin() {
		var shared int64
		if err := s.DB.Table("users").Where("source = ? AND shared", "local").Count(&shared).Error; err != nil {
			return err
		}
		if shared > 0 {
			if _, err := s.ensureClusterIdentity(ctx); err != nil {
				return fmt.Errorf("cluster_leave_user_identity_unavailable: %w", err)
			}
		}
	}
	drainCtx, cancel := context.WithTimeout(ctx, leaveDrainTimeout)
	err = s.DrainMutations(drainCtx)
	cancel()
	if err != nil {
		s.reopenMutations()
		return &ClusterLeaveError{Code: "cluster_leave_active_mutations", Cause: err}
	}

	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	if record, err = s.loadClusterRecord(); err != nil {
		s.reopenMutations()
		return err
	}
	if strings.TrimSpace(record.ReaddressPhase) != "" {
		// The readdress keeps its fence.
		return &ClusterLeaveError{Code: "cluster_readdress_in_progress"}
	}
	phase := LeavePhaseCleaning
	if record.Enabled {
		phase = LeavePhaseRemoving
		if err := s.leavePreflight(ctx, keepGuests); err != nil {
			s.reopenMutations()
			return err
		}
	}
	if err := s.setLeavePhase(phase, nil); err != nil {
		s.reopenMutations()
		return err
	}
	s.leaveComplete.Store(false)
	return nil
}

func (s *Service) leavePreflight(ctx context.Context, keepGuests bool) error {
	localNodeID := s.LocalNodeID()
	db := s.DB.WithContext(ctx)
	dependencies, err := s.replicatedPeerRemovalDependencies(ctx, localNodeID)
	if err != nil {
		return err
	}
	dependencies = blockingPeerRemovalDependencies(dependencies, true)
	if !keepGuests {
		report, err := ScanLocalGuestIdentityInventory(db, localNodeID)
		if err != nil {
			return fmt.Errorf("guest_identity_inventory_scan_failed: %w", err)
		}
		for _, entry := range report.Entries {
			appendPeerRemovalDependency(&dependencies, PeerRemovalDependencyGuest, entry.GuestID, entry.Name, entry.GuestType, "registered")
		}
	}
	inFlight, err := durableGuestIdentityInFlightIDs(db)
	if err != nil {
		return fmt.Errorf("scan_active_guest_operations: %w", err)
	}
	s.guestIdentityRuntimeMu.Lock()
	for guestID := range s.guestIdentityLocalReservations {
		inFlight = append(inFlight, guestID)
	}
	s.guestIdentityRuntimeMu.Unlock()
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i] < inFlight[j] })
	for _, guestID := range inFlight {
		appendPeerRemovalDependency(&dependencies, PeerRemovalDependencyGuestOperation, guestID, fmt.Sprintf("guest:%d", guestID), "admission", "active")
	}
	var tasks []taskModels.GuestLifecycleTask
	if err := db.Where("status IN ?", []string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).
		Order("id ASC").Find(&tasks).Error; err != nil {
		return fmt.Errorf("scan_active_lifecycle_tasks: %w", err)
	}
	for _, task := range tasks {
		appendPeerRemovalDependency(&dependencies, PeerRemovalDependencyLifecycleTask, task.ID,
			fmt.Sprintf("%s:%d", task.GuestType, task.GuestID), task.Action, task.Status)
	}
	if len(dependencies) != 0 {
		return &PeerRemovalBlockedError{Conflict: PeerRemovalConflict{NodeID: localNodeID, Dependencies: dependencies}}
	}
	return nil
}

func (s *Service) advanceLeave(ctx context.Context) error {
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	switch strings.TrimSpace(record.LeavePhase) {
	case "":
		return nil
	case LeavePhaseCleaning:
		return s.finishLeave()
	}
	err = s.leaveMembership(ctx)
	var blocked *PeerRemovalBlockedError
	switch {
	case err == nil:
		return s.finishLeave()
	case errors.As(err, &blocked):
		if clearErr := s.clearLeave(); clearErr != nil {
			return errors.Join(err, clearErr)
		}
		return err
	default:
		if recordErr := s.setLeavePhase(LeavePhaseRemoving, err); recordErr != nil {
			logger.L.Warn().Err(recordErr).Msg("cluster_leave_state_update_failed")
		}
		return &ClusterLeaveError{Code: "cluster_leave_membership_unconfirmed", Cause: err}
	}
}

func (s *Service) leaveMembership(ctx context.Context) error {
	r := s.Raft()
	if r == nil || r.State() == raft.Shutdown {
		return fmt.Errorf("raft_not_running")
	}
	if r.State() == raft.Leader {
		future := r.GetConfiguration()
		if err := future.Error(); err != nil {
			return fmt.Errorf("raft_configuration_unavailable: %w", err)
		}
		configuration := future.Configuration()
		if len(configuration.Servers) == 1 && strings.TrimSpace(string(configuration.Servers[0].ID)) == s.LocalNodeID() {
			return nil
		}
		if err := s.transferLeadershipForLeave(ctx, configuration); err != nil {
			return err
		}
	}
	if err := s.requestRemoval(ctx); err != nil && !errors.Is(err, ErrPeerNotFound) {
		return err
	}
	return nil
}

func (s *Service) transferLeadershipForLeave(ctx context.Context, configuration raft.Configuration) error {
	localNodeID := s.LocalNodeID()
	var candidates []raft.Server
	for _, server := range configuration.Servers {
		if server.Suffrage == raft.Voter && strings.TrimSpace(string(server.ID)) != localNodeID {
			candidates = append(candidates, server)
		}
	}
	if len(candidates) == 0 {
		return &ClusterLeaveError{Code: "cluster_leave_leadership_transfer_unavailable"}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	var transferErr error
	for _, candidate := range candidates {
		for range 3 {
			transferErr = s.Raft().LeadershipTransferToServer(candidate.ID, candidate.Address).Error()
			if !errors.Is(transferErr, raft.ErrLeadershipTransferInProgress) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if transferErr == nil || s.Raft().State() != raft.Leader {
			transferErr = nil
			break
		}
	}
	if transferErr != nil {
		return fmt.Errorf("cluster_leave_leadership_transfer_failed: %w", transferErr)
	}

	waitCtx, cancel := context.WithTimeout(ctx, leaveLeaderWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if leaderAddress, leaderID := s.Raft().LeaderWithID(); leaderAddress != "" && strings.TrimSpace(string(leaderID)) != localNodeID {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("cluster_leave_leadership_transfer_unconfirmed: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) requestRemoval(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, leaveControlTimeout)
	defer cancel()
	conflict, err := CallLeader[RemoveMembershipRequest, PeerRemovalConflict](
		ctx, s, "remove-peer", RemoveMembershipRequest{NodeID: s.LocalNodeID()},
	)
	if errors.Is(err, ErrPeerRemovalBlocked) {
		return &PeerRemovalBlockedError{Conflict: conflict}
	}
	return err
}

// A node can only ask to remove itself.
func (s *Service) removePeerCall(ctx context.Context, issuer string, req RemoveMembershipRequest) (PeerRemovalConflict, error) {
	if strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.NodeID) != issuer {
		return PeerRemovalConflict{}, fmt.Errorf("cluster_leave_issuer_mismatch")
	}
	err := s.RemoveMember(ctx, req.NodeID, true)
	var blocked *PeerRemovalBlockedError
	if errors.As(err, &blocked) {
		return blocked.Conflict, err
	}
	return PeerRemovalConflict{}, err
}

func (s *Service) finishLeave() error {
	if err := s.setLeavePhase(LeavePhaseCleaning, nil); err != nil {
		return err
	}
	if err := s.FinalizeLocalDecluster(); err != nil {
		if recordErr := s.setLeavePhase(LeavePhaseCleaning, err); recordErr != nil {
			logger.L.Warn().Err(recordErr).Msg("cluster_leave_state_update_failed")
		}
		return err
	}
	s.notifyLeaveComplete()
	return nil
}

// CancelLeave abandons a leave that can't finish, typically because the leader
// is gone, and reopens the node. It's refused once the leader removed the
// node: only local cleanup is left then, and the reconciler finishes it. A
// leader that can't be asked can't confirm that, so a node it did remove has
// to be reset afterwards.
func (s *Service) CancelLeave(ctx context.Context) error {
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	switch strings.TrimSpace(record.LeavePhase) {
	case "":
		return nil
	case LeavePhaseCleaning:
		return ErrLeaveAlreadyRemoved
	}
	if removed, err := s.removedFromCluster(ctx); err == nil && removed {
		return ErrLeaveAlreadyRemoved
	}
	return s.clearLeave()
}

func (s *Service) ForceLocalReset(ctx context.Context) error {
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	record, err := s.loadClusterRecord()
	if err != nil {
		return err
	}
	drainCtx, cancel := context.WithTimeout(ctx, leaveDrainTimeout)
	err = s.DrainMutations(drainCtx)
	cancel()
	if err != nil {
		if strings.TrimSpace(record.LeavePhase) == "" {
			s.reopenMutations()
		}
		return &ClusterLeaveError{Code: "cluster_leave_active_mutations", Cause: err}
	}
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	return s.finishLeave()
}

func (s *Service) FinalizeLocalDecluster() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("cluster_service_not_initialized")
	}
	s.mutationGate.Close()
	if err := s.stopRaftRuntime(); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_leave_raft_stop_failed")
	}
	now := time.Now()
	if err := s.DB.Model(&taskModels.GuestLifecycleTask{}).
		Where("status IN ?", []string{taskModels.LifecycleTaskStatusQueued, taskModels.LifecycleTaskStatusRunning}).
		Updates(map[string]any{
			"status":      taskModels.LifecycleTaskStatusFailed,
			"error":       "cluster_declustered",
			"message":     "cluster_declustered",
			"finished_at": &now,
		}).Error; err != nil {
		return fmt.Errorf("cluster_leave_task_cleanup_failed: %w", err)
	}
	if err := s.CleanRaftDir(); err != nil {
		return err
	}
	if err := s.CleanLocalClusterSSHKeys(); err != nil {
		return err
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := clearClusteredDataTx(tx); err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM cluster_nodes").Error; err != nil {
			return fmt.Errorf("failed_to_clean_cluster_nodes: %w", err)
		}
		return markDeclusteredTx(tx)
	}); err != nil {
		return err
	}
	s.joinComplete.Store(false)
	return nil
}

// Only a leader's answer counts, so a node that reaches none can't tell.
func (s *Service) removedFromCluster(ctx context.Context) (bool, error) {
	localNodeID := s.LocalNodeID()
	var hosts []string
	if r := s.Raft(); r != nil && r.State() != raft.Shutdown {
		if leaderAddress, _ := r.LeaderWithID(); leaderAddress != "" {
			hosts = append(hosts, raftAddressHost(string(leaderAddress)))
		}
		if future := r.GetConfiguration(); future.Error() == nil {
			for _, server := range future.Configuration().Servers {
				if strings.TrimSpace(string(server.ID)) != localNodeID {
					hosts = append(hosts, raftAddressHost(string(server.Address)))
				}
			}
		}
	}
	// A joiner may know no member but the one it joined through, and Raft stops
	// on a node that learns it was removed.
	if record, err := s.loadClusterRecord(); err == nil && record.HasIncompleteJoin() {
		hosts = append(hosts, record.JoinLeaderIP)
	}
	lastErr := fmt.Errorf("cluster_membership_unavailable")
	asked := make(map[string]bool, len(hosts))
	for i := 0; i < len(hosts); i++ {
		host := strings.TrimSpace(hosts[i])
		if host == "" || asked[host] {
			continue
		}
		asked[host] = true
		status, err := s.queryMembershipStatus(ctx, host, localNodeID)
		if err == nil {
			return !status.Present, nil
		}
		lastErr = err
		if status.LeaderAddress != "" {
			hosts = append(hosts, raftAddressHost(status.LeaderAddress))
		}
	}
	return false, lastErr
}

func (s *Service) queryMembershipStatus(ctx context.Context, host, nodeID string) (MembershipStatus, error) {
	result := MembershipStatus{NodeID: nodeID}
	payload, err := json.Marshal(struct {
		NodeID string `json:"nodeId"`
	}{NodeID: nodeID})
	if err != nil {
		return result, err
	}
	response, err := s.peerHTTP(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/api/cluster/membership-status", ClusterAPIHost(host)),
		payload,
		map[string]string{"Accept": "application/json", "Content-Type": "application/json"},
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
	if response.StatusCode < 200 || response.StatusCode >= 300 || apiResponse.Status != "success" {
		return result, fmt.Errorf(
			"cluster_membership_status_rejected: status=%d message=%s error=%s",
			response.StatusCode, apiResponse.Message, apiResponse.Error,
		)
	}
	if strings.TrimSpace(result.NodeID) != nodeID || strings.TrimSpace(result.LeaderID) == "" {
		return result, fmt.Errorf("cluster_membership_status_invalid")
	}
	return result, nil
}

func (s *Service) mayHaveBeenRemoved() bool {
	record, err := s.loadClusterRecord()
	if err != nil || !record.Enabled || record.HasIncompleteJoin() || strings.TrimSpace(record.ReaddressPhase) != "" {
		return false
	}
	r := s.Raft()
	if r == nil || r.State() == raft.Shutdown || r.State() == raft.Leader {
		return false
	}
	future := r.GetConfiguration()
	if future.Error() != nil {
		return false
	}
	_, present, err := resolveRaftMember(future.Configuration(), s.LocalNodeID())
	leaderAddress, _ := r.LeaderWithID()
	return err == nil && (!present || leaderAddress == "")
}

func (s *Service) leaveAfterRemoval(ctx context.Context) error {
	s.leaveInitiationMu.Lock()
	defer s.leaveInitiationMu.Unlock()
	drainCtx, cancel := context.WithTimeout(ctx, leaveDrainTimeout)
	if err := s.DrainMutations(drainCtx); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_leave_drain_incomplete")
	}
	cancel()
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	return s.finishLeave()
}

func (s *Service) reconcileLeave(ctx context.Context, lastProbe *time.Time) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status, err := s.LeaveStatus()
	if err != nil {
		return
	}
	if status.Phase != "" {
		if err := s.advanceLeave(ctx); err != nil {
			logger.L.Debug().Err(err).Msg("cluster_leave_reconcile_deferred")
		}
		return
	}
	if time.Since(*lastProbe) < membershipProbeInterval || !s.mayHaveBeenRemoved() {
		return
	}
	*lastProbe = time.Now()
	if removed, err := s.removedFromCluster(ctx); err != nil || !removed {
		return
	}
	logger.L.Warn().Msg("cluster_node_removed_by_cluster")
	if err := s.leaveAfterRemoval(ctx); err != nil {
		logger.L.Warn().Err(err).Msg("cluster_leave_after_removal_deferred")
	}
}

func (s *Service) startLeaveReconciler(ctx context.Context) {
	go func() {
		var lastProbe time.Time
		for {
			s.reconcileLeave(ctx, &lastProbe)
			select {
			case <-ctx.Done():
				return
			case <-time.After(leaveReconcileInterval):
			}
		}
	}()
}
