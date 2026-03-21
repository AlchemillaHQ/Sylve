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
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/hashicorp/raft"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

const (
	DefaultReplicationLeaseTTL    = 10 * time.Second
	DefaultReplicationRenewWindow = 3 * time.Second
)

func (s *Service) ListReplicationPolicies() ([]clusterModels.ReplicationPolicy, error) {
	s.cleanupOrphanReplicationRows()

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
	id := uint(0)
	var err error
	if !bypassRaft {
		id, err = s.newRaftObjectID("replication_policies")
		if err != nil {
			return fmt.Errorf("new_replication_policy_id_failed: %w", err)
		}
	}

	policy, targets, err := s.buildReplicationPolicy(id, input)
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

	policy, targets, err := s.buildReplicationPolicy(id, input)
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
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("policy_id = ?", id).Delete(&clusterModels.ReplicationPolicyTarget{}).Error; err != nil {
				return err
			}
			if err := tx.Where("policy_id = ?", id).Delete(&clusterModels.ReplicationLease{}).Error; err != nil {
				return err
			}
			if err := tx.Where("policy_id = ?", id).Delete(&clusterModels.ReplicationEvent{}).Error; err != nil {
				return err
			}
			if err := tx.Where("policy_id = ?", id).Delete(&clusterModels.ReplicationReceipt{}).Error; err != nil {
				return err
			}
			return tx.Delete(&clusterModels.ReplicationPolicy{}, id).Error
		})
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

func (s *Service) buildReplicationPolicy(id uint, input clusterServiceInterfaces.ReplicationPolicyReq) (*clusterModels.ReplicationPolicy, []clusterModels.ReplicationPolicyTarget, error) {
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
	if id > 0 {
		if err := s.DB.First(&existingByID, id).Error; err == nil {
			existingByIDFound = true
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
		if sourceNodeID == "" {
			return nil, nil, fmt.Errorf("source_node_required_for_pinned_mode")
		}
		if !s.backupRunnerNodeExists(sourceNodeID) {
			return nil, nil, fmt.Errorf("source_node_not_found")
		}
	}

	if sourceMode == clusterModels.ReplicationSourceModeFollowActive {
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
	if input.Enabled != nil {
		enabled = *input.Enabled
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

	activeNodeID := sourceNodeID
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
	if overrideActiveNodeID := strings.TrimSpace(input.ActiveNodeID); overrideActiveNodeID != "" {
		activeNodeID = overrideActiveNodeID
	}
	if input.OwnerEpoch > 0 {
		ownerEpoch = input.OwnerEpoch
	}

	policy := &clusterModels.ReplicationPolicy{
		ID:           id,
		Name:         name,
		Description:  description,
		GuestType:    guestType,
		GuestID:      input.GuestID,
		SourceNodeID: sourceNodeID,
		ActiveNodeID: activeNodeID,
		OwnerEpoch:   ownerEpoch,
		SourceMode:   sourceMode,
		FailbackMode: failbackMode,
		FailoverMode: failoverMode,
		CronExpr:     cronExpr,
		Enabled:      enabled,
		NextRunAt:    next,
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

	statusByNode := map[string]string{}
	nodes, err := s.Nodes()
	if err == nil {
		for _, node := range nodes {
			statusByNode[strings.TrimSpace(node.NodeUUID)] = strings.TrimSpace(strings.ToLower(node.Status))
		}
	}

	resources, err := s.Resources()
	if err != nil {
		return "", fmt.Errorf("resolve_guest_owner_resources_failed: %w", err)
	}

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
			matches = append(matches, nodeID)
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

func (s *Service) UpdateReplicationPolicyTransition(
	policyID uint,
	transition clusterModels.ReplicationPolicyTransition,
	bypassRaft bool,
) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}

	if bypassRaft {
		return clusterModels.UpsertReplicationPolicyTransitionTxn(s.DB, policyID, &transition)
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
	s.cleanupOrphanReplicationRows()

	if limit <= 0 {
		limit = 200
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

func (s *Service) ListReplicationReceipts(policyID uint) ([]clusterModels.ReplicationReceipt, error) {
	s.cleanupOrphanReplicationRows()

	query := s.DB.Order("last_attempt_at DESC")
	if policyID > 0 {
		query = query.Where("policy_id = ?", policyID)
	}

	var receipts []clusterModels.ReplicationReceipt
	if err := query.Find(&receipts).Error; err != nil {
		return nil, err
	}
	if len(receipts) == 0 {
		return receipts, nil
	}

	policyIDSet := make(map[uint]struct{}, len(receipts))
	for _, receipt := range receipts {
		if receipt.PolicyID > 0 {
			policyIDSet[receipt.PolicyID] = struct{}{}
		}
	}

	policyIDs := make([]uint, 0, len(policyIDSet))
	for id := range policyIDSet {
		policyIDs = append(policyIDs, id)
	}

	type policyIntervalRow struct {
		ID       uint
		CronExpr string
	}
	var intervalRows []policyIntervalRow
	if len(policyIDs) > 0 {
		if err := s.DB.
			Model(&clusterModels.ReplicationPolicy{}).
			Select("id", "cron_expr").
			Where("id IN ?", policyIDs).
			Find(&intervalRows).Error; err != nil {
			return nil, err
		}
	}

	freshnessByPolicyID := make(map[uint]int64, len(intervalRows))
	for _, row := range intervalRows {
		windowSeconds, err := replicationFreshnessWindowSeconds(row.CronExpr)
		if err != nil || windowSeconds <= 0 {
			continue
		}
		freshnessByPolicyID[row.ID] = windowSeconds
	}

	for idx := range receipts {
		if windowSeconds, ok := freshnessByPolicyID[receipts[idx].PolicyID]; ok && windowSeconds > 0 {
			value := windowSeconds
			receipts[idx].FreshnessWindowSec = &value
		}
	}

	return receipts, nil
}

func (s *Service) UpsertLocalReplicationReceipt(receipt clusterModels.ReplicationReceipt) error {
	if receipt.PolicyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	if receipt.LastAttemptAt.IsZero() {
		return fmt.Errorf("replication_receipt_last_attempt_required")
	}
	if strings.TrimSpace(strings.ToLower(receipt.Status)) == "success" && receipt.LastSuccessAt == nil {
		lastSuccessAt := receipt.LastAttemptAt
		receipt.LastSuccessAt = &lastSuccessAt
	}
	return clusterModels.UpsertReplicationReceiptTxn(s.DB, &receipt)
}

func (s *Service) DeleteLocalReplicationReceiptsByPolicy(policyID uint) error {
	if policyID == 0 {
		return fmt.Errorf("invalid_policy_id")
	}
	return s.DB.Where("policy_id = ?", policyID).Delete(&clusterModels.ReplicationReceipt{}).Error
}

func (s *Service) PruneLocalReplicationReceipts(localNodeID string) error {
	localNodeID = strings.TrimSpace(localNodeID)

	query := s.DB.Model(&clusterModels.ReplicationReceipt{})
	if localNodeID != "" {
		query = query.Where("target_node_id = ?", localNodeID)
	}

	var receipts []clusterModels.ReplicationReceipt
	if err := query.Find(&receipts).Error; err != nil {
		return err
	}
	if len(receipts) == 0 {
		return nil
	}

	policyIDSet := make(map[uint]struct{}, len(receipts))
	for _, receipt := range receipts {
		if receipt.PolicyID > 0 {
			policyIDSet[receipt.PolicyID] = struct{}{}
		}
	}

	policyIDs := make([]uint, 0, len(policyIDSet))
	for id := range policyIDSet {
		policyIDs = append(policyIDs, id)
	}

	var policies []clusterModels.ReplicationPolicy
	if len(policyIDs) > 0 {
		if err := s.DB.Preload("Targets").Where("id IN ?", policyIDs).Find(&policies).Error; err != nil {
			return err
		}
	}

	targetSetByPolicy := make(map[uint]map[string]struct{}, len(policies))
	for _, policy := range policies {
		targetSet := make(map[string]struct{}, len(policy.Targets))
		for _, target := range policy.Targets {
			targetID := strings.TrimSpace(target.NodeID)
			if targetID == "" {
				continue
			}
			targetSet[targetID] = struct{}{}
		}
		targetSetByPolicy[policy.ID] = targetSet
	}

	staleIDs := make([]uint, 0)
	for _, receipt := range receipts {
		if localNodeID != "" && strings.TrimSpace(receipt.TargetNodeID) != localNodeID {
			staleIDs = append(staleIDs, receipt.ID)
			continue
		}

		targetSet, policyExists := targetSetByPolicy[receipt.PolicyID]
		if !policyExists {
			staleIDs = append(staleIDs, receipt.ID)
			continue
		}
		if _, ok := targetSet[strings.TrimSpace(receipt.TargetNodeID)]; !ok {
			staleIDs = append(staleIDs, receipt.ID)
		}
	}

	if len(staleIDs) == 0 {
		return nil
	}

	return s.DB.Where("id IN ?", staleIDs).Delete(&clusterModels.ReplicationReceipt{}).Error
}

func replicationFreshnessWindowSeconds(cronExpr string) (int64, error) {
	spec := strings.TrimSpace(cronExpr)
	if spec == "" {
		return 0, fmt.Errorf("cron_expr_required")
	}

	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return 0, err
	}

	anchor := time.Now().UTC()
	first := schedule.Next(anchor)
	second := schedule.Next(first)
	if !second.After(first) {
		return 0, fmt.Errorf("invalid_cron_interval")
	}

	window := second.Sub(first) * 2
	if window <= 0 {
		return 0, fmt.Errorf("invalid_cron_interval")
	}
	return int64(window / time.Second), nil
}

func (s *Service) cleanupOrphanReplicationRows() {
	if s.DB == nil {
		return
	}

	policyIDs := s.DB.Model(&clusterModels.ReplicationPolicy{}).Select("id")
	_ = s.DB.Where("policy_id NOT IN (?)", policyIDs).Delete(&clusterModels.ReplicationPolicyTarget{}).Error

	policyIDs = s.DB.Model(&clusterModels.ReplicationPolicy{}).Select("id")
	_ = s.DB.Where("policy_id NOT IN (?)", policyIDs).Delete(&clusterModels.ReplicationLease{}).Error

	policyIDs = s.DB.Model(&clusterModels.ReplicationPolicy{}).Select("id")
	_ = s.DB.Where("policy_id IS NOT NULL AND policy_id NOT IN (?)", policyIDs).Delete(&clusterModels.ReplicationEvent{}).Error

	policyIDs = s.DB.Model(&clusterModels.ReplicationPolicy{}).Select("id")
	_ = s.DB.Where("policy_id NOT IN (?)", policyIDs).Delete(&clusterModels.ReplicationReceipt{}).Error
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
