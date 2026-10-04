// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
)

const (
	maxISCSINameLength = 223
	maxNicknameLength  = 128
	maxQuotedLength    = 255
	maxZVolLength      = 1024
	maxTargetLUNNumber = 1023
)

func validateBareConfigToken(value, field string, maxLength int) error {
	if len(value) > maxLength {
		return invalidRequest(fmt.Sprintf("%s_too_long", field))
	}

	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("{};#\"'\\=", r) {
			return invalidRequest(fmt.Sprintf("%s_contains_invalid_characters", field))
		}
	}

	return nil
}

func validateQuotedConfigValue(value, field string, maxLength int) error {
	if !utf8.ValidString(value) {
		return invalidRequest(fmt.Sprintf("%s_contains_invalid_characters", field))
	}
	if len(value) > maxLength {
		return invalidRequest(fmt.Sprintf("%s_too_long", field))
	}

	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return invalidRequest(fmt.Sprintf("%s_contains_invalid_characters", field))
		}
	}

	return nil
}

func normalizeInitiatorTargetAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalidRequest("target_address_required")
	}

	if addr, err := netip.ParseAddr(value); err == nil {
		return formatAddress(addr), nil
	}

	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
		if err != nil || !addr.Is6() {
			return "", invalidRequest("invalid_target_address")
		}
		return formatAddress(addr), nil
	}

	if host, portText, err := net.SplitHostPort(value); err == nil {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return "", invalidRequest("target_port_must_be_between_1_and_65535")
		}

		normalizedHost, err := normalizeAddressHost(host)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(normalizedHost, strconv.Itoa(port)), nil
	}

	if strings.Contains(value, ":") {
		return "", invalidRequest("invalid_target_address")
	}

	return normalizeHostname(value)
}

func validateNativeQuotedValue(value, field string) error {
	if err := validateQuotedConfigValue(value, field, maxQuotedLength); err != nil {
		return err
	}
	if strings.Contains(value, `"`) {
		return invalidRequest(field + "_contains_invalid_characters")
	}
	return nil
}

func nativeQuote(value string) string { return `"` + value + `"` }

func normalizeAddressHost(value string) (string, error) {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.String(), nil
	}
	return normalizeHostname(value)
}

func normalizeHostname(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 253 {
		return "", invalidRequest("invalid_target_address")
	}

	hostname := strings.TrimSuffix(value, ".")
	if hostname == "" {
		return "", invalidRequest("invalid_target_address")
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) == 0 || len(label) > 63 || !isASCIILetterOrDigit(label[0]) || !isASCIILetterOrDigit(label[len(label)-1]) {
			return "", invalidRequest("invalid_target_address")
		}
		for i := 1; i < len(label)-1; i++ {
			if !isASCIILetterOrDigit(label[i]) && label[i] != '-' {
				return "", invalidRequest("invalid_target_address")
			}
		}
	}

	return value, nil
}

func isASCIILetterOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func formatAddress(addr netip.Addr) string {
	if addr.Is6() {
		return "[" + addr.String() + "]"
	}
	return addr.String()
}

func validateZVol(value string) error {
	if value == "" {
		return invalidRequest("zvol_required")
	}
	if len(value) > maxZVolLength {
		return invalidRequest("zvol_too_long")
	}
	if strings.HasPrefix(value, "/") || !strings.Contains(value, "/") || path.Clean(value) != value {
		return invalidRequest("invalid_zvol")
	}

	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalidRequest("invalid_zvol")
		}
	}
	for _, r := range value {
		if r > unicode.MaxASCII || !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/%", r)) {
			return invalidRequest("invalid_zvol")
		}
	}

	return nil
}

var portalZonePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

type portalEndpoint struct {
	address netip.Addr
	port    int
	scope   int
}

func (e portalEndpoint) key() string {
	family := "v4"
	if e.address.Is6() {
		family = "v6"
	}
	address := e.address.String()
	return fmt.Sprintf("tcp|%s|%d:%s|%d", family, len(address), address, e.port)
}

func (e portalEndpoint) listen() string {
	return net.JoinHostPort(e.address.String(), strconv.Itoa(e.port))
}

func (e portalEndpoint) sameSocket(other portalEndpoint) bool {
	return e.port == other.port && e.scope == other.scope && e.address.WithZone("") == other.address.WithZone("")
}

func (e portalEndpoint) overlaps(other portalEndpoint, v6Only bool) bool {
	if e.port != other.port {
		return false
	}
	if e.sameSocket(other) {
		return true
	}
	if e.address.Is4() == other.address.Is4() {
		return e.address.IsUnspecified() || other.address.IsUnspecified()
	}
	return !v6Only && (e.address.Is6() && e.address.IsUnspecified() || other.address.Is6() && other.address.IsUnspecified())
}

func (s *Service) endpoint(address string, port int) (portalEndpoint, error) {
	var result portalEndpoint
	address = strings.TrimSpace(address)
	if address == "" {
		return result, invalidRequest("portal_address_required")
	}
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = address[1 : len(address)-1]
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return result, invalidRequest("portal_address_must_be_an_ip_address")
	}
	if parsed.Is4In6() {
		return result, invalidRequest("portal_ipv4_mapped_ipv6_not_supported")
	}
	if port == 0 {
		port = 3260
	}
	if port < 1 || port > 65535 {
		return result, invalidRequest("portal_port_must_be_between_1_and_65535")
	}
	zone := parsed.Zone()
	if parsed.IsLinkLocalUnicast() && parsed.Is6() {
		if !portalZonePattern.MatchString(zone) {
			return result, invalidRequest("portal_ipv6_interface_zone_required")
		}
		lookup := s.interfaceLookup
		if lookup == nil {
			lookup = net.InterfaceByName
		}
		iface, err := lookup(zone)
		if err != nil || iface == nil || iface.Index <= 0 || !portalZonePattern.MatchString(iface.Name) {
			return result, invalidRequest("portal_ipv6_interface_zone_unknown")
		}
		parsed = parsed.WithZone(iface.Name)
		result.scope = iface.Index
	} else if zone != "" {
		return result, invalidRequest("portal_ipv6_zone_not_allowed")
	}
	result.address, result.port = parsed, port
	return result, nil
}

func (s *Service) groupName(endpoint portalEndpoint) string {
	if s.portalGroupName != nil {
		return s.portalGroupName(endpoint)
	}
	hash := sha256.Sum256([]byte(endpoint.key()))
	return fmt.Sprintf("pg-e-%x", hash[:8])
}

func (s *Service) readIPv6OnlyContext(ctx context.Context) bool {
	if s.ipv6Only != nil {
		return s.ipv6Only()
	}
	out, err := s.runTargetCommand(ctx, "", "/sbin/sysctl", "-n", "net.inet6.ip6.v6only")
	return err == nil && strings.TrimSpace(out) == "1"
}

func (s *Service) endpointsOverlap(ctx context.Context, endpoints []portalEndpoint) bool {
	v6Only := true
	for _, endpoint := range endpoints {
		if endpoint.address.Is6() && endpoint.address.IsUnspecified() {
			v6Only = s.readIPv6OnlyContext(ctx)
			break
		}
	}
	for i, endpoint := range endpoints {
		for _, other := range endpoints[:i] {
			if endpoint.overlaps(other, v6Only) {
				return true
			}
		}
	}
	return false
}

func (s *Service) collectPortalEndpoints(targets []iscsiModels.ISCSITarget) ([]portalEndpoint, map[uint][]portalEndpoint, error) {
	ctx, cancel := s.targetContext()
	defer cancel()
	return s.collectPortalEndpointsContext(ctx, targets)
}

func (s *Service) collectPortalEndpointsContext(ctx context.Context, targets []iscsiModels.ISCSITarget) ([]portalEndpoint, map[uint][]portalEndpoint, error) {
	attachments := make(map[uint][]portalEndpoint)
	unique := make(map[string]portalEndpoint)
	names := make(map[string]string)
	for _, target := range targets {
		seen := make(map[string]bool)
		for _, portal := range target.Portals {
			endpoint, err := s.endpoint(portal.Address, portal.Port)
			if err != nil {
				return nil, nil, err
			}
			key := endpoint.key()
			if seen[key] {
				return nil, nil, resourceConflict("portal_already_exists", nil)
			}
			seen[key] = true
			name := s.groupName(endpoint)
			if old, exists := names[name]; exists && old != key {
				return nil, nil, resourceConflict("portal_group_hash_collision", nil)
			}
			names[name] = key
			unique[key] = endpoint
			attachments[target.ID] = append(attachments[target.ID], endpoint)
		}
		sort.Slice(attachments[target.ID], func(i, j int) bool { return attachments[target.ID][i].key() < attachments[target.ID][j].key() })
	}
	endpoints := make([]portalEndpoint, 0, len(unique))
	for _, endpoint := range unique {
		endpoints = append(endpoints, endpoint)
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].key() < endpoints[j].key() })
	if s.endpointsOverlap(ctx, endpoints) {
		return nil, nil, resourceConflict("portal_listener_overlap", nil)
	}
	return endpoints, attachments, nil
}
