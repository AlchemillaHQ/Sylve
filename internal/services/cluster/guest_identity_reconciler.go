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
	"errors"
	"fmt"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/hashicorp/raft"
	"gorm.io/gorm"
)

const guestIdentityReconcileInterval = 10 * time.Second

func guestIdentityRegistrationFromReport(
	nodeID string,
	report GuestIdentityInventoryReport,
) (clusterModels.GuestIdentityRegisterNodeInventory, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return clusterModels.GuestIdentityRegisterNodeInventory{}, fmt.Errorf("guest_identity_inventory_node_id_required")
	}
	if err := requireCleanGuestIdentityInventory(report); err != nil {
		return clusterModels.GuestIdentityRegisterNodeInventory{}, err
	}
	entries := make([]clusterModels.GuestIdentityEntry, len(report.Entries))
	for i, entry := range report.Entries {
		if strings.TrimSpace(entry.NodeID) != nodeID {
			return clusterModels.GuestIdentityRegisterNodeInventory{}, fmt.Errorf(
				"guest_identity_inventory_node_mismatch: expected=%s actual=%s",
				nodeID,
				strings.TrimSpace(entry.NodeID),
			)
		}
		entries[i] = clusterModels.GuestIdentityEntry{
			GuestKind: entry.GuestType,
			GuestID:   entry.GuestID,
		}
	}
	digest := clusterModels.GuestIdentityInventoryDigest(nodeID, entries)
	if report.Digest != digest {
		return clusterModels.GuestIdentityRegisterNodeInventory{}, fmt.Errorf("guest_identity_inventory_digest_mismatch")
	}
	return clusterModels.GuestIdentityRegisterNodeInventory{
		NodeID:          nodeID,
		InventoryDigest: digest,
		Entries:         entries,
	}, nil
}

func (s *Service) registerGuestIdentityInventoryReport(
	nodeID string,
	report GuestIdentityInventoryReport,
) error {
	payload, err := guestIdentityRegistrationFromReport(nodeID, report)
	if err != nil {
		return err
	}
	return s.applyGuestIdentityRaftAction("register_node_inventory", payload)
}

func (s *Service) initializeGuestIdentityRegistryForFoundingNode(
	nodeID string,
	report GuestIdentityInventoryReport,
) error {
	if s == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return fmt.Errorf("guest_identity_registry_bootstrap_requires_leader")
	}
	if err := s.registerGuestIdentityInventoryReport(nodeID, report); err != nil {
		return fmt.Errorf("guest_identity_registry_bootstrap_inventory_failed: %w", err)
	}
	if err := s.applyGuestIdentityRaftAction("activate_registry", clusterModels.GuestIdentityActivateRegistry{
		VoterNodeIDs: []string{strings.TrimSpace(nodeID)},
	}); err != nil {
		return fmt.Errorf("guest_identity_registry_bootstrap_activation_failed: %w", err)
	}
	return nil
}

func enrolledGuestIdentityNodes(db *gorm.DB) (map[string]struct{}, error) {
	var enrollments []clusterModels.GuestIdentityEnrollment
	if err := db.Order("node_id ASC").Find(&enrollments).Error; err != nil {
		return nil, err
	}
	enrolled := make(map[string]struct{}, len(enrollments))
	for _, enrollment := range enrollments {
		enrolled[strings.TrimSpace(enrollment.NodeID)] = struct{}{}
	}
	return enrolled, nil
}

func guestIdentityRegistryBootstrapBlockedByCutoverMigration(ctx context.Context, db *gorm.DB) (bool, error) {
	if db == nil || !db.Migrator().HasTable(&clusterModels.ReplicationGuestOperation{}) {
		return false, nil
	}
	var count int64
	err := db.WithContext(ctx).
		Model(&clusterModels.ReplicationGuestOperation{}).
		Where("operation = ? AND state = ?",
			clusterModels.ReplicationGuestOperationMigration,
			clusterModels.ReplicationGuestOperationCutover,
		).
		Count(&count).Error
	return count > 0, err
}

func (s *Service) guestIdentityInventoryForVoter(
	ctx context.Context,
	voter guestIdentityInventoryVoter,
	clusterToken *string,
) (GuestIdentityInventoryReport, error) {
	localNodeID := strings.TrimSpace(s.guestIdentityInventoryLocalNodeID())
	if voter.nodeID == localNodeID {
		snapshot, err := s.LocalGuestIdentityInventory(ctx)
		return snapshot.Report, err
	}
	if s.AuthService == nil {
		return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_auth_service_unavailable")
	}
	if strings.TrimSpace(*clusterToken) == "" {
		token, err := s.AuthService.CreateInternalClusterJWT(localNodeID)
		if err != nil {
			return GuestIdentityInventoryReport{}, fmt.Errorf("guest_identity_inventory_cluster_token_failed: %w", err)
		}
		*clusterToken = token
	}
	endpoint, err := s.guestIdentityInventoryRemoteAPI(voter.nodeID, voter.address)
	if err != nil {
		return GuestIdentityInventoryReport{}, err
	}
	return s.fetchRemoteGuestIdentityInventory(ctx, voter.nodeID, endpoint, *clusterToken)
}

func (s *Service) activateGuestIdentityRegistryIfReady(ctx context.Context) error {
	s.membershipLifecycleMu.Lock()
	defer s.membershipLifecycleMu.Unlock()
	if s.Raft == nil || s.Raft.State() != raft.Leader {
		return raft.ErrNotLeader
	}
	if err := s.Raft.Barrier(raftApplyTimeout).Error(); err != nil {
		return fmt.Errorf("guest_identity_activation_barrier_failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return fmt.Errorf("guest_identity_activation_configuration_failed: %w", err)
	}
	voters, err := strictGuestIdentityInventoryVoters(
		configurationFuture.Configuration(),
		s.guestIdentityInventoryLocalNodeID(),
	)
	if err != nil {
		return err
	}
	enrolled, err := enrolledGuestIdentityNodes(s.DB.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("guest_identity_enrollment_list_failed: %w", err)
	}
	voterIDs := make([]string, len(voters))
	for i, voter := range voters {
		voterIDs[i] = voter.nodeID
		if _, exists := enrolled[voter.nodeID]; !exists {
			return clusterModels.ErrGuestIdentityRegistryInitializing
		}
	}
	return s.applyGuestIdentityRaftAction("activate_registry", clusterModels.GuestIdentityActivateRegistry{
		VoterNodeIDs: voterIDs,
	})
}

func (s *Service) ReconcileGuestIdentityRegistry(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.DB == nil || s.Raft == nil || s.Raft.State() != raft.Leader {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var registry clusterModels.GuestIdentityRegistry
	err := s.DB.WithContext(ctx).Where("id = ?", clusterModels.GuestIdentityRegistryID).First(&registry).Error
	if err == nil && registry.Phase == clusterModels.GuestIdentityRegistryPhaseActive {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("guest_identity_registry_read_failed: %w", err)
	}
	blocked, err := guestIdentityRegistryBootstrapBlockedByCutoverMigration(ctx, s.DB)
	if err != nil {
		return fmt.Errorf("guest_identity_registry_migration_check_failed: %w", err)
	}
	if blocked {
		return clusterModels.ErrGuestIdentityRegistryInitializing
	}

	configurationFuture := s.Raft.GetConfiguration()
	if err := configurationFuture.Error(); err != nil {
		return fmt.Errorf("guest_identity_configuration_failed: %w", err)
	}
	voters, err := strictGuestIdentityInventoryVoters(
		configurationFuture.Configuration(),
		s.guestIdentityInventoryLocalNodeID(),
	)
	if err != nil {
		return err
	}
	enrolled, err := enrolledGuestIdentityNodes(s.DB.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("guest_identity_enrollment_list_failed: %w", err)
	}

	var clusterToken string
	collectionErrors := make([]error, 0)
	for _, voter := range voters {
		if _, exists := enrolled[voter.nodeID]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(collectionErrors, err)...)
		}
		report, err := s.guestIdentityInventoryForVoter(ctx, voter, &clusterToken)
		if err != nil {
			collectionErrors = append(collectionErrors, fmt.Errorf("node_id=%s: %w", voter.nodeID, err))
			continue
		}
		if err := s.registerGuestIdentityInventoryReport(voter.nodeID, report); err != nil {
			collectionErrors = append(collectionErrors, fmt.Errorf("node_id=%s: %w", voter.nodeID, err))
			continue
		}
		enrolled[voter.nodeID] = struct{}{}
	}

	for _, voter := range voters {
		if _, exists := enrolled[voter.nodeID]; !exists {
			if len(collectionErrors) == 0 {
				return clusterModels.ErrGuestIdentityRegistryInitializing
			}
			return errors.Join(collectionErrors...)
		}
	}
	if err := s.activateGuestIdentityRegistryIfReady(ctx); err != nil {
		return errors.Join(append(collectionErrors, err)...)
	}
	return errors.Join(collectionErrors...)
}

func (s *Service) startGuestIdentityReconciler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	run := func() {
		if err := s.ReconcileGuestIdentityRegistry(ctx); err != nil &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, clusterModels.ErrGuestIdentityRegistryInitializing) {
			logger.L.Debug().Err(err).Msg("guest_identity_reconciliation_deferred")
		}
	}
	go func() {
		run()
		ticker := time.NewTicker(guestIdentityReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
