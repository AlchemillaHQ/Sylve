// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package bridgevlan

import "errors"

var errUnsupportedPlatform = errors.New("FreeBSD 15 bridge VLAN filtering is unavailable on this platform")

type unsupportedBackend struct{}

func newNativeBackend() backend { return unsupportedBackend{} }

func (unsupportedBackend) BridgeState(string) (BridgeState, error) {
	return BridgeState{}, errUnsupportedPlatform
}
func (unsupportedBackend) SetVLANFiltering(string, bool) error { return errUnsupportedPlatform }
func (unsupportedBackend) SetDefaultPVID(string, int) error    { return errUnsupportedPlatform }
func (unsupportedBackend) SetDefaultQinQ(string, bool) error   { return errUnsupportedPlatform }
func (unsupportedBackend) memberState(string, string) (memberState, error) {
	return memberState{}, errUnsupportedPlatform
}
func (unsupportedBackend) AddMember(string, string) error          { return errUnsupportedPlatform }
func (unsupportedBackend) RemoveMember(string, string) error       { return errUnsupportedPlatform }
func (unsupportedBackend) SetMemberPVID(string, string, int) error { return errUnsupportedPlatform }
func (unsupportedBackend) SetMemberTaggedVLANs(string, string, []int) error {
	return errUnsupportedPlatform
}
func (unsupportedBackend) SetMemberVLANProtocol(string, string, uint16) error {
	return errUnsupportedPlatform
}
func (unsupportedBackend) SetMemberQinQ(string, string, bool) error { return errUnsupportedPlatform }
func (unsupportedBackend) SetMemberPrivate(string, string, bool) error {
	return errUnsupportedPlatform
}
