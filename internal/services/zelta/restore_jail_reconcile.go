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
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var ErrSwitchNotFound = errors.New("switch_not_found")

type jailConfigBuilder interface {
	CreateJailConfig(data jailModels.Jail, mountPoint string, mac string) (string, error)
}

type jailDevfsCleaner interface {
	RemoveDevfsRulesForCTID(ctid uint) error
}

type jailMetadataWriter interface {
	WriteJailJSON(ctid uint) error
}

func (s *Service) reconcileRestoredJailFromDataset(ctx context.Context, dataset string) error {
	return s.reconcileRestoredJailFromDatasetWithOptions(ctx, dataset, true)
}

func (s *Service) reconcileRestoredJailFromDatasetWithOptions(ctx context.Context, dataset string, restoreNetwork bool) error {
	return s.reconcileRestoredJailFromDatasetMode(ctx, dataset, restoreNetwork, false)
}

func (s *Service) reconcileRestoredJailFromDatasetAsNew(ctx context.Context, dataset string, restoreNetwork bool) error {
	return s.reconcileRestoredJailFromDatasetMode(ctx, dataset, restoreNetwork, true)
}

func (s *Service) reconcileRestoredJailFromDatasetMode(
	ctx context.Context,
	dataset string,
	restoreNetwork bool,
	strictAsNew bool,
) error {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return nil
	}

	_, fallbackCTID := inferRestoreDatasetKind(dataset)
	if fallbackCTID == 0 {
		return nil
	}

	restoredMeta, mountPoint, err := s.readLocalRestoredJailMetadata(ctx, dataset)
	if err != nil {
		return err
	}
	if restoredMeta == nil {
		return fmt.Errorf("restored_jail_metadata_not_found")
	}
	restored := &restoredMeta.Jail

	rewriteRestoredJailMetadataIdentity(restoredMeta, fallbackCTID)
	if restored.CTID == 0 {
		return fmt.Errorf("restored_jail_ctid_missing")
	}
	if strictAsNew {
		if err := s.writeJailMetadataToDisk(restoredMeta, mountPoint); err != nil {
			return fmt.Errorf("failed_to_rewrite_restored_jail_metadata_identity: %w", err)
		}
	}

	reconciled, err := s.upsertRestoredJailState(ctx, dataset, restoredMeta, restoreNetwork, strictAsNew)
	if err != nil {
		return err
	}

	if mountPoint == "" || mountPoint == "-" || mountPoint == "legacy" || mountPoint == "none" {
		mountPoint = "/" + strings.Trim(dataset, "/")
	}

	if err := s.writeRestoredJailConfigFiles(reconciled, mountPoint); err != nil {
		return err
	}
	if strictAsNew {
		writer, ok := s.Jail.(jailMetadataWriter)
		if !ok {
			return fmt.Errorf("restored_jail_metadata_writer_unavailable")
		}
		if err := writer.WriteJailJSON(reconciled.CTID); err != nil {
			return fmt.Errorf("failed_to_refresh_restored_jail_metadata: %w", err)
		}
	}

	logger.L.Info().
		Uint("ctid", reconciled.CTID).
		Str("dataset", dataset).
		Msg("restored_jail_reconciled")

	return nil
}

type restoredJailMetadata struct {
	Jail      jailModels.Jail
	Snapshots []jailModels.JailSnapshot
}

func rewriteRestoredJailMetadataIdentity(meta *restoredJailMetadata, ctid uint) {
	if meta == nil || ctid == 0 {
		return
	}
	meta.Jail.CTID = ctid
}

func (s *Service) readLocalRestoredJailMetadata(ctx context.Context, dataset string) (*restoredJailMetadata, string, error) {
	mountedOut, err := s.runLocalZFSGet(ctx, "mounted", dataset)
	if err != nil {
		return nil, "", fmt.Errorf("failed_to_read_dataset_mounted_property: %w", err)
	}
	mountpointOut, err := s.runLocalZFSGet(ctx, "mountpoint", dataset)
	if err != nil {
		return nil, "", fmt.Errorf("failed_to_read_dataset_mountpoint_property: %w", err)
	}

	mounted := strings.EqualFold(strings.TrimSpace(mountedOut), "yes")
	mountPoint := strings.TrimSpace(mountpointOut)
	if mountPoint == "" || mountPoint == "-" || mountPoint == "none" || mountPoint == "legacy" {
		return nil, "", fmt.Errorf("dataset_mountpoint_not_usable")
	}

	if !mounted {
		if err := s.mountLocalDataset(ctx, dataset); err != nil {
			return nil, "", fmt.Errorf("failed_to_mount_restored_dataset: %w", err)
		}
	}

	metaPath := filepath.Join(strings.TrimSuffix(mountPoint, "/"), ".sylve", "jail.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, mountPoint, nil
		}
		return nil, "", fmt.Errorf("failed_to_read_restored_jail_metadata: %w", err)
	}

	type jailMetadataPayload struct {
		jailModels.Jail
		Snapshots []jailModels.JailSnapshot `json:"snapshots"`
	}

	payload := jailMetadataPayload{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", fmt.Errorf("invalid_restored_jail_metadata_json: %w", err)
	}

	return &restoredJailMetadata{
		Jail:      payload.Jail,
		Snapshots: payload.Snapshots,
	}, mountPoint, nil
}

func (s *Service) upsertRestoredJailState(
	ctx context.Context,
	dataset string,
	restoredMeta *restoredJailMetadata,
	restoreNetwork bool,
	strictAsNew bool,
) (*jailModels.Jail, error) {
	if restoredMeta == nil {
		return nil, fmt.Errorf("restored_jail_metadata_not_found")
	}
	restored := &restoredMeta.Jail

	basePool := strings.TrimSpace(strings.Split(strings.TrimSpace(dataset), "/")[0])

	datasetGUID := ""
	if guid, err := s.runLocalZFSGet(ctx, "guid", dataset); err == nil {
		datasetGUID = strings.TrimSpace(guid)
	}

	ctid := restored.CTID
	if ctid == 0 {
		return nil, fmt.Errorf("restored_jail_ctid_missing")
	}

	name := strings.TrimSpace(restored.Name)
	if name == "" {
		name = fmt.Sprintf("jail-%d", ctid)
	}

	hostname := strings.TrimSpace(restored.Hostname)
	if restored.Type == "" {
		restored.Type = jailModels.JailTypeFreeBSD
	}
	if restored.StartAtBoot == nil {
		v := false
		restored.StartAtBoot = &v
	}
	if restored.ResourceLimits == nil {
		v := true
		restored.ResourceLimits = &v
	}

	var reconciled jailModels.Jail
	requiresStandardSwitchSync := false
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if strictAsNew {
			var vmCount int64
			if err := tx.Model(&vmModels.VM{}).Where("rid = ?", ctid).Count(&vmCount).Error; err != nil {
				return fmt.Errorf("failed_to_lookup_existing_vm_by_rid: %w", err)
			}
			if vmCount > 0 {
				return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=vm", ctid)
			}
		}

		var existing jailModels.Jail
		existingFound := false
		lookup := tx.Where("ct_id = ?", ctid).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return fmt.Errorf("failed_to_lookup_existing_jail_by_ctid: %w", lookup.Error)
		}
		existingFound = lookup.RowsAffected > 0

		resolvedName, err := s.ensureUniqueRestoredJailName(tx, name, ctid, existing.ID)
		if err != nil {
			return err
		}

		baseJail := jailModels.Jail{
			CTID:              ctid,
			Name:              resolvedName,
			Hostname:          hostname,
			Description:       restored.Description,
			Type:              restored.Type,
			StartAtBoot:       restored.StartAtBoot,
			StartOrder:        restored.StartOrder,
			WoL:               restored.WoL,
			InheritIPv4:       restored.InheritIPv4,
			InheritIPv6:       restored.InheritIPv6,
			ResourceLimits:    restored.ResourceLimits,
			Cores:             restored.Cores,
			CPUSet:            append([]int(nil), restored.CPUSet...),
			Memory:            restored.Memory,
			DevFSRuleset:      restored.DevFSRuleset,
			Fstab:             restored.Fstab,
			CleanEnvironment:  restored.CleanEnvironment,
			AdditionalOptions: restored.AdditionalOptions,
			AllowedOptions:    append([]string(nil), restored.AllowedOptions...),
			MetadataMeta:      restored.MetadataMeta,
			MetadataEnv:       restored.MetadataEnv,
			StartLogs:         restored.StartLogs,
			StopLogs:          restored.StopLogs,
			StartedAt:         restored.StartedAt,
			StoppedAt:         restored.StoppedAt,
		}

		if existingFound {
			if strictAsNew {
				return fmt.Errorf("guest_id_already_in_use: guest_id=%d guest_type=jail", ctid)
			}
			if err := tx.Model(&existing).Select(
				"Name",
				"Hostname",
				"Description",
				"Type",
				"StartAtBoot",
				"StartOrder",
				"WoL",
				"InheritIPv4",
				"InheritIPv6",
				"ResourceLimits",
				"Cores",
				"CPUSet",
				"Memory",
				"DevFSRuleset",
				"Fstab",
				"CleanEnvironment",
				"AdditionalOptions",
				"AllowedOptions",
				"MetadataMeta",
				"MetadataEnv",
				"StartLogs",
				"StopLogs",
				"StartedAt",
				"StoppedAt",
			).Updates(&baseJail).Error; err != nil {
				return fmt.Errorf("failed_to_update_restored_jail_record: %w", err)
			}
			baseJail.ID = existing.ID
		} else {
			if err := tx.Create(&baseJail).Error; err != nil {
				return fmt.Errorf("failed_to_create_restored_jail_record: %w", err)
			}
		}

		hooks := normalizeRestoredJailHooks(baseJail.ID, restored.JailHooks)
		storages := normalizeRestoredJailStorages(baseJail.ID, restored.Storages, basePool, datasetGUID)
		var networks []jailModels.Network
		requiresSwitchSync := false
		if restoreNetwork {
			var err error
			networks, requiresSwitchSync, err = s.normalizeRestoredJailNetworks(tx, ctid, baseJail.ID, restored.Networks)
			if err != nil {
				return err
			}
			requiresStandardSwitchSync = requiresStandardSwitchSync || requiresSwitchSync

			if err := tx.Where("jid = ?", baseJail.ID).Delete(&jailModels.Network{}).Error; err != nil {
				return fmt.Errorf("failed_to_replace_restored_jail_networks: %w", err)
			}
		}

		if err := tx.Where("jid = ?", baseJail.ID).Delete(&jailModels.JailHooks{}).Error; err != nil {
			return fmt.Errorf("failed_to_replace_restored_jail_hooks: %w", err)
		}
		if err := tx.Where("jid = ?", baseJail.ID).Delete(&jailModels.Storage{}).Error; err != nil {
			return fmt.Errorf("failed_to_replace_restored_jail_storages: %w", err)
		}

		if len(hooks) > 0 {
			if err := tx.Create(&hooks).Error; err != nil {
				return fmt.Errorf("failed_to_insert_restored_jail_hooks: %w", err)
			}
		}
		if len(storages) > 0 {
			if err := tx.Create(&storages).Error; err != nil {
				return fmt.Errorf("failed_to_insert_restored_jail_storages: %w", err)
			}
		}
		if restoreNetwork && len(networks) > 0 {
			if err := tx.Create(&networks).Error; err != nil {
				return fmt.Errorf("failed_to_insert_restored_jail_networks: %w", err)
			}
		}

		if err := reconcileRestoredJailSnapshots(
			tx,
			baseJail.ID,
			ctid,
			restoredMeta.Snapshots,
			dataset,
		); err != nil {
			return err
		}

		if err := tx.
			Preload("Storages").
			Preload("JailHooks").
			Preload("Networks").
			First(&reconciled, baseJail.ID).Error; err != nil {
			return fmt.Errorf("failed_to_reload_reconciled_jail: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if requiresStandardSwitchSync && s.Network != nil {
		if err := s.Network.SyncStandardSwitches(nil, "sync"); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("ctid", ctid).
				Msg("failed_to_sync_standard_switches_after_restore_reconcile")
		}
	}

	return &reconciled, nil
}

func reconcileRestoredJailSnapshots(
	tx *gorm.DB,
	jailID uint,
	ctid uint,
	snapshots []jailModels.JailSnapshot,
	rootDataset string,
) error {
	if tx == nil || jailID == 0 || ctid == 0 {
		return nil
	}

	rootDataset = normalizeRestoreDestinationDataset(rootDataset)

	if err := tx.Where("jid = ?", jailID).Delete(&jailModels.JailSnapshot{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_restored_jail_snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return nil
	}

	ordered := append([]jailModels.JailSnapshot(nil), snapshots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})

	oldToNewIDs := make(map[uint]uint, len(ordered))
	insertedByName := make(map[string]uint, len(ordered))
	var latestInsertedID uint

	for _, snapshot := range ordered {
		snapshotName := strings.TrimSpace(snapshot.SnapshotName)
		if snapshotName == "" {
			continue
		}

		if !isBackupSnapshotShortName(snapshotName) {
			continue
		}

		if _, exists := insertedByName[snapshotName]; exists {
			continue
		}

		name := strings.TrimSpace(snapshot.Name)
		if name == "" {
			name = snapshotName
		}
		description := strings.TrimSpace(snapshot.Description)

		recordRoot := rootDataset
		if recordRoot == "" {
			recordRoot = normalizeRestoreDestinationDataset(snapshot.RootDataset)
		}
		if recordRoot == "" {
			continue
		}

		record := jailModels.JailSnapshot{
			JailID:       jailID,
			CTID:         ctid,
			Name:         name,
			Description:  description,
			SnapshotName: snapshotName,
			RootDataset:  recordRoot,
		}
		if !snapshot.CreatedAt.IsZero() {
			record.CreatedAt = snapshot.CreatedAt
		}
		if !snapshot.UpdatedAt.IsZero() {
			record.UpdatedAt = snapshot.UpdatedAt
		}

		parentID := uint(0)
		if snapshot.ParentSnapshotID != nil && *snapshot.ParentSnapshotID > 0 {
			if mapped, ok := oldToNewIDs[*snapshot.ParentSnapshotID]; ok {
				parentID = mapped
			}
		}
		if parentID == 0 && latestInsertedID > 0 {
			parentID = latestInsertedID
		}
		if parentID > 0 {
			record.ParentSnapshotID = &parentID
		}

		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("failed_to_insert_restored_jail_snapshot: %w", err)
		}

		if snapshot.ID > 0 {
			oldToNewIDs[snapshot.ID] = record.ID
		}
		insertedByName[snapshotName] = record.ID
		latestInsertedID = record.ID
	}

	return nil
}

func normalizeRestoredJailHooks(jailID uint, hooks []jailModels.JailHooks) []jailModels.JailHooks {
	out := make([]jailModels.JailHooks, 0, len(hooks))
	for _, hook := range hooks {
		out = append(out, jailModels.JailHooks{
			JailID:  jailID,
			Phase:   hook.Phase,
			Enabled: hook.Enabled,
			Script:  hook.Script,
		})
	}
	return out
}

func normalizeRestoredJailStorages(jailID uint, storages []jailModels.Storage, pool, guid string) []jailModels.Storage {
	out := make([]jailModels.Storage, 0, len(storages)+1)
	hasBase := false

	for _, storage := range storages {
		next := jailModels.Storage{
			JailID: jailID,
			Pool:   strings.TrimSpace(storage.Pool),
			GUID:   strings.TrimSpace(storage.GUID),
			Name:   strings.TrimSpace(storage.Name),
			IsBase: storage.IsBase,
		}

		if next.IsBase {
			hasBase = true
			if pool != "" {
				next.Pool = pool
			}
			if guid != "" {
				next.GUID = guid
			}
			if next.Name == "" {
				next.Name = "Base Filesystem"
			}
		}

		if next.Pool == "" {
			next.Pool = pool
		}

		out = append(out, next)
	}

	if !hasBase {
		out = append(out, jailModels.Storage{
			JailID: jailID,
			Pool:   pool,
			GUID:   guid,
			Name:   "Base Filesystem",
			IsBase: true,
		})
	}

	return out
}

type restoredSwitchResolution struct {
	ID   uint
	Type string
}

func normalizeRestoredSwitchType(switchType string) string {
	if strings.EqualFold(strings.TrimSpace(switchType), "manual") {
		return "manual"
	}
	return "standard"
}

func restoredSwitchResolutionKey(net jailModels.Network) string {
	switchType := normalizeRestoredSwitchType(net.SwitchType)
	switchName := ""
	switchBridge := ""

	if switchType == "manual" {
		if net.ManualSwitch != nil {
			switchName = strings.ToLower(strings.TrimSpace(net.ManualSwitch.Name))
			switchBridge = strings.ToLower(strings.TrimSpace(net.ManualSwitch.Bridge))
		}
	} else {
		if net.StandardSwitch != nil {
			switchName = strings.ToLower(strings.TrimSpace(net.StandardSwitch.Name))
			switchBridge = strings.ToLower(strings.TrimSpace(net.StandardSwitch.BridgeName))
		}
	}

	return fmt.Sprintf("%s:%d:%s:%s", switchType, net.SwitchID, switchName, switchBridge)
}

func (s *Service) normalizeRestoredJailNetworks(tx *gorm.DB, ctid, jailID uint, networks []jailModels.Network) ([]jailModels.Network, bool, error) {
	out := make([]jailModels.Network, 0, len(networks))
	usedNames := make(map[string]struct{})
	resolvedSwitches := make(map[string]restoredSwitchResolution)

	for idx, net := range networks {
		next := net
		next.ID = 0
		next.JailID = jailID

		switchKey := restoredSwitchResolutionKey(next)
		if resolved, ok := resolvedSwitches[switchKey]; ok {
			next.SwitchID = resolved.ID
			next.SwitchType = resolved.Type
		} else {
			resolvedSwitchID, resolvedSwitchType, err := s.ensureRestoredJailSwitch(
				tx,
				ctid,
				idx,
				next.SwitchType,
				next.StandardSwitch,
				next.ManualSwitch,
			)
			if err != nil {
				if errors.Is(err, ErrSwitchNotFound) {
					continue
				}
				return nil, false, fmt.Errorf("failed_to_ensure_restored_jail_switch: %w", err)
			}

			next.SwitchID = resolvedSwitchID
			next.SwitchType = resolvedSwitchType
			resolvedSwitches[switchKey] = restoredSwitchResolution{
				ID:   resolvedSwitchID,
				Type: resolvedSwitchType,
			}
		}

		macObj, err := s.ensureRestoredNetworkObject(
			tx,
			next.MacID,
			next.MacAddressObj,
			"Mac",
			fmt.Sprintf("restored-jail-%d-mac", ctid),
		)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_ensure_restored_mac_object: %w", err)
		}
		next.MacID = objectIDPtr(macObj)

		ipv4Obj, err := s.ensureRestoredNetworkObject(
			tx,
			next.IPv4ID,
			next.IPv4Obj,
			"Host",
			fmt.Sprintf("restored-jail-%d-ipv4", ctid),
		)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_ensure_restored_ipv4_object: %w", err)
		}
		next.IPv4ID = objectIDPtr(ipv4Obj)

		ipv4GWObj, err := s.ensureRestoredNetworkObject(
			tx,
			next.IPv4GwID,
			next.IPv4GwObj,
			"Host",
			fmt.Sprintf("restored-jail-%d-ipv4-gw", ctid),
		)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_ensure_restored_ipv4_gateway_object: %w", err)
		}
		next.IPv4GwID = objectIDPtr(ipv4GWObj)

		ipv6Obj, err := s.ensureRestoredNetworkObject(
			tx,
			next.IPv6ID,
			next.IPv6Obj,
			"Host",
			fmt.Sprintf("restored-jail-%d-ipv6", ctid),
		)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_ensure_restored_ipv6_object: %w", err)
		}
		next.IPv6ID = objectIDPtr(ipv6Obj)

		ipv6GWObj, err := s.ensureRestoredNetworkObject(
			tx,
			next.IPv6GwID,
			next.IPv6GwObj,
			"Host",
			fmt.Sprintf("restored-jail-%d-ipv6-gw", ctid),
		)
		if err != nil {
			return nil, false, fmt.Errorf("failed_to_ensure_restored_ipv6_gateway_object: %w", err)
		}
		next.IPv6GwID = objectIDPtr(ipv6GWObj)

		next.MacAddressObj = nil
		next.IPv4Obj = nil
		next.IPv4GwObj = nil
		next.IPv6Obj = nil
		next.IPv6GwObj = nil
		next.StandardSwitch = nil
		next.ManualSwitch = nil

		name, err := s.ensureUniqueRestoredJailNetworkName(tx, next.Name, jailID, ctid, idx, usedNames)
		if err != nil {
			return nil, false, err
		}
		next.Name = name

		out = append(out, next)
	}

	return out, false, nil
}

func (s *Service) ensureRestoredJailSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	switchType string,
	standardMetadata *networkModels.StandardSwitch,
	manualMetadata *networkModels.ManualSwitch,
) (uint, string, error) {
	switchType = normalizeRestoredSwitchType(switchType)

	switch switchType {
	case "manual":
		switchID, err := s.ensureRestoredManualSwitch(tx, ctid, index, 0, manualMetadata)
		if err != nil {
			return 0, "", err
		}
		return switchID, "manual", nil
	default:
		switchID, _, err := s.ensureRestoredStandardSwitch(tx, ctid, index, 0, standardMetadata)
		if err != nil {
			return 0, "", err
		}
		return switchID, "standard", nil
	}
}

func (s *Service) ensureRestoredStandardSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	_ uint,
	metadata *networkModels.StandardSwitch,
) (uint, bool, error) {
	if metadata == nil {
		return 0, false, ErrSwitchNotFound
	}

	name := strings.TrimSpace(metadata.Name)
	if name != "" {
		var byName networkModels.StandardSwitch
		err := tx.
			Where("name = ?", name).
			First(&byName).Error
		if err == nil {
			return byName.ID, false, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, fmt.Errorf("failed_to_lookup_standard_switch_by_name: %w", err)
		}
	}

	bridgeName := strings.TrimSpace(metadata.BridgeName)
	if bridgeName != "" {
		var byBridge networkModels.StandardSwitch
		err := tx.Where("bridge_name = ?", bridgeName).First(&byBridge).Error
		if err == nil {
			return byBridge.ID, false, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, fmt.Errorf("failed_to_lookup_standard_switch_by_bridge: %w", err)
		}
	}

	logger.L.Warn().
		Uint("ctid", ctid).
		Int("network_idx", index).
		Str("switch_name", name).
		Str("switch_bridge", bridgeName).
		Msg("standard_switch_not_found_on_target_skipping_network")

	return 0, false, ErrSwitchNotFound
}

func (s *Service) ensureRestoredManualSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	_ uint,
	metadata *networkModels.ManualSwitch,
) (uint, error) {
	if metadata == nil {
		return 0, ErrSwitchNotFound
	}

	name := strings.TrimSpace(metadata.Name)
	if name != "" {
		var byName networkModels.ManualSwitch
		err := tx.Where("name = ?", name).First(&byName).Error
		if err == nil {
			return byName.ID, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("failed_to_lookup_manual_switch_by_name: %w", err)
		}
	}

	bridge := strings.TrimSpace(metadata.Bridge)
	if bridge != "" {
		var byBridge networkModels.ManualSwitch
		err := tx.Where("bridge = ?", bridge).First(&byBridge).Error
		if err == nil {
			return byBridge.ID, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("failed_to_lookup_manual_switch_by_bridge: %w", err)
		}
	}

	logger.L.Warn().
		Uint("ctid", ctid).
		Int("network_idx", index).
		Str("switch_name", name).
		Str("switch_bridge", bridge).
		Msg("manual_switch_not_found_on_target_skipping_network")

	return 0, ErrSwitchNotFound
}

func objectIDPtr(object *networkModels.Object) *uint {
	if object == nil || object.ID == 0 {
		return nil
	}
	id := object.ID
	return &id
}

func (s *Service) ensureRestoredNetworkObject(tx *gorm.DB, _ *uint, metadata *networkModels.Object, fallbackType, fallbackName string) (*networkModels.Object, error) {
	desiredType := strings.TrimSpace(fallbackType)
	if metadata != nil && strings.TrimSpace(metadata.Type) != "" {
		desiredType = strings.TrimSpace(metadata.Type)
	}

	if metadata != nil {
		name := strings.TrimSpace(metadata.Name)
		if name != "" {
			var byName networkModels.Object
			err := tx.Preload("Entries").Preload("Resolutions").Where("name = ?", name).First(&byName).Error
			if err == nil {
				if desiredType == "" || strings.EqualFold(byName.Type, desiredType) {
					if err := s.mergeRestoredNetworkObjectData(tx, &byName, metadata); err != nil {
						return nil, err
					}
					return &byName, nil
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed_to_lookup_network_object_by_name: %w", err)
			}
		}

		for _, entry := range metadata.Entries {
			value := strings.TrimSpace(entry.Value)
			if value == "" {
				continue
			}

			query := tx.
				Model(&networkModels.Object{}).
				Joins("JOIN object_entries ON object_entries.object_id = objects.id").
				Where("object_entries.value = ?", value)
			if desiredType != "" {
				query = query.Where("objects.type = ?", desiredType)
			}

			var byEntry networkModels.Object
			err := query.Preload("Entries").Preload("Resolutions").First(&byEntry).Error
			if err == nil {
				if err := s.mergeRestoredNetworkObjectData(tx, &byEntry, metadata); err != nil {
					return nil, err
				}
				return &byEntry, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed_to_lookup_network_object_by_entry: %w", err)
			}
		}
	}

	if metadata == nil {
		return nil, nil
	}
	if desiredType == "" {
		return nil, fmt.Errorf("restored_network_object_type_missing")
	}

	name := strings.TrimSpace(metadata.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	if name == "" {
		name = "restored-network-object"
	}

	uniqueName, err := s.ensureUniqueRestoredObjectName(tx, name)
	if err != nil {
		return nil, err
	}

	created := networkModels.Object{
		Name:    uniqueName,
		Type:    desiredType,
		Comment: metadata.Comment,
	}
	if err := tx.Create(&created).Error; err != nil {
		return nil, fmt.Errorf("failed_to_create_restored_network_object: %w", err)
	}

	if err := s.mergeRestoredNetworkObjectData(tx, &created, metadata); err != nil {
		return nil, err
	}

	if err := tx.Preload("Entries").Preload("Resolutions").First(&created, created.ID).Error; err != nil {
		return nil, fmt.Errorf("failed_to_reload_created_network_object: %w", err)
	}

	return &created, nil
}

func (s *Service) mergeRestoredNetworkObjectData(tx *gorm.DB, object *networkModels.Object, metadata *networkModels.Object) error {
	if object == nil || metadata == nil {
		return nil
	}

	if object.Comment == "" && strings.TrimSpace(metadata.Comment) != "" {
		if err := tx.Model(object).Update("comment", strings.TrimSpace(metadata.Comment)).Error; err != nil {
			return fmt.Errorf("failed_to_update_restored_network_object_comment: %w", err)
		}
	}

	existingEntries := make(map[string]struct{}, len(object.Entries))
	for _, entry := range object.Entries {
		existingEntries[strings.TrimSpace(entry.Value)] = struct{}{}
	}

	for _, entry := range metadata.Entries {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		if _, exists := existingEntries[value]; exists {
			continue
		}

		if err := tx.Create(&networkModels.ObjectEntry{
			ObjectID: object.ID,
			Value:    value,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_insert_restored_network_object_entry: %w", err)
		}
		existingEntries[value] = struct{}{}
	}

	existingResolutions := make(map[string]struct{}, len(object.Resolutions))
	for _, resolution := range object.Resolutions {
		existingResolutions[strings.TrimSpace(resolution.ResolvedIP)] = struct{}{}
	}

	for _, resolution := range metadata.Resolutions {
		value := strings.TrimSpace(resolution.ResolvedIP)
		if value == "" {
			continue
		}
		if _, exists := existingResolutions[value]; exists {
			continue
		}

		if err := tx.Create(&networkModels.ObjectResolution{
			ObjectID:   object.ID,
			ResolvedIP: value,
		}).Error; err != nil {
			return fmt.Errorf("failed_to_insert_restored_network_object_resolution: %w", err)
		}
		existingResolutions[value] = struct{}{}
	}

	return nil
}

func (s *Service) ensureUniqueRestoredObjectName(tx *gorm.DB, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "restored-network-object"
	}

	candidate := base
	for i := 1; ; i++ {
		var existing networkModels.Object
		lookup := tx.Where("name = ?", candidate).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return "", fmt.Errorf("failed_to_validate_network_object_name: %w", lookup.Error)
		}
		if lookup.RowsAffected == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Service) ensureUniqueRestoredJailName(tx *gorm.DB, name string, ctid uint, currentID uint) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("jail-%d", ctid)
	}

	base := name
	candidate := base
	for i := 1; ; i++ {
		var existing jailModels.Jail
		lookup := tx.Where("name = ?", candidate).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return "", fmt.Errorf("failed_to_validate_restored_jail_name: %w", lookup.Error)
		}
		if lookup.RowsAffected == 0 {
			return candidate, nil
		}
		if existing.ID == currentID {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Service) ensureUniqueRestoredJailNetworkName(
	tx *gorm.DB,
	name string,
	jailID uint,
	ctid uint,
	index int,
	used map[string]struct{},
) (string, error) {
	base := strings.TrimSpace(name)
	if base == "" {
		base = fmt.Sprintf("Restored Network %d (%d)", index+1, ctid)
	}

	candidate := base
	for i := 1; ; i++ {
		if _, ok := used[candidate]; ok {
			candidate = fmt.Sprintf("%s-%d", base, i)
			continue
		}

		var existing jailModels.Network
		lookup := tx.Where("name = ?", candidate).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return "", fmt.Errorf("failed_to_validate_restored_jail_network_name: %w", lookup.Error)
		}
		if lookup.RowsAffected == 0 {
			used[candidate] = struct{}{}
			return candidate, nil
		}
		if existing.JailID == jailID {
			used[candidate] = struct{}{}
			return candidate, nil
		}

		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Service) writeRestoredJailConfigFiles(jail *jailModels.Jail, mountPoint string) error {
	if jail == nil {
		return fmt.Errorf("restored_jail_required")
	}

	builder, ok := s.Jail.(jailConfigBuilder)
	if !ok {
		return fmt.Errorf("jail_service_create_config_unavailable")
	}

	if cleaner, ok := s.Jail.(jailDevfsCleaner); ok {
		if err := cleaner.RemoveDevfsRulesForCTID(jail.CTID); err != nil {
			logger.L.Warn().
				Err(err).
				Uint("ctid", jail.CTID).
				Msg("failed_to_cleanup_devfs_rules_before_restore_config_rebuild")
		}
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, strconv.FormatUint(uint64(jail.CTID), 10))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_config_directory: %w", err)
	}

	logPath := filepath.Join(jailDir, fmt.Sprintf("%d.log", jail.CTID))
	if _, err := os.Stat(logPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(logPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("failed_to_create_restored_jail_log_file: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed_to_stat_restored_jail_log_file: %w", err)
	}

	fstabPath := filepath.Join(jailDir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(jail.Fstab), 0644); err != nil {
		return fmt.Errorf("failed_to_write_restored_jail_fstab: %w", err)
	}

	mac := ""
	if len(jail.Networks) > 0 {
		if jail.Networks[0].MacID == nil || *jail.Networks[0].MacID == 0 {
			return fmt.Errorf("restored_jail_primary_network_missing_mac")
		}
		mac, err = s.getFirstObjectEntryValue(*jail.Networks[0].MacID)
		if err != nil {
			return fmt.Errorf("failed_to_resolve_restored_jail_primary_mac: %w", err)
		}
	}

	cfg, err := builder.CreateJailConfig(*jail, mountPoint, mac)
	if err != nil {
		return fmt.Errorf("failed_to_build_restored_jail_config: %w", err)
	}

	confPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", jail.CTID))
	if err := os.WriteFile(confPath, []byte(cfg), 0644); err != nil {
		return fmt.Errorf("failed_to_write_restored_jail_config: %w", err)
	}

	return nil
}

func (s *Service) getFirstObjectEntryValue(objectID uint) (string, error) {
	var entry networkModels.ObjectEntry
	if err := s.DB.Where("object_id = ?", objectID).Order("id ASC").First(&entry).Error; err != nil {
		return "", err
	}

	value := strings.TrimSpace(entry.Value)
	if value == "" {
		return "", fmt.Errorf("network_object_entry_value_empty")
	}

	return value, nil
}

func (s *Service) restoreJailSwitchExists(tx *gorm.DB, switchID uint, switchType string) (bool, error) {
	if switchID == 0 {
		return true, nil
	}

	switch strings.ToLower(strings.TrimSpace(switchType)) {
	case "manual":
		var manualSwitch networkModels.ManualSwitch
		err := tx.First(&manualSwitch, switchID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	default:
		var standardSwitch networkModels.StandardSwitch
		err := tx.First(&standardSwitch, switchID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
}
