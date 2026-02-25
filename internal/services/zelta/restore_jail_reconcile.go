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
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

type jailConfigBuilder interface {
	CreateJailConfig(data jailModels.Jail, mountPoint string, mac string) (string, error)
}

type jailDevfsCleaner interface {
	RemoveDevfsRulesForCTID(ctid uint) error
}

func (s *Service) reconcileRestoredJailFromDataset(ctx context.Context, dataset string) error {
	return s.reconcileRestoredJailFromDatasetWithOptions(ctx, dataset, true)
}

func (s *Service) reconcileRestoredJailFromDatasetWithOptions(ctx context.Context, dataset string, restoreNetwork bool) error {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return nil
	}

	_, fallbackCTID := inferRestoreDatasetKind(dataset)
	if fallbackCTID == 0 {
		return nil
	}

	restored, mountPoint, err := s.readLocalRestoredJailMetadata(ctx, dataset)
	if err != nil {
		return err
	}
	if restored == nil {
		return fmt.Errorf("restored_jail_metadata_not_found")
	}

	if restored.CTID == 0 {
		restored.CTID = fallbackCTID
	}
	if restored.CTID == 0 {
		return fmt.Errorf("restored_jail_ctid_missing")
	}

	reconciled, err := s.upsertRestoredJailState(ctx, dataset, restored, restoreNetwork)
	if err != nil {
		return err
	}

	if mountPoint == "" || mountPoint == "-" || mountPoint == "legacy" || mountPoint == "none" {
		mountPoint = "/" + strings.Trim(dataset, "/")
	}

	if err := s.writeRestoredJailConfigFiles(reconciled, mountPoint); err != nil {
		return err
	}

	logger.L.Info().
		Uint("ctid", reconciled.CTID).
		Str("dataset", dataset).
		Msg("restored_jail_reconciled")

	return nil
}

func (s *Service) readLocalRestoredJailMetadata(ctx context.Context, dataset string) (*jailModels.Jail, string, error) {
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
		if _, err := utils.RunCommandWithContext(ctx, "zfs", "mount", dataset); err != nil {
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

	var restored jailModels.Jail
	if err := json.Unmarshal(raw, &restored); err != nil {
		return nil, "", fmt.Errorf("invalid_restored_jail_metadata_json: %w", err)
	}

	return &restored, mountPoint, nil
}

func (s *Service) runLocalZFSGet(ctx context.Context, property, dataset string) (string, error) {
	output, err := utils.RunCommandWithContext(ctx, "zfs", "get", "-H", "-o", "value", property, dataset)
	if err != nil {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(output), err)
	}
	return strings.TrimSpace(output), nil
}

func (s *Service) upsertRestoredJailState(
	ctx context.Context,
	dataset string,
	restored *jailModels.Jail,
	restoreNetwork bool,
) (*jailModels.Jail, error) {
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
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var existing jailModels.Jail
		existingFound := true
		if err := tx.Where("ct_id = ?", ctid).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				existingFound = false
			} else {
				return fmt.Errorf("failed_to_lookup_existing_jail_by_ctid: %w", err)
			}
		}

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
			if err := tx.Model(&existing).Select(
				"Name",
				"Hostname",
				"Description",
				"Type",
				"StartAtBoot",
				"StartOrder",
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
	standardSwitchesCreated := false

	for idx, net := range networks {
		next := net
		next.ID = 0
		next.JailID = jailID

		switchKey := restoredSwitchResolutionKey(next)
		if resolved, ok := resolvedSwitches[switchKey]; ok {
			next.SwitchID = resolved.ID
			next.SwitchType = resolved.Type
		} else {
			resolvedSwitchID, resolvedSwitchType, createdStandardSwitch, err := s.ensureRestoredJailSwitch(
				tx,
				ctid,
				idx,
				next.SwitchID,
				next.SwitchType,
				next.StandardSwitch,
				next.ManualSwitch,
			)
			if err != nil {
				return nil, false, fmt.Errorf("failed_to_ensure_restored_jail_switch: %w", err)
			}
			if resolvedSwitchID == 0 {
				logger.L.Warn().
					Uint("ctid", ctid).
					Uint("switch_id", next.SwitchID).
					Str("switch_type", next.SwitchType).
					Msg("skipping_restored_jail_network_with_unresolved_switch")
				continue
			}

			next.SwitchID = resolvedSwitchID
			next.SwitchType = resolvedSwitchType
			resolvedSwitches[switchKey] = restoredSwitchResolution{
				ID:   resolvedSwitchID,
				Type: resolvedSwitchType,
			}
			if createdStandardSwitch {
				standardSwitchesCreated = true
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

	return out, standardSwitchesCreated, nil
}

func (s *Service) ensureRestoredJailSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	existingSwitchID uint,
	switchType string,
	standardMetadata *networkModels.StandardSwitch,
	manualMetadata *networkModels.ManualSwitch,
) (uint, string, bool, error) {
	switchType = normalizeRestoredSwitchType(switchType)

	switch switchType {
	case "manual":
		switchID, err := s.ensureRestoredManualSwitch(tx, ctid, index, existingSwitchID, manualMetadata)
		if err != nil {
			return 0, "", false, err
		}
		return switchID, "manual", false, nil
	default:
		switchID, created, err := s.ensureRestoredStandardSwitch(tx, ctid, index, existingSwitchID, standardMetadata)
		if err != nil {
			return 0, "", false, err
		}
		return switchID, "standard", created, nil
	}
}

func (s *Service) ensureRestoredStandardSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	existingSwitchID uint,
	metadata *networkModels.StandardSwitch,
) (uint, bool, error) {
	if existingSwitchID > 0 {
		var existing networkModels.StandardSwitch
		err := tx.First(&existing, existingSwitchID).Error
		if err == nil {
			if metadata == nil {
				return existing.ID, false, nil
			}

			nameMatch := true
			bridgeMatch := true
			metadataName := strings.TrimSpace(metadata.Name)
			metadataBridge := strings.TrimSpace(metadata.BridgeName)
			if metadataName != "" {
				nameMatch = strings.EqualFold(strings.TrimSpace(existing.Name), metadataName)
			}
			if metadataBridge != "" {
				bridgeMatch = strings.EqualFold(strings.TrimSpace(existing.BridgeName), metadataBridge)
			}
			if nameMatch && bridgeMatch {
				return existing.ID, false, nil
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, fmt.Errorf("failed_to_lookup_standard_switch_by_id: %w", err)
		}
	}

	if metadata != nil {
		name := strings.TrimSpace(metadata.Name)
		if name != "" {
			var byName networkModels.StandardSwitch
			err := tx.
				Preload("Ports").
				Preload("AddressObj").
				Preload("AddressObj.Entries").
				Preload("Address6Obj").
				Preload("Address6Obj.Entries").
				Preload("NetworkObj").
				Preload("NetworkObj.Entries").
				Preload("Network6Obj").
				Preload("Network6Obj.Entries").
				Preload("GatewayAddressObj").
				Preload("GatewayAddressObj.Entries").
				Preload("Gateway6AddressObj").
				Preload("Gateway6AddressObj.Entries").
				Where("name = ?", name).
				First(&byName).Error
			if err == nil {
				if restoredStandardSwitchCompatible(&byName, metadata) {
					return byName.ID, false, nil
				}
				// Respect operator-intent around switch naming collisions:
				// if a switch with this name already exists but differs, skip this restored network.
				return 0, false, nil
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

		addressObj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.AddressID,
			metadata.AddressObj,
			"Host",
			fmt.Sprintf("restored-switch-%d-%d-address", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_address_object: %w", err)
		}

		address6Obj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.Address6ID,
			metadata.Address6Obj,
			"Host",
			fmt.Sprintf("restored-switch-%d-%d-address6", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_address6_object: %w", err)
		}

		networkObj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.NetworkID,
			metadata.NetworkObj,
			"Network",
			fmt.Sprintf("restored-switch-%d-%d-network4", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_network4_object: %w", err)
		}

		network6Obj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.Network6ID,
			metadata.Network6Obj,
			"Network",
			fmt.Sprintf("restored-switch-%d-%d-network6", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_network6_object: %w", err)
		}

		gatewayAddressObj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.GatewayAddressID,
			metadata.GatewayAddressObj,
			"Host",
			fmt.Sprintf("restored-switch-%d-%d-gateway4", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_gateway4_object: %w", err)
		}

		gateway6AddressObj, err := s.ensureRestoredNetworkObject(
			tx,
			metadata.Gateway6AddressID,
			metadata.Gateway6AddressObj,
			"Host",
			fmt.Sprintf("restored-switch-%d-%d-gateway6", ctid, index+1),
		)
		if err != nil {
			return 0, false, fmt.Errorf("failed_to_ensure_restored_standard_switch_gateway6_object: %w", err)
		}

		switchName, err := s.ensureUniqueRestoredSwitchName(tx, metadata.Name, ctid, index, "standard")
		if err != nil {
			return 0, false, err
		}

		bridge, err := s.ensureUniqueRestoredSwitchBridge(tx, metadata.BridgeName, ctid, index, "standard")
		if err != nil {
			return 0, false, err
		}

		mtu := metadata.MTU
		if mtu <= 0 {
			mtu = 1500
		}

		created := networkModels.StandardSwitch{
			Name:              switchName,
			BridgeName:        bridge,
			MTU:               mtu,
			VLAN:              metadata.VLAN,
			Address:           strings.TrimSpace(metadata.Address),
			Address6:          strings.TrimSpace(metadata.Address6),
			AddressID:         objectIDPtr(addressObj),
			Address6ID:        objectIDPtr(address6Obj),
			NetworkID:         objectIDPtr(networkObj),
			Network6ID:        objectIDPtr(network6Obj),
			GatewayAddressID:  objectIDPtr(gatewayAddressObj),
			Gateway6AddressID: objectIDPtr(gateway6AddressObj),
			DisableIPv6:       metadata.DisableIPv6,
			Private:           metadata.Private,
			DefaultRoute:      metadata.DefaultRoute,
			DHCP:              metadata.DHCP,
			SLAAC:             metadata.SLAAC,
		}
		if err := tx.Create(&created).Error; err != nil {
			return 0, false, fmt.Errorf("failed_to_create_restored_standard_switch: %w", err)
		}

		for _, port := range metadata.Ports {
			name := strings.TrimSpace(port.Name)
			if name == "" {
				continue
			}
			if err := tx.Create(&networkModels.NetworkPort{
				Name:     name,
				SwitchID: created.ID,
			}).Error; err != nil {
				return 0, false, fmt.Errorf("failed_to_insert_restored_standard_switch_port: %w", err)
			}
		}

		return created.ID, true, nil
	}

	return 0, false, nil
}

func (s *Service) ensureRestoredManualSwitch(
	tx *gorm.DB,
	ctid uint,
	index int,
	existingSwitchID uint,
	metadata *networkModels.ManualSwitch,
) (uint, error) {
	if existingSwitchID > 0 {
		var existing networkModels.ManualSwitch
		err := tx.First(&existing, existingSwitchID).Error
		if err == nil {
			if metadata == nil {
				return existing.ID, nil
			}

			nameMatch := true
			bridgeMatch := true
			metadataName := strings.TrimSpace(metadata.Name)
			metadataBridge := strings.TrimSpace(metadata.Bridge)
			if metadataName != "" {
				nameMatch = strings.EqualFold(strings.TrimSpace(existing.Name), metadataName)
			}
			if metadataBridge != "" {
				bridgeMatch = strings.EqualFold(strings.TrimSpace(existing.Bridge), metadataBridge)
			}
			if nameMatch && bridgeMatch {
				return existing.ID, nil
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("failed_to_lookup_manual_switch_by_id: %w", err)
		}
	}

	if metadata != nil {
		name := strings.TrimSpace(metadata.Name)
		if name != "" {
			var byName networkModels.ManualSwitch
			err := tx.Where("name = ?", name).First(&byName).Error
			if err == nil {
				metadataBridge := strings.TrimSpace(metadata.Bridge)
				if metadataBridge == "" || strings.EqualFold(strings.TrimSpace(byName.Bridge), metadataBridge) {
					return byName.ID, nil
				}
				// Name conflict with different characteristics: skip restoring this network.
				return 0, nil
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

		switchName, err := s.ensureUniqueRestoredSwitchName(tx, metadata.Name, ctid, index, "manual")
		if err != nil {
			return 0, err
		}
		bridge, err = s.ensureUniqueRestoredSwitchBridge(tx, metadata.Bridge, ctid, index, "manual")
		if err != nil {
			return 0, err
		}

		created := networkModels.ManualSwitch{
			Name:   switchName,
			Bridge: bridge,
		}
		if err := tx.Create(&created).Error; err != nil {
			return 0, fmt.Errorf("failed_to_create_restored_manual_switch: %w", err)
		}

		return created.ID, nil
	}

	return 0, nil
}

func normalizeRestoredSwitchMTU(mtu int) int {
	if mtu <= 0 {
		return 1500
	}
	return mtu
}

func restoredObjectEntriesMatch(existing *networkModels.Object, metadata *networkModels.Object) bool {
	if metadata == nil {
		return true
	}
	if existing == nil {
		return false
	}
	if strings.TrimSpace(metadata.Type) != "" && !strings.EqualFold(strings.TrimSpace(existing.Type), strings.TrimSpace(metadata.Type)) {
		return false
	}

	metadataEntries := make([]string, 0, len(metadata.Entries))
	for _, entry := range metadata.Entries {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		metadataEntries = append(metadataEntries, value)
	}
	if len(metadataEntries) == 0 {
		return true
	}

	existingEntries := make(map[string]struct{}, len(existing.Entries))
	for _, entry := range existing.Entries {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		existingEntries[value] = struct{}{}
	}
	for _, value := range metadataEntries {
		if _, ok := existingEntries[value]; !ok {
			return false
		}
	}
	return true
}

func restoredPortsCompatible(existing []networkModels.NetworkPort, metadata []networkModels.NetworkPort) bool {
	if len(metadata) == 0 {
		return true
	}

	existingSet := make(map[string]struct{}, len(existing))
	for _, port := range existing {
		name := strings.ToLower(strings.TrimSpace(port.Name))
		if name == "" {
			continue
		}
		existingSet[name] = struct{}{}
	}

	for _, port := range metadata {
		name := strings.ToLower(strings.TrimSpace(port.Name))
		if name == "" {
			continue
		}
		if _, ok := existingSet[name]; !ok {
			return false
		}
	}

	return true
}

func restoredStandardSwitchCompatible(existing *networkModels.StandardSwitch, metadata *networkModels.StandardSwitch) bool {
	if existing == nil || metadata == nil {
		return false
	}

	if strings.TrimSpace(metadata.BridgeName) != "" &&
		!strings.EqualFold(strings.TrimSpace(existing.BridgeName), strings.TrimSpace(metadata.BridgeName)) {
		return false
	}
	if normalizeRestoredSwitchMTU(existing.MTU) != normalizeRestoredSwitchMTU(metadata.MTU) {
		return false
	}
	if existing.VLAN != metadata.VLAN {
		return false
	}
	if existing.DisableIPv6 != metadata.DisableIPv6 ||
		existing.Private != metadata.Private ||
		existing.DefaultRoute != metadata.DefaultRoute ||
		existing.DHCP != metadata.DHCP ||
		existing.SLAAC != metadata.SLAAC {
		return false
	}

	if !restoredPortsCompatible(existing.Ports, metadata.Ports) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.AddressObj, metadata.AddressObj) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.Address6Obj, metadata.Address6Obj) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.NetworkObj, metadata.NetworkObj) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.Network6Obj, metadata.Network6Obj) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.GatewayAddressObj, metadata.GatewayAddressObj) {
		return false
	}
	if !restoredObjectEntriesMatch(existing.Gateway6AddressObj, metadata.Gateway6AddressObj) {
		return false
	}

	return true
}

func (s *Service) ensureUniqueRestoredSwitchName(tx *gorm.DB, base string, ctid uint, index int, switchType string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		if strings.EqualFold(strings.TrimSpace(switchType), "manual") {
			base = fmt.Sprintf("Restored Manual Switch %d (%d)", index+1, ctid)
		} else {
			base = fmt.Sprintf("Restored Standard Switch %d (%d)", index+1, ctid)
		}
	}

	candidate := base
	for i := 1; ; i++ {
		standardCount := int64(0)
		if err := tx.Model(&networkModels.StandardSwitch{}).Where("name = ?", candidate).Count(&standardCount).Error; err != nil {
			return "", fmt.Errorf("failed_to_validate_standard_switch_name: %w", err)
		}
		if standardCount == 0 {
			manualCount := int64(0)
			if err := tx.Model(&networkModels.ManualSwitch{}).Where("name = ?", candidate).Count(&manualCount).Error; err != nil {
				return "", fmt.Errorf("failed_to_validate_manual_switch_name: %w", err)
			}
			if manualCount == 0 {
				return candidate, nil
			}
		}

		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Service) ensureUniqueRestoredSwitchBridge(tx *gorm.DB, base string, ctid uint, index int, switchType string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		prefix := "ssw"
		if strings.EqualFold(strings.TrimSpace(switchType), "manual") {
			prefix = "msw"
		}
		base = strings.ToLower(fmt.Sprintf("%s%s", prefix, utils.ShortHash(fmt.Sprintf("%d-%d-%s", ctid, index, switchType))))
	}

	candidate := base
	for i := 1; ; i++ {
		standardCount := int64(0)
		if err := tx.Model(&networkModels.StandardSwitch{}).Where("bridge_name = ?", candidate).Count(&standardCount).Error; err != nil {
			return "", fmt.Errorf("failed_to_validate_standard_switch_bridge: %w", err)
		}
		if standardCount == 0 {
			manualCount := int64(0)
			if err := tx.Model(&networkModels.ManualSwitch{}).Where("bridge = ?", candidate).Count(&manualCount).Error; err != nil {
				return "", fmt.Errorf("failed_to_validate_manual_switch_bridge: %w", err)
			}
			if manualCount == 0 {
				return candidate, nil
			}
		}

		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func objectIDPtr(object *networkModels.Object) *uint {
	if object == nil || object.ID == 0 {
		return nil
	}
	id := object.ID
	return &id
}

func restoredNetworkObjectLooksLikeMetadata(existing *networkModels.Object, metadata *networkModels.Object) bool {
	if existing == nil || metadata == nil {
		return true
	}

	metadataName := strings.TrimSpace(metadata.Name)
	if metadataName != "" && strings.EqualFold(strings.TrimSpace(existing.Name), metadataName) {
		return true
	}

	metadataEntries := make(map[string]struct{}, len(metadata.Entries))
	for _, entry := range metadata.Entries {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		metadataEntries[value] = struct{}{}
	}
	if len(metadataEntries) == 0 {
		return metadataName == ""
	}

	existingEntries := make(map[string]struct{}, len(existing.Entries))
	for _, entry := range existing.Entries {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		existingEntries[value] = struct{}{}
	}
	for value := range metadataEntries {
		if _, ok := existingEntries[value]; !ok {
			return false
		}
	}

	return true
}

func (s *Service) ensureRestoredNetworkObject(tx *gorm.DB, existingID *uint, metadata *networkModels.Object, fallbackType, fallbackName string) (*networkModels.Object, error) {
	desiredType := strings.TrimSpace(fallbackType)
	if metadata != nil && strings.TrimSpace(metadata.Type) != "" {
		desiredType = strings.TrimSpace(metadata.Type)
	}

	if existingID != nil && *existingID > 0 {
		var byID networkModels.Object
		err := tx.Preload("Entries").Preload("Resolutions").First(&byID, *existingID).Error
		if err == nil {
			if desiredType == "" || strings.EqualFold(byID.Type, desiredType) {
				if metadata == nil || restoredNetworkObjectLooksLikeMetadata(&byID, metadata) {
					if err := s.mergeRestoredNetworkObjectData(tx, &byID, metadata); err != nil {
						return nil, err
					}
					return &byID, nil
				}
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed_to_lookup_network_object_by_id: %w", err)
		}
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
		err := tx.Where("name = ?", candidate).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed_to_validate_network_object_name: %w", err)
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
		err := tx.Where("name = ?", candidate).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed_to_validate_restored_jail_name: %w", err)
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
		err := tx.Where("name = ?", candidate).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			used[candidate] = struct{}{}
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed_to_validate_restored_jail_network_name: %w", err)
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
