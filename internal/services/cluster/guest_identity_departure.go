// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"errors"
	"fmt"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

// Caller holds clusterJoinMu, which serializes the membership check with joins.
func (s *Service) releaseDepartedGuestIdentityClaimsLocked(nodeID string) error {
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return fmt.Errorf("not_leader")
	}
	future := s.Raft.GetConfiguration()
	if err := future.Error(); err != nil {
		return err
	}
	if _, present, err := resolveRaftMember(future.Configuration(), nodeID); err != nil {
		return err
	} else if present {
		return fmt.Errorf("cluster_leave_membership_still_present")
	}
	var departure clusterModels.GuestIdentityDeparture
	if err := s.DB.Where("node_id = ?", nodeID).Take(&departure).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var remaining int64
			if err := s.DB.Model(&clusterModels.GuestIdentityClaim{}).Where("owner_node_id = ?", nodeID).Count(&remaining).Error; err != nil {
				return err
			}
			if remaining != 0 {
				return fmt.Errorf("cluster_leave_departure_record_missing")
			}
			return nil
		}
		return err
	}
	return s.applyGuestIdentityRaftAction("release_departure", clusterModels.GuestIdentityDepartureCommand{
		NodeID: departure.NodeID, LeaveID: departure.LeaveID, InventoryDigest: departure.InventoryDigest,
	})
}

func (s *Service) ReconcileGuestIdentityDepartures(ctx context.Context) error {
	if s == nil || s.DB == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.clusterJoinMu.Lock()
	defer s.clusterJoinMu.Unlock()
	if s.Raft.State() != raft.Leader {
		return nil
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return err
	}
	var departures []clusterModels.GuestIdentityDeparture
	if err := s.DB.WithContext(ctx).Order("node_id ASC").Find(&departures).Error; err != nil {
		return err
	}
	for _, departure := range departures {
		if err := ctx.Err(); err != nil {
			return err
		}
		future := s.Raft.GetConfiguration()
		if err := future.Error(); err != nil {
			return err
		}
		if _, present, err := resolveRaftMember(future.Configuration(), departure.NodeID); err != nil {
			return err
		} else if present {
			continue
		}
		if err := s.applyGuestIdentityRaftAction("release_departure", clusterModels.GuestIdentityDepartureCommand{
			NodeID: departure.NodeID, LeaveID: departure.LeaveID, InventoryDigest: departure.InventoryDigest,
		}); err != nil {
			return fmt.Errorf("node_id=%s: %w", departure.NodeID, err)
		}
	}
	return nil
}
