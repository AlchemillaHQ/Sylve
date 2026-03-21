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
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	replicationFenceReasonPolicyOwnerMismatch = "policy_owner_mismatch"
	replicationFenceReasonOwnerLeaseInvalid   = "owner_lease_invalid"
)

type replicationGuestDriver interface {
	sourceDatasets(ctx context.Context, guestID uint) ([]string, error)
	activate(ctx context.Context, guestID uint) error
	demote(ctx context.Context, guestID uint) error
	selfFence(ctx context.Context, policyID uint, guestID uint, localNodeID, expectedOwner, reason string)
}

func (s *Service) replicationGuestDriver(guestType string) (replicationGuestDriver, error) {
	switch strings.TrimSpace(guestType) {
	case clusterModels.ReplicationGuestTypeJail:
		return jailReplicationGuestDriver{service: s}, nil
	case clusterModels.ReplicationGuestTypeVM:
		return vmReplicationGuestDriver{service: s}, nil
	default:
		return nil, fmt.Errorf("invalid_guest_type")
	}
}

type jailReplicationGuestDriver struct {
	service *Service
}

func (d jailReplicationGuestDriver) activate(ctx context.Context, guestID uint) error {
	return d.service.activateReplicationJail(ctx, guestID)
}

func (d jailReplicationGuestDriver) sourceDatasets(_ context.Context, guestID uint) ([]string, error) {
	dataset, err := d.service.resolveJailReplicationSourceDataset(guestID)
	if err != nil {
		return nil, err
	}
	return []string{dataset}, nil
}

func (d jailReplicationGuestDriver) demote(_ context.Context, guestID uint) error {
	return d.service.stopLocalJailIfPresent(guestID)
}

func (d jailReplicationGuestDriver) selfFence(
	ctx context.Context,
	policyID uint,
	guestID uint,
	localNodeID,
	expectedOwner,
	reason string,
) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = replicationFenceReasonPolicyOwnerMismatch
	}
	if err := d.service.stopLocalJailIfPresent(guestID); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_jail_stop_failed")
		return
	}

	retired, retireErr := d.service.retireStaleNonOwnerJailMetadata(ctx, guestID, localNodeID, expectedOwner)
	if retireErr != nil {
		logger.L.Warn().
			Err(retireErr).
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_retire_stale_jail_metadata_failed")
	}
	if retired {
		logger.L.Info().
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_jail_metadata_retired")
	}
}

type vmReplicationGuestDriver struct {
	service *Service
}

func (d vmReplicationGuestDriver) activate(ctx context.Context, guestID uint) error {
	return d.service.activateReplicationVM(ctx, guestID)
}

func (d vmReplicationGuestDriver) sourceDatasets(ctx context.Context, guestID uint) ([]string, error) {
	return d.service.resolveVMBackupSourceDatasets(ctx, guestID, "")
}

func (d vmReplicationGuestDriver) demote(_ context.Context, guestID uint) error {
	return d.service.stopVMIfPresent(guestID)
}

func (d vmReplicationGuestDriver) selfFence(
	ctx context.Context,
	policyID uint,
	guestID uint,
	localNodeID,
	expectedOwner,
	reason string,
) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = replicationFenceReasonPolicyOwnerMismatch
	}
	if err := d.service.stopVMIfPresent(guestID); err != nil {
		logger.L.Warn().
			Err(err).
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_vm_stop_failed")
		return
	}

	retired, retireErr := d.service.retireStaleNonOwnerVMMetadata(ctx, guestID, localNodeID, expectedOwner)
	if retireErr != nil {
		logger.L.Warn().
			Err(retireErr).
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_retire_stale_vm_metadata_failed")
	}
	if retired {
		logger.L.Info().
			Uint("policy_id", policyID).
			Uint("guest_id", guestID).
			Str("reason", reason).
			Msg("replication_self_fence_vm_metadata_retired")
	}
}
