// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	utils "github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func isValidPortToken(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}

	// Reject grouped inputs like "80,443" or "80 443". One token per entry only.
	if strings.ContainsAny(v, ", \t\r\n") {
		return false
	}

	if strings.Contains(v, ":") {
		parts := strings.Split(v, ":")
		if len(parts) != 2 {
			return false
		}

		start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || !utils.IsValidPort(start) {
			return false
		}

		end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || !utils.IsValidPort(end) {
			return false
		}

		return start <= end
	}

	port, err := strconv.Atoi(v)
	if err != nil {
		return false
	}

	return utils.IsValidPort(port)
}

func objectRefreshSettings(oType string) (autoUpdate bool, refreshInterval uint) {
	switch oType {
	case "FQDN", "List":
		return true, uint(defaultObjectRefreshInterval / time.Second)
	default:
		return false, 0
	}
}

func (s *Service) GetObjects() ([]networkModels.Object, error) {
	var objects []networkModels.Object

	err := s.DB.
		Preload("Entries").
		Find(&objects).Error

	if err != nil {
		return objects, fmt.Errorf("failed to retrieve network objects: %w", err)
	}

	if err := s.populateObjectUsage(objects); err != nil {
		return nil, err
	}

	return objects, nil
}

func (s *Service) populateObjectUsage(objects []networkModels.Object) error {
	if len(objects) == 0 {
		return nil
	}

	objectIDs := make([]uint, 0, len(objects))
	for _, object := range objects {
		objectIDs = append(objectIDs, object.ID)
	}

	used := make(map[uint]bool, len(objectIDs))
	usedBy := make(map[uint]string, len(objectIDs))

	markFromColumn := func(table, column, owner string) error {
		if !s.DB.Migrator().HasTable(table) || !s.DB.Migrator().HasColumn(table, column) {
			return nil
		}

		ids := []uint{}
		if err := s.DB.Table(table).
			Where(column+" IN ?", objectIDs).
			Pluck(column, &ids).Error; err != nil {
			return err
		}

		for _, id := range ids {
			used[id] = true
			if owner == "dhcp" {
				usedBy[id] = owner
				continue
			}
			if owner != "" && usedBy[id] == "" {
				usedBy[id] = owner
			}
		}

		return nil
	}

	// Firewall references.
	firewallColumns := []struct {
		table  string
		column string
	}{
		{"firewall_traffic_rules", "source_obj_id"},
		{"firewall_traffic_rules", "dest_obj_id"},
		{"firewall_traffic_rules", "src_port_obj_id"},
		{"firewall_traffic_rules", "dst_port_obj_id"},
		{"firewall_nat_rules", "source_obj_id"},
		{"firewall_nat_rules", "dest_obj_id"},
		{"firewall_nat_rules", "translate_to_obj_id"},
		{"firewall_nat_rules", "dnat_target_obj_id"},
		{"firewall_nat_rules", "dst_port_obj_id"},
		{"firewall_nat_rules", "redirect_port_obj_id"},
	}
	for _, col := range firewallColumns {
		if err := markFromColumn(col.table, col.column, "firewall"); err != nil {
			return err
		}
	}

	// DHCP references.
	if err := markFromColumn("dhcp_static_leases", "ip_object_id", ""); err != nil {
		return err
	}
	if err := markFromColumn("dhcp_static_leases", "mac_object_id", "dhcp"); err != nil {
		return err
	}
	if err := markFromColumn("dhcp_static_leases", "duid_object_id", "dhcp"); err != nil {
		return err
	}

	// VM / jail / switch references.
	for _, column := range []string{"mac_id"} {
		if err := markFromColumn("vm_networks", column, ""); err != nil {
			return err
		}
	}
	for _, column := range []string{"mac_id", "ipv4_id", "ipv4_gw_id", "ipv6_id", "ipv6_gw_id"} {
		if err := markFromColumn("jail_networks", column, ""); err != nil {
			return err
		}
	}
	for _, column := range []string{"network_object_id", "network6_object_id", "gateway_address_object_id", "gateway6_address_object_id"} {
		if err := markFromColumn("standard_switches", column, ""); err != nil {
			return err
		}
	}

	for i := range objects {
		id := objects[i].ID
		objects[i].IsUsed = used[id]
		objects[i].IsUsedBy = usedBy[id]
	}

	return nil
}

func validateType(oType string) error {
	validTypes := map[string]bool{
		"Host":    true,
		"Network": true,
		"Port":    true,
		"Country": true,
		"List":    true,
		"Mac":     true,
		"FQDN":    true,
		"DUID":    true,
	}

	if !validTypes[oType] {
		return fmt.Errorf("invalid object type: %s", oType)
	}

	return nil
}

func validateValues(oType string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("values cannot be empty for type: %s", oType)
	}

	if oType == "Host" {
		isIPv4 := false
		isIPv6 := false

		for _, value := range values {
			if utils.IsValidIPv4(value) {
				isIPv4 = true
			} else if utils.IsValidIPv6(value) {
				isIPv6 = true
			} else {
				return fmt.Errorf("invalid host value: %s", value)
			}

			if isIPv4 && isIPv6 {
				return fmt.Errorf("cannot mix IPv4 and IPv6 in host values")
			}
		}
	}

	if oType == "Network" {
		isIPv4 := false
		isIPv6 := false

		for _, value := range values {
			if utils.IsValidIPv4CIDR(value) {
				isIPv4 = true
			} else if utils.IsValidIPv6CIDR(value) {
				isIPv6 = true
			} else {
				return fmt.Errorf("invalid network value: %s", value)
			}

			if isIPv4 && isIPv6 {
				return fmt.Errorf("cannot mix IPv4 and IPv6 in network values")
			}
		}
	}

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("value cannot be empty for type: %s", oType)
		}

		if oType == "Port" {
			if !isValidPortToken(value) {
				return fmt.Errorf("invalid port value: %s", value)
			}
		}

		if oType == "Country" {
			if !utils.IsValidCountryCode(value) {
				return fmt.Errorf("invalid country code: %s", value)
			}
		}

		if oType == "Mac" {
			if !utils.IsValidMAC(value) {
				return fmt.Errorf("invalid MAC address: %s", value)
			}
		}

		if oType == "FQDN" {
			if !utils.IsValidFQDN(value) {
				return fmt.Errorf("invalid FQDN: %s", value)
			}
		}

		if oType == "List" {
			parsed, err := url.ParseRequestURI(value)
			if err != nil || parsed == nil {
				return fmt.Errorf("invalid list source url: %s", value)
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return fmt.Errorf("invalid list source url scheme: %s", value)
			}
		}

		if oType == "DUID" {
			if !utils.IsValidDUID(value) {
				return fmt.Errorf("invalid DUID: %s", value)
			}
		}
	}

	return nil
}

func (s *Service) IsObjectUsed(id uint) (bool, string, error) {
	var object networkModels.Object

	if err := s.DB.First(&object, id).Error; err != nil {
		return false, "", fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}

	if object.Type == "Host" {
		var switches []networkModels.StandardSwitch
		var jailNetworks []jailModels.Network
		var dhcpLeases []networkModels.DHCPStaticLease

		if err := s.DB.
			Preload("NetworkObj.Entries").
			Preload("Network6Obj.Entries").
			Preload("GatewayAddressObj.Entries").
			Preload("Gateway6AddressObj.Entries").
			Find(&switches).Error; err != nil {
			return true, "", err
		}

		if err := s.DB.Find(&jailNetworks).Error; err != nil {
			return true, "", fmt.Errorf("failed to find jail networks: %d %w", id, err)
		}

		if err := s.DB.Preload("IPObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, "", fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, sw := range switches {
			if sw.GatewayAddressObj != nil {
				if sw.GatewayAddressObj.ID == id {
					return true, "", nil
				}
			}

			if sw.Gateway6AddressObj != nil {
				if sw.Gateway6AddressObj.ID == id {
					return true, "", nil
				}
			}
		}

		for _, jn := range jailNetworks {
			if jn.IPv4ID != nil {
				if *jn.IPv4ID == id {
					return true, "", nil
				}
			}

			if jn.IPv4GwID != nil {
				if *jn.IPv4GwID == id {
					return true, "", nil
				}
			}

			if jn.IPv6ID != nil {
				if *jn.IPv6ID == id {
					return true, "", nil
				}
			}

			if jn.IPv6GwID != nil {
				if *jn.IPv6GwID == id {
					return true, "", nil
				}
			}

			if jn.MacID != nil {
				if *jn.MacID == id {
					return true, "", nil
				}
			}
		}

		for _, dl := range dhcpLeases {
			if dl.IPObject != nil {
				if dl.IPObject.ID == id {
					return true, "", nil
				}
			}
		}

		return false, "", nil
	}

	if object.Type == "Mac" {
		var vmNetworks []vmModels.Network
		var jailNetworks []jailModels.Network
		var dhcpLeases []networkModels.DHCPStaticLease

		if err := s.DB.Where("mac_id = ?", id).Find(&vmNetworks).Error; err != nil {
			return true, "", fmt.Errorf("failed to find VM networks using object %d: %w", id, err)
		}

		if len(vmNetworks) > 0 {
			return true, "", nil
		}

		if err := s.DB.Where("mac_id = ?", id).Find(&jailNetworks).Error; err != nil {
			return true, "", fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
		}

		if len(jailNetworks) > 0 {
			return true, "", nil
		}

		if err := s.DB.Preload("MACObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, "", fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, dl := range dhcpLeases {
			if dl.MACObject != nil {
				if dl.MACObject.ID == id {
					return true, "dhcp", nil
				}
			}
		}

		return false, "", nil
	}

	if object.Type == "Network" {
		var jailNetworks []jailModels.Network
		if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
			return true, "", fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
		}

		if len(jailNetworks) > 0 {
			return true, "", nil
		}

		var switches []networkModels.StandardSwitch

		if err := s.DB.Where("network_object_id = ? OR network6_object_id = ?", id, id).Find(&switches).Error; err != nil {
			return true, "", fmt.Errorf("failed to find switches using object %d: %w", id, err)
		}

		if len(switches) > 0 {
			return true, "", nil
		}
	}

	if object.Type == "DUID" {
		var dhcpLeases []networkModels.DHCPStaticLease
		if err := s.DB.Preload("DUIDObject.Entries").Find(&dhcpLeases).Error; err != nil {
			return true, "", fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
		}

		for _, dl := range dhcpLeases {
			if dl.DUIDObject != nil {
				if dl.DUIDObject.ID == id {
					return true, "dhcp", nil
				}
			}
		}
	}

	{
		var trafficCount int64
		if err := s.DB.
			Model(&networkModels.FirewallTrafficRule{}).
			Where("source_obj_id = ? OR dest_obj_id = ? OR src_port_obj_id = ? OR dst_port_obj_id = ?", id, id, id, id).
			Count(&trafficCount).Error; err != nil {
			return true, "", fmt.Errorf("failed to check firewall traffic rule usage for object %d: %w", id, err)
		}
		if trafficCount > 0 {
			return true, "firewall", nil
		}
	}

	{
		var natCount int64
		if err := s.DB.
			Model(&networkModels.FirewallNATRule{}).
			Where(
				"source_obj_id = ? OR dest_obj_id = ? OR translate_to_obj_id = ? OR dnat_target_obj_id = ? OR dst_port_obj_id = ? OR redirect_port_obj_id = ?",
				id, id, id, id, id, id,
			).
			Count(&natCount).Error; err != nil {
			return true, "", fmt.Errorf("failed to check firewall nat rule usage for object %d: %w", id, err)
		}
		if natCount > 0 {
			return true, "firewall", nil
		}
	}

	return false, "", nil
}

func (s *Service) CreateObject(name string, oType string, values []string) (uint, error) {
	if err := validateType(oType); err != nil {
		return 0, err
	}

	if err := validateValues(oType, values); err != nil {
		return 0, err
	}

	var count int64
	if err := s.DB.
		Model(&networkModels.Object{}).
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		return 0, err
	}

	if count > 0 {
		return 0, fmt.Errorf("object_with_name_already_exists: %s", name)
	}

	entries := make([]networkModels.ObjectEntry, len(values))
	for i, value := range values {
		entries[i] = networkModels.ObjectEntry{
			Value: value,
		}
	}

	autoUpdate, refreshInterval := objectRefreshSettings(oType)

	object := networkModels.Object{
		Name:                   name,
		Type:                   oType,
		AutoUpdate:             autoUpdate,
		RefreshIntervalSeconds: refreshInterval,
		Entries:                entries,
	}

	if err := s.DB.Create(&object).Error; err != nil {
		return 0, err
	}

	if oType == "FQDN" || oType == "List" {
		if err := s.RefreshObjectByID(object.ID); err != nil {
			_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectListSnapshot{}).Error
			_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectResolution{}).Error
			_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectEntry{}).Error
			_ = s.DB.Delete(&networkModels.Object{}, object.ID).Error
			return 0, err
		}
	} else if err := s.ApplyFirewallIfEnabled(); err != nil {
		_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectListSnapshot{}).Error
		_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectResolution{}).Error
		_ = s.DB.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectEntry{}).Error
		_ = s.DB.Delete(&networkModels.Object{}, object.ID).Error
		return 0, err
	}

	return object.ID, nil
}

func (s *Service) DeleteObject(id uint) error {
	used, _, err := s.IsObjectUsed(id)
	if err != nil {
		return fmt.Errorf("failed to check if object %d is used: %w", id, err)
	}

	if used {
		return fmt.Errorf("object %d is currently in use and cannot be deleted", id)
	}

	var object networkModels.Object
	if err := s.DB.Preload("Entries").Preload("Resolutions").First(&object, id).Error; err != nil {
		return fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}
	if err := s.hydrateListSnapshotResolutions(&object); err != nil {
		return err
	}
	previousState := cloneObjectState(object)

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectListSnapshot{}).Error; err != nil {
		return fmt.Errorf("failed to delete list snapshots for object %d: %w", id, err)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
		return fmt.Errorf("failed to delete resolutions for object %d: %w", id, err)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return fmt.Errorf("failed to delete entries for object %d: %w", id, err)
	}

	if err := s.DB.Delete(&networkModels.Object{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete object %d: %w", id, err)
	}

	if err := s.ApplyFirewallIfEnabled(); err != nil {
		if rollbackErr := s.restoreObjectState(previousState); rollbackErr != nil {
			return fmt.Errorf("failed to apply firewall after object %d deletion: %w (rollback_failed: %v)", id, err, rollbackErr)
		}
		return err
	}

	return nil
}

func (s *Service) BulkDeleteObjects(ids []uint) error {
	for _, id := range ids {
		used, _, err := s.IsObjectUsed(id)
		if err != nil {
			return fmt.Errorf("failed to check if object %d is used: %w", id, err)
		}

		if used {
			var obj networkModels.Object
			if dErr := s.DB.First(&obj, id).Error; dErr == nil {
				return fmt.Errorf("object '%s' is in use and cannot be deleted", obj.Name)
			}
			return fmt.Errorf("object %d is in use and cannot be deleted", id)
		}
	}

	for _, id := range ids {
		if err := s.DeleteObject(id); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) IsObjectUsedByJail(id uint) (bool, []uint, error) {
	var jailNetworks []jailModels.Network
	var jailIds []uint

	if err := s.DB.Where("mac_id = ? OR ipv4_id = ? OR ipv6_id = ?", id, id, id).Find(&jailNetworks).Error; err != nil {
		return false, []uint{}, fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
	}

	if len(jailNetworks) > 0 {
		for _, jn := range jailNetworks {
			jailIds = append(jailIds, jn.JailID)
		}

		return true, jailIds, nil
	}

	return false, []uint{}, nil
}

func cloneObjectState(object networkModels.Object) networkModels.Object {
	out := object
	if object.LastRefreshAt != nil {
		t := *object.LastRefreshAt
		out.LastRefreshAt = &t
	}
	out.Entries = append([]networkModels.ObjectEntry(nil), object.Entries...)
	out.Resolutions = append([]networkModels.ObjectResolution(nil), object.Resolutions...)
	return out
}

func (s *Service) hydrateListSnapshotResolutions(object *networkModels.Object) error {
	if object == nil || object.Type != "List" {
		return nil
	}

	values, err := s.loadListSnapshotValues(object.ID)
	if err != nil {
		return fmt.Errorf("failed to load list snapshot for object %d: %w", object.ID, err)
	}

	if len(values) == 0 {
		return nil
	}

	object.Resolutions = make([]networkModels.ObjectResolution, 0, len(values))
	for _, value := range values {
		object.Resolutions = append(object.Resolutions, networkModels.ObjectResolution{
			ObjectID:      object.ID,
			ResolvedValue: value,
		})
	}

	return nil
}

func (s *Service) restoreObjectState(previous networkModels.Object) error {
	restored := networkModels.Object{
		ID:                     previous.ID,
		Name:                   previous.Name,
		Type:                   previous.Type,
		Comment:                previous.Comment,
		AutoUpdate:             previous.AutoUpdate,
		RefreshIntervalSeconds: previous.RefreshIntervalSeconds,
		SourceChecksum:         previous.SourceChecksum,
		ResolutionChecksum:     previous.ResolutionChecksum,
		LastRefreshAt:          previous.LastRefreshAt,
		LastRefreshError:       previous.LastRefreshError,
		CreatedAt:              previous.CreatedAt,
		UpdatedAt:              previous.UpdatedAt,
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&restored).Error; err != nil {
			return fmt.Errorf("failed to restore object %d: %w", previous.ID, err)
		}

		if err := tx.Where("object_id = ?", previous.ID).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
			return fmt.Errorf("failed to clear object %d entries during rollback: %w", previous.ID, err)
		}
		if err := tx.Where("object_id = ?", previous.ID).Delete(&networkModels.ObjectListSnapshot{}).Error; err != nil {
			return fmt.Errorf("failed to clear object %d list snapshots during rollback: %w", previous.ID, err)
		}
		if err := tx.Where("object_id = ?", previous.ID).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
			return fmt.Errorf("failed to clear object %d resolutions during rollback: %w", previous.ID, err)
		}

		for _, entry := range previous.Entries {
			row := networkModels.ObjectEntry{
				ObjectID:  previous.ID,
				Value:     entry.Value,
				CreatedAt: entry.CreatedAt,
				UpdatedAt: entry.UpdatedAt,
			}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("failed to restore object %d entry: %w", previous.ID, err)
			}
		}

		if previous.Type != "List" {
			for _, resolution := range previous.Resolutions {
				row := networkModels.ObjectResolution{
					ObjectID:      previous.ID,
					ResolvedIP:    resolution.ResolvedIP,
					ResolvedValue: resolution.ResolvedValue,
					CreatedAt:     resolution.CreatedAt,
					UpdatedAt:     resolution.UpdatedAt,
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("failed to restore object %d resolution: %w", previous.ID, err)
				}
			}
		}

		if previous.Type == "List" {
			values := make([]string, 0, len(previous.Resolutions))
			for _, resolution := range previous.Resolutions {
				v := strings.TrimSpace(resolution.ResolvedValue)
				if v != "" {
					values = append(values, v)
				}
			}
			if err := storeListSnapshot(tx, previous.ID, previous.ResolutionChecksum, values); err != nil {
				return fmt.Errorf("failed to restore object %d list snapshot: %w", previous.ID, err)
			}
		}

		return nil
	})
}

func (s *Service) replaceObjectWithValues(id uint, name string, oType string, values []string, autoUpdate bool, refreshInterval uint, preserveResolutions bool) error {
	updates := map[string]interface{}{
		"name":                     name,
		"type":                     oType,
		"auto_update":              autoUpdate,
		"refresh_interval_seconds": refreshInterval,
	}

	if !preserveResolutions {
		updates["last_refresh_at"] = nil
		updates["last_refresh_error"] = ""
		updates["source_checksum"] = ""
		updates["resolution_checksum"] = ""
	}

	if err := s.DB.Model(&networkModels.Object{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update object %d: %w", id, err)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
	}

	if !preserveResolutions {
		if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
			return fmt.Errorf("failed to delete resolutions for object %d: %w", id, err)
		}
		if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectListSnapshot{}).Error; err != nil {
			return fmt.Errorf("failed to delete list snapshots for object %d: %w", id, err)
		}
	}

	for _, value := range values {
		entry := networkModels.ObjectEntry{
			ObjectID: id,
			Value:    value,
		}

		if err := s.DB.Create(&entry).Error; err != nil {
			return fmt.Errorf("failed to create entry for object %d: %w", id, err)
		}
	}

	return nil
}

func (s *Service) EditObject(id uint, name string, oType string, values []string) error {
	if err := validateType(oType); err != nil {
		return err
	}

	if err := validateValues(oType, values); err != nil {
		return err
	}

	var count int64
	if err := s.DB.
		Model(&networkModels.Object{}).
		Where("name = ? AND id != ?", name, id).
		Count(&count).Error; err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("object_with_name_already_exists: %s", name)
	}

	used, _, err := s.IsObjectUsed(id)
	if err != nil {
		return fmt.Errorf("failed to check if object %d is used: %w", id, err)
	}

	var object networkModels.Object
	if err := s.DB.Preload("Entries").
		Preload("Resolutions").
		First(&object, id).Error; err != nil {
		return fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}
	if err := s.hydrateListSnapshotResolutions(&object); err != nil {
		return err
	}
	previousState := cloneObjectState(object)

	autoUpdate, refreshInterval := objectRefreshSettings(oType)

	/* This object isn't used anywhere, yay! It's going to be an easy edit */
	if !used {
		preserveResolutions := (object.Type == "FQDN" || object.Type == "List") && (oType == "FQDN" || oType == "List")
		if err := s.replaceObjectWithValues(id, name, oType, values, autoUpdate, refreshInterval, preserveResolutions); err != nil {
			return err
		}
	} else {
		updated := false

		if object.Type == "Mac" {
			var vmNetworks []vmModels.Network
			var jailNetworks []jailModels.Network
			var dhcpLeases []networkModels.DHCPStaticLease

			if err := s.DB.Preload("AddressObj.Entries").Where("mac_id = ?", id).Find(&vmNetworks).Error; err != nil {
				return fmt.Errorf("failed to find VM networks using object %d: %w", id, err)
			}

			if err := s.DB.Preload("MacAddressObj.Entries").Where("mac_id = ?", id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if err := s.DB.Preload("MACObject.Entries").Where("mac_object_id = ?", id).Find(&dhcpLeases).Error; err != nil {
				return fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
			}

			var vm vmModels.VM
			if len(vmNetworks) > 0 {
				if err := s.DB.First(&vm, vmNetworks[0].VMID).Error; err != nil {
					return fmt.Errorf("failed to find VM for network %d: %w", vmNetworks[0].ID, err)
				}
			}

			/* Object was used in a VM, but now we're changing it to something else, we can't do that */
			if len(vmNetworks) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_vm")
			}

			/* MAC Used in a VM */
			if len(vmNetworks) > 0 && oType == "Mac" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d VM networks, please ensure only one MAC is provided", id, len(vmNetworks))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if s.LibVirt.IsVirtualizationEnabled() {
					active, err := s.LibVirt.IsDomainInactive(vm.RID)

					if err != nil {
						return fmt.Errorf("failed to check if VM %d is inactive: %w", vm.RID, err)
					}

					if !active {
						return fmt.Errorf("cannot_change_object_of_active_vm")
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				if s.LibVirt.IsVirtualizationEnabled() {
					err = s.LibVirt.FindAndChangeMAC(vm.RID, object.Entries[0].Value, values[0])
					if err != nil {
						return fmt.Errorf("failed to change MAC address in VM %d: %w", vm.RID, err)
					}
				}

				updated = true
			}

			/* Object was used in a Jail, but now we're changing it to something else, we can't do that */
			if len(jailNetworks) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			/* MAC Used in a Jail */
			if len(jailNetworks) > 0 && oType == "Mac" {
				err := s.AddNetworkObjectEditJailTrigger(id, name, oType, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}

				updated = true
			}

			/* Object was used in DHCP leases, but now we're changing it to something else, we can't do that */
			if len(dhcpLeases) > 0 && oType != "Mac" {
				return fmt.Errorf("cannot_change_object_type_dhcp")
			}

			/* MAC Used in DHCP leases */
			if len(dhcpLeases) > 0 && oType == "Mac" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d DHCP leases, please ensure only one MAC is provided", id, len(dhcpLeases))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}
					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.WriteDHCPConfig()
				if err != nil {
					logger.L.Error().Err(err).Msgf("failed to write DHCP config after editing object %d", id)
				}

				updated = true
			}
		}

		if object.Type == "Host" {
			/* IP used by switches */
			var switches []networkModels.StandardSwitch
			if err := s.DB.
				Preload("NetworkObj.Entries").
				Preload("Network6Obj.Entries").
				Preload("GatewayAddressObj.Entries").
				Preload("Gateway6AddressObj.Entries").
				Where("gateway_address_object_id = ? OR gateway6_address_object_id = ?", id, id).
				Find(&switches).Error; err != nil {
				return fmt.Errorf("failed to find standard switches using object %d: %w", id, err)
			}

			if len(switches) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_host_used_in_switches")
			}

			if len(switches) > 0 && oType == "Host" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d standard switches, please ensure only one IP is provided", id, len(switches))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.SyncStandardSwitches(nil, "sync")
				if err != nil {
					return fmt.Errorf("failed to sync standard switches after editing object %d: %w", id, err)
				}

				updated = true
			}

			/* IP used by jails */
			var jailNetworks []jailModels.Network
			if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if len(jailNetworks) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			if len(jailNetworks) > 0 && oType == "Host" {
				err := s.AddNetworkObjectEditJailTrigger(id, name, oType, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}

				updated = true
			}

			var dhcpLeases []networkModels.DHCPStaticLease
			if err := s.DB.Preload("IPObject.Entries").Where("ip_object_id = ?", id).Find(&dhcpLeases).Error; err != nil {
				return fmt.Errorf("failed to find DHCP leases: %d %w", id, err)
			}

			/* Object was used in DHCP leases, but now we're changing it to something else, we can't do that */
			if len(dhcpLeases) > 0 && oType != "Host" {
				return fmt.Errorf("cannot_change_object_type_dhcp")
			}

			/* IP Used in DHCP leases */
			if len(dhcpLeases) > 0 && oType == "Host" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d DHCP leases, please ensure only one IP is provided", id, len(dhcpLeases))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}
					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.WriteDHCPConfig()
				if err != nil {
					logger.L.Error().Err(err).Msgf("failed to write DHCP config after editing object %d", id)
				}

				updated = true
			}
		}

		if object.Type == "Network" {
			/* Network used by switches */
			var switches []networkModels.StandardSwitch
			if err := s.DB.
				Preload("NetworkObj.Entries").
				Preload("Network6Obj.Entries").
				Preload("GatewayAddressObj.Entries").
				Preload("Gateway6AddressObj.Entries").
				Where("network_object_id = ? OR network6_object_id = ?", id, id).
				Find(&switches).Error; err != nil {
				return fmt.Errorf("failed to find standard switches using object %d: %w", id, err)
			}

			if len(switches) > 0 && oType != "Network" {
				return fmt.Errorf("cannot_change_object_type_network_used_in_switches")
			}

			if len(switches) > 0 && oType == "Network" {
				if len(values) != 1 {
					return fmt.Errorf("cannot edit object %d, it is used by %d standard switches, please ensure only one network is provided", id, len(switches))
				}

				hasChange := false

				if object.Name != name || object.Type != oType {
					hasChange = true
				}

				object.Name = name
				object.Type = oType

				for _, value := range values {
					for _, entry := range object.Entries {
						if entry.Value == value && !hasChange {
							return fmt.Errorf("no_detected_changes")
						}
					}
				}

				if err := s.DB.Save(&object).Error; err != nil {
					return fmt.Errorf("failed to update object %d: %w", id, err)
				}

				if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
					return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
				}

				for _, value := range values {
					entry := networkModels.ObjectEntry{
						ObjectID: id,
						Value:    value,
					}

					if err := s.DB.Create(&entry).Error; err != nil {
						return fmt.Errorf("failed to create entry for object %d: %w", id, err)
					}
				}

				err := s.SyncStandardSwitches(nil, "sync")
				if err != nil {
					return fmt.Errorf("failed to sync standard switches after editing object %d: %w", id, err)
				}

				updated = true
			}

			/* Network used by jails */
			var jailNetworks []jailModels.Network
			if err := s.DB.Where("ipv4_id = ? OR ipv4_gw_id = ? OR ipv6_id = ? OR ipv6_gw_id = ?", id, id, id, id).Find(&jailNetworks).Error; err != nil {
				return fmt.Errorf("failed to find jail networks using object %d: %w", id, err)
			}

			if len(jailNetworks) > 0 && oType != "Network" {
				return fmt.Errorf("cannot_change_object_type_jail")
			}

			if len(jailNetworks) > 0 && oType == "Network" {
				err := s.AddNetworkObjectEditJailTrigger(id, name, oType, values)
				if err != nil {
					return fmt.Errorf("failed to add network object edit jail trigger for object %d: %w", id, err)
				}

				updated = true
			}
		}

		if !updated {
			preserveResolutions := (object.Type == "FQDN" || object.Type == "List") && (oType == "FQDN" || oType == "List")
			if err := s.replaceObjectWithValues(id, name, oType, values, autoUpdate, refreshInterval, preserveResolutions); err != nil {
				return err
			}
		}
	}

	if oType == "FQDN" || oType == "List" {
		if err := s.RefreshObjectByID(id); err != nil {
			if rollbackErr := s.restoreObjectState(previousState); rollbackErr != nil {
				return fmt.Errorf("failed to refresh dynamic object %d after edit: %w (rollback_failed: %v)", id, err, rollbackErr)
			}
			return err
		}
	} else if err := s.ApplyFirewallIfEnabled(); err != nil {
		if rollbackErr := s.restoreObjectState(previousState); rollbackErr != nil {
			return fmt.Errorf("failed to apply firewall after object %d edit: %w (rollback_failed: %v)", id, err, rollbackErr)
		}
		return err
	}

	return nil
}

func (s *Service) AddNetworkObjectEditJailTrigger(id uint, name string, oType string, values []string) error {
	if len(values) != 1 {
		return fmt.Errorf("at_most_1_entry_allowed")
	}

	if err := s.DB.Model(&networkModels.Object{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"name": name,
			"type": oType,
		},
	).Error; err != nil {
		return fmt.Errorf("failed to update object %d metadata: %w", id, err)
	}

	if err := s.DB.Where("object_id = ?", id).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return fmt.Errorf("failed to delete existing entries for object %d: %w", id, err)
	}

	for _, value := range values {
		entry := networkModels.ObjectEntry{
			ObjectID: id,
			Value:    value,
		}

		if err := s.DB.Create(&entry).Error; err != nil {
			return fmt.Errorf("failed to create entry for object %d: %w", id, err)
		}
	}

	used, jailIds, err := s.IsObjectUsedByJail(id)
	if err != nil {
		return fmt.Errorf("failed to check if object %d is used by a jail: %w", id, err)
	}

	if used {
		var idsToUpdate []int64
		for _, jid := range jailIds {
			idsToUpdate = append(idsToUpdate, int64(jid))
		}

		if s.OnJailObjectUpdate != nil {
			s.OnJailObjectUpdate(jailIds)
		}
	}

	return nil
}

func (s *Service) GetObjectEntryByID(id uint) (string, error) {
	var object networkModels.Object

	if err := s.DB.Preload("Entries").First(&object, id).Error; err != nil {
		return "", fmt.Errorf("failed to find object with ID %d: %w", id, err)
	}

	if len(object.Entries) == 0 {
		return "", fmt.Errorf("no entries found for object with ID %d", id)
	}

	if len(object.Entries) > 1 {
		return "", fmt.Errorf("multiple entries found for object with ID %d, expected only one", id)
	}

	return object.Entries[0].Value, nil
}
