// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func normalizedMutationDataset(name string) string {
	name = strings.TrimSpace(strings.Trim(name, "/"))
	if at := strings.LastIndex(name, "@"); at > 0 {
		name = name[:at]
	}
	return strings.TrimSpace(strings.Trim(name, "/"))
}

func replicationDatasetPathsOverlap(left, right string) bool {
	left = normalizedMutationDataset(left)
	right = normalizedMutationDataset(right)
	return left != "" && right != "" &&
		(left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/"))
}

func (s *Service) protectedReplicationDatasetRoots(
	policies []clusterModels.ReplicationPolicy,
) ([]string, error) {
	roots := make(map[string]struct{})
	for _, policy := range policies {
		switch strings.TrimSpace(policy.GuestType) {
		case clusterModels.ReplicationGuestTypeVM:
			var vm vmModels.VM
			err := s.DB.Preload("Storages").Preload("Storages.Dataset").
				Where("rid = ?", policy.GuestID).First(&vm).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("replication_vm_storage_lookup_failed: %w", err)
			}
			for _, storage := range vm.Storages {
				if storage.Type == vmModels.VMStorageTypeFilesystem {
					if dataset := normalizedMutationDataset(storage.Dataset.Name); storage.Enable && dataset != "" {
						roots[dataset] = struct{}{}
					}
					continue
				}
				pool := strings.TrimSpace(storage.Pool)
				if pool == "" {
					pool = strings.TrimSpace(storage.Dataset.Pool)
				}
				if pool == "" {
					if dataset := normalizedMutationDataset(storage.Dataset.Name); dataset != "" {
						if slash := strings.Index(dataset, "/"); slash > 0 {
							pool = dataset[:slash]
						}
					}
				}
				if pool != "" {
					roots[fmt.Sprintf("%s/sylve/virtual-machines/%d", pool, policy.GuestID)] = struct{}{}
				}
			}
		case clusterModels.ReplicationGuestTypeJail:
			var jail jailModels.Jail
			err := s.DB.Preload("Storages").Where("ct_id = ?", policy.GuestID).First(&jail).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("replication_jail_storage_lookup_failed: %w", err)
			}
			for _, storage := range jail.Storages {
				if pool := strings.TrimSpace(storage.Pool); pool != "" {
					roots[fmt.Sprintf("%s/sylve/jails/%d", pool, policy.GuestID)] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(roots))
	for root := range roots {
		out = append(out, root)
	}
	return out, nil
}

func (s *Service) replicationMutationProtectionState() (
	[]clusterModels.ReplicationPolicy,
	map[uint]struct{},
	error,
) {
	var policies []clusterModels.ReplicationPolicy
	if err := s.DB.Where("enabled = ?", true).Find(&policies).Error; err != nil {
		return nil, nil, fmt.Errorf("replication_policy_lookup_failed: %w", err)
	}
	protectedPolicyIDs := make(map[uint]struct{}, len(policies))
	guestKeys := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		protectedPolicyIDs[policy.ID] = struct{}{}
		guestKeys[fmt.Sprintf("%s:%d", policy.GuestType, policy.GuestID)] = struct{}{}
	}

	if replicationguard.GuestOperationSchemaReady(s.DB) {
		var operations []clusterModels.ReplicationGuestOperation
		if err := s.DB.Find(&operations).Error; err != nil {
			return nil, nil, fmt.Errorf("replication_guest_operation_lookup_failed: %w", err)
		}
		for _, operation := range operations {
			guestType := strings.TrimSpace(operation.GuestType)
			if guestType == "" || operation.GuestID == 0 {
				continue
			}
			key := fmt.Sprintf("%s:%d", guestType, operation.GuestID)
			if _, exists := guestKeys[key]; !exists {
				policies = append(policies, clusterModels.ReplicationPolicy{
					GuestType: guestType, GuestID: operation.GuestID,
				})
				guestKeys[key] = struct{}{}
			}
			var matching []clusterModels.ReplicationPolicy
			if err := s.DB.Select("id").Where("guest_type = ? AND guest_id = ?", guestType, operation.GuestID).
				Find(&matching).Error; err != nil {
				return nil, nil, fmt.Errorf("replication_operation_policy_lookup_failed: %w", err)
			}
			for _, policy := range matching {
				protectedPolicyIDs[policy.ID] = struct{}{}
			}
		}
	}
	return policies, protectedPolicyIDs, nil
}

func (s *Service) RequireReplicationDatasetMutationAllowed(ctx context.Context, names ...string) error {
	if s == nil || s.DB == nil || s.GZFS == nil {
		return fmt.Errorf("replication_dataset_guard_unavailable")
	}
	policies, protectedPolicyIDs, err := s.replicationMutationProtectionState()
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	roots, err := s.protectedReplicationDatasetRoots(policies)
	if err != nil {
		return err
	}

	for _, rawName := range names {
		name := normalizedMutationDataset(rawName)
		if name == "" {
			return fmt.Errorf("replication_dataset_guard_name_required")
		}
		for _, root := range roots {
			if replicationDatasetPathsOverlap(name, root) {
				return fmt.Errorf("replication_protected_dataset_mutation_blocked:%s", name)
			}
		}

		// Standby roots can exist without local guest metadata. Recursive
		// property lookup also catches deletion of an ancestor containing one.
		output, propertyErr := utils.RunCommandWithContext(
			ctx,
			"zfs", "get", "-H", "-o", "value", "-r", "sylve:replication-policy-id", name,
		)
		if propertyErr != nil {
			return fmt.Errorf("replication_dataset_provenance_lookup_failed_%s: %w", name, propertyErr)
		}
		for _, value := range strings.Fields(output) {
			policyID, parseErr := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if parseErr != nil || policyID == 0 {
				continue
			}
			if _, protected := protectedPolicyIDs[uint(policyID)]; protected {
				return fmt.Errorf("replication_protected_dataset_mutation_blocked:%s:policy_%d", name, policyID)
			}
		}
	}
	return nil
}

// RequireReplicationDatasetCreateAllowed guards the exact prospective path.
// Unlike destructive mutations, creation must not recursively guard the
// parent: doing so would reject every unrelated sibling below a pool that
// contains one protected guest. The exact path is checked against known local
// roots, while the existing parent is checked for inherited standby
// provenance so children cannot be created inside a protected replica.
func (s *Service) RequireReplicationDatasetCreateAllowed(ctx context.Context, prospectiveName string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("replication_dataset_guard_unavailable")
	}
	name := normalizedMutationDataset(prospectiveName)
	if name == "" || !strings.Contains(name, "/") {
		return fmt.Errorf("replication_dataset_guard_name_required")
	}
	policies, protectedPolicyIDs, err := s.replicationMutationProtectionState()
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	roots, err := s.protectedReplicationDatasetRoots(policies)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if replicationDatasetPathsOverlap(name, root) {
			return fmt.Errorf("replication_protected_dataset_mutation_blocked:%s", name)
		}
	}

	parent := name[:strings.LastIndex(name, "/")]
	output, propertyErr := utils.RunCommandWithContext(
		ctx,
		"zfs", "get", "-H", "-o", "value", "sylve:replication-policy-id", parent,
	)
	if propertyErr != nil {
		return fmt.Errorf("replication_dataset_provenance_lookup_failed_%s: %w", parent, propertyErr)
	}
	for _, value := range strings.Fields(output) {
		policyID, parseErr := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if parseErr != nil || policyID == 0 {
			continue
		}
		if _, protected := protectedPolicyIDs[uint(policyID)]; protected {
			return fmt.Errorf("replication_protected_dataset_mutation_blocked:%s:policy_%d", name, policyID)
		}
	}
	return nil
}

func (s *Service) RequireReplicationDatasetGUIDMutationAllowed(ctx context.Context, guids ...string) error {
	names := make([]string, 0, len(guids))
	for _, guid := range guids {
		dataset, err := s.GZFS.ZFS.GetByGUID(ctx, strings.TrimSpace(guid), false)
		if err != nil {
			return fmt.Errorf("replication_dataset_guid_lookup_failed_%s: %w", guid, err)
		}
		if dataset == nil {
			return fmt.Errorf("replication_dataset_guid_not_found:%s", guid)
		}
		names = append(names, dataset.Name)
	}
	return s.RequireReplicationDatasetMutationAllowed(ctx, names...)
}

func (s *Service) RequireReplicationPoolMutationAllowed(ctx context.Context, guid string) error {
	pool, err := s.GZFS.Zpool.GetByGUID(ctx, strings.TrimSpace(guid))
	if err != nil {
		return fmt.Errorf("replication_pool_guid_lookup_failed: %w", err)
	}
	if pool == nil {
		return fmt.Errorf("replication_pool_guid_not_found:%s", strings.TrimSpace(guid))
	}
	return s.RequireReplicationDatasetMutationAllowed(ctx, pool.Name)
}
