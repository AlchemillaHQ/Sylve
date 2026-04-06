// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package network

type wireGuardInterfaceOpsUnsupported struct{}

func newWireGuardInterfaceOps() wireGuardInterfaceOps {
	return wireGuardInterfaceOpsUnsupported{}
}

func (wireGuardInterfaceOpsUnsupported) Exists(string) (bool, error) {
	return false, errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) Create(string) (string, error) {
	return "", errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) Rename(string, string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) Destroy(string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) AddAddress(string, string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) SetMTU(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) SetMetric(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) SetFIB(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsUnsupported) Up(string) error {
	return errWireGuardInterfaceOpsUnsupported
}
