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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/gzfs"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/logger"
)

func (s *Service) ImportMigratedVM(ctx context.Context, rid uint) (warnings []string, err error) {
	if rid == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}

	dataset, err := s.discoverLocalVMDataset(ctx, rid)
	if err != nil {
		return nil, err
	}
	if dataset == "" {
		return nil, fmt.Errorf("migrated_vm_dataset_not_found_on_target")
	}

	meta, err := s.readLocalRestoredVMMetadata(ctx, dataset, rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_read_migrated_vm_metadata: %w", err)
	}
	if meta == nil || meta.VM.RID == 0 {
		return nil, fmt.Errorf("migrated_vm_metadata_invalid")
	}

	filteredNetworks, netWarnings := s.filterVMNetworksByBridgeExistence(meta.VM.Networks)
	warnings = append(warnings, netWarnings...)

	if len(filteredNetworks) < len(meta.VM.Networks) {
		meta.VM.Networks = filteredNetworks
		if writeErr := s.writeVMMetadataToDataset(ctx, dataset, meta); writeErr != nil {
			return warnings, fmt.Errorf("failed_to_write_filtered_vm_metadata: %w", writeErr)
		}
	}

	if err := s.reconcileRestoredVMFromDatasetWithOptions(ctx, dataset, true); err != nil {
		return warnings, fmt.Errorf("failed_to_import_migrated_vm: %w", err)
	}

	if err := s.destroyDatasetMigrationSnapshots(ctx, dataset); err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("failed_to_destroy_migration_snapshots_on_target")
	}

	return warnings, nil
}

func (s *Service) ImportMigratedJail(ctx context.Context, ctID uint) (warnings []string, err error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_jail_ctid")
	}

	dataset, err := s.discoverLocalJailDataset(ctx, ctID)
	if err != nil {
		return nil, err
	}
	if dataset == "" {
		return nil, fmt.Errorf("migrated_jail_dataset_not_found_on_target")
	}

	meta, mountPoint, err := s.readLocalRestoredJailMetadata(ctx, dataset)
	if err != nil {
		return nil, fmt.Errorf("failed_to_read_migrated_jail_metadata: %w", err)
	}
	if meta == nil || meta.Jail.CTID == 0 {
		return nil, fmt.Errorf("migrated_jail_metadata_invalid")
	}

	filteredNetworks, netWarnings := s.filterJailNetworksByBridgeExistence(meta.Jail.Networks)
	warnings = append(warnings, netWarnings...)

	if len(filteredNetworks) < len(meta.Jail.Networks) {
		if mountPoint == "" || mountPoint == "-" || mountPoint == "legacy" || mountPoint == "none" {
			mountPoint = "/" + strings.Trim(dataset, "/")
		}
		meta.Jail.Networks = filteredNetworks
		if writeErr := s.writeJailMetadataToDisk(meta, mountPoint); writeErr != nil {
			return warnings, fmt.Errorf("failed_to_write_filtered_jail_metadata: %w", writeErr)
		}
	}

	if err := s.reconcileRestoredJailFromDatasetWithOptions(ctx, dataset, true); err != nil {
		return warnings, fmt.Errorf("failed_to_import_migrated_jail: %w", err)
	}

	if err := s.destroyDatasetMigrationSnapshots(ctx, dataset); err != nil {
		logger.L.Warn().Err(err).Str("dataset", dataset).Msg("failed_to_destroy_migration_snapshots_on_target")
	}

	return warnings, nil
}

func (s *Service) discoverLocalVMDataset(ctx context.Context, rid uint) (string, error) {
	list, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		return "", err
	}

	suffix := fmt.Sprintf("/sylve/virtual-machines/%d", rid)
	for _, ds := range list {
		name := ds.Name
		if strings.HasSuffix(name, suffix) {
			return name, nil
		}
	}

	return "", nil
}

func (s *Service) discoverLocalJailDataset(ctx context.Context, ctID uint) (string, error) {
	list, err := s.GZFS.ZFS.List(ctx, true)
	if err != nil {
		return "", err
	}

	suffix := fmt.Sprintf("/sylve/jails/%d", ctID)
	for _, ds := range list {
		if strings.HasSuffix(ds.Name, suffix) {
			return ds.Name, nil
		}
	}

	return "", nil
}

func (s *Service) filterVMNetworksByBridgeExistence(networks []vmModels.Network) ([]vmModels.Network, []string) {
	var filtered []vmModels.Network
	var warnings []string

	for _, net := range networks {
		bridge := s.resolveNetworkBridge(net)
		if bridge == "" {
			filtered = append(filtered, net)
			continue
		}

		if !s.checkLocalBridgeExists(bridge) {
			warnings = append(warnings, fmt.Sprintf("network_skipped_missing_bridge: %s", bridge))
			continue
		}

		filtered = append(filtered, net)
	}

	return filtered, warnings
}

func (s *Service) filterJailNetworksByBridgeExistence(networks []jailModels.Network) ([]jailModels.Network, []string) {
	var filtered []jailModels.Network
	var warnings []string

	for _, net := range networks {
		bridge := s.resolveJailNetworkBridge(net)
		if bridge == "" {
			filtered = append(filtered, net)
			continue
		}

		if !s.checkLocalBridgeExists(bridge) {
			warnings = append(warnings, fmt.Sprintf("network_skipped_missing_bridge: %s", bridge))
			continue
		}

		filtered = append(filtered, net)
	}

	return filtered, warnings
}

func (s *Service) resolveNetworkBridge(net vmModels.Network) string {
	switch strings.ToLower(strings.TrimSpace(net.SwitchType)) {
	case "standard":
		if net.StandardSwitch != nil && strings.TrimSpace(net.StandardSwitch.BridgeName) != "" {
			return strings.TrimSpace(net.StandardSwitch.BridgeName)
		}
	case "manual":
		if net.ManualSwitch != nil && strings.TrimSpace(net.ManualSwitch.Bridge) != "" {
			return strings.TrimSpace(net.ManualSwitch.Bridge)
		}
	}
	return ""
}

func (s *Service) resolveJailNetworkBridge(net jailModels.Network) string {
	switch strings.ToLower(strings.TrimSpace(net.SwitchType)) {
	case "standard":
		if net.StandardSwitch != nil && strings.TrimSpace(net.StandardSwitch.BridgeName) != "" {
			return strings.TrimSpace(net.StandardSwitch.BridgeName)
		}
	case "manual":
		if net.ManualSwitch != nil && strings.TrimSpace(net.ManualSwitch.Bridge) != "" {
			return strings.TrimSpace(net.ManualSwitch.Bridge)
		}
	}
	return ""
}

func (s *Service) checkLocalBridgeExists(bridge string) bool {
	if bridge == "" {
		return false
	}
	cmd := exec.Command("/sbin/ifconfig", bridge)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
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
		Snapshots []jailModels.JailSnapshot `json:"snapshots"`
	}

	payload := jailMetadataPayload{
		Jail:      meta.Jail,
		Snapshots: meta.Snapshots,
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

	for _, snap := range snaps {
		if snap == nil {
			continue
		}
		fullName := snap.Name
		atIdx := strings.LastIndex(fullName, "@")
		if atIdx < 0 {
			continue
		}
		shortName := fullName[atIdx+1:]
		if !strings.HasPrefix(shortName, "sylve-migrate") {
			continue
		}

		if destroyErr := snap.Destroy(ctx, false, false); destroyErr != nil {
			logger.L.Warn().
				Str("snapshot", fullName).
				Err(destroyErr).
				Msg("failed_to_destroy_target_migration_snapshot")
		}
	}

	return nil
}
