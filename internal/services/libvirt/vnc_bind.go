// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"net"
	"strings"
)

const DefaultVNCBindAddress = "127.0.0.1"

func NormalizeVNCBindAddress(bind string) string {
	clean := strings.TrimSpace(bind)
	if clean == "" {
		return DefaultVNCBindAddress
	}

	return clean
}

func ValidateVNCBindAddress(bind string) error {
	if net.ParseIP(NormalizeVNCBindAddress(bind)) == nil {
		return fmt.Errorf("invalid_vnc_bind_ip")
	}

	return nil
}

func NormalizeVNCBindAddressForDial(bind string) string {
	normalized := NormalizeVNCBindAddress(bind)
	ip := net.ParseIP(normalized)
	if ip == nil {
		return DefaultVNCBindAddress
	}

	if ip.IsUnspecified() {
		if ip.To4() != nil {
			return "127.0.0.1"
		}

		return "::1"
	}

	return normalized
}
