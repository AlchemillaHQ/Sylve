// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package pciconf

// GetPCIDevices is a cross-platform stub that returns an empty list.
// If your application logic requires at least one PCI device to be present
// to pass a unit test, you can inject a mock PCIDevice struct here.
func GetPCIDevices() ([]PCIDevice, error) {
	// Returning an empty slice prevents panics in tests while
	// accurately reflecting that no FreeBSD-style PCI devices were found.
	return []PCIDevice{}, nil
}
