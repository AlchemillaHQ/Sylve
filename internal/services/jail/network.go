// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	jailNetworkRCConfStart = "# >>> Sylve-Managed Network >>>"
	jailNetworkRCConfEnd   = "# <<< Sylve-Managed Network <<<"
	jailNetworkLegacyMark  = "# Sylve Network Configuration"
)

type jailNetworkObjectRole string

const (
	jailNetworkMAC    jailNetworkObjectRole = "mac"
	jailNetworkIPv4   jailNetworkObjectRole = "ipv4"
	jailNetworkIPv4GW jailNetworkObjectRole = "ipv4_gateway"
	jailNetworkIPv6   jailNetworkObjectRole = "ipv6"
	jailNetworkIPv6GW jailNetworkObjectRole = "ipv6_gateway"
)

func validateAssignableJailIPv4CIDR(cidr string) error {
	if !utils.IsAssignableIPv4CIDR(cidr) {
		return fmt.Errorf("invalid_ip4_cidr_not_assignable")
	}
	return nil
}

func validateAssignableJailIPv6CIDR(cidr string) error {
	if !utils.IsAssignableIPv6CIDR(cidr) {
		return fmt.Errorf("invalid_ip6_cidr_not_assignable")
	}
	return nil
}

func loadJailForNetwork(db *gorm.DB, ctID uint) (*jailModels.Jail, error) {
	var jail jailModels.Jail
	err := db.
		Preload("Storages").
		Preload("Networks").
		Where("ct_id = ?", ctID).
		First(&jail).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("jail_not_found: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_load_jail: %w", err)
	}
	sort.Slice(jail.Networks, func(i, j int) bool { return jail.Networks[i].ID < jail.Networks[j].ID })
	return &jail, nil
}

func loadJailNetwork(db *gorm.DB, jailID, networkID uint) (*jailModels.Network, error) {
	var network jailModels.Network
	err := db.
		Preload("MacAddressObj.Entries").
		Preload("IPv4Obj.Entries").
		Preload("IPv4GwObj.Entries").
		Preload("IPv6Obj.Entries").
		Preload("IPv6GwObj.Entries").
		Where("id = ? AND jid = ?", networkID, jailID).
		First(&network).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("network_not_found: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("failed_to_load_network: %w", err)
	}
	return &network, nil
}

func (s *Service) ensureJailNetworkMutationAllowedLocked(ctID uint) (*jailModels.Jail, error) {
	if ctID == 0 {
		return nil, fmt.Errorf("invalid_ct_id")
	}
	allowed, err := s.canMutateProtectedJail(ctID)
	if err != nil {
		return nil, fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("replication_lease_not_owned")
	}
	restoring, err := s.jailRestoreInProgress(ctID)
	if err != nil {
		return nil, fmt.Errorf("restore_fence_check_failed: %w", err)
	}
	if restoring {
		return nil, fmt.Errorf("restore_in_progress")
	}
	jail, err := loadJailForNetwork(s.DB, ctID)
	if err != nil {
		return nil, err
	}
	running, err := s.IsJailRunning(ctID)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_jail_state: %w", err)
	}
	if running {
		return nil, fmt.Errorf("jail_network_change_requires_inactive")
	}
	if s.NetworkService == nil {
		return nil, fmt.Errorf("network_service_unavailable")
	}
	return jail, nil
}

func (s *Service) captureJailNetworkFiles(ctID uint, jail *jailModels.Jail) ([]jailFileSnapshot, error) {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_jails_path: %w", err)
	}
	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctID))
	paths := []string{
		filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID)),
		filepath.Join(jailDir, "scripts", "pre-start.sh"),
		filepath.Join(jailDir, "scripts", "start.sh"),
		filepath.Join(jailDir, "scripts", "post-start.sh"),
	}
	if jail.Type == jailModels.JailTypeFreeBSD {
		mountPoint, mountErr := s.GetJailBaseMountPoint(ctID)
		if mountErr != nil {
			return nil, mountErr
		}
		paths = append(paths, filepath.Join(mountPoint, "etc", "rc.conf"))
	}
	for _, storage := range jail.Storages {
		if storage.IsBase {
			paths = append(paths, filepath.Join("/", storage.Pool, "sylve", "jails", fmt.Sprintf("%d", ctID), ".sylve", "jail.json"))
		}
	}

	return captureJailFiles(paths)
}

func jailNetworkMutationFailure(primary error, snapshots []jailFileSnapshot, compensate func() error) error {
	var compensationErr error
	if compensate != nil {
		compensationErr = compensate()
	}
	return errors.Join(primary, compensationErr, restoreJailFiles(snapshots))
}

func removeJailNetworkRCConfBlock(content string) string {
	removeBlock := func(value, start, end string) string {
		startIndex := strings.Index(value, start)
		if startIndex < 0 {
			return value
		}
		endIndex := strings.Index(value[startIndex+len(start):], end)
		if endIndex < 0 {
			return value[:startIndex]
		}
		endIndex += startIndex + len(start) + len(end)
		return value[:startIndex] + value[endIndex:]
	}

	content = removeBlock(content, jailNetworkRCConfStart, jailNetworkRCConfEnd)
	if legacyIndex := strings.Index(content, jailNetworkLegacyMark); legacyIndex >= 0 {
		content = content[:legacyIndex]
	}
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return ""
	}
	return trimmed + "\n"
}

func replaceJailNetworkRCConfBlock(content string, lines []string) string {
	content = removeJailNetworkRCConfBlock(content)
	if len(lines) == 0 {
		return content
	}
	return content + jailNetworkRCConfStart + "\n" + strings.Join(lines, "\n") + "\n" + jailNetworkRCConfEnd + "\n"
}

func removeJailNetworkConfigLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "vnet;" || strings.HasPrefix(trimmed, "vnet.interface") ||
			strings.HasPrefix(trimmed, "ip4=") || strings.HasPrefix(trimmed, "ip6=") {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func jailNetworkObjectType(role jailNetworkObjectRole) string {
	switch role {
	case jailNetworkMAC:
		return "Mac"
	case jailNetworkIPv4, jailNetworkIPv6:
		return "Network"
	default:
		return "Host"
	}
}

func normalizeJailNetworkValue(role jailNetworkObjectRole, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch role {
	case jailNetworkMAC:
		mac, err := net.ParseMAC(value)
		if err != nil || len(mac) != 6 {
			return "", fmt.Errorf("invalid_mac")
		}
		return strings.ToLower(mac.String()), nil
	case jailNetworkIPv4:
		if err := validateAssignableJailIPv4CIDR(value); err != nil {
			return "", err
		}
		return value, nil
	case jailNetworkIPv6:
		if err := validateAssignableJailIPv6CIDR(value); err != nil {
			return "", err
		}
		return value, nil
	case jailNetworkIPv4GW:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return "", fmt.Errorf("invalid_ipv4_gateway")
		}
		return ip.To4().String(), nil
	case jailNetworkIPv6GW:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return "", fmt.Errorf("invalid_ipv6_gateway")
		}
		return ip.String(), nil
	default:
		return "", fmt.Errorf("invalid_network_object_role")
	}
}

func (s *Service) ensureJailNetworkObjectUnused(db *gorm.DB, role jailNetworkObjectRole, objectID, excludeNetworkID uint) error {
	var where string
	switch role {
	case jailNetworkMAC:
		where = "mac_id = ?"
	case jailNetworkIPv4:
		where = "ipv4_id = ?"
	case jailNetworkIPv6:
		where = "ipv6_id = ?"
	default:
		return nil
	}
	var count int64
	query := db.Model(&jailModels.Network{}).Where(where, objectID)
	if excludeNetworkID > 0 {
		query = query.Where("id <> ?", excludeNetworkID)
	}
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_network_object_usage: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("network_object_already_used")
	}
	return nil
}

func (s *Service) ensureJailNetworkValueUnused(db *gorm.DB, role jailNetworkObjectRole, value string, excludeNetworkID uint) error {
	var column string
	switch role {
	case jailNetworkMAC:
		column = "mac_id"
	case jailNetworkIPv4:
		column = "ipv4_id"
	case jailNetworkIPv6:
		column = "ipv6_id"
	default:
		return nil
	}
	var count int64
	query := db.Table("jail_networks").
		Joins("JOIN object_entries ON object_entries.object_id = jail_networks."+column).
		Where("LOWER(TRIM(object_entries.value)) = LOWER(?)", value)
	if excludeNetworkID > 0 {
		query = query.Where("jail_networks.id <> ?", excludeNetworkID)
	}
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_network_value_usage: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("network_object_already_used")
	}
	return nil
}

func (s *Service) resolveJailNetworkObject(
	db *gorm.DB,
	objectID *uint,
	raw string,
	role jailNetworkObjectRole,
	objectName string,
	excludeNetworkID uint,
	generateWhenEmpty bool,
) (*uint, []uint, error) {
	raw = strings.TrimSpace(raw)
	if objectID != nil && *objectID > 0 && raw != "" {
		return nil, nil, fmt.Errorf("conflicting_network_value_sources")
	}
	if objectID != nil && *objectID > 0 {
		var object networkModels.Object
		err := db.Preload("Entries").First(&object, *objectID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("network_object_not_found: %w", err)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("failed_to_load_network_object: %w", err)
		}
		if !strings.EqualFold(object.Type, jailNetworkObjectType(role)) {
			return nil, nil, fmt.Errorf("network_object_type_mismatch")
		}
		if len(object.Entries) != 1 {
			return nil, nil, fmt.Errorf("network_object_requires_single_entry")
		}
		if _, err := normalizeJailNetworkValue(role, object.Entries[0].Value); err != nil {
			return nil, nil, err
		}
		if err := s.ensureJailNetworkObjectUnused(db, role, object.ID, excludeNetworkID); err != nil {
			return nil, nil, err
		}
		id := object.ID
		return &id, nil, nil
	}

	if raw == "" && generateWhenEmpty {
		raw = utils.GenerateRandomMAC()
	}
	if raw == "" {
		return nil, nil, nil
	}
	normalized, err := normalizeJailNetworkValue(role, raw)
	if err != nil {
		return nil, nil, err
	}
	if err := s.ensureJailNetworkValueUnused(db, role, normalized, excludeNetworkID); err != nil {
		return nil, nil, err
	}
	object := networkModels.Object{
		Name: uniqueObjectName(db, objectName),
		Type: jailNetworkObjectType(role),
	}
	if err := db.Create(&object).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_create_network_object: %w", err)
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: object.ID, Value: normalized}).Error; err != nil {
		return nil, nil, fmt.Errorf("failed_to_create_network_object_entry: %w", err)
	}
	return &object.ID, []uint{object.ID}, nil
}

func findJailNetworkSwitch(db *gorm.DB, name string) (uint, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, "", "", fmt.Errorf("switch_name_required")
	}
	var standard networkModels.StandardSwitch
	if err := db.Where("name = ?", name).First(&standard).Error; err == nil {
		return standard.ID, "standard", standard.Name, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, "", "", fmt.Errorf("failed_to_load_switch: %w", err)
	}
	var manual networkModels.ManualSwitch
	if err := db.Where("name = ?", name).First(&manual).Error; err == nil {
		return manual.ID, "manual", manual.Name, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, "", "", fmt.Errorf("failed_to_load_switch: %w", err)
	}
	return 0, "", "", fmt.Errorf("switch_not_found")
}

func validateJailNetworkShape(jail *jailModels.Jail, network *jailModels.Network) error {
	if network.VLAN != nil && (*network.VLAN < 0 || *network.VLAN > 4095) {
		return fmt.Errorf("invalid_vlan")
	}
	if jail.Type == jailModels.JailTypeLinux && (network.DHCP || network.SLAAC) {
		return fmt.Errorf("cannot_set_dhcp_or_slaac_when_linux_jail")
	}
	if network.DHCP && network.SLAAC && network.DefaultGateway {
		return fmt.Errorf("cannot_set_dhcp_slaac_and_default_gateway_together")
	}
	if network.DefaultGateway && !network.DHCP && !network.SLAAC && network.IPv4GwID == nil && network.IPv6GwID == nil {
		return fmt.Errorf("default_gateway_requires_gateway")
	}
	return nil
}

func validateJailNetworkUniqueness(db *gorm.DB, jailID, excludeNetworkID uint, name string, defaultGateway bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("network_name_required")
	}
	if len(name) > 128 {
		return fmt.Errorf("invalid_network_name")
	}
	var count int64
	nameQuery := db.Model(&jailModels.Network{}).Where("jid = ? AND name = ?", jailID, name)
	if excludeNetworkID > 0 {
		nameQuery = nameQuery.Where("id <> ?", excludeNetworkID)
	}
	if err := nameQuery.Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_network_name: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("jail_network_name_exists")
	}
	if !defaultGateway {
		return nil
	}
	count = 0
	gatewayQuery := db.Model(&jailModels.Network{}).Where("jid = ? AND default_gateway = ?", jailID, true)
	if excludeNetworkID > 0 {
		gatewayQuery = gatewayQuery.Where("id <> ?", excludeNetworkID)
	}
	if err := gatewayQuery.Count(&count).Error; err != nil {
		return fmt.Errorf("failed_to_check_default_gateway: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("jail_default_gateway_exists")
	}
	return nil
}

func deleteJailNetworkObjects(db *gorm.DB, objectIDs []uint) error {
	if len(objectIDs) == 0 {
		return nil
	}
	if err := db.Where("object_id IN ?", objectIDs).Delete(&networkModels.ObjectEntry{}).Error; err != nil {
		return err
	}
	return db.Where("id IN ?", objectIDs).Delete(&networkModels.Object{}).Error
}

func createJailNetworkRow(db *gorm.DB, network *jailModels.Network) error {
	return db.Omit(clause.Associations).Create(network).Error
}

func saveJailNetworkRow(db *gorm.DB, network *jailModels.Network) error {
	return db.Select("*").Omit(clause.Associations).Save(network).Error
}

func (s *Service) cleanupJailNetworkRuntime(ctID uint, network jailModels.Network) error {
	if network.VLAN != nil && *network.VLAN > 0 {
		vlanIface := fmt.Sprintf("%s_net%da.%d", s.GetCTIDHash(ctID), network.ID, *network.VLAN)
		if _, err := utils.RunCommand("/sbin/ifconfig", vlanIface, "destroy"); err != nil {
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, "does not exist") && !strings.Contains(message, "not found") {
				return fmt.Errorf("failed_to_delete_vlan_interface: %w", err)
			}
		}
	}
	epair := fmt.Sprintf("%s_net%d", s.GetCTIDHash(ctID), network.ID)
	if err := s.NetworkService.DeleteEpair(epair); err != nil {
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "does not exist") && !strings.Contains(message, "not found") {
			return fmt.Errorf("failed_to_delete_epair: %w", err)
		}
	}
	return nil
}

func (s *Service) SetInheritance(ctID uint, ipv4 bool, ipv6 bool) (jailServiceInterfaces.JailNetworkInheritanceResult, error) {
	result := jailServiceInterfaces.JailNetworkInheritanceResult{
		CTID:              ctID,
		InheritIPv4:       ipv4,
		InheritIPv6:       ipv6,
		RemovedNetworkIDs: []uint{},
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailNetworkMutationAllowedLocked(ctID)
	if err != nil {
		return result, err
	}
	if jail.InheritIPv4 == ipv4 && jail.InheritIPv6 == ipv6 {
		return result, nil
	}
	snapshots, err := s.captureJailNetworkFiles(ctID, jail)
	if err != nil {
		return result, err
	}
	previousNetworks := append([]jailModels.Network(nil), jail.Networks...)
	previousIPv4, previousIPv6 := jail.InheritIPv4, jail.InheritIPv6
	if ipv4 || ipv6 {
		for _, network := range previousNetworks {
			result.RemovedNetworkIDs = append(result.RemovedNetworkIDs, network.ID)
		}
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return result, tx.Error
	}
	if err := tx.Model(&jailModels.Jail{}).Where("id = ?", jail.ID).Updates(map[string]any{
		"inherit_ipv4": ipv4,
		"inherit_ipv6": ipv6,
	}).Error; err != nil {
		tx.Rollback()
		return result, fmt.Errorf("failed_to_update_network_inheritance: %w", err)
	}
	if (ipv4 || ipv6) && len(previousNetworks) > 0 {
		if err := tx.Where("jid = ?", jail.ID).Delete(&jailModels.Network{}).Error; err != nil {
			tx.Rollback()
			return result, fmt.Errorf("failed_to_delete_inherited_networks: %w", err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return result, fmt.Errorf("failed_to_commit_network_inheritance: %w", err)
	}

	compensate := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&jailModels.Jail{}).Where("id = ?", jail.ID).Updates(map[string]any{
				"inherit_ipv4": previousIPv4,
				"inherit_ipv6": previousIPv6,
			}).Error; err != nil {
				return fmt.Errorf("failed_to_restore_network_inheritance: %w", err)
			}
			if ipv4 || ipv6 {
				for _, network := range previousNetworks {
					if err := createJailNetworkRow(tx, &network); err != nil {
						return fmt.Errorf("failed_to_restore_jail_network: %w", err)
					}
				}
			}
			return nil
		})
	}
	updatedJail, err := loadJailForNetwork(s.DB, ctID)
	if err != nil {
		return result, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.SyncNetwork(ctID, *updatedJail); err != nil {
		return result, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if ipv4 || ipv6 {
		for _, network := range previousNetworks {
			if err := s.cleanupJailNetworkRuntime(ctID, network); err != nil {
				return result, jailNetworkMutationFailure(err, snapshots, compensate)
			}
		}
	}
	return result, nil
}

func (s *Service) AddNetwork(ctID uint, req jailServiceInterfaces.AddJailNetworkRequest) (*jailModels.Network, error) {
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailNetworkMutationAllowedLocked(ctID)
	if err != nil {
		return nil, err
	}
	if jail.InheritIPv4 || jail.InheritIPv6 {
		return nil, fmt.Errorf("cannot_add_network_when_inheriting_network")
	}
	tx := s.DB.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	rollback := func(err error) (*jailModels.Network, error) {
		tx.Rollback()
		return nil, err
	}
	switchID, switchType, switchName, err := findJailNetworkSwitch(tx, req.SwitchName)
	if err != nil {
		return rollback(err)
	}
	vlan := 0
	if req.VLAN != nil {
		vlan = *req.VLAN
	}
	network := jailModels.Network{
		JailID:         jail.ID,
		Name:           strings.TrimSpace(req.Name),
		SwitchID:       switchID,
		SwitchType:     switchType,
		DHCP:           req.DHCP != nil && *req.DHCP,
		SLAAC:          req.SLAAC != nil && *req.SLAAC,
		DefaultGateway: req.DefaultGateway != nil && *req.DefaultGateway,
		VLAN:           &vlan,
	}
	createdObjectIDs := make([]uint, 0, 5)
	appendCreated := func(ids []uint) { createdObjectIDs = append(createdObjectIDs, ids...) }
	objectPrefix := fmt.Sprintf("%s-%s", jail.Name, switchName)
	var created []uint
	network.MacID, created, err = s.resolveJailNetworkObject(tx, req.MacID, req.MACRaw, jailNetworkMAC, objectPrefix+"-MAC", 0, true)
	if err != nil {
		return rollback(err)
	}
	appendCreated(created)

	if network.DHCP {
		if (req.IP4 != nil && *req.IP4 > 0) || strings.TrimSpace(req.IP4Raw) != "" ||
			(req.IP4GW != nil && *req.IP4GW > 0) || strings.TrimSpace(req.IP4GwRaw) != "" {
			return rollback(fmt.Errorf("conflicting_network_value_sources"))
		}
	} else {
		network.IPv4ID, created, err = s.resolveJailNetworkObject(tx, req.IP4, req.IP4Raw, jailNetworkIPv4, objectPrefix+"-IPv4", 0, false)
		if err != nil {
			return rollback(err)
		}
		appendCreated(created)
		network.IPv4GwID, created, err = s.resolveJailNetworkObject(tx, req.IP4GW, req.IP4GwRaw, jailNetworkIPv4GW, objectPrefix+"-IPv4-GW", 0, false)
		if err != nil {
			return rollback(err)
		}
		appendCreated(created)
	}
	if network.SLAAC {
		if (req.IP6 != nil && *req.IP6 > 0) || strings.TrimSpace(req.IP6Raw) != "" ||
			(req.IP6GW != nil && *req.IP6GW > 0) || strings.TrimSpace(req.IP6GwRaw) != "" {
			return rollback(fmt.Errorf("conflicting_network_value_sources"))
		}
	} else {
		network.IPv6ID, created, err = s.resolveJailNetworkObject(tx, req.IP6, req.IP6Raw, jailNetworkIPv6, objectPrefix+"-IPv6", 0, false)
		if err != nil {
			return rollback(err)
		}
		appendCreated(created)
		network.IPv6GwID, created, err = s.resolveJailNetworkObject(tx, req.IP6GW, req.IP6GwRaw, jailNetworkIPv6GW, objectPrefix+"-IPv6-GW", 0, false)
		if err != nil {
			return rollback(err)
		}
		appendCreated(created)
	}
	if err := validateJailNetworkShape(jail, &network); err != nil {
		return rollback(err)
	}
	if err := validateJailNetworkUniqueness(tx, jail.ID, 0, network.Name, network.DefaultGateway); err != nil {
		return rollback(err)
	}
	if err := createJailNetworkRow(tx, &network); err != nil {
		return rollback(fmt.Errorf("failed_to_create_network: %w", err))
	}
	snapshots, err := s.captureJailNetworkFiles(ctID, jail)
	if err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed_to_commit_network: %w", err)
	}

	compensate := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("id = ? AND jid = ?", network.ID, jail.ID).Delete(&jailModels.Network{}).Error; err != nil {
				return fmt.Errorf("failed_to_remove_created_network: %w", err)
			}
			if err := deleteJailNetworkObjects(tx, createdObjectIDs); err != nil {
				return fmt.Errorf("failed_to_remove_created_network_objects: %w", err)
			}
			return nil
		})
	}
	updatedJail, err := loadJailForNetwork(s.DB, ctID)
	if err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.SyncNetwork(ctID, *updatedJail); err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	createdNetwork, err := loadJailNetwork(s.DB, jail.ID, network.ID)
	if err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	return createdNetwork, nil
}

func (s *Service) DeleteNetwork(ctID uint, networkID uint) error {
	if networkID == 0 {
		return fmt.Errorf("invalid_network_id")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailNetworkMutationAllowedLocked(ctID)
	if err != nil {
		return err
	}
	network, err := loadJailNetwork(s.DB, jail.ID, networkID)
	if err != nil {
		return err
	}
	previous := *network
	snapshots, err := s.captureJailNetworkFiles(ctID, jail)
	if err != nil {
		return err
	}
	if err := s.DB.Where("id = ? AND jid = ?", networkID, jail.ID).Delete(&jailModels.Network{}).Error; err != nil {
		return fmt.Errorf("failed_to_delete_network: %w", err)
	}
	compensate := func() error {
		return createJailNetworkRow(s.DB, &previous)
	}
	updatedJail, err := loadJailForNetwork(s.DB, ctID)
	if err != nil {
		return jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.SyncNetwork(ctID, *updatedJail); err != nil {
		return jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.cleanupJailNetworkRuntime(ctID, previous); err != nil {
		return jailNetworkMutationFailure(err, snapshots, compensate)
	}
	return nil
}

func (s *Service) EditNetwork(ctID uint, networkID uint, req jailServiceInterfaces.EditJailNetworkRequest) (*jailModels.Network, error) {
	if networkID == 0 {
		return nil, fmt.Errorf("invalid_network_id")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jail, err := s.ensureJailNetworkMutationAllowedLocked(ctID)
	if err != nil {
		return nil, err
	}
	if jail.InheritIPv4 || jail.InheritIPv6 {
		return nil, fmt.Errorf("cannot_edit_network_when_inheriting_network")
	}
	existing, err := loadJailNetwork(s.DB, jail.ID, networkID)
	if err != nil {
		return nil, err
	}
	previous := *existing
	tx := s.DB.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	rollback := func(err error) (*jailModels.Network, error) {
		tx.Rollback()
		return nil, err
	}
	network := *existing
	if req.Name != nil {
		network.Name = strings.TrimSpace(*req.Name)
	}
	if req.SwitchName != nil {
		switchID, switchType, _, err := findJailNetworkSwitch(tx, *req.SwitchName)
		if err != nil {
			return rollback(err)
		}
		network.SwitchID = switchID
		network.SwitchType = switchType
	}
	if req.VLAN != nil {
		vlan := *req.VLAN
		network.VLAN = &vlan
	}
	if req.DHCP != nil {
		network.DHCP = *req.DHCP
	}
	if req.SLAAC != nil {
		network.SLAAC = *req.SLAAC
	}
	if req.DefaultGateway != nil {
		network.DefaultGateway = *req.DefaultGateway
	}
	createdObjectIDs := make([]uint, 0, 5)
	appendCreated := func(ids []uint) { createdObjectIDs = append(createdObjectIDs, ids...) }
	objectPrefix := fmt.Sprintf("%s-%s", jail.Name, network.Name)
	var created []uint
	if req.MacID != nil || req.MACRaw != nil {
		raw := ""
		if req.MACRaw != nil {
			raw = *req.MACRaw
		}
		network.MacID, created, err = s.resolveJailNetworkObject(tx, req.MacID, raw, jailNetworkMAC, objectPrefix+"-MAC", networkID, true)
		if err != nil {
			return rollback(err)
		}
		appendCreated(created)
	}
	if network.DHCP {
		if (req.IP4 != nil && *req.IP4 > 0) || (req.IP4Raw != nil && strings.TrimSpace(*req.IP4Raw) != "") ||
			(req.IP4GW != nil && *req.IP4GW > 0) || (req.IP4GwRaw != nil && strings.TrimSpace(*req.IP4GwRaw) != "") {
			return rollback(fmt.Errorf("conflicting_network_value_sources"))
		}
		network.IPv4ID = nil
		network.IPv4GwID = nil
	} else {
		if req.IP4 != nil || req.IP4Raw != nil {
			raw := ""
			if req.IP4Raw != nil {
				raw = *req.IP4Raw
			}
			network.IPv4ID, created, err = s.resolveJailNetworkObject(tx, req.IP4, raw, jailNetworkIPv4, objectPrefix+"-IPv4", networkID, false)
			if err != nil {
				return rollback(err)
			}
			appendCreated(created)
		}
		if req.IP4GW != nil || req.IP4GwRaw != nil {
			raw := ""
			if req.IP4GwRaw != nil {
				raw = *req.IP4GwRaw
			}
			network.IPv4GwID, created, err = s.resolveJailNetworkObject(tx, req.IP4GW, raw, jailNetworkIPv4GW, objectPrefix+"-IPv4-GW", networkID, false)
			if err != nil {
				return rollback(err)
			}
			appendCreated(created)
		}
	}
	if network.SLAAC {
		if (req.IP6 != nil && *req.IP6 > 0) || (req.IP6Raw != nil && strings.TrimSpace(*req.IP6Raw) != "") ||
			(req.IP6GW != nil && *req.IP6GW > 0) || (req.IP6GwRaw != nil && strings.TrimSpace(*req.IP6GwRaw) != "") {
			return rollback(fmt.Errorf("conflicting_network_value_sources"))
		}
		network.IPv6ID = nil
		network.IPv6GwID = nil
	} else {
		if req.IP6 != nil || req.IP6Raw != nil {
			raw := ""
			if req.IP6Raw != nil {
				raw = *req.IP6Raw
			}
			network.IPv6ID, created, err = s.resolveJailNetworkObject(tx, req.IP6, raw, jailNetworkIPv6, objectPrefix+"-IPv6", networkID, false)
			if err != nil {
				return rollback(err)
			}
			appendCreated(created)
		}
		if req.IP6GW != nil || req.IP6GwRaw != nil {
			raw := ""
			if req.IP6GwRaw != nil {
				raw = *req.IP6GwRaw
			}
			network.IPv6GwID, created, err = s.resolveJailNetworkObject(tx, req.IP6GW, raw, jailNetworkIPv6GW, objectPrefix+"-IPv6-GW", networkID, false)
			if err != nil {
				return rollback(err)
			}
			appendCreated(created)
		}
	}
	if err := validateJailNetworkShape(jail, &network); err != nil {
		return rollback(err)
	}
	if err := validateJailNetworkUniqueness(tx, jail.ID, networkID, network.Name, network.DefaultGateway); err != nil {
		return rollback(err)
	}
	if err := saveJailNetworkRow(tx, &network); err != nil {
		return rollback(fmt.Errorf("failed_to_update_network: %w", err))
	}
	snapshots, err := s.captureJailNetworkFiles(ctID, jail)
	if err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed_to_commit_network_update: %w", err)
	}

	compensate := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			if err := saveJailNetworkRow(tx, &previous); err != nil {
				return fmt.Errorf("failed_to_restore_network: %w", err)
			}
			if err := deleteJailNetworkObjects(tx, createdObjectIDs); err != nil {
				return fmt.Errorf("failed_to_remove_created_network_objects: %w", err)
			}
			return nil
		})
	}
	updatedJail, err := loadJailForNetwork(s.DB, ctID)
	if err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.SyncNetwork(ctID, *updatedJail); err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	if err := s.cleanupJailNetworkRuntime(ctID, previous); err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	updatedNetwork, err := loadJailNetwork(s.DB, jail.ID, networkID)
	if err != nil {
		return nil, jailNetworkMutationFailure(err, snapshots, compensate)
	}
	return updatedNetwork, nil
}

func (s *Service) networkObjectValue(objectID *uint, role jailNetworkObjectRole) (string, error) {
	if objectID == nil || *objectID == 0 {
		return "", nil
	}
	value, err := s.NetworkService.GetObjectEntryByID(*objectID)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_network_object_value: %w", err)
	}
	return normalizeJailNetworkValue(role, value)
}

func (s *Service) SyncNetwork(ctID uint, jail jailModels.Jail) error {
	restoring, err := s.jailRestoreInProgress(ctID)
	if err != nil {
		return fmt.Errorf("restore_fence_check_failed: %w", err)
	}
	if restoring {
		return fmt.Errorf("restore_in_progress")
	}
	if s.NetworkService == nil {
		return fmt.Errorf("network_service_unavailable")
	}
	sort.Slice(jail.Networks, func(i, j int) bool { return jail.Networks[i].ID < jail.Networks[j].ID })

	mountPoint, err := s.GetJailBaseMountPoint(ctID)
	if err != nil {
		return err
	}
	cfg, err := s.GetJailConfig(ctID)
	if err != nil {
		return err
	}
	lines := removeJailNetworkConfigLines(strings.Split(cfg, "\n"))

	hookPaths := make(map[string]string, 3)
	hookContents := make(map[string]string, 3)
	for _, hookName := range []string{"pre-start", "start", "post-start"} {
		hookPath, pathErr := s.GetHookScriptPath(ctID, hookName)
		if pathErr != nil {
			continue
		}
		content, readErr := os.ReadFile(hookPath)
		if readErr != nil {
			return fmt.Errorf("failed_to_read_network_hook: %w", readErr)
		}
		hookPaths[hookName] = hookPath
		hookContents[hookName] = s.RemoveSylveNetworkFromHook(string(content))
	}

	rcConfPath := ""
	rcConfBase := ""
	if jail.Type == jailModels.JailTypeFreeBSD {
		rcConfPath = filepath.Join(mountPoint, "etc", "rc.conf")
		rcConf, readErr := os.ReadFile(rcConfPath)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("failed_to_read_rc_conf: %w", readErr)
		}
		rcConfBase = removeJailNetworkRCConfBlock(string(rcConf))
	}

	var jailConfig string
	var preStartNetwork string
	var postStartNetwork string
	var rcConfLines []string
	if jail.InheritIPv4 || jail.InheritIPv6 {
		var inherited strings.Builder
		if jail.InheritIPv4 {
			inherited.WriteString("\tip4=inherit;\n")
		}
		if jail.InheritIPv6 {
			inherited.WriteString("\tip6=inherit;\n")
		}
		jailConfig, err = s.AppendToConfig(ctID, strings.Join(lines, "\n"), inherited.String())
		if err != nil {
			return err
		}
	} else if len(jail.Networks) == 0 {
		jailConfig, err = s.AppendToConfig(ctID, strings.Join(lines, "\n"), "\tip4=disable;\n\tip6=disable;\n")
		if err != nil {
			return err
		}
	} else {
		ctidHash := s.GetCTIDHash(ctID)
		preStartPath, ok := hookPaths["pre-start"]
		if !ok {
			return fmt.Errorf("hook_script_not_found: pre-start")
		}
		postStartPath := hookPaths["post-start"]
		if jail.Type == jailModels.JailTypeLinux {
			filtered := make([]string, 0, len(lines))
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "exec.start") && strings.Contains(trimmed, "\"/usr/local/sylve/scripts/start.sh\"") {
					continue
				}
				if postStartPath != "" && strings.HasPrefix(trimmed, "exec.poststart") && strings.Contains(trimmed, fmt.Sprintf("\"%s\"", postStartPath)) {
					continue
				}
				filtered = append(filtered, line)
			}
			lines = filtered
		}

		var jailCfgBuilder strings.Builder
		jailCfgBuilder.WriteString("\tvnet;\n")
		hasManagedPreStart := false
		for _, line := range lines {
			if strings.Contains(line, fmt.Sprintf("\"%s\"", preStartPath)) {
				hasManagedPreStart = true
				break
			}
		}
		if !hasManagedPreStart {
			jailCfgBuilder.WriteString(fmt.Sprintf("\texec.prestart += \"%s\";\n", preStartPath))
		}
		for _, network := range jail.Networks {
			if network.SwitchID > 0 {
				jailCfgBuilder.WriteString(fmt.Sprintf("\tvnet.interface += \"%s_net%db\";\n", ctidHash, network.ID))
			}
		}

		var preStartBuilder strings.Builder
		var postStartBuilder strings.Builder
		for _, network := range jail.Networks {
			if network.SwitchID == 0 {
				continue
			}
			if network.VLAN != nil && (*network.VLAN < 0 || *network.VLAN > 4095) {
				return fmt.Errorf("invalid_vlan")
			}
			mac, err := s.networkObjectValue(network.MacID, jailNetworkMAC)
			if err != nil {
				return err
			}
			if mac == "" {
				return fmt.Errorf("network_mac_required")
			}
			previousMAC, err := utils.PreviousMAC(mac)
			if err != nil {
				return fmt.Errorf("failed_to_get_previous_mac: %w", err)
			}
			epairA := fmt.Sprintf("%s_net%da", ctidHash, network.ID)
			epairB := fmt.Sprintf("%s_net%db", ctidHash, network.ID)
			bridgeName, err := s.NetworkService.GetBridgeNameByIDType(network.SwitchID, network.SwitchType)
			if err != nil {
				return fmt.Errorf("failed_to_get_bridge_name: %w", err)
			}

			preStartBuilder.WriteString(fmt.Sprintf("# Setup Network Interface %s\n", epairB))
			preStartBuilder.WriteString(fmt.Sprintf("ifconfig %s ether %s up\n", epairA, previousMAC))
			preStartBuilder.WriteString(fmt.Sprintf("ifconfig %s descr \"(%s) (%d)\"\n", epairA, jail.Name, jail.CTID))
			preStartBuilder.WriteString(fmt.Sprintf("ifconfig %s ether %s up\n\n", epairB, mac))
			if network.VLAN != nil && *network.VLAN > 0 {
				vlanIface := fmt.Sprintf("%s.%d", epairA, *network.VLAN)
				preStartBuilder.WriteString(fmt.Sprintf("if ! ifconfig %s > /dev/null 2>&1; then\n", vlanIface))
				preStartBuilder.WriteString(fmt.Sprintf("\tifconfig vlan create vlandev %s vlan %d name %s group svm-vlan up\n", epairA, *network.VLAN, vlanIface))
				preStartBuilder.WriteString("fi\n")
				preStartBuilder.WriteString(fmt.Sprintf("if ! ifconfig %s | grep -qw %s; then\n", bridgeName, vlanIface))
				preStartBuilder.WriteString(fmt.Sprintf("\tifconfig %s addm %s 2>&1 || true\n", bridgeName, vlanIface))
				preStartBuilder.WriteString("fi\n")
			} else {
				preStartBuilder.WriteString(fmt.Sprintf("if ! ifconfig %s | grep -qw %s; then\n", bridgeName, epairA))
				preStartBuilder.WriteString(fmt.Sprintf("\tifconfig %s addm %s 2>&1 || true\n", bridgeName, epairA))
				preStartBuilder.WriteString("fi\n")
			}
			preStartBuilder.WriteString(fmt.Sprintf("# End Setup Network Interface %s\n\n", epairB))

			ipv4, err := s.networkObjectValue(network.IPv4ID, jailNetworkIPv4)
			if err != nil {
				return err
			}
			ipv4Gateway, err := s.networkObjectValue(network.IPv4GwID, jailNetworkIPv4GW)
			if err != nil {
				return err
			}
			ipv6, err := s.networkObjectValue(network.IPv6ID, jailNetworkIPv6)
			if err != nil {
				return err
			}
			ipv6Gateway, err := s.networkObjectValue(network.IPv6GwID, jailNetworkIPv6GW)
			if err != nil {
				return err
			}

			if jail.Type == jailModels.JailTypeLinux {
				if network.DHCP || network.SLAAC {
					return fmt.Errorf("cannot_set_dhcp_or_slaac_when_linux_jail")
				}
				if ipv4 != "" {
					postStartBuilder.WriteString(fmt.Sprintf("ifconfig -j %s %s inet %s\n", ctidHash, epairB, ipv4))
					if network.DefaultGateway && ipv4Gateway != "" {
						postStartBuilder.WriteString(fmt.Sprintf("route -j %s add default %s\n", ctidHash, ipv4Gateway))
					}
				}
				if ipv6 != "" {
					postStartBuilder.WriteString(fmt.Sprintf("ifconfig -j %s %s inet6 %s\n", ctidHash, epairB, ipv6))
					if network.DefaultGateway && ipv6Gateway != "" {
						postStartBuilder.WriteString(fmt.Sprintf("route -6 -j %s add default %s\n", ctidHash, ipv6Gateway))
					}
				}
				if ipv4 != "" || ipv6 != "" {
					postStartBuilder.WriteString("\n")
				}
				continue
			}

			if network.DHCP {
				rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_net%db=\"SYNCDHCP\"", ctidHash, network.ID))
			} else if ipv4 != "" {
				ip, mask, err := utils.SplitIPv4AndMask(ipv4)
				if err != nil {
					return fmt.Errorf("failed_to_split_ipv4_address_and_mask: %w", err)
				}
				rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_net%db=\"inet %s netmask %s\"", ctidHash, network.ID, ip, mask))
				if network.DefaultGateway && ipv4Gateway != "" {
					rcConfLines = append(rcConfLines, fmt.Sprintf("defaultrouter=\"%s\"", ipv4Gateway))
				}
			}
			if network.SLAAC {
				rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_net%db_ipv6=\"inet6 accept_rtadv\"", ctidHash, network.ID))
				rcConfLines = append(rcConfLines, "rtsold_enable=\"YES\"")
			} else if ipv6 != "" {
				rcConfLines = append(rcConfLines, fmt.Sprintf("ifconfig_%s_net%db_ipv6=\"inet6 %s\"", ctidHash, network.ID, ipv6))
				if network.DefaultGateway && ipv6Gateway != "" {
					rcConfLines = append(rcConfLines, fmt.Sprintf("ipv6_defaultrouter=\"%s\"", ipv6Gateway))
				}
			}
		}
		preStartNetwork = preStartBuilder.String()
		postStartNetwork = postStartBuilder.String()
		if jail.Type == jailModels.JailTypeLinux && strings.TrimSpace(postStartNetwork) != "" {
			if postStartPath == "" {
				return fmt.Errorf("hook_script_not_found: post-start")
			}
			jailCfgBuilder.WriteString(fmt.Sprintf("\texec.poststart += \"%s\";\n", postStartPath))
		}
		jailConfig, err = s.AppendToConfig(ctID, strings.Join(lines, "\n"), jailCfgBuilder.String())
		if err != nil {
			return err
		}
	}

	if path := hookPaths["pre-start"]; path != "" {
		content := hookContents["pre-start"]
		if strings.TrimSpace(preStartNetwork) != "" {
			content = s.AddSylveNetworkToHook(content, preStartNetwork)
		}
		if err := utils.AtomicWriteFile(path, []byte(content), 0o755); err != nil {
			return fmt.Errorf("failed_to_write_pre_start_network_hook: %w", err)
		}
	}
	if path := hookPaths["start"]; path != "" {
		if err := utils.AtomicWriteFile(path, []byte(hookContents["start"]), 0o755); err != nil {
			return fmt.Errorf("failed_to_write_start_network_hook: %w", err)
		}
	}
	if path := hookPaths["post-start"]; path != "" {
		content := hookContents["post-start"]
		if jail.Type == jailModels.JailTypeLinux && strings.TrimSpace(postStartNetwork) != "" {
			content = s.AddSylveNetworkToHookAtEnd(content, postStartNetwork)
		}
		if err := utils.AtomicWriteFile(path, []byte(content), 0o755); err != nil {
			return fmt.Errorf("failed_to_write_post_start_network_hook: %w", err)
		}
	}
	if rcConfPath != "" {
		if err := utils.AtomicWriteFile(rcConfPath, []byte(replaceJailNetworkRCConfBlock(rcConfBase, rcConfLines)), 0o644); err != nil {
			return fmt.Errorf("failed_to_write_rc_conf: %w", err)
		}
	}
	if err := s.SaveJailConfig(ctID, jailConfig); err != nil {
		return err
	}
	if err := s.WriteJailJSON(ctID); err != nil {
		return fmt.Errorf("failed_to_sync_jail_metadata: %w", err)
	}
	return nil
}

func (s *Service) networkUpdateWorker() {
	pending := make(map[int64]bool)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case jailID, ok := <-s.networkUpdateChan:
			if !ok {
				return
			}
			pending[jailID] = true
		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}
			toProcess := make([]int64, 0, len(pending))
			for id := range pending {
				toProcess = append(toProcess, id)
			}
			clear(pending)
			s.processNetworkUpdateBatch(toProcess)
		}
	}
}

func (s *Service) processNetworkUpdateBatch(ids []int64) {
	for _, jailID := range ids {
		func(id int64) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.L.Error().Msgf("Recovered from panic in networkUpdateWorker for jail %d: %v", id, recovered)
				}
			}()

			s.actionMutex.Lock()
			defer s.actionMutex.Unlock()
			var jail jailModels.Jail
			if err := s.DB.Preload("Networks").First(&jail, "id = ?", id).Error; err != nil {
				logger.L.Warn().Int64("id", id).Msg("Jail disappeared before worker could sync")
				return
			}
			if err := s.SyncNetwork(jail.CTID, jail); err != nil {
				logger.L.Error().Err(err).Uint("ctid", jail.CTID).Msg("Sync failed")
			}
		}(jailID)
	}
}
