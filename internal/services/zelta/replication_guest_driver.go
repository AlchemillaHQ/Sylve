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
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
)

var errReplicationVMFilesystemStorageUnsupported = errors.New(vmModels.ReplicationFilesystemStorageUnsupported)

const (
	replicationFenceReasonPolicyOwnerMismatch = "policy_owner_mismatch"
	replicationFenceReasonOwnerLeaseInvalid   = "owner_lease_invalid"
)

type replicationGuestDriver interface {
	sourceDatasets(ctx context.Context, guestID uint) ([]string, error)
	activate(ctx context.Context, guestID uint, transitionRunID string, desiredRunning bool) error
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

func (d jailReplicationGuestDriver) activate(ctx context.Context, guestID uint, transitionRunID string, desiredRunning bool) error {
	return d.service.activateReplicationJail(ctx, guestID, transitionRunID, desiredRunning)
}

func (d jailReplicationGuestDriver) sourceDatasets(ctx context.Context, guestID uint) ([]string, error) {
	// Jails may have storage roots on more than one pool. Discover every local
	// guest root so one generation/manifest covers the full workload.
	datasets, err := d.service.findLocalGuestDatasets(ctx, clusterModels.ReplicationGuestTypeJail, guestID)
	if err == nil && len(datasets) > 0 {
		return datasets, nil
	}
	dataset, fallbackErr := d.service.resolveJailReplicationSourceDataset(guestID)
	if fallbackErr != nil {
		if err != nil {
			return nil, fmt.Errorf("discover_jail_replication_datasets_failed: %v; fallback_failed: %w", err, fallbackErr)
		}
		return nil, fallbackErr
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
	if err := d.service.forceKillReplicationJail(ctx, guestID); err != nil {
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

func (d vmReplicationGuestDriver) activate(ctx context.Context, guestID uint, transitionRunID string, desiredRunning bool) error {
	return d.service.activateReplicationVM(ctx, guestID, transitionRunID, desiredRunning)
}

func (d vmReplicationGuestDriver) sourceDatasets(ctx context.Context, guestID uint) ([]string, error) {
	vm, err := d.service.findVMByRID(guestID)
	if err != nil {
		return nil, fmt.Errorf("replication_vm_storage_eligibility_lookup_failed: %w", err)
	}
	if vm == nil {
		return nil, fmt.Errorf("replication_vm_not_found")
	}
	if err := requireSupportedReplicationVMStorages(vm.Storages); err != nil {
		return nil, err
	}
	return d.replicationSourceDatasets(ctx, vm)
}

func requireSupportedReplicationVMStorages(storages []vmModels.Storage) error {
	for _, storage := range storages {
		if storage.Enable && storage.Type == vmModels.VMStorageTypeFilesystem {
			return errReplicationVMFilesystemStorageUnsupported
		}
	}
	return nil
}

func (d vmReplicationGuestDriver) replicationSourceDatasets(ctx context.Context, vm *vmModels.VM) ([]string, error) {
	if vm == nil || vm.RID == 0 {
		return nil, fmt.Errorf("replication_vm_not_found")
	}
	backupRoots := d.service.listEnabledBackupRoots()
	allowedPools := make(map[string]struct{})
	seen := make(map[string]struct{})
	sources := make([]string, 0)
	addSource := func(dataset string) {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" || datasetWithinAnyRoot(dataset, backupRoots) {
			return
		}
		if _, exists := seen[dataset]; exists {
			return
		}
		seen[dataset] = struct{}{}
		sources = append(sources, dataset)
	}

	for _, storage := range vm.Storages {
		// Downloaded ISO/IMG media is carried in vm.json. A stale legacy Pool
		// value on the row must never become a replication source root.
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			continue
		}
		// Enabled filesystem storage is rejected above; a disabled share is
		// intentionally excluded and must not contribute its arbitrary pool.
		if storage.Type == vmModels.VMStorageTypeFilesystem {
			continue
		}
		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			pool = strings.TrimSpace(storage.Dataset.Pool)
		}
		if pool == "" {
			datasetName := normalizeDatasetPath(storage.Dataset.Name)
			if slash := strings.Index(datasetName, "/"); slash > 0 {
				pool = datasetName[:slash]
			}
		}
		if pool == "" {
			continue
		}
		allowedPools[pool] = struct{}{}
		addSource(fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, vm.RID))
	}

	localDatasets, listErr := d.service.listLocalFilesystemDatasets(ctx)
	if listErr == nil {
		for _, dataset := range localDatasets {
			if isReplicationLineageDatasetPath(dataset) {
				continue
			}
			kind, rid := inferRestoreDatasetKind(dataset)
			if kind != clusterModels.ReplicationGuestTypeVM || rid != vm.RID || vmDatasetRoot(dataset) != dataset {
				continue
			}
			pool := dataset
			if slash := strings.Index(pool, "/"); slash > 0 {
				pool = pool[:slash]
			}
			if _, allowed := allowedPools[pool]; !allowed {
				continue
			}
			addSource(dataset)
		}
	} else if len(sources) == 0 {
		return nil, listErr
	}

	sort.Strings(sources)
	if len(sources) == 0 {
		return nil, fmt.Errorf("vm_source_datasets_not_found")
	}
	return sources, nil
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
	if err := d.service.forceKillReplicationVM(ctx, guestID); err != nil {
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
