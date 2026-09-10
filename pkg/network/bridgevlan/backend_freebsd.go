// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package bridgevlan

/*
#include <stdlib.h>

#include "backend_freebsd.h"
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

type nativeBackend struct{}

func newNativeBackend() backend { return nativeBackend{} }

func nativeInterfaceNameSize() int {
	return int(C.IFNAMSIZ)
}

func validateNativeInterfaceName(role, name string) error {
	switch {
	case name == "":
		return fmt.Errorf("invalid %s interface name: empty", role)
	case strings.IndexByte(name, 0) >= 0:
		return fmt.Errorf("invalid %s interface name %q: contains NUL", role, name)
	case len(name) >= nativeInterfaceNameSize():
		return fmt.Errorf(
			"invalid %s interface name %q: exceeds %d bytes",
			role,
			name,
			nativeInterfaceNameSize()-1,
		)
	default:
		return nil
	}
}

func cNames(bridge, member string) (*C.char, *C.char, func(), error) {
	if err := validateNativeInterfaceName("bridge", bridge); err != nil {
		return nil, nil, nil, err
	}
	if err := validateNativeInterfaceName("member", member); err != nil {
		return nil, nil, nil, err
	}
	cBridge := C.CString(bridge)
	cMember := C.CString(member)
	return cBridge, cMember, func() {
		C.free(unsafe.Pointer(cBridge))
		C.free(unsafe.Pointer(cMember))
	}, nil
}

func cBridgeName(bridge string) (*C.char, func(), error) {
	if err := validateNativeInterfaceName("bridge", bridge); err != nil {
		return nil, nil, err
	}
	cBridge := C.CString(bridge)
	return cBridge, func() { C.free(unsafe.Pointer(cBridge)) }, nil
}

func nativeCallError(operation string, result C.int, callErr error) error {
	if result == 0 {
		return nil
	}
	if callErr == nil {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, callErr)
}

func (nativeBackend) BridgeState(bridge string) (BridgeState, error) {
	cBridge, cleanup, err := cBridgeName(bridge)
	if err != nil {
		return BridgeState{}, err
	}
	defer cleanup()
	var flags C.uint32_t
	var pvid C.uint16_t
	result, callErr := C.sylve_bridge_get_state(cBridge, &flags, &pvid)
	if err := nativeCallError("get bridge state", result, callErr); err != nil {
		return BridgeState{}, err
	}
	return BridgeState{
		VLANFiltering: uint32(flags)&uint32(C.sylve_bridge_vlanfilter_flag()) != 0,
		DefaultPVID:   int(pvid),
		DefaultQinQ:   uint32(flags)&uint32(C.sylve_bridge_default_qinq_flag()) != 0,
	}, nil
}

func (nativeBackend) SetVLANFiltering(bridge string, enabled bool) error {
	cBridge, cleanup, err := cBridgeName(bridge)
	if err != nil {
		return err
	}
	defer cleanup()
	value := C.int(0)
	if enabled {
		value = 1
	}
	result, callErr := C.sylve_bridge_set_filtering(cBridge, value)
	return nativeCallError("set bridge VLAN filtering", result, callErr)
}

func (nativeBackend) SetDefaultPVID(bridge string, pvid int) error {
	if pvid != 0 && !ValidVLAN(pvid) {
		return fmt.Errorf("%w: %d", ErrInvalidVLAN, pvid)
	}
	cBridge, cleanup, err := cBridgeName(bridge)
	if err != nil {
		return err
	}
	defer cleanup()
	result, callErr := C.sylve_bridge_set_default_pvid(cBridge, C.uint16_t(pvid))
	return nativeCallError("set bridge default PVID", result, callErr)
}

func (nativeBackend) SetDefaultQinQ(bridge string, enabled bool) error {
	cBridge, cleanup, err := cBridgeName(bridge)
	if err != nil {
		return err
	}
	defer cleanup()
	value := C.int(0)
	if enabled {
		value = 1
	}
	result, callErr := C.sylve_bridge_set_default_qinq(cBridge, value)
	return nativeCallError("set bridge default Q-in-Q", result, callErr)
}

func (nativeBackend) memberState(bridge, member string) (memberState, error) {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return memberState{}, err
	}
	defer cleanup()
	var flags C.uint32_t
	var pvid C.uint16_t
	var protocol C.uint16_t
	allowed := make([]C.uint8_t, C.BRVLAN_SETSIZE)
	result, callErr := C.sylve_bridge_get_member(cBridge, cMember, &flags, &pvid, &protocol, &allowed[0])
	if err := nativeCallError("get bridge member state", result, callErr); err != nil {
		return memberState{}, err
	}
	tagged := make([]int, 0)
	for vlan := MinVLAN; vlan <= MaxVLAN; vlan++ {
		if allowed[vlan] != 0 {
			tagged = append(tagged, vlan)
		}
	}
	return memberState{
		PVID: int(pvid), TaggedVLANs: tagged,
		VLANProtocol: uint16(protocol),
		QinQ:         uint32(flags)&uint32(C.sylve_bridge_member_qinq_flag()) != 0,
		Private:      uint32(flags)&uint32(C.sylve_bridge_member_private_flag()) != 0,
	}, nil
}

func (nativeBackend) AddMember(bridge, member string) error {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	result, callErr := C.sylve_bridge_add_member(cBridge, cMember)
	return nativeCallError("add bridge member", result, callErr)
}

func (nativeBackend) RemoveMember(bridge, member string) error {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	result, callErr := C.sylve_bridge_remove_member(cBridge, cMember)
	return nativeCallError("remove bridge member", result, callErr)
}

func (nativeBackend) SetMemberPVID(bridge, member string, pvid int) error {
	if pvid != 0 && !ValidVLAN(pvid) {
		return fmt.Errorf("%w: %d", ErrInvalidVLAN, pvid)
	}
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	result, callErr := C.sylve_bridge_set_member_pvid(cBridge, cMember, C.uint16_t(pvid))
	return nativeCallError("set bridge member PVID", result, callErr)
}

func (nativeBackend) SetMemberTaggedVLANs(bridge, member string, vlans []int) error {
	values := make([]C.uint16_t, len(vlans))
	for index, vlan := range vlans {
		if !ValidVLAN(vlan) {
			return fmt.Errorf("%w: %d", ErrInvalidVLAN, vlan)
		}
		values[index] = C.uint16_t(vlan)
	}
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	var pointer *C.uint16_t
	if len(values) != 0 {
		pointer = &values[0]
	}
	result, callErr := C.sylve_bridge_set_member_vlans(cBridge, cMember, pointer, C.size_t(len(values)))
	return nativeCallError("set bridge member tagged VLANs", result, callErr)
}

func (nativeBackend) SetMemberVLANProtocol(bridge, member string, protocol uint16) error {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	result, callErr := C.sylve_bridge_set_member_protocol(cBridge, cMember, C.uint16_t(protocol))
	return nativeCallError("set bridge member VLAN protocol", result, callErr)
}

func (nativeBackend) SetMemberQinQ(bridge, member string, enabled bool) error {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	value := C.int(0)
	if enabled {
		value = 1
	}
	result, callErr := C.sylve_bridge_set_member_qinq(cBridge, cMember, value)
	return nativeCallError("set bridge member Q-in-Q", result, callErr)
}

func (nativeBackend) SetMemberPrivate(bridge, member string, enabled bool) error {
	cBridge, cMember, cleanup, err := cNames(bridge, member)
	if err != nil {
		return err
	}
	defer cleanup()
	value := C.int(0)
	if enabled {
		value = 1
	}
	result, callErr := C.sylve_bridge_set_member_private(cBridge, cMember, value)
	return nativeCallError("set bridge member private state", result, callErr)
}
