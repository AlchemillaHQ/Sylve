// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

const replicationProcessFenceTimeout = 2*replicationLeaseRenewalInterval + replicationSelfFenceInterval

func (s *Service) resetReplicationProcessAuthority() {
	s.replicationFenceMu.Lock()
	s.replicationLeaseAuthorities = make(map[uint]replicationLeaseAuthority)
	s.replicationStartupReady = false
	s.replicationFenceMu.Unlock()
}

func (s *Service) setReplicationStartupReady(ready bool) {
	s.replicationFenceMu.Lock()
	s.replicationStartupReady = ready
	s.replicationFenceMu.Unlock()
}

func (s *Service) replicationStartupIsReady() bool {
	s.replicationFenceMu.Lock()
	defer s.replicationFenceMu.Unlock()
	return s.replicationStartupReady
}

func (s *Service) localOwnedReplicationPolicyIDs(ctx context.Context, localNodeID string) ([]uint, error) {
	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Find(&policies).Error; err != nil {
		return nil, err
	}
	policyIDs := make([]uint, 0, len(policies))
	for i := range policies {
		if strings.TrimSpace(replicationPolicyOwnerNode(&policies[i])) == localNodeID {
			policyIDs = append(policyIDs, policies[i].ID)
		}
	}
	return policyIDs, nil
}

func (s *Service) PrepareReplicationStartup(ctx context.Context) error {
	if s == nil || s.Cluster == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, replicationProcessFenceTimeout)
	defer cancel()

	s.resetReplicationProcessAuthority()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("replication_startup_local_node_id_unavailable")
	}
	if err := s.selfFenceReplicationLeasesForLocalNode(ctx, localNodeID, false); err != nil {
		return fmt.Errorf("replication_startup_cold_fence_failed: %w", err)
	}

	policyIDs, err := s.localOwnedReplicationPolicyIDs(ctx, localNodeID)
	if err != nil {
		return fmt.Errorf("replication_startup_policy_query_failed: %w", err)
	}
	if len(policyIDs) == 0 {
		s.setReplicationStartupReady(true)
		return nil
	}

	renewalAttempted := false
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !renewalAttempted && s.Cluster.LocalNodeIsLeader() {
			renewalAttempted = true
			if err := s.runReplicationLeaseRenewalTick(ctx); err != nil {
				return fmt.Errorf("replication_startup_lease_renewal_failed: %w", err)
			}
		}

		ready := true
		for _, policyID := range policyIDs {
			policy, err := s.Cluster.GetReplicationPolicyByID(policyID)
			if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !policy.Enabled) {
				continue
			}
			if err != nil {
				ready = false
				break
			}
			if strings.TrimSpace(replicationPolicyOwnerNode(policy)) != localNodeID {
				continue
			}
			if s.validateLocalReplicationPolicyLease(policy) != nil {
				ready = false
				break
			}
		}
		if ready {
			if err := s.selfFenceReplicationLeasesForLocalNode(ctx, localNodeID, false); err != nil {
				return fmt.Errorf("replication_startup_authority_apply_failed: %w", err)
			}
			s.setReplicationStartupReady(true)
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("replication_startup_authority_timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) CanAutostartReplicationGuest(guestType string, guestID uint) (bool, error) {
	guestType = strings.TrimSpace(strings.ToLower(guestType))
	if guestID == 0 || (guestType != clusterModels.ReplicationGuestTypeVM && guestType != clusterModels.ReplicationGuestTypeJail) {
		return false, fmt.Errorf("invalid_replication_guest")
	}

	var policy clusterModels.ReplicationPolicy
	result := s.DB.Where("guest_type = ? AND guest_id = ? AND enabled = ?", guestType, guestID, true).
		Limit(1).Find(&policy)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return true, nil
	}

	s.replicationFenceMu.Lock()
	startupReady := s.replicationStartupReady
	s.replicationFenceMu.Unlock()
	if !startupReady {
		return false, nil
	}
	return s.validateLocalReplicationPolicyLease(&policy) == nil, nil
}

func (s *Service) FenceReplicationShutdown(ctx context.Context) error {
	if s == nil || s.Cluster == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, replicationProcessFenceTimeout)
	defer cancel()

	s.resetReplicationProcessAuthority()
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("replication_shutdown_local_node_id_unavailable")
	}
	if err := s.selfFenceReplicationLeasesForLocalNode(ctx, localNodeID, false); err != nil {
		return fmt.Errorf("replication_shutdown_fence_failed: %w", err)
	}
	return nil
}
