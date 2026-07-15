// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const (
	DefaultReplicationLeaseTTL                = 10 * time.Second
	DefaultReplicationRenewWindow             = 3 * time.Second
	ReplicationVMFilesystemStorageUnsupported = vmModels.ReplicationFilesystemStorageUnsupported
)

type replicationPolicyMutationIntent string

const (
	replicationPolicyMutationCreate replicationPolicyMutationIntent = "create"
	replicationPolicyMutationUpdate replicationPolicyMutationIntent = "update"
)

func (s *Service) ListReplicationPolicies() ([]clusterModels.ReplicationPolicy, error) {
	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Preload("Targets").Order("name ASC").Find(&policies).Error; err != nil {
		return policies, err
	}

	runtimeSnapshot := s.buildReplicationHARuntimeSnapshot()
	for idx := range policies {
		eval := s.evaluateReplicationPolicyHA(&policies[idx], ReplicationPolicyHAEvalOptions{
			RuntimeSnapshot: &runtimeSnapshot,
		})
		s.ApplyReplicationPolicyHAState(&policies[idx], eval)
	}

	return policies, nil
}

func (s *Service) GetReplicationPolicyByID(id uint) (*clusterModels.ReplicationPolicy, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_policy_id")
	}

	var policy clusterModels.ReplicationPolicy
	if err := s.DB.Preload("Targets").First(&policy, id).Error; err != nil {
		return nil, err
	}
	eval := s.EvaluateReplicationPolicyHA(&policy)
	s.ApplyReplicationPolicyHAState(&policy, eval)
	return &policy, nil
}

func (s *Service) ProposeReplicationPolicyCreate(input clusterServiceInterfaces.ReplicationPolicyReq, bypassRaft bool) error {
	id, err := s.newRaftObjectID("replication_policies")
	if err != nil {
		return fmt.Errorf("new_replication_policy_id_failed: %w", err)
	}

	policy, targets, err := s.buildReplicationPolicy(id, input, replicationPolicyMutationCreate)
	if err != nil {
		return err
	}

	if bypassRaft {
		return clusterModels.UpsertReplicationPolicyTxn(s.DB, policy, targets)
	}

	data, err := json.Marshal(clusterModels.ReplicationPolicyPayload{
		Policy:  *policy,
		Targets: targets,
	})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy",
		Action: "create",
		Data:   data,
	})
}

func (s *Service) ProposeReplicationPolicyUpdate(id uint, input clusterServiceInterfaces.ReplicationPolicyReq, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	policy, targets, err := s.buildReplicationPolicy(id, input, replicationPolicyMutationUpdate)
	if err != nil {
		return err
	}

	payload := clusterModels.ReplicationPolicyPayload{
		Policy:             *policy,
		Targets:            targets,
		ExpectedOwnerEpoch: policy.OwnerEpoch,
	}
	if bypassRaft {
		return clusterModels.UpdateReplicationPolicyTxn(s.DB, &payload)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) ProposeReplicationPolicyDelete(id uint, bypassRaft bool) error {
	if id == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	if !bypassRaft {
		runtimeSnapshot := s.buildReplicationHARuntimeSnapshot()
		if !runtimeSnapshot.QuorumAvailable {
			return NewReplicationHAIneligibleError([]string{ReplicationHAReasonQuorumLost})
		}
	}

	if bypassRaft {
		return clusterModels.DeleteReplicationPolicyTxn(s.DB, id)
	}

	data, err := json.Marshal(struct {
		ID uint `json:"id"`
	}{ID: id})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_delete_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy",
		Action: "delete",
		Data:   data,
	})
}

type ReplicationPolicyRuntimeState struct {
	ID         uint       `json:"id"`
	LastRunAt  *time.Time `json:"lastRunAt"`
	LastStatus string     `json:"lastStatus"`
	LastError  string     `json:"lastError"`
	NextRunAt  *time.Time `json:"nextRunAt"`
}

func (s *Service) ProposeReplicationPolicyStateUpdate(update ReplicationPolicyRuntimeState, bypassRaft bool) error {
	if update.ID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if update.LastStatus == "" {
		return fmt.Errorf("last_status_required")
	}

	if bypassRaft {
		return s.DB.Model(&clusterModels.ReplicationPolicy{}).Where("id = ?", update.ID).Updates(map[string]any{
			"last_run_at": update.LastRunAt,
			"last_status": update.LastStatus,
			"last_error":  update.LastError,
			"next_run_at": update.NextRunAt,
		}).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_policy_state_update: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy",
		Action: "state_update",
		Data:   data,
	})
}

// guestExistsInCluster checks whether a guest (VM or jail) exists on any
// node in the cluster by aggregating per-node resources.
func (s *Service) guestExistsInCluster(guestType string, guestID uint) (bool, error) {
	nodeID, err := s.ResolveReplicationGuestOwnerNode(guestType, guestID)
	if err != nil {
		return false, err
	}
	return nodeID != "", nil
}

func (s *Service) buildReplicationPolicy(
	id uint,
	input clusterServiceInterfaces.ReplicationPolicyReq,
	intent replicationPolicyMutationIntent,
) (*clusterModels.ReplicationPolicy, []clusterModels.ReplicationPolicyTarget, error) {
	if intent != replicationPolicyMutationCreate && intent != replicationPolicyMutationUpdate {
		return nil, nil, fmt.Errorf("invalid_replication_policy_mutation_intent")
	}
	if id == 0 {
		return nil, nil, fmt.Errorf("invalid_policy_id")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, nil, fmt.Errorf("name_required")
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 1024 {
		return nil, nil, fmt.Errorf("description_too_long")
	}

	guestType := strings.TrimSpace(strings.ToLower(input.GuestType))
	if guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail {
		return nil, nil, fmt.Errorf("invalid_guest_type")
	}
	if input.GuestID == 0 {
		return nil, nil, fmt.Errorf("guest_id_required")
	}

	var resolvedCreateOwner string
	var resourceSnapshot []clusterServiceInterfaces.NodeResources
	resourceSnapshotLoaded := false
	if intent == replicationPolicyMutationCreate {
		resources, err := s.Resources()
		if err != nil {
			return nil, nil, fmt.Errorf("failed_to_verify_guest: %w", err)
		}
		resourceSnapshot = resources
		resourceSnapshotLoaded = true
		owners := replicationGuestOwnerMatches(resources, guestType, input.GuestID)
		switch len(owners) {
		case 0:
			return nil, nil, fmt.Errorf("guest_not_found")
		case 1:
			resolvedCreateOwner = owners[0]
		default:
			return nil, nil, fmt.Errorf("guest_owner_ambiguous")
		}
	}

	sourceMode := strings.TrimSpace(strings.ToLower(input.SourceMode))
	if sourceMode == "" {
		sourceMode = clusterModels.ReplicationSourceModeFollowActive
	}
	if sourceMode != clusterModels.ReplicationSourceModeFollowActive && sourceMode != clusterModels.ReplicationSourceModePinned {
		return nil, nil, fmt.Errorf("invalid_source_mode")
	}

	failbackMode := strings.TrimSpace(strings.ToLower(input.FailbackMode))
	if failbackMode == "" {
		failbackMode = clusterModels.ReplicationFailbackManual
	}
	if failbackMode != clusterModels.ReplicationFailbackManual && failbackMode != clusterModels.ReplicationFailbackAuto {
		return nil, nil, fmt.Errorf("invalid_failback_mode")
	}

	sourceNodeID := strings.TrimSpace(input.SourceNodeID)
	var existingByID clusterModels.ReplicationPolicy
	existingByIDFound := false
	if intent == replicationPolicyMutationUpdate {
		if err := s.DB.First(&existingByID, id).Error; err == nil {
			existingByIDFound = true
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("replication_policy_not_found")
		} else {
			return nil, nil, fmt.Errorf("failed_to_load_replication_policy: %w", err)
		}
		if existingByID.GuestType != guestType || existingByID.GuestID != input.GuestID {
			return nil, nil, fmt.Errorf("replication_policy_guest_identity_immutable")
		}
		if existingByID.ProtectionState == clusterModels.ReplicationProtectionStateDeleting {
			return nil, nil, fmt.Errorf("replication_policy_deleting")
		}
	}

	failoverMode := strings.TrimSpace(strings.ToLower(input.FailoverMode))
	if failoverMode == "" && existingByIDFound {
		failoverMode = strings.TrimSpace(strings.ToLower(existingByID.FailoverMode))
	}
	if failoverMode == "" {
		failoverMode = clusterModels.ReplicationFailoverManual
	}
	if failoverMode != clusterModels.ReplicationFailoverManual &&
		failoverMode != clusterModels.ReplicationFailoverAutoSafe &&
		failoverMode != clusterModels.ReplicationFailoverAutoForce {
		return nil, nil, fmt.Errorf("invalid_failover_mode")
	}

	if sourceMode == clusterModels.ReplicationSourceModePinned {
		if sourceNodeID == "" && existingByIDFound {
			sourceNodeID = strings.TrimSpace(existingByID.SourceNodeID)
		}
		if sourceNodeID == "" {
			return nil, nil, fmt.Errorf("source_node_required_for_pinned_mode")
		}
		if !s.backupRunnerNodeExists(sourceNodeID) {
			return nil, nil, fmt.Errorf("source_node_not_found")
		}
	}

	if sourceMode == clusterModels.ReplicationSourceModeFollowActive {
		if intent == replicationPolicyMutationCreate {
			sourceNodeID = resolvedCreateOwner
		} else if sourceNodeID == "" {
			sourceNodeID = strings.TrimSpace(existingByID.SourceNodeID)
		}
		if sourceNodeID == "" {
			resolvedOwner, err := s.ResolveReplicationGuestOwnerNode(guestType, input.GuestID)
			if err != nil {
				return nil, nil, err
			}
			sourceNodeID = strings.TrimSpace(resolvedOwner)
		}
		if sourceNodeID == "" {
			return nil, nil, fmt.Errorf("guest_owner_node_not_found")
		}
		if !s.backupRunnerNodeExists(sourceNodeID) {
			return nil, nil, fmt.Errorf("source_node_not_found")
		}
	}

	targets, err := s.buildReplicationTargets(id, input.Targets)
	if err != nil {
		return nil, nil, err
	}

	enabled := true
	if existingByIDFound {
		enabled = existingByID.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if enabled && guestType == clusterModels.ReplicationGuestTypeVM {
		if !resourceSnapshotLoaded {
			resources, err := s.Resources()
			if err != nil {
				return nil, nil, fmt.Errorf("replication_vm_storage_eligibility_unavailable: %w", err)
			}
			resourceSnapshot = resources
			resourceSnapshotLoaded = true
		}
		ownerNodeID := resolvedCreateOwner
		if ownerNodeID == "" && existingByIDFound {
			ownerNodeID = strings.TrimSpace(existingByID.ActiveNodeID)
			if ownerNodeID == "" {
				ownerNodeID = strings.TrimSpace(existingByID.SourceNodeID)
			}
		}
		if err := requireReplicationVMStorageEligibility(
			resourceSnapshot, ownerNodeID, input.GuestID,
		); err != nil {
			return nil, nil, err
		}
	}

	cronExpr := strings.TrimSpace(input.CronExpr)
	var next *time.Time
	if cronExpr != "" {
		schedule, err := cron.ParseStandard(cronExpr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid_cron_expr")
		}
		if enabled {
			n := schedule.Next(time.Now().UTC())
			next = &n
		}
	}

	activeNodeID := resolvedCreateOwner
	ownerEpoch := uint64(1)
	if existingByIDFound {
		previousActiveNodeID := strings.TrimSpace(existingByID.ActiveNodeID)
		if previousActiveNodeID != "" {
			activeNodeID = previousActiveNodeID
		}
		if existingByID.OwnerEpoch > 0 {
			ownerEpoch = existingByID.OwnerEpoch
		}
	}
	if activeNodeID == "" {
		return nil, nil, fmt.Errorf("guest_owner_node_not_found")
	}

	protectionState := clusterModels.ReplicationProtectionStateInitializing
	if !enabled {
		protectionState = clusterModels.ReplicationProtectionStateUnprotected
	} else if existingByIDFound {
		protectionState = existingByID.ProtectionState
		if !existingByID.Enabled {
			protectionState = clusterModels.ReplicationProtectionStateInitializing
		}
	}

	crashRecovery := resolveOptional(existingByIDFound, existingByID.CrashRecovery, input.CrashRecovery, true)
	crashRestartMax := resolveOptional(existingByIDFound, existingByID.CrashRestartMax, input.CrashRestartMax, 3)
	poolHealthCheck := resolveOptional(existingByIDFound, existingByID.PoolHealthCheck, input.PoolHealthCheck, true)
	poolCapacityPct := resolveOptional(existingByIDFound, existingByID.PoolCapacityPct, input.PoolCapacityPct, 90)

	policy := &clusterModels.ReplicationPolicy{
		ID:              id,
		Name:            name,
		Description:     description,
		GuestType:       guestType,
		GuestID:         input.GuestID,
		SourceNodeID:    sourceNodeID,
		ActiveNodeID:    activeNodeID,
		OwnerEpoch:      ownerEpoch,
		SourceMode:      sourceMode,
		FailbackMode:    failbackMode,
		FailoverMode:    failoverMode,
		CronExpr:        cronExpr,
		Enabled:         enabled,
		ProtectionState: protectionState,
		CrashRecovery:   crashRecovery,
		CrashRestartMax: crashRestartMax,
		PoolHealthCheck: poolHealthCheck,
		PoolCapacityPct: poolCapacityPct,
		NextRunAt:       next,
	}

	// Preserve transition state from the existing row.
	// The OnConflict.DoUpdates list already excludes transition columns
	// so the DB preserves them on UPDATE, but explicitly carrying them
	// forward guards against GORM edge cases and the INSERT branch.
	if existingByIDFound {
		policy.TransitionState = existingByID.TransitionState
		policy.TransitionRunID = existingByID.TransitionRunID
		policy.TransitionReason = existingByID.TransitionReason
		policy.TransitionSourceNodeID = existingByID.TransitionSourceNodeID
		policy.TransitionTargetNodeID = existingByID.TransitionTargetNodeID
		policy.TransitionOwnerEpoch = existingByID.TransitionOwnerEpoch
		policy.TransitionRequestedAt = existingByID.TransitionRequestedAt
		policy.TransitionDemotedAt = existingByID.TransitionDemotedAt
		policy.TransitionCatchupAt = existingByID.TransitionCatchupAt
		policy.TransitionPromotedAt = existingByID.TransitionPromotedAt
		policy.TransitionCompletedAt = existingByID.TransitionCompletedAt
		policy.TransitionError = existingByID.TransitionError
		policy.TransitionAllowUnsafe = existingByID.TransitionAllowUnsafe
		policy.TransitionMovePinnedSource = existingByID.TransitionMovePinnedSource
		policy.TransitionTriggerValidationRun = existingByID.TransitionTriggerValidationRun
		policy.TransitionOriginalRunning = existingByID.TransitionOriginalRunning
	}

	var existing clusterModels.ReplicationPolicy
	err = s.DB.Where("guest_type = ? AND guest_id = ?", guestType, input.GuestID).First(&existing).Error
	if err == nil && existing.ID != id {
		return nil, nil, fmt.Errorf("guest_already_protected_by_policy")
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, fmt.Errorf("failed_to_check_existing_policy: %w", err)
	}

	haEval := s.EvaluateReplicationPolicyHAWithTargets(policy, targets)
	if !haEval.Eligible {
		return nil, nil, NewReplicationHAIneligibleError(haEval.Reasons)
	}

	return policy, targets, nil
}

func (s *Service) ResolveReplicationGuestOwnerNode(guestType string, guestID uint) (string, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if guestID == 0 {
		return "", fmt.Errorf("guest_id_required")
	}
	if guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail {
		return "", fmt.Errorf("invalid_guest_type")
	}

	matches, err := s.resolveReplicationGuestOwnerMatches(guestType, guestID)
	if err != nil {
		return "", err
	}

	statusByNode := map[string]string{}
	nodes, err := s.Nodes()
	if err == nil {
		for _, node := range nodes {
			statusByNode[strings.TrimSpace(node.NodeUUID)] = strings.TrimSpace(strings.ToLower(node.Status))
		}
	}

	if len(matches) == 0 {
		return "", nil
	}

	online := make([]string, 0, len(matches))
	for _, nodeID := range matches {
		if status, ok := statusByNode[nodeID]; ok && status == "online" {
			online = append(online, nodeID)
		}
	}

	if len(online) > 0 {
		sort.Strings(online)
		return online[0], nil
	}

	sort.Strings(matches)
	return matches[0], nil
}

func (s *Service) resolveReplicationGuestOwnerMatches(guestType string, guestID uint) ([]string, error) {
	resources, err := s.Resources()
	if err != nil {
		return nil, fmt.Errorf("resolve_guest_owner_resources_failed: %w", err)
	}
	return replicationGuestOwnerMatches(resources, guestType, guestID), nil
}

func replicationGuestOwnerMatches(
	resources []clusterServiceInterfaces.NodeResources,
	guestType string,
	guestID uint,
) []string {
	seen := make(map[string]struct{}, len(resources))
	matches := make([]string, 0, len(resources))
	for _, node := range resources {
		nodeID := strings.TrimSpace(node.NodeUUID)
		if nodeID == "" {
			continue
		}

		found := false
		switch guestType {
		case clusterModels.ReplicationGuestTypeVM:
			for _, vm := range node.VMs {
				if vm.RID == guestID {
					found = true
					break
				}
			}
		case clusterModels.ReplicationGuestTypeJail:
			for _, jail := range node.Jails {
				if jail.CTID == guestID {
					found = true
					break
				}
			}
		}

		if found {
			if _, exists := seen[nodeID]; !exists {
				seen[nodeID] = struct{}{}
				matches = append(matches, nodeID)
			}
		}
	}
	sort.Strings(matches)
	return matches
}

func requireReplicationVMStorageEligibility(
	resources []clusterServiceInterfaces.NodeResources,
	ownerNodeID string,
	rid uint,
) error {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if ownerNodeID == "" || rid == 0 {
		return fmt.Errorf("replication_vm_storage_eligibility_unavailable")
	}
	found := false
	for _, node := range resources {
		if strings.TrimSpace(node.NodeUUID) != ownerNodeID {
			continue
		}
		for _, vm := range node.VMs {
			if vm.RID != rid {
				continue
			}
			if found {
				return fmt.Errorf("replication_vm_storage_eligibility_ambiguous")
			}
			found = true
			if vm.HasEnabledFilesystemStorage {
				return fmt.Errorf(ReplicationVMFilesystemStorageUnsupported)
			}
		}
	}
	if !found {
		return fmt.Errorf("replication_vm_storage_eligibility_unavailable")
	}
	return nil
}

func (s *Service) buildReplicationTargets(policyID uint, input []clusterServiceInterfaces.ReplicationPolicyTargetReq) ([]clusterModels.ReplicationPolicyTarget, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("targets_required")
	}

	seen := make(map[string]struct{}, len(input))
	targets := make([]clusterModels.ReplicationPolicyTarget, 0, len(input))
	for _, t := range input {
		nodeID := strings.TrimSpace(t.NodeID)
		if nodeID == "" {
			return nil, fmt.Errorf("target_node_required")
		}
		if !s.backupRunnerNodeExists(nodeID) {
			return nil, fmt.Errorf("target_node_not_found")
		}
		if _, ok := seen[nodeID]; ok {
			return nil, fmt.Errorf("duplicate_target_node")
		}
		seen[nodeID] = struct{}{}

		weight := t.Weight
		if weight == 0 {
			weight = 100
		}
		targets = append(targets, clusterModels.ReplicationPolicyTarget{
			PolicyID: policyID,
			NodeID:   nodeID,
			Weight:   weight,
		})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Weight == targets[j].Weight {
			return targets[i].NodeID < targets[j].NodeID
		}
		return targets[i].Weight > targets[j].Weight
	})

	return targets, nil
}

func (s *Service) ListReplicationLeases() ([]clusterModels.ReplicationLease, error) {
	var leases []clusterModels.ReplicationLease
	if err := s.DB.Order("policy_id ASC").Find(&leases).Error; err != nil {
		return nil, err
	}
	return leases, nil
}

func (s *Service) GetReplicationLeaseByPolicyID(policyID uint) (*clusterModels.ReplicationLease, error) {
	if policyID == 0 {
		return nil, fmt.Errorf("invalid_policy_id")
	}

	var lease clusterModels.ReplicationLease
	if err := s.DB.Where("policy_id = ?", policyID).First(&lease).Error; err != nil {
		return nil, err
	}
	return &lease, nil
}

func (s *Service) UpsertReplicationLease(lease clusterModels.ReplicationLease, bypassRaft bool) error {
	if bypassRaft {
		return clusterModels.UpsertReplicationLeaseTxn(s.DB, &lease)
	}

	data, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_lease: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_lease",
		Action: "upsert",
		Data:   data,
	})
}

func (s *Service) UpsertReplicationLeasesBatch(leases []clusterModels.ReplicationLease) error {
	if len(leases) == 0 {
		return nil
	}

	data, err := json.Marshal(leases)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_leases_batch: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_lease",
		Action: "upsert_batch",
		Data:   data,
	})
}

func (s *Service) DeleteReplicationLease(policyID uint, bypassRaft bool) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	if bypassRaft {
		return s.DB.Where("policy_id = ?", policyID).Delete(&clusterModels.ReplicationLease{}).Error
	}

	data, err := json.Marshal(struct {
		PolicyID uint `json:"policyId"`
	}{PolicyID: policyID})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_lease_delete_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_lease",
		Action: "delete",
		Data:   data,
	})
}

func (s *Service) requireReplicationRaftLeader() error {
	if s == nil || s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}
	if s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	return nil
}

func (s *Service) UpdateReplicationPolicyTransition(
	policyID uint,
	transition clusterModels.ReplicationPolicyTransition,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}

	data, err := json.Marshal(struct {
		PolicyID   uint                                      `json:"policyId"`
		Transition clusterModels.ReplicationPolicyTransition `json:"transition"`
	}{
		PolicyID:   policyID,
		Transition: transition,
	})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_transition_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy_transition",
		Action: "update",
		Data:   data,
	})
}

// BeginReplicationPolicyTransition durably acquires a policy transition lock.
// The same RunID may be replayed, while a competing RunID is rejected until
// the persisted transition reaches a terminal state.
func (s *Service) BeginReplicationPolicyTransition(
	begin clusterModels.ReplicationPolicyTransitionBegin,
	bypassRaft bool,
) error {
	if bypassRaft {
		return clusterModels.BeginReplicationPolicyTransitionTxn(s.DB, &begin)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}

	data, err := json.Marshal(begin)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_transition_begin: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy_transition",
		Action: "begin",
		Data:   data,
	})
}

// CommitReplicationOwnershipTransition performs the ownership cutover as one
// Raft/FSM transaction. Callers must first persist the transition run and pass
// its run ID in ExpectedTransitionRunID.
func (s *Service) CommitReplicationOwnershipTransition(
	payload clusterModels.ReplicationOwnershipTransitionPayload,
	bypassRaft bool,
) error {
	if bypassRaft {
		return clusterModels.ApplyReplicationOwnershipTransitionTxn(s.DB, &payload)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_ownership_transition: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_ownership_transition",
		Action: "commit",
		Data:   data,
	})
}

func (s *Service) ReassignDisabledReplicationPolicyOwner(
	payload clusterModels.ReplicationDisabledOwnerReassignment,
	bypassRaft bool,
) error {
	if bypassRaft {
		return clusterModels.ReassignDisabledReplicationPolicyOwnerTxn(s.DB, &payload)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_disabled_replication_owner_reassignment: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "replication_ownership_transition", Action: "reassign_disabled", Data: data,
	})
}

func (s *Service) AcquireReplicationGuestOperation(
	payload clusterModels.ReplicationGuestOperationAcquire,
	bypassRaft bool,
) error {
	if payload.AcquiredAt.IsZero() {
		payload.AcquiredAt = time.Now().UTC()
	}
	if bypassRaft {
		return clusterModels.AcquireReplicationGuestOperationTxn(s.DB, &payload)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_guest_operation_acquire: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "replication_guest_operation", Action: "acquire", Data: data,
	})
}

func (s *Service) applyReplicationGuestOperationTransition(
	action string,
	payload clusterModels.ReplicationGuestOperationTransition,
	bypassRaft bool,
) error {
	if (action == "seal" || action == "complete") && payload.OccurredAt.IsZero() {
		payload.OccurredAt = time.Now().UTC()
	}
	if bypassRaft {
		switch action {
		case "seal":
			return clusterModels.SealReplicationGuestOperationTxn(s.DB, &payload)
		case "abort":
			return clusterModels.AbortReplicationGuestOperationTxn(s.DB, &payload)
		case "complete":
			return clusterModels.CompleteReplicationGuestOperationTxn(s.DB, &payload)
		default:
			return fmt.Errorf("invalid_replication_guest_operation_action")
		}
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_guest_operation_%s: %w", action, err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type: "replication_guest_operation", Action: action, Data: data,
	})
}

func (s *Service) SealReplicationGuestOperation(payload clusterModels.ReplicationGuestOperationTransition, bypassRaft bool) error {
	return s.applyReplicationGuestOperationTransition("seal", payload, bypassRaft)
}

func (s *Service) AbortReplicationGuestOperation(payload clusterModels.ReplicationGuestOperationTransition, bypassRaft bool) error {
	return s.applyReplicationGuestOperationTransition("abort", payload, bypassRaft)
}

func (s *Service) CompleteReplicationGuestOperation(payload clusterModels.ReplicationGuestOperationTransition, bypassRaft bool) error {
	return s.applyReplicationGuestOperationTransition("complete", payload, bypassRaft)
}

// UpdateReplicationTargetReadiness publishes a verified per-target generation
// through Raft. ExpectedOwnerEpoch rejects late results from an old owner.
func (s *Service) UpdateReplicationTargetReadiness(
	update clusterModels.ReplicationTargetReadinessUpdate,
	bypassRaft bool,
) error {
	if update.EvaluatedAt.IsZero() {
		update.EvaluatedAt = time.Now().UTC()
	}
	if bypassRaft {
		return clusterModels.UpdateReplicationTargetReadinessTxn(s.DB, &update)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_target_readiness: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_target_readiness",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) UpdateReplicationPolicyProtectionState(
	policyID uint,
	expectedOwnerEpoch uint64,
	state string,
	bypassRaft bool,
) error {
	update := clusterModels.ReplicationPolicyProtectionStateUpdate{
		PolicyID:           policyID,
		ExpectedOwnerEpoch: expectedOwnerEpoch,
		State:              state,
	}
	if bypassRaft {
		return clusterModels.UpdateReplicationPolicyProtectionStateTxn(s.DB, &update)
	}
	if err := s.requireReplicationRaftLeader(); err != nil {
		return err
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_replication_policy_protection_state: %w", err)
	}
	return s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_policy_protection_state",
		Action: "update",
		Data:   data,
	})
}

func (s *Service) CreateOrUpdateReplicationEvent(event clusterModels.ReplicationEvent, bypassRaft bool) (uint, error) {
	action := "create"
	if event.ID == 0 {
		id, err := s.newRaftObjectID("replication_events")
		if err != nil {
			return 0, fmt.Errorf("new_replication_event_id_failed: %w", err)
		}
		event.ID = id
	} else {
		action = "update"
	}

	if bypassRaft {
		if err := s.DB.Save(&event).Error; err != nil {
			return 0, err
		}
		return event.ID, nil
	}

	data, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("failed_to_marshal_replication_event_payload: %w", err)
	}

	if err := s.applyRaftCommand(clusterModels.Command{
		Type:   "replication_event",
		Action: action,
		Data:   data,
	}); err != nil {
		return 0, err
	}

	return event.ID, nil
}

func (s *Service) ListReplicationEvents(limit int, policyID uint) ([]clusterModels.ReplicationEvent, error) {
	if limit <= 0 {
		limit = defaultEventListLimit
	}
	query := s.DB.Order("started_at DESC").Limit(limit)
	if policyID > 0 {
		query = query.Where("policy_id = ?", policyID)
	}

	var events []clusterModels.ReplicationEvent
	if err := query.Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Service) GetReplicationEventByID(id uint) (*clusterModels.ReplicationEvent, error) {
	if id == 0 {
		return nil, fmt.Errorf("invalid_event_id")
	}
	var event clusterModels.ReplicationEvent
	if err := s.DB.First(&event, id).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *Service) ListClusterSSHIdentities() ([]clusterModels.ClusterSSHIdentity, error) {
	var identities []clusterModels.ClusterSSHIdentity
	if err := s.DB.Order("node_uuid ASC").Find(&identities).Error; err != nil {
		return nil, err
	}
	return identities, nil
}

func (s *Service) UpsertClusterSSHIdentity(identity clusterModels.ClusterSSHIdentity, bypassRaft bool) error {
	if bypassRaft {
		return clusterModels.UpsertClusterSSHIdentityTxn(s.DB, &identity)
	}

	data, err := json.Marshal(identity)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_cluster_ssh_identity_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "cluster_ssh_identity",
		Action: "upsert",
		Data:   data,
	})
}

func (s *Service) DeleteClusterSSHIdentity(nodeUUID string, bypassRaft bool) error {
	nodeUUID = strings.TrimSpace(nodeUUID)
	if nodeUUID == "" {
		return nil
	}

	if bypassRaft {
		return s.DB.Where("node_uuid = ?", nodeUUID).Delete(&clusterModels.ClusterSSHIdentity{}).Error
	}

	data, err := json.Marshal(struct {
		NodeUUID string `json:"nodeUUID"`
	}{NodeUUID: nodeUUID})
	if err != nil {
		return fmt.Errorf("failed_to_marshal_cluster_ssh_identity_delete_payload: %w", err)
	}

	return s.applyRaftCommand(clusterModels.Command{
		Type:   "cluster_ssh_identity",
		Action: "delete",
		Data:   data,
	})
}

func (s *Service) ResolveSSHHostForNode(nodeID string) (string, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "", fmt.Errorf("node_id_required")
	}

	nodes, err := s.Nodes()
	if err == nil {
		for _, node := range nodes {
			if strings.TrimSpace(node.NodeUUID) != nodeID {
				continue
			}
			host, _, splitErr := net.SplitHostPort(strings.TrimSpace(node.API))
			if splitErr == nil && strings.TrimSpace(host) != "" {
				return strings.TrimSpace(host), nil
			}
		}
	}

	if s.Raft != nil {
		fut := s.Raft.GetConfiguration()
		if fut.Error() == nil {
			for _, server := range fut.Configuration().Servers {
				if string(server.ID) != nodeID {
					continue
				}
				host, _, splitErr := net.SplitHostPort(string(server.Address))
				if splitErr == nil && strings.TrimSpace(host) != "" {
					return strings.TrimSpace(host), nil
				}
				if strings.TrimSpace(string(server.Address)) != "" {
					return strings.TrimSpace(string(server.Address)), nil
				}
			}
		}
	}

	return "", fmt.Errorf("node_host_not_found")
}

func (s *Service) CanLocalNodeStartProtectedGuest(guestType string, guestID uint) (bool, error) {
	detail := s.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return false, fmt.Errorf("local_node_id_unavailable")
	}
	return CanNodeStartProtectedGuest(s.DB, guestType, guestID, strings.TrimSpace(detail.NodeID))
}

func (s *Service) CanLocalNodeStartProtectedGuestForTransition(
	guestType string,
	guestID uint,
	transitionRunID string,
) (bool, error) {
	detail := s.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return false, fmt.Errorf("local_node_id_unavailable")
	}
	return CanNodeStartProtectedGuestForTransition(
		s.DB,
		guestType,
		guestID,
		strings.TrimSpace(detail.NodeID),
		transitionRunID,
	)
}

func (s *Service) LocalNodeID() string {
	detail := s.Detail()
	if detail == nil {
		return ""
	}
	return strings.TrimSpace(detail.NodeID)
}

func (s *Service) LocalNodeIsLeader() bool {
	return s.Raft != nil && s.Raft.State() == raft.Leader
}

func resolveOptional[T any](hasExisting bool, existing T, input *T, defaultVal T) T {
	if input != nil {
		return *input
	}
	if hasExisting {
		return existing
	}
	return defaultVal
}
