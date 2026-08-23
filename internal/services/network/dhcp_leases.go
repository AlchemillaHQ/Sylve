// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"unicode"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func parseDHCPFileLeases(data []byte) ([]networkServiceInterfaces.FileLeases, error) {
	leases := make([]networkServiceInterfaces.FileLeases, 0, 16)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	const maxLine = 64 * 1024
	scanner.Buffer(make([]byte, 0, 4*1024), maxLine)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		// This is the dnsmasq server DUID, not a client lease.
		if len(fields) >= 2 && strings.EqualFold(fields[0], "duid") {
			continue
		}
		if len(fields) < 4 {
			continue
		}

		expiry, err := parseDHCPLeaseExpiry(fields[0])
		if err != nil {
			continue
		}
		address, err := netip.ParseAddr(fields[2])
		if err != nil || address.Zone() != "" {
			continue
		}

		lease := networkServiceInterfaces.FileLeases{
			Expiry: expiry,
			IP:     address.String(),
		}
		if fields[3] != "*" {
			lease.Hostname = fields[3]
		}

		if address.Is4() {
			lease.MAC = strings.ToLower(fields[1])
			if len(fields) > 4 && fields[4] != "*" {
				lease.ClientID = strings.ToLower(fields[4])
			}
		} else if address.Is6() && !address.Is4In6() {
			lease.IAID = fields[1]
			if len(fields) > 4 && fields[4] != "*" {
				lease.DUID = strings.ToLower(fields[4])
			}
		} else {
			continue
		}

		leases = append(leases, lease)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return leases, nil
}

func parseDHCPLeaseExpiry(value string) (uint64, error) {
	var expiry uint64
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid DHCP lease expiry")
		}
		if expiry > (^uint64(0)-uint64(char-'0'))/10 {
			return 0, fmt.Errorf("DHCP lease expiry overflow")
		}
		expiry = expiry*10 + uint64(char-'0')
	}
	if value == "" {
		return 0, fmt.Errorf("empty DHCP lease expiry")
	}
	return expiry, nil
}

func (s *Service) getFileLeases() ([]networkServiceInterfaces.FileLeases, error) {
	data, err := s.readDHCPLeaseFile()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []networkServiceInterfaces.FileLeases{}, nil
		}
		return nil, err
	}
	return parseDHCPFileLeases(data)
}

func (s *Service) GetLeases() (networkServiceInterfaces.Leases, error) {
	s.dhcpRuntimeMutex.Lock()
	fileLeases, err := s.getFileLeases()
	s.dhcpRuntimeMutex.Unlock()
	if err != nil {
		return networkServiceInterfaces.Leases{}, err
	}

	dbLeases := make([]networkModels.DHCPStaticLease, 0)
	if err := s.DB.
		Preload("DHCPRange.StandardSwitch").
		Preload("DHCPRange.StandardSwitch.Ports").
		Preload("DHCPRange.ManualSwitch").
		Preload("IPObject.Entries").
		Preload("MACObject.Entries").
		Preload("DUIDObject.Entries").
		Find(&dbLeases).Error; err != nil {
		return networkServiceInterfaces.Leases{}, err
	}
	for i := range dbLeases {
		if dbLeases[i].DHCPRange != nil {
			ensureStandardSwitchPortCollection(dbLeases[i].DHCPRange.StandardSwitch)
		}
	}

	return networkServiceInterfaces.Leases{File: fileLeases, DB: dbLeases}, nil
}

type normalizedStaticMapRequest struct {
	hostname     string
	comments     string
	rangeID      uint
	ipObjectID   uint
	macObjectID  uint
	duidObjectID uint
}

func normalizeStaticMapRequest(
	tx *gorm.DB,
	hostname string,
	comments string,
	ipObjectID *uint,
	macObjectID *uint,
	duidObjectID *uint,
	dhcpRangeID uint,
) (*normalizedStaticMapRequest, error) {
	hostname = strings.TrimSpace(hostname)
	if !isValidDHCPLeaseHostname(hostname) {
		return nil, invalidDHCPLease("invalid_dhcp_lease_hostname", nil)
	}
	if len(comments) > MaxDHCPLeaseCommentsBytes {
		return nil, invalidDHCPLease("dhcp_lease_comments_too_long", nil)
	}
	if dhcpRangeID == 0 {
		return nil, invalidDHCPLease("invalid_dhcp_range_id", nil)
	}

	var dhcpRange networkModels.DHCPRange
	if err := tx.Select("id", "type").First(&dhcpRange, "id = ?", dhcpRangeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, dhcpLeaseNotFound("dhcp_range_not_found", err)
		}
		return nil, fmt.Errorf("load_dhcp_lease_range: %w", err)
	}
	if dhcpRange.Type != "ipv4" && dhcpRange.Type != "ipv6" {
		return nil, invalidDHCPLease("invalid_dhcp_range_type", nil)
	}

	ipID := optionalRequestID(ipObjectID)
	macID := optionalRequestID(macObjectID)
	duidID := optionalRequestID(duidObjectID)
	if ipID == 0 {
		return nil, invalidDHCPLease("dhcp_ip_object_required", nil)
	}

	ipObject, err := loadDHCPLeaseObject(tx, ipID, "Host", "ip")
	if err != nil {
		return nil, err
	}
	ipAddress, err := netip.ParseAddr(strings.TrimSpace(ipObject.Entries[0].Value))
	if err != nil || ipAddress.Zone() != "" || ipAddress.Is4In6() ||
		(dhcpRange.Type == "ipv4" && !ipAddress.Is4()) ||
		(dhcpRange.Type == "ipv6" && !ipAddress.Is6()) {
		return nil, invalidDHCPLease("dhcp_ip_object_family_mismatch", err)
	}

	switch dhcpRange.Type {
	case "ipv4":
		if macID == 0 {
			return nil, invalidDHCPLease("dhcp_ipv4_mac_required", nil)
		}
		if duidID != 0 {
			return nil, invalidDHCPLease("dhcp_ipv4_duid_not_allowed", nil)
		}
		macObject, err := loadDHCPLeaseObject(tx, macID, "Mac", "mac")
		if err != nil {
			return nil, err
		}
		if !utils.IsValidMAC(strings.TrimSpace(macObject.Entries[0].Value)) {
			return nil, invalidDHCPLease("invalid_dhcp_mac_object_value", nil)
		}
	case "ipv6":
		if duidID == 0 {
			return nil, invalidDHCPLease("dhcp_ipv6_duid_required", nil)
		}
		if macID != 0 {
			return nil, invalidDHCPLease("dhcp_ipv6_mac_not_allowed", nil)
		}
		duidObject, err := loadDHCPLeaseObject(tx, duidID, "DUID", "duid")
		if err != nil {
			return nil, err
		}
		if !utils.IsValidDUID(strings.TrimSpace(duidObject.Entries[0].Value)) {
			return nil, invalidDHCPLease("invalid_dhcp_duid_object_value", nil)
		}
	}

	return &normalizedStaticMapRequest{
		hostname:     hostname,
		comments:     comments,
		rangeID:      dhcpRangeID,
		ipObjectID:   ipID,
		macObjectID:  macID,
		duidObjectID: duidID,
	}, nil
}

func optionalRequestID(id *uint) uint {
	if id == nil {
		return 0
	}
	return *id
}

func isValidDHCPLeaseHostname(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > MaxDHCPLeaseHostnameBytes {
		return false
	}
	isAlphaNumeric := func(char byte) bool {
		return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
	}
	if !isAlphaNumeric(hostname[0]) || !isAlphaNumeric(hostname[len(hostname)-1]) {
		return false
	}
	for i := range len(hostname) {
		char := hostname[i]
		if !isAlphaNumeric(char) && char != '-' && char != '_' {
			return false
		}
	}
	return !strings.Contains(hostname, "--") && !strings.Contains(hostname, "__")
}

func loadDHCPLeaseObject(tx *gorm.DB, id uint, expectedType string, role string) (*networkModels.Object, error) {
	var object networkModels.Object
	if err := tx.Preload("Entries").First(&object, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, dhcpLeaseNotFound("dhcp_"+role+"_object_not_found", err)
		}
		return nil, fmt.Errorf("load_dhcp_%s_object: %w", role, err)
	}
	if object.Type != expectedType {
		return nil, invalidDHCPLease("invalid_dhcp_"+role+"_object_type", nil)
	}
	if len(object.Entries) != 1 {
		return nil, invalidDHCPLease("dhcp_"+role+"_object_requires_one_value", nil)
	}
	return &object, nil
}

func checkStaticMapConflicts(tx *gorm.DB, candidate *normalizedStaticMapRequest, excludeID uint) error {
	query := tx.Model(&networkModels.DHCPStaticLease{}).
		Where("dhcp_range_id = ? AND lower(hostname) = lower(?)", candidate.rangeID, candidate.hostname)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("check_dhcp_hostname_conflict: %w", err)
	}
	if count > 0 {
		return conflictingDHCPLease("duplicate_hostname", nil)
	}

	checks := []struct {
		column string
		id     uint
		code   string
	}{
		{column: "ip_object_id", id: candidate.ipObjectID, code: "duplicate_ip_in_range"},
		{column: "mac_object_id", id: candidate.macObjectID, code: "duplicate_mac_in_range"},
		{column: "d_uid_object_id", id: candidate.duidObjectID, code: "duplicate_duid_in_range"},
	}
	for _, check := range checks {
		if check.id == 0 {
			continue
		}
		query := tx.Model(&networkModels.DHCPStaticLease{}).
			Where("dhcp_range_id = ? AND "+check.column+" = ?", candidate.rangeID, check.id)
		if excludeID != 0 {
			query = query.Where("id <> ?", excludeID)
		}
		count = 0
		if err := query.Count(&count).Error; err != nil {
			return fmt.Errorf("check_%s_conflict: %w", check.column, err)
		}
		if count > 0 {
			return conflictingDHCPLease(check.code, nil)
		}
	}
	return nil
}

func mapDBErr(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	isUnique := strings.Contains(message, "unique constraint failed")
	switch {
	case strings.Contains(message, "uniq_l_ip_per_range"),
		strings.Contains(message, "uniq_ip_per_range"),
		(isUnique && strings.Contains(message, "ip_object_id") && strings.Contains(message, "dhcp_range_id")):
		return conflictingDHCPLease("duplicate_ip_in_range", err)
	case strings.Contains(message, "uniq_l_mac_per_range"),
		strings.Contains(message, "uniq_mac_per_range"),
		(isUnique && strings.Contains(message, "mac_object_id") && strings.Contains(message, "dhcp_range_id")):
		return conflictingDHCPLease("duplicate_mac_in_range", err)
	case strings.Contains(message, "uniq_l_duid_per_range"),
		strings.Contains(message, "uniq_duid_per_range"),
		(isUnique && (strings.Contains(message, "d_uid_object_id") || strings.Contains(message, "duid_object_id")) && strings.Contains(message, "dhcp_range_id")):
		return conflictingDHCPLease("duplicate_duid_in_range", err)
	default:
		return err
	}
}

func (s *Service) CreateStaticMap(req *networkServiceInterfaces.CreateStaticMapRequest) (uint, error) {
	if req == nil {
		return 0, invalidDHCPLease("invalid_dhcp_lease_request", nil)
	}

	var createdID uint
	err := s.applyDHCPMutation("create_dhcp_lease", func(tx *gorm.DB) (bool, error) {
		candidate, err := normalizeStaticMapRequest(
			tx,
			req.Hostname,
			req.Comments,
			req.IPObjectID,
			req.MACObjectID,
			req.DUIDObjectID,
			req.DHCPRangeID,
		)
		if err != nil {
			return false, err
		}
		if err := checkStaticMapConflicts(tx, candidate, 0); err != nil {
			return false, err
		}

		lease := networkModels.DHCPStaticLease{
			Hostname:     candidate.hostname,
			Comments:     candidate.comments,
			IPObjectID:   utils.PtrIfNonZero(candidate.ipObjectID),
			MACObjectID:  utils.PtrIfNonZero(candidate.macObjectID),
			DUIDObjectID: utils.PtrIfNonZero(candidate.duidObjectID),
			DHCPRangeID:  candidate.rangeID,
		}
		if err := tx.Create(&lease).Error; err != nil {
			return false, mapDBErr(fmt.Errorf("create_static_dhcp_lease: %w", err))
		}
		createdID = lease.ID
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	return createdID, nil
}

func (s *Service) ModifyStaticMap(id uint, req *networkServiceInterfaces.ModifyStaticMapRequest) error {
	if id == 0 {
		return invalidDHCPLease("invalid_dhcp_lease_id", nil)
	}
	if req == nil {
		return invalidDHCPLease("invalid_dhcp_lease_request", nil)
	}

	return s.applyDHCPMutation("modify_dhcp_lease", func(tx *gorm.DB) (bool, error) {
		var current networkModels.DHCPStaticLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, dhcpLeaseNotFound("dhcp_lease_not_found", err)
			}
			return false, fmt.Errorf("load_static_dhcp_lease: %w", err)
		}

		candidate, err := normalizeStaticMapRequest(
			tx,
			req.Hostname,
			req.Comments,
			req.IPObjectID,
			req.MACObjectID,
			req.DUIDObjectID,
			req.DHCPRangeID,
		)
		if err != nil {
			return false, err
		}
		if err := checkStaticMapConflicts(tx, candidate, id); err != nil {
			return false, err
		}
		if staticMapMatches(&current, candidate) {
			return false, nil
		}

		updates := map[string]any{
			"hostname":        candidate.hostname,
			"comments":        candidate.comments,
			"ip_object_id":    nullableStaticMapID(candidate.ipObjectID),
			"mac_object_id":   nullableStaticMapID(candidate.macObjectID),
			"d_uid_object_id": nullableStaticMapID(candidate.duidObjectID),
			"dhcp_range_id":   candidate.rangeID,
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return false, mapDBErr(fmt.Errorf("modify_static_dhcp_lease: %w", err))
		}
		return true, nil
	})
}

func staticMapMatches(current *networkModels.DHCPStaticLease, candidate *normalizedStaticMapRequest) bool {
	return current.Hostname == candidate.hostname &&
		current.Comments == candidate.comments &&
		current.DHCPRangeID == candidate.rangeID &&
		staticMapIDEquals(current.IPObjectID, candidate.ipObjectID) &&
		staticMapIDEquals(current.MACObjectID, candidate.macObjectID) &&
		staticMapIDEquals(current.DUIDObjectID, candidate.duidObjectID)
}

func staticMapIDEquals(current *uint, requested uint) bool {
	if requested == 0 {
		return current == nil
	}
	return current != nil && *current == requested
}

func nullableStaticMapID(id uint) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Service) DeleteStaticMap(id uint) error {
	if id == 0 {
		return invalidDHCPLease("invalid_dhcp_lease_id", nil)
	}

	return s.applyDHCPMutation("delete_dhcp_lease", func(tx *gorm.DB) (bool, error) {
		var current networkModels.DHCPStaticLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, dhcpLeaseNotFound("dhcp_lease_not_found", err)
			}
			return false, fmt.Errorf("load_static_dhcp_lease: %w", err)
		}
		if err := tx.Delete(&current).Error; err != nil {
			return false, fmt.Errorf("delete_static_dhcp_lease: %w", err)
		}
		return true, nil
	})
}

func normalizeDynamicLeaseRequest(req *networkServiceInterfaces.DeleteDynamicLeaseRequest) (string, netip.Addr, error) {
	if req == nil {
		return "", netip.Addr{}, invalidDHCPLease("invalid_dynamic_dhcp_lease_request", nil)
	}
	identifier := strings.ToLower(strings.TrimSpace(req.Identifier))
	ip := strings.TrimSpace(req.IP)
	if identifier == "" || len(identifier) > MaxDynamicDHCPLeaseIdentifierBytes ||
		strings.IndexFunc(identifier, unicode.IsSpace) >= 0 {
		return "", netip.Addr{}, invalidDHCPLease("invalid_dynamic_dhcp_lease_identifier", nil)
	}
	address, err := netip.ParseAddr(ip)
	if err != nil || address.Zone() != "" || address.Is4In6() {
		return "", netip.Addr{}, invalidDHCPLease("invalid_dynamic_dhcp_lease_ip", err)
	}
	if address.Is4() {
		if !utils.IsValidMAC(identifier) {
			return "", netip.Addr{}, invalidDHCPLease("invalid_dynamic_dhcp_lease_identifier", nil)
		}
	} else if !utils.IsValidDUID(identifier) {
		return "", netip.Addr{}, invalidDHCPLease("invalid_dynamic_dhcp_lease_identifier", nil)
	}
	return identifier, address, nil
}

func (s *Service) DeleteDynamicLease(req *networkServiceInterfaces.DeleteDynamicLeaseRequest) error {
	identifier, address, err := normalizeDynamicLeaseRequest(req)
	if err != nil {
		return err
	}

	s.dhcpRuntimeMutex.Lock()
	defer s.dhcpRuntimeMutex.Unlock()

	original, err := s.readDHCPLeaseFile()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dhcpLeaseNotFound("dynamic_dhcp_lease_not_found", err)
		}
		return fmt.Errorf("read_dynamic_dhcp_leases: %w", err)
	}

	lines := strings.Split(string(original), "\n")
	remaining := make([]string, 0, len(lines))
	removed := false
	for _, raw := range lines {
		fields := strings.Fields(strings.TrimSpace(raw))
		if dynamicLeaseLineMatches(fields, identifier, address) {
			removed = true
			continue
		}
		remaining = append(remaining, raw)
	}
	if !removed {
		return dhcpLeaseNotFound("dynamic_dhcp_lease_not_found", nil)
	}

	if err := s.writeDHCPLeaseFile([]byte(strings.Join(remaining, "\n"))); err != nil {
		return fmt.Errorf("write_dynamic_dhcp_leases: %w", err)
	}
	if err := s.restartDNSMasq(); err != nil {
		s.restoreDHCPLeaseRuntimeAfterFailure(original, "delete_dynamic_dhcp_lease")
		return fmt.Errorf("restart_dnsmasq_after_dynamic_lease_delete: %w", err)
	}
	return nil
}

func dynamicLeaseLineMatches(fields []string, identifier string, address netip.Addr) bool {
	if len(fields) < 4 || strings.HasPrefix(fields[0], "#") || strings.EqualFold(fields[0], "duid") {
		return false
	}
	rowAddress, err := netip.ParseAddr(fields[2])
	if err != nil || rowAddress != address {
		return false
	}
	rowIdentifier := ""
	if rowAddress.Is4() {
		rowIdentifier = fields[1]
	} else if rowAddress.Is6() && !rowAddress.Is4In6() && len(fields) > 4 {
		rowIdentifier = fields[4]
	}
	return strings.EqualFold(rowIdentifier, identifier)
}

func (s *Service) restoreDHCPLeaseRuntimeAfterFailure(snapshot []byte, operation string) {
	if err := s.writeDHCPLeaseFile(snapshot); err != nil {
		logger.L.Error().Err(err).Str("operation", operation).Msg("dhcp_lease_file_restore_failed")
		return
	}
	if err := s.restartDNSMasq(); err != nil {
		logger.L.Error().Err(err).Str("operation", operation).Msg("dhcp_runtime_restore_failed")
	}
}
