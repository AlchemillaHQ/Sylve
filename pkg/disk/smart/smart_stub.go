// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package smart

import (
	"strings"
)

// Read is a cross-platform stub. It returns safe dummy data
// so that testing and compilation pass on non-FreeBSD systems.
func Read(devicePath string) (*DeviceInfo, error) {
	cleanPath := strings.TrimPrefix(devicePath, "/dev/")

	return &DeviceInfo{
		Device:          cleanPath,
		Protocol:        "Mock",
		Temperature:     35, // Safe dummy temperature
		PowerOnHours:    100,
		PowerCycleCount: 10,
		Attributes:      []Attribute{},
	}, nil
}
