// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/beevik/etree"
	"gorm.io/gorm"
)

type resolvedVMNetworkSwitch struct {
	id       uint
	typeName string
	name     string
	bridge   string
	standard *networkModels.StandardSwitch
	manual   *networkModels.ManualSwitch
}

type networkRuntimeHooks struct {
	syncVMNetworks func(ctx context.Context, db *gorm.DB, rid uint) error
}

func normalizeVMNetworkContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func normalizeVMNetworkEmulation(value string) (string, error) {
	emulation := strings.ToLower(strings.TrimSpace(value))
	if emulation != "virtio" && emulation != "e1000" {
		return "", fmt.Errorf("invalid_emulation_type: %s", value)
	}
	return emulation, nil
}

func normalizeVMNetworkMAC(value string) (string, error) {
	mac := strings.ToLower(strings.TrimSpace(value))
	if !utils.IsValidMACAddress(mac) {
		return "", fmt.Errorf("invalid_mac_address: %s", value)
	}
	return strings.ReplaceAll(mac, "-", ":"), nil
}

func findVMNetworkOwnerWithDB(db *gorm.DB, rid uint) (vmModels.VM, error) {
	var vm vmModels.VM
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Where("rid = ?", rid).
		First(&vm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vm, fmt.Errorf("vm_not_found: %w", err)
		}
		return vm, fmt.Errorf("failed_to_find_vm: %w", err)
	}
	return vm, nil
}

func findVMNetworkRecordWithDB(db *gorm.DB, vmID, networkID uint) (vmModels.Network, error) {
	var network vmModels.Network
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Preload("AddressObj").
		Preload("AddressObj.Entries").
		First(&network, "id = ? AND vm_id = ?", networkID, vmID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return network, fmt.Errorf("network_not_found: %w", err)
		}
		return network, fmt.Errorf("failed_to_find_network_record: %w", err)
	}
	return network, nil
}

func resolveVMNetworkSwitchByName(db *gorm.DB, name string) (resolvedVMNetworkSwitch, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return resolvedVMNetworkSwitch{}, fmt.Errorf("invalid_switch_name")
	}

	var standard networkModels.StandardSwitch
	err := db.First(&standard, "name = ?", name).Error
	if err == nil {
		return resolvedVMNetworkSwitch{
			id:       standard.ID,
			typeName: "standard",
			name:     standard.Name,
			bridge:   standard.BridgeName,
			standard: &standard,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return resolvedVMNetworkSwitch{}, fmt.Errorf("failed_to_find_standard_switch: %w", err)
	}

	var manual networkModels.ManualSwitch
	err = db.First(&manual, "name = ?", name).Error
	if err == nil {
		return resolvedVMNetworkSwitch{
			id:       manual.ID,
			typeName: "manual",
			name:     manual.Name,
			bridge:   manual.Bridge,
			manual:   &manual,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return resolvedVMNetworkSwitch{}, fmt.Errorf("failed_to_find_manual_switch: %w", err)
	}

	return resolvedVMNetworkSwitch{}, fmt.Errorf("switch_not_found: %s", name)
}

func resolveVMNetworkSwitchByID(db *gorm.DB, switchType string, switchID uint) (resolvedVMNetworkSwitch, error) {
	switch strings.ToLower(strings.TrimSpace(switchType)) {
	case "standard":
		var standard networkModels.StandardSwitch
		if err := db.First(&standard, switchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return resolvedVMNetworkSwitch{}, fmt.Errorf("switch_not_found: standard:%d", switchID)
			}
			return resolvedVMNetworkSwitch{}, fmt.Errorf("failed_to_find_standard_switch: %w", err)
		}
		return resolvedVMNetworkSwitch{
			id:       standard.ID,
			typeName: "standard",
			name:     standard.Name,
			bridge:   standard.BridgeName,
			standard: &standard,
		}, nil
	case "manual":
		var manual networkModels.ManualSwitch
		if err := db.First(&manual, switchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return resolvedVMNetworkSwitch{}, fmt.Errorf("switch_not_found: manual:%d", switchID)
			}
			return resolvedVMNetworkSwitch{}, fmt.Errorf("failed_to_find_manual_switch: %w", err)
		}
		return resolvedVMNetworkSwitch{
			id:       manual.ID,
			typeName: "manual",
			name:     manual.Name,
			bridge:   manual.Bridge,
			manual:   &manual,
		}, nil
	default:
		return resolvedVMNetworkSwitch{}, fmt.Errorf("switch_not_found: %s:%d", switchType, switchID)
	}
}

func resolveVMNetworkMACObject(db *gorm.DB, macID uint) (networkModels.Object, string, error) {
	var macObject networkModels.Object
	if err := db.Preload("Entries").First(&macObject, macID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return macObject, "", fmt.Errorf("mac_object_not_found: %w", err)
		}
		return macObject, "", fmt.Errorf("failed_to_find_mac_object: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(macObject.Type), "Mac") {
		return macObject, "", fmt.Errorf("invalid_mac_object_type: %s", macObject.Type)
	}
	if len(macObject.Entries) == 0 {
		return macObject, "", fmt.Errorf("mac_object_has_no_entries: %d", macID)
	}
	if len(macObject.Entries) != 1 {
		return macObject, "", fmt.Errorf("mac_object_has_multiple_entries: %d", macID)
	}

	mac, err := normalizeVMNetworkMAC(macObject.Entries[0].Value)
	if err != nil {
		return macObject, "", err
	}
	return macObject, mac, nil
}

func ensureVMNetworkMACAvailable(db *gorm.DB, macID, excludeNetworkID uint, mac string) error {
	var vmRefs int64
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Model(&vmModels.Network{}).
		Joins("LEFT JOIN object_entries AS vm_mac_entries ON vm_mac_entries.object_id = vm_networks.mac_id").
		Where("vm_networks.id <> ?", excludeNetworkID).
		Where(
			"vm_networks.mac_id = ? OR LOWER(TRIM(vm_networks.mac)) = ? OR LOWER(TRIM(vm_mac_entries.value)) = ?",
			macID,
			mac,
			mac,
		).
		Count(&vmRefs).Error; err != nil {
		return fmt.Errorf("failed_to_check_vm_network_mac_usage: %w", err)
	}
	if vmRefs > 0 {
		return fmt.Errorf("mac_address_already_in_use: %s", mac)
	}

	var jailRefs int64
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Model(&jailModels.Network{}).
		Joins("LEFT JOIN object_entries AS jail_mac_entries ON jail_mac_entries.object_id = jail_networks.mac_id").
		Where("jail_networks.mac_id = ? OR LOWER(TRIM(jail_mac_entries.value)) = ?", macID, mac).
		Count(&jailRefs).Error; err != nil {
		return fmt.Errorf("failed_to_check_jail_network_mac_usage: %w", err)
	}
	if jailRefs > 0 {
		return fmt.Errorf("mac_address_already_in_use: %s", mac)
	}

	return nil
}

func createVMNetworkMACObject(
	db *gorm.DB,
	vmName string,
	switchName string,
) (networkModels.Object, string, error) {
	base := fmt.Sprintf("%s-%s", strings.TrimSpace(vmName), strings.TrimSpace(switchName))
	name := base
	for suffix := 0; ; suffix++ {
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}

		var exists int64
		if err := db.Model(&networkModels.Object{}).Where("name = ?", name).Count(&exists).Error; err != nil {
			return networkModels.Object{}, "", fmt.Errorf("failed_to_check_mac_object_exists: %w", err)
		}
		if exists == 0 {
			break
		}
	}

	mac, err := normalizeVMNetworkMAC(utils.GenerateRandomMAC())
	if err != nil {
		return networkModels.Object{}, "", fmt.Errorf("failed_to_generate_mac_address: %w", err)
	}
	macObject := networkModels.Object{Name: name, Type: "Mac"}
	if err := db.Create(&macObject).Error; err != nil {
		return networkModels.Object{}, "", fmt.Errorf("failed_to_create_mac_object: %w", err)
	}
	entry := networkModels.ObjectEntry{ObjectID: macObject.ID, Value: mac}
	if err := db.Create(&entry).Error; err != nil {
		return networkModels.Object{}, "", fmt.Errorf("failed_to_create_mac_entry: %w", err)
	}
	macObject.Entries = []networkModels.ObjectEntry{entry}
	return macObject, mac, nil
}

func loadVMNetworkResponseWithDB(db *gorm.DB, vmID, networkID uint) (vmModels.Network, error) {
	network, err := findVMNetworkRecordWithDB(db, vmID, networkID)
	if err != nil {
		return network, err
	}
	sw, err := resolveVMNetworkSwitchByID(db, network.SwitchType, network.SwitchID)
	if err != nil {
		return network, err
	}
	network.StandardSwitch = sw.standard
	network.ManualSwitch = sw.manual
	return network, nil
}

func (s *Service) normalizeNetworkRuntimeHooks(hooks networkRuntimeHooks) networkRuntimeHooks {
	if hooks.syncVMNetworks == nil {
		hooks.syncVMNetworks = func(ctx context.Context, db *gorm.DB, rid uint) error {
			return s.syncVMNetworksWithDB(ctx, db, rid)
		}
	}
	return hooks
}

func (s *Service) syncVMNetworksWithDB(ctx context.Context, db *gorm.DB, rid uint) error {
	if db == nil {
		return fmt.Errorf("db_not_initialized")
	}
	ctx = normalizeVMNetworkContext(ctx)

	if err := s.requireConnection(); err != nil {
		return err
	}
	shutoff, err := s.IsDomainShutOff(rid)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return fmt.Errorf("domain_state_not_shutoff: %d", rid)
	}

	domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}
	xmlDesc, err := s.conn().DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xmlDesc); err != nil {
		return fmt.Errorf("failed_to_parse_vm_xml: %w", err)
	}
	domainElement := doc.Root()
	if domainElement == nil || domainElement.Tag != "domain" {
		return fmt.Errorf("malformed_vm_xml: missing_domain_element")
	}
	devicesElement := domainElement.FindElement("devices")
	if devicesElement == nil {
		devicesElement = domainElement.CreateElement("devices")
	}

	// VM network rows are authoritative for bridge interfaces managed by Sylve.
	// Rebuilding them also removes stale legacy/raw-MAC interfaces reliably.
	for _, iface := range doc.FindElements("//interface[@type='bridge']") {
		if parent := iface.Parent(); parent != nil {
			parent.RemoveChild(iface)
		}
	}

	vm, err := findVMNetworkOwnerWithDB(db, rid)
	if err != nil {
		return err
	}
	var networks []vmModels.Network
	if err := db.Session(&gorm.Session{SkipHooks: true}).
		Preload("AddressObj").
		Preload("AddressObj.Entries").
		Where("vm_id = ?", vm.ID).
		Order("id ASC").
		Find(&networks).Error; err != nil {
		return fmt.Errorf("failed_to_list_vm_networks: %w", err)
	}

	for _, network := range networks {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("network_sync_cancelled: %w", err)
		}
		if !network.Enable {
			continue
		}

		var mac string
		if network.MacID != nil && *network.MacID > 0 {
			_, mac, err = resolveVMNetworkMACObject(db, *network.MacID)
		} else {
			mac, err = normalizeVMNetworkMAC(network.MAC)
			if strings.TrimSpace(network.MAC) == "" {
				err = fmt.Errorf("network_mac_address_missing: %d", network.ID)
			}
		}
		if err != nil {
			return fmt.Errorf("failed_to_resolve_network_mac_%d: %w", network.ID, err)
		}

		sw, err := resolveVMNetworkSwitchByID(db, network.SwitchType, network.SwitchID)
		if err != nil {
			return fmt.Errorf("failed_to_resolve_network_switch_%d: %w", network.ID, err)
		}
		emulation, err := normalizeVMNetworkEmulation(network.Emulation)
		if err != nil {
			return fmt.Errorf("failed_to_resolve_network_emulation_%d: %w", network.ID, err)
		}

		iface := devicesElement.CreateElement("interface")
		iface.CreateAttr("type", "bridge")
		macElement := iface.CreateElement("mac")
		macElement.CreateAttr("address", mac)
		sourceElement := iface.CreateElement("source")
		sourceElement.CreateAttr("bridge", sw.bridge)
		modelElement := iface.CreateElement("model")
		modelElement.CreateAttr("type", emulation)
	}

	newXML, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed_to_serialize_modified_xml: %w", err)
	}
	if _, err := s.conn().DomainDefineXML(newXML); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}
	if err := s.writeVMJsonWithDB(db, rid); err != nil {
		return fmt.Errorf("failed_to_write_vm_json_after_network_sync: %w", err)
	}
	return nil
}

func (s *Service) restoreVMNetworkMutation(rid uint, oldXML string) error {
	var restoreErr error
	if strings.TrimSpace(oldXML) != "" {
		if _, err := s.conn().DomainDefineXML(oldXML); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_domain_xml: %w", err))
		}
	}
	if err := s.WriteVMJson(rid); err != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_vm_json: %w", err))
	}
	return restoreErr
}

func (s *Service) NetworkAttach(
	req libvirtServiceInterfaces.NetworkAttachRequest,
	ctx context.Context,
) (*vmModels.Network, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}
	req.SwitchName = strings.TrimSpace(req.SwitchName)
	if req.SwitchName == "" {
		return nil, fmt.Errorf("invalid_switch_name")
	}
	emulation, err := normalizeVMNetworkEmulation(req.Emulation)
	if err != nil {
		return nil, err
	}
	req.Emulation = emulation

	vm, err := findVMNetworkOwnerWithDB(s.DB, req.RID)
	if err != nil {
		return nil, err
	}
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return nil, err
	}
	shutoff, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return nil, fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}
	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	network, err := s.networkAttachApply(normalizeVMNetworkContext(ctx), req, vm, networkRuntimeHooks{})
	if err == nil {
		return network, nil
	}
	if restoreErr := s.restoreVMNetworkMutation(req.RID, oldXML); restoreErr != nil {
		return nil, errors.Join(err, fmt.Errorf("network_reconciliation_failed: %w", restoreErr))
	}
	return nil, err
}

func (s *Service) networkAttachApply(
	ctx context.Context,
	req libvirtServiceInterfaces.NetworkAttachRequest,
	vm vmModels.VM,
	hooks networkRuntimeHooks,
) (*vmModels.Network, error) {
	hooks = s.normalizeNetworkRuntimeHooks(hooks)
	var result vmModels.Network
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		sw, err := resolveVMNetworkSwitchByName(tx, req.SwitchName)
		if err != nil {
			return err
		}

		var macObject networkModels.Object
		var mac string
		if req.MacID == nil || *req.MacID == 0 {
			macObject, mac, err = createVMNetworkMACObject(tx, vm.Name, sw.name)
		} else {
			macObject, mac, err = resolveVMNetworkMACObject(tx, *req.MacID)
		}
		if err != nil {
			return err
		}
		if err := ensureVMNetworkMACAvailable(tx, macObject.ID, 0, mac); err != nil {
			return err
		}

		macID := macObject.ID
		network := vmModels.Network{
			VMID:       vm.ID,
			SwitchID:   sw.id,
			SwitchType: sw.typeName,
			MacID:      &macID,
			AddressObj: &macObject,
			Emulation:  req.Emulation,
			Enable:     true,
		}
		if err := tx.Create(&network).Error; err != nil {
			return fmt.Errorf("failed_to_create_network_record: %w", err)
		}
		if err := hooks.syncVMNetworks(ctx, tx, req.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_networks: %w", err)
		}

		result, err = loadVMNetworkResponseWithDB(tx, vm.ID, network.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) NetworkUpdate(
	req libvirtServiceInterfaces.NetworkUpdateRequest,
	ctx context.Context,
) (*vmModels.Network, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return nil, fmt.Errorf("invalid_rid")
	}
	if req.NetworkID == 0 {
		return nil, fmt.Errorf("invalid_network_id")
	}
	if req.SwitchName == nil && req.Emulation == nil && req.MacID == nil && req.Enable == nil {
		return nil, fmt.Errorf("empty_network_update")
	}
	if req.SwitchName != nil {
		value := strings.TrimSpace(*req.SwitchName)
		if value == "" {
			return nil, fmt.Errorf("invalid_switch_name")
		}
		req.SwitchName = &value
	}
	if req.Emulation != nil {
		value, err := normalizeVMNetworkEmulation(*req.Emulation)
		if err != nil {
			return nil, err
		}
		req.Emulation = &value
	}

	vm, err := findVMNetworkOwnerWithDB(s.DB, req.RID)
	if err != nil {
		return nil, err
	}
	if _, err := findVMNetworkRecordWithDB(s.DB, vm.ID, req.NetworkID); err != nil {
		return nil, err
	}
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return nil, err
	}
	shutoff, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return nil, fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}
	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	network, err := s.networkUpdateApply(normalizeVMNetworkContext(ctx), req, vm, networkRuntimeHooks{})
	if err == nil {
		return network, nil
	}
	if restoreErr := s.restoreVMNetworkMutation(req.RID, oldXML); restoreErr != nil {
		return nil, errors.Join(err, fmt.Errorf("network_reconciliation_failed: %w", restoreErr))
	}
	return nil, err
}

func (s *Service) networkUpdateApply(
	ctx context.Context,
	req libvirtServiceInterfaces.NetworkUpdateRequest,
	vm vmModels.VM,
	hooks networkRuntimeHooks,
) (*vmModels.Network, error) {
	hooks = s.normalizeNetworkRuntimeHooks(hooks)
	var result vmModels.Network
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		network, err := findVMNetworkRecordWithDB(tx, vm.ID, req.NetworkID)
		if err != nil {
			return err
		}

		var sw resolvedVMNetworkSwitch
		if req.SwitchName != nil {
			sw, err = resolveVMNetworkSwitchByName(tx, *req.SwitchName)
		} else {
			sw, err = resolveVMNetworkSwitchByID(tx, network.SwitchType, network.SwitchID)
		}
		if err != nil {
			return err
		}

		emulation := network.Emulation
		if req.Emulation != nil {
			emulation = *req.Emulation
		}
		emulation, err = normalizeVMNetworkEmulation(emulation)
		if err != nil {
			return err
		}

		enabled := network.Enable
		if req.Enable != nil {
			enabled = *req.Enable
		}

		var macObject networkModels.Object
		var macID *uint
		var mac string
		switch {
		case req.MacID != nil && *req.MacID == 0:
			macObject, mac, err = createVMNetworkMACObject(tx, vm.Name, sw.name)
			if err == nil {
				id := macObject.ID
				macID = &id
			}
		case req.MacID != nil:
			macObject, mac, err = resolveVMNetworkMACObject(tx, *req.MacID)
			if err == nil {
				id := macObject.ID
				macID = &id
			}
		case network.MacID != nil && *network.MacID > 0:
			macObject, mac, err = resolveVMNetworkMACObject(tx, *network.MacID)
			if err == nil {
				id := macObject.ID
				macID = &id
			}
		default:
			if strings.TrimSpace(network.MAC) == "" {
				err = fmt.Errorf("network_mac_address_missing: %d", network.ID)
			} else {
				mac, err = normalizeVMNetworkMAC(network.MAC)
			}
		}
		if err != nil {
			return err
		}
		macObjectID := uint(0)
		if macID != nil {
			macObjectID = *macID
		}
		if err := ensureVMNetworkMACAvailable(tx, macObjectID, network.ID, mac); err != nil {
			return err
		}

		updates := map[string]any{
			"switch_id":   sw.id,
			"switch_type": sw.typeName,
			"emulation":   emulation,
			"enable":      enabled,
			"mac_id":      macID,
			"mac":         "",
		}
		if macID == nil {
			updates["mac"] = mac
		}
		if err := tx.Model(&vmModels.Network{}).
			Where("id = ? AND vm_id = ?", network.ID, vm.ID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("failed_to_update_network_record: %w", err)
		}
		if err := hooks.syncVMNetworks(ctx, tx, req.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_networks: %w", err)
		}

		result, err = loadVMNetworkResponseWithDB(tx, vm.ID, network.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Service) NetworkDetach(
	req libvirtServiceInterfaces.NetworkDetachRequest,
	ctx context.Context,
) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("db_not_initialized")
	}
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	if req.RID == 0 {
		return fmt.Errorf("invalid_rid")
	}
	if req.NetworkID == 0 {
		return fmt.Errorf("invalid_network_id")
	}
	vm, err := findVMNetworkOwnerWithDB(s.DB, req.RID)
	if err != nil {
		return err
	}
	if _, err := findVMNetworkRecordWithDB(s.DB, vm.ID, req.NetworkID); err != nil {
		return err
	}
	if err := s.requireVMMutationOwnership(req.RID); err != nil {
		return err
	}
	shutoff, err := s.IsDomainShutOff(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_check_vm_shutoff: %w", err)
	}
	if !shutoff {
		return fmt.Errorf("domain_state_not_shutoff: %d", req.RID)
	}
	oldXML, err := s.GetVMXML(req.RID)
	if err != nil {
		return fmt.Errorf("failed_to_capture_domain_xml: %w", err)
	}

	err = s.networkDetachApply(normalizeVMNetworkContext(ctx), req, vm.ID, networkRuntimeHooks{})
	if err == nil {
		return nil
	}
	if restoreErr := s.restoreVMNetworkMutation(req.RID, oldXML); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("network_reconciliation_failed: %w", restoreErr))
	}
	return err
}

func (s *Service) networkDetachApply(
	ctx context.Context,
	req libvirtServiceInterfaces.NetworkDetachRequest,
	vmID uint,
	hooks networkRuntimeHooks,
) error {
	hooks = s.normalizeNetworkRuntimeHooks(hooks)
	return s.DB.Transaction(func(tx *gorm.DB) error {
		network, err := findVMNetworkRecordWithDB(tx, vmID, req.NetworkID)
		if err != nil {
			return err
		}
		if err := tx.Delete(&network).Error; err != nil {
			return fmt.Errorf("failed_to_delete_network_record: %w", err)
		}
		if err := hooks.syncVMNetworks(ctx, tx, req.RID); err != nil {
			return fmt.Errorf("failed_to_sync_vm_networks: %w", err)
		}
		return nil
	})
}

func (s *Service) FindAndChangeMAC(rid uint, oldMac string, newMac string) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	xml, err := s.conn().DomainGetXMLDesc(domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	doc := etree.NewDocument()
	if err := doc.ReadFromString(xml); err != nil {
		return fmt.Errorf("failed_to_parse_domain_xml: %w", err)
	}

	oldMac = strings.ToLower(oldMac)
	newMac = strings.ToLower(newMac)

	macEl := doc.FindElement("//mac[@address='" + oldMac + "']")
	if macEl == nil {
		return fmt.Errorf("mac_address_not_found_in_xml: %s", oldMac)
	}

	addrAttr := macEl.SelectAttr("address")
	if addrAttr != nil {
		addrAttr.Value = newMac
	} else {
		macEl.CreateAttr("address", newMac)
	}

	out, err := doc.WriteToString()
	if err != nil {
		return fmt.Errorf("failed to serialize XML: %w", err)
	}

	if err := s.conn().DomainUndefineFlags(domain, 0); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	if _, err := s.conn().DomainDefineXML(out); err != nil {
		return fmt.Errorf("failed_to_define_domain_with_modified_xml: %w", err)
	}

	err = s.WriteVMJson(rid)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after MAC modification")
	}

	return nil
}

func (s *Service) FindVmByMac(mac string) (vmModels.VM, error) {
	var vm vmModels.VM

	vms, err := s.FindVMsByMac(mac)
	if err != nil {
		return vm, fmt.Errorf("failed_to_find_network: %w", err)
	}

	if len(vms) == 0 {
		return vm, fmt.Errorf("failed_to_find_network: %w", gorm.ErrRecordNotFound)
	}

	for _, candidate := range vms {
		if candidate.WoL {
			return candidate, nil
		}
	}

	vm = vms[0]
	return vm, fmt.Errorf("vm_wol_disabled: %s", vm.Name)
}

func (s *Service) FindVMsByMac(mac string) ([]vmModels.VM, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return []vmModels.VM{}, nil
	}

	var vmIDs []uint
	err := s.DB.
		Session(&gorm.Session{SkipHooks: true}).
		Model(&vmModels.Network{}).
		Joins("LEFT JOIN objects ON vm_networks.mac_id = objects.id").
		Joins("LEFT JOIN object_entries ON object_entries.object_id = objects.id").
		Where("LOWER(object_entries.value) = ? OR LOWER(vm_networks.mac) = ?", mac, mac).
		Distinct("vm_networks.vm_id").
		Pluck("vm_networks.vm_id", &vmIDs).Error
	if err != nil {
		return nil, fmt.Errorf("failed_to_find_vm_networks: %w", err)
	}

	if len(vmIDs) == 0 {
		return []vmModels.VM{}, nil
	}

	var vms []vmModels.VM
	if err := s.DB.
		Where("id IN ?", vmIDs).
		Find(&vms).Error; err != nil {
		return nil, fmt.Errorf("failed_to_find_vms: %w", err)
	}

	return vms, nil
}

func (s *Service) FindJailsByMac(mac string) ([]jailModels.Jail, error) {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return []jailModels.Jail{}, nil
	}

	var jailIDs []uint
	err := s.DB.
		Session(&gorm.Session{SkipHooks: true}).
		Model(&jailModels.Network{}).
		Joins("LEFT JOIN objects ON jail_networks.mac_id = objects.id").
		Joins("LEFT JOIN object_entries ON object_entries.object_id = objects.id").
		Where("LOWER(object_entries.value) = ?", mac).
		Distinct("jail_networks.jid").
		Pluck("jail_networks.jid", &jailIDs).Error
	if err != nil {
		return nil, fmt.Errorf("failed_to_find_jail_networks: %w", err)
	}

	if len(jailIDs) == 0 {
		return []jailModels.Jail{}, nil
	}

	var jails []jailModels.Jail
	if err := s.DB.
		Where("id IN ?", jailIDs).
		Find(&jails).Error; err != nil {
		return nil, fmt.Errorf("failed_to_find_jails: %w", err)
	}

	return jails, nil
}
