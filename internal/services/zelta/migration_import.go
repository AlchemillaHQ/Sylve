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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) requireLocalGuestIdentityPlacement(
	ctx context.Context,
	guestKind string,
	guestID uint,
) error {
	if s == nil {
		return fmt.Errorf("guest_identity_cluster_service_unavailable")
	}
	localNodeID := strings.TrimSpace(s.Cluster.LocalNodeID())
	if localNodeID == "" {
		return fmt.Errorf("guest_identity_local_node_id_unavailable")
	}
	placementErr := s.Cluster.RequireGuestPlacement(ctx, guestKind, guestID, localNodeID)
	if placementErr == nil {
		return nil
	}
	return fmt.Errorf("guest_identity_local_placement_invalid: %w", placementErr)
}

// ImportMigratedVMWithRoots registers a VM whose datasets a migration received
// into roots. pools maps each source pool to the local pool that replaced it.
func (s *Service) ImportMigratedVMWithRoots(
	ctx context.Context,
	rid uint,
	roots []string,
	pools map[string]string,
) (warnings []string, err error) {
	if rid == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}
	if err := s.requireLocalGuestIdentityPlacement(
		ctx, clusterModels.ReplicationGuestTypeVM, rid,
	); err != nil {
		return nil, err
	}
	// Keep target-side VNC selection and VM registration in one critical
	// section so concurrent migrations cannot both claim the same free port.
	s.migrationVMImportMu.Lock()
	defer s.migrationVMImportMu.Unlock()

	roots, err = s.ValidateMigratedVMRoots(ctx, rid, roots)
	if err != nil {
		return nil, err
	}

	metadataDataset := ""
	var migratedMetadata *restoredVMMetadata
	for _, dataset := range roots {
		meta, readErr := s.readLocalRestoredVMMetadata(ctx, dataset, rid)
		if readErr != nil {
			return nil, fmt.Errorf("failed_to_read_migrated_vm_metadata_from_%s: %w", dataset, readErr)
		}
		if meta == nil {
			continue
		}
		if meta.VM.RID != rid {
			return nil, fmt.Errorf("migrated_vm_metadata_identity_mismatch_on_%s", dataset)
		}
		if metadataDataset == "" {
			metadataDataset = dataset
			migratedMetadata = meta
		}
	}
	if metadataDataset == "" || migratedMetadata == nil {
		return nil, fmt.Errorf("migrated_vm_metadata_invalid")
	}
	if err := libvirt.ValidateVMBootConfiguration(migratedMetadata.VM); err != nil {
		return nil, err
	}

	rewrite := len(pools) > 0
	remapMigratedVMMetadataPools(migratedMetadata, pools)
	if migratedMetadata.VM.VNCEnabled {
		requestedPort := migratedMetadata.VM.VNCPort
		resolvedPort, reassigned, resolveErr := s.resolveMigratedVMVNCPort(rid, requestedPort)
		if resolveErr != nil {
			return warnings, fmt.Errorf("failed_to_resolve_migrated_vm_vnc_port: %w", resolveErr)
		}
		if reassigned {
			migratedMetadata.VM.VNCPort = resolvedPort
			rewrite = true
			warnings = append(warnings, fmt.Sprintf(
				"warning_target_vnc_port_reassigned: %d -> %d",
				requestedPort,
				resolvedPort,
			))
		}
	}
	if rewrite {
		if err := s.writeVMMetadataToDataset(ctx, metadataDataset, migratedMetadata); err != nil {
			return warnings, fmt.Errorf("failed_to_persist_migrated_vm_metadata: %w", err)
		}
	}

	if err := s.reconcileMigratedVMFromDataset(ctx, metadataDataset); err != nil {
		s.cleanupOrphanedVMRegistration(rid)
		return warnings, fmt.Errorf("failed_to_import_migrated_vm: %w", err)
	}

	if err := activateMigratedDatasetRoots(ctx, roots, s.prepareReplicatedDatasetForActivation); err != nil {
		return warnings, fmt.Errorf("failed_to_prepare_migrated_vm_datasets_for_activation: %w", err)
	}

	if err := s.destroyMigratedDatasetSnapshots(ctx, roots); err != nil {
		return warnings, fmt.Errorf("failed_to_cleanup_migrated_vm_snapshots: %w", err)
	}

	return warnings, nil
}

func (s *Service) resolveMigratedVMVNCPort(rid uint, requestedPort int) (int, bool, error) {
	if s == nil || s.DB == nil {
		return 0, false, fmt.Errorf("migration_vnc_database_unavailable")
	}

	var configuredPorts []int
	if err := s.DB.Model(&vmModels.VM{}).
		Where("rid <> ? AND vnc_port > 0", rid).
		Pluck("vnc_port", &configuredPorts).Error; err != nil {
		return 0, false, fmt.Errorf("failed_to_list_target_vnc_ports: %w", err)
	}

	used := make(map[int]struct{}, len(configuredPorts))
	for _, port := range configuredPorts {
		if port > 0 && port <= 65535 {
			used[port] = struct{}{}
		}
	}

	portAvailable := func(port int) bool {
		if port < 1 || port > 65535 {
			return false
		}
		if _, exists := used[port]; exists {
			return false
		}
		return !utils.IsTCPPortInUse(port)
	}

	if portAvailable(requestedPort) {
		return requestedPort, false, nil
	}
	for port := 5900; port <= 65535; port++ {
		if portAvailable(port) {
			return port, true, nil
		}
	}

	return 0, false, fmt.Errorf("no_available_vnc_port")
}

func (s *Service) ImportMigratedJailWithRoots(
	ctx context.Context,
	ctID uint,
	roots []string,
) (warnings []string, err error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_jail_ctid")
	}
	if err := s.requireLocalGuestIdentityPlacement(
		ctx, clusterModels.ReplicationGuestTypeJail, ctID,
	); err != nil {
		return nil, err
	}

	roots, err = s.ValidateMigratedJailRoots(ctx, ctID, roots)
	if err != nil {
		return nil, err
	}

	metadataDataset, metadataMountpoint := "", ""
	var migratedMetadata *restoredJailMetadata
	for _, dataset := range roots {
		meta, mountPoint, readErr := s.readLocalRestoredJailMetadata(ctx, dataset)
		if readErr != nil {
			return nil, fmt.Errorf("failed_to_read_migrated_jail_metadata_from_%s: %w", dataset, readErr)
		}
		if meta == nil {
			continue
		}
		if meta.Jail.CTID != ctID {
			return nil, fmt.Errorf("migrated_jail_metadata_identity_mismatch_on_%s", dataset)
		}
		if metadataDataset == "" {
			metadataDataset, metadataMountpoint, migratedMetadata = dataset, mountPoint, meta
		}
	}
	if metadataDataset == "" {
		return nil, fmt.Errorf("migrated_jail_metadata_invalid")
	}
	if err := s.rebaseMigratedJailFstab(migratedMetadata, metadataMountpoint); err != nil {
		return nil, err
	}

	if err := s.reconcileMigratedJailFromDataset(ctx, metadataDataset); err != nil {
		return warnings, fmt.Errorf("failed_to_import_migrated_jail: %w", err)
	}

	if err := activateMigratedDatasetRoots(ctx, roots, s.prepareReplicatedDatasetForActivation); err != nil {
		return warnings, fmt.Errorf("failed_to_prepare_migrated_jail_datasets_for_activation: %w", err)
	}

	if err := s.destroyMigratedDatasetSnapshots(ctx, roots); err != nil {
		return warnings, fmt.Errorf("failed_to_cleanup_migrated_jail_snapshots: %w", err)
	}

	return warnings, nil
}

// rebaseMigratedJailFstab moves the jail's fstab mounts to where its root is
// mounted on this node, which differs when the migration changed its pool.
func (s *Service) rebaseMigratedJailFstab(meta *restoredJailMetadata, mountPoint string) error {
	source := meta.RootMountpoint
	if source == "" || source == mountPoint || strings.TrimSpace(meta.Jail.Fstab) == "" {
		return nil
	}
	fstab, err := jailService.RebaseFstab(meta.Jail.Fstab, source, mountPoint)
	if err != nil {
		return fmt.Errorf("failed_to_rebase_migrated_jail_fstab: %w", err)
	}
	meta.Jail.Fstab = fstab
	if err := s.writeJailMetadataToDisk(meta, mountPoint); err != nil {
		return fmt.Errorf("failed_to_persist_migrated_jail_metadata: %w", err)
	}
	return nil
}

// remapMigratedVMMetadataPools points vm.json at the pools the VM's datasets
// were received into. Datasets outside the VM's own roots, such as a shared
// filesystem, did not move and keep their names.
func remapMigratedVMMetadataPools(meta *restoredVMMetadata, pools map[string]string) {
	if len(pools) == 0 {
		return
	}
	guestRoot := fmt.Sprintf("/sylve/virtual-machines/%d", meta.VM.RID)
	remap := func(dataset string) string {
		pool, rest, _ := strings.Cut(strings.TrimSpace(dataset), "/")
		rest = "/" + rest
		target, ok := pools[pool]
		if !ok || (rest != guestRoot && !strings.HasPrefix(rest, guestRoot+"/")) {
			return dataset
		}
		return target + rest
	}

	for i := range meta.VM.Storages {
		storage := &meta.VM.Storages[i]
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			continue
		}
		if name := strings.TrimSpace(storage.Dataset.Name); name != "" {
			if remapped := remap(name); remapped != name {
				pool, _, _ := strings.Cut(remapped, "/")
				storage.Dataset.Name = remapped
				storage.Pool, storage.Dataset.Pool = pool, pool
			}
			continue
		}
		if target, ok := pools[strings.TrimSpace(storage.Pool)]; ok && storage.Type != vmModels.VMStorageTypeFilesystem {
			storage.Pool, storage.Dataset.Pool = target, target
		}
	}
	// Roots from pools that were merged into one collapse to that one root.
	for i := range meta.Snapshots {
		var roots []string
		for _, root := range meta.Snapshots[i].RootDatasets {
			if root = remap(root); !slices.Contains(roots, root) {
				roots = append(roots, root)
			}
		}
		meta.Snapshots[i].RootDatasets = roots
	}
}

// MergeMigratedVMRoots moves the disk datasets a migration staged beside each
// VM root into that root, then destroys the staging dataset. Disk datasets are
// named by storage ID, so they can't collide. A retried import finds the
// staging dataset already gone and skips it.
func (s *Service) MergeMigratedVMRoots(ctx context.Context, rid uint, staged map[string][]string) error {
	for root, stagings := range staged {
		if _, err := s.ValidateMigratedVMRoots(ctx, rid, []string{root}); err != nil {
			return err
		}
		for _, staging := range stagings {
			if !strings.HasPrefix(staging, root+".merge-") {
				return fmt.Errorf("migration_merge_staging_invalid: %s", staging)
			}
			ds, err := s.getLocalDataset(ctx, staging)
			if err != nil {
				return err
			}
			if ds == nil {
				continue
			}
			datasets, err := s.GZFS.ZFS.List(ctx, true, staging)
			if err != nil {
				return fmt.Errorf("migration_merge_list_%s_failed: %w", staging, err)
			}
			for _, child := range datasets {
				name, found := strings.CutPrefix(child.Name, staging+"/")
				if !found || strings.Contains(name, "/") || child.Type == gzfs.DatasetTypeSnapshot {
					continue
				}
				if err := s.renameLocalDataset(ctx, child.Name, root+"/"+name); err != nil {
					return fmt.Errorf("migration_merge_move_%s_failed: %w", child.Name, err)
				}
			}
			if err := s.destroyLocalDataset(ctx, staging, true); err != nil {
				return fmt.Errorf("migration_merge_cleanup_%s_failed: %w", staging, err)
			}
		}
	}
	return nil
}

func (s *Service) ValidateMigratedVMRoots(ctx context.Context, rid uint, roots []string) ([]string, error) {
	return s.validateMigratedGuestRoots(ctx, "virtual-machines", rid, roots)
}

func (s *Service) ValidateMigratedJailRoots(ctx context.Context, ctID uint, roots []string) ([]string, error) {
	return s.validateMigratedGuestRoots(ctx, "jails", ctID, roots)
}

func (s *Service) validateMigratedGuestRoots(
	ctx context.Context,
	guestDir string,
	guestID uint,
	roots []string,
) ([]string, error) {
	if guestID == 0 || (guestDir != "virtual-machines" && guestDir != "jails") {
		return nil, fmt.Errorf("migration_dataset_manifest_identity_invalid")
	}
	if s == nil || s.GZFS == nil || s.GZFS.ZFS == nil {
		return nil, fmt.Errorf("zfs_service_not_available")
	}

	seen := make(map[string]struct{}, len(roots))
	normalized := make([]string, 0, len(roots))
	wantID := strconv.FormatUint(uint64(guestID), 10)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		parts := strings.Split(root, "/")
		if len(parts) != 4 || parts[0] == "" || parts[1] != "sylve" ||
			parts[2] != guestDir || parts[3] != wantID || strings.Contains(root, "@") {
			return nil, fmt.Errorf("migration_dataset_manifest_root_invalid: %s", root)
		}
		if _, duplicate := seen[root]; duplicate {
			return nil, fmt.Errorf("migration_dataset_manifest_root_duplicate: %s", root)
		}
		seen[root] = struct{}{}
		dataset, err := s.GZFS.ZFS.Get(ctx, root, false)
		if err != nil {
			return nil, fmt.Errorf("migration_dataset_manifest_root_unavailable_%s: %w", root, err)
		}
		if dataset == nil {
			return nil, fmt.Errorf("migration_dataset_manifest_root_unavailable_%s", root)
		}
		normalized = append(normalized, root)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("migration_dataset_manifest_empty")
	}
	sort.Strings(normalized)
	return normalized, nil
}

func activateMigratedDatasetRoots(
	ctx context.Context,
	roots []string,
	activate func(context.Context, string) error,
) error {
	if ctx == nil || len(roots) == 0 || activate == nil {
		return fmt.Errorf("migration_dataset_activation_input_invalid")
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			return fmt.Errorf("migration_dataset_activation_root_invalid")
		}
		if err := activate(ctx, root); err != nil {
			return fmt.Errorf("migration_dataset_activation_failed_%s: %w", root, err)
		}
	}
	return nil
}

func (s *Service) writeVMMetadataToDataset(ctx context.Context, dataset string, meta *restoredVMMetadata) error {
	mountedOut, err := s.runLocalZFSGet(ctx, "mounted", dataset)
	if err != nil {
		return err
	}
	mountpointOut, err := s.runLocalZFSGet(ctx, "mountpoint", dataset)
	if err != nil {
		return err
	}

	mounted := strings.EqualFold(strings.TrimSpace(mountedOut), "yes")
	mountPoint := strings.TrimSpace(mountpointOut)
	if mountPoint == "" || mountPoint == "-" || mountPoint == "none" || mountPoint == "legacy" {
		return fmt.Errorf("dataset_mountpoint_invalid: %s", dataset)
	}

	if !mounted {
		if err := s.mountLocalDataset(ctx, dataset); err != nil {
			return err
		}
	}

	type vmMetadataPayload struct {
		vmModels.VM
		Snapshots []vmModels.VMSnapshot `json:"snapshots"`
	}

	payload := vmMetadataPayload{
		VM:        meta.VM,
		Snapshots: meta.Snapshots,
	}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed_to_marshal_vm_metadata: %w", err)
	}

	metaPath := filepath.Join(strings.TrimSuffix(mountPoint, "/"), ".sylve", "vm.json")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		return fmt.Errorf("failed_to_create_metadata_dir: %w", err)
	}
	if err := os.WriteFile(metaPath, raw, 0644); err != nil {
		return fmt.Errorf("failed_to_write_vm_metadata_file: %w", err)
	}

	return nil
}

func (s *Service) writeJailMetadataToDisk(meta *restoredJailMetadata, mountPoint string) error {
	if mountPoint == "" {
		return fmt.Errorf("mountpoint_required")
	}

	type jailMetadataPayload struct {
		jailModels.Jail
		Snapshots      []jailModels.JailSnapshot `json:"snapshots"`
		RootMountpoint string                    `json:"rootMountpoint"`
	}

	payload := jailMetadataPayload{
		Jail:           meta.Jail,
		Snapshots:      meta.Snapshots,
		RootMountpoint: mountPoint,
	}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed_to_marshal_jail_metadata: %w", err)
	}

	metaPath := filepath.Join(strings.TrimSuffix(mountPoint, "/"), ".sylve", "jail.json")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		return fmt.Errorf("failed_to_create_metadata_dir: %w", err)
	}
	if err := os.WriteFile(metaPath, raw, 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_metadata_file: %w", err)
	}

	return nil
}

func (s *Service) destroyDatasetMigrationSnapshots(ctx context.Context, dataset string) error {
	snaps, err := s.GZFS.ZFS.ListWithPrefix(ctx, gzfs.DatasetTypeSnapshot, dataset, true)
	if err != nil {
		return err
	}

	var cleanupErrs []error
	for _, snap := range snaps {
		if snap == nil {
			continue
		}
		fullName := snap.Name
		if !isGeneratedMigrationSnapshotPath(dataset, fullName) {
			continue
		}

		if destroyErr := snap.Destroy(ctx, false, false); destroyErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy_migration_snapshot_%s_failed: %w", fullName, destroyErr))
			logger.L.Warn().
				Str("snapshot", fullName).
				Err(destroyErr).
				Msg("failed_to_destroy_target_migration_snapshot")
		}
	}

	return errors.Join(cleanupErrs...)
}

func (s *Service) destroyMigratedDatasetSnapshots(ctx context.Context, roots []string) error {
	var cleanupErrs []error
	for _, dataset := range roots {
		if err := s.destroyDatasetMigrationSnapshots(ctx, dataset); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup_migration_snapshots_%s_failed: %w", dataset, err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func isGeneratedMigrationSnapshotPath(root, fullName string) bool {
	root = strings.TrimSpace(root)
	fullName = strings.TrimSpace(fullName)
	at := strings.LastIndex(fullName, "@")
	if root == "" || at <= 0 || at == len(fullName)-1 {
		return false
	}
	dataset := fullName[:at]
	return (dataset == root || strings.HasPrefix(dataset, root+"/")) &&
		isGeneratedMigrationSnapshotName(fullName[at+1:])
}

func isGeneratedMigrationSnapshotName(shortName string) bool {
	name := strings.TrimSpace(shortName)
	const prefix = "sylve-migrate-"
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == name {
		return false
	}
	for _, phasePrefix := range []string{"initial-", "final-", "pre-migration-"} {
		timestamp := strings.TrimPrefix(suffix, phasePrefix)
		if timestamp == suffix || timestamp == "" {
			continue
		}
		parsed, err := strconv.ParseInt(timestamp, 10, 64)
		if err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == timestamp {
			return true
		}
	}
	return false
}
