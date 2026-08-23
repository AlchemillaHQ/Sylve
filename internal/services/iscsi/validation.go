// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"fmt"
	"net"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxISCSINameLength = 223
	maxNicknameLength  = 128
	maxQuotedLength    = 255
	maxZVolLength      = 1024
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

func normalizePortalAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalidRequest("portal_address_required")
	}

	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", invalidRequest("portal_address_must_be_an_ip_address")
	}

	return formatAddress(addr), nil
}

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
