// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bridgevlan

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"syscall"
)

const vlanProtocol8021Q uint16 = 0x8100

var (
	ErrBridgeNotFiltered  = errors.New("bridge VLAN filtering is disabled")
	ErrBridgeDefaultPVID  = errors.New("bridge default PVID does not match")
	ErrBridgeDefaultQinQ  = errors.New("bridge default Q-in-Q is enabled")
	ErrMemberPolicyDrift  = errors.New("bridge member VLAN policy does not match")
	ErrMemberPrivateDrift = errors.New("bridge member private state does not match")
)

type BridgeState struct {
	VLANFiltering bool `json:"vlanFiltering"`
	DefaultPVID   int  `json:"defaultPvid"`
	DefaultQinQ   bool `json:"defaultQinQ"`
}

type memberState struct {
	PVID         int
	TaggedVLANs  []int
	VLANProtocol uint16
	QinQ         bool
	Private      bool
}

type backend interface {
	BridgeState(bridge string) (BridgeState, error)
	SetVLANFiltering(bridge string, enabled bool) error
	SetDefaultPVID(bridge string, pvid int) error
	SetDefaultQinQ(bridge string, enabled bool) error
	memberState(bridge, member string) (memberState, error)
	AddMember(bridge, member string) error
	RemoveMember(bridge, member string) error
	SetMemberPVID(bridge, member string, pvid int) error
	SetMemberTaggedVLANs(bridge, member string, vlans []int) error
	SetMemberVLANProtocol(bridge, member string, protocol uint16) error
	SetMemberQinQ(bridge, member string, enabled bool) error
	SetMemberPrivate(bridge, member string, enabled bool) error
}

type controller struct {
	backend backend
}

func newController(backend backend) *controller {
	return &controller{backend: backend}
}

var defaultController = newController(newNativeBackend())

var bridgeOperationLocks sync.Map

func InspectBridge(name string) (BridgeState, error) {
	return defaultController.InspectBridge(name)
}

func ValidateFilteredBridge(name string, expectedDefaultAccessVLAN *int) (BridgeState, error) {
	return defaultController.ValidateFilteredBridge(name, expectedDefaultAccessVLAN)
}

func PrepareManagedFilteredBridge(name string) error {
	return defaultController.PrepareManagedFilteredBridge(name)
}

func SetDefaultAccessVLAN(name string, vlan *int) error {
	return defaultController.SetDefaultAccessVLAN(name, vlan)
}

func RemoveMember(bridge, member string) error {
	return defaultController.RemoveMember(bridge, member)
}

func ConfigureMember(
	bridge, member string,
	expectedDefaultAccessVLAN *int,
	policy PortPolicy,
) error {
	return defaultController.ConfigureMember(bridge, member, expectedDefaultAccessVLAN, policy)
}

func MemberPolicyMatches(bridge, member string, policy PortPolicy) (bool, error) {
	return defaultController.MemberPolicyMatches(bridge, member, policy)
}

func SetMemberPrivate(bridge, member string, enabled bool) error {
	return defaultController.SetMemberPrivate(bridge, member, enabled)
}

func (c *controller) InspectBridge(name string) (BridgeState, error) {
	state, err := c.backend.BridgeState(name)
	if err != nil {
		return BridgeState{}, fmt.Errorf("inspect bridge %q VLAN state: %w", name, err)
	}
	return state, nil
}

func desiredDefaultPVID(value *int) (int, error) {
	if value == nil {
		return 0, nil
	}
	if !ValidVLAN(*value) {
		return 0, fmt.Errorf("%w: %d", ErrInvalidVLAN, *value)
	}
	return *value, nil
}

func (c *controller) ValidateFilteredBridge(name string, expectedDefaultAccessVLAN *int) (BridgeState, error) {
	expected, err := desiredDefaultPVID(expectedDefaultAccessVLAN)
	if err != nil {
		return BridgeState{}, err
	}
	state, err := c.InspectBridge(name)
	if err != nil {
		return BridgeState{}, err
	}
	if !state.VLANFiltering {
		return state, fmt.Errorf("%w: %s", ErrBridgeNotFiltered, name)
	}
	if state.DefaultPVID != expected {
		return state, fmt.Errorf("%w: bridge %s has %d, expected %d", ErrBridgeDefaultPVID, name, state.DefaultPVID, expected)
	}
	if state.DefaultQinQ {
		return state, fmt.Errorf("%w: %s", ErrBridgeDefaultQinQ, name)
	}
	return state, nil
}

func (c *controller) inspectMember(bridge, member string) (memberState, error) {
	state, err := c.backend.memberState(bridge, member)
	if err != nil {
		return memberState{}, fmt.Errorf("inspect bridge %q member %q VLAN state: %w", bridge, member, err)
	}
	if state.TaggedVLANs == nil {
		state.TaggedVLANs = []int{}
	}
	return state, nil
}

func (c *controller) PrepareManagedFilteredBridge(name string) error {
	return c.withBridgeLock(name, func() error {
		return c.prepareManagedFilteredBridge(name)
	})
}

func (c *controller) prepareManagedFilteredBridge(name string) error {
	if err := c.backend.SetDefaultPVID(name, 0); err != nil {
		return fmt.Errorf("clear bridge %q default PVID: %w", name, err)
	}
	if err := c.backend.SetDefaultQinQ(name, false); err != nil {
		return fmt.Errorf("disable bridge %q default Q-in-Q: %w", name, err)
	}
	if err := c.backend.SetVLANFiltering(name, true); err != nil {
		return fmt.Errorf("enable bridge %q VLAN filtering: %w", name, err)
	}
	_, err := c.ValidateFilteredBridge(name, nil)
	return err
}

func (c *controller) SetDefaultAccessVLAN(name string, vlan *int) error {
	return c.withBridgeLock(name, func() error {
		return c.setDefaultAccessVLAN(name, vlan)
	})
}

func (c *controller) setDefaultAccessVLAN(name string, vlan *int) error {
	desired, err := desiredDefaultPVID(vlan)
	if err != nil {
		return err
	}
	state, err := c.InspectBridge(name)
	if err != nil {
		return err
	}
	if !state.VLANFiltering {
		return fmt.Errorf("%w: %s", ErrBridgeNotFiltered, name)
	}
	if state.DefaultQinQ {
		if err := c.backend.SetDefaultQinQ(name, false); err != nil {
			return fmt.Errorf("disable bridge %q default Q-in-Q: %w", name, err)
		}
	}
	if err := c.backend.SetDefaultPVID(name, desired); err != nil {
		return fmt.Errorf("set bridge %q default PVID: %w", name, err)
	}
	_, err = c.ValidateFilteredBridge(name, vlan)
	return err
}

type policyApplyError struct {
	Bridge       string
	Member       string
	Stage        string
	Cause        error
	RollbackErr  error
	IsolationErr error
}

func (e *policyApplyError) Error() string {
	message := fmt.Sprintf("apply VLAN policy to %s/%s failed at %s: %v", e.Bridge, e.Member, e.Stage, e.Cause)
	if e.RollbackErr != nil {
		message += fmt.Sprintf("; rollback failed: %v", e.RollbackErr)
	}
	if e.IsolationErr != nil {
		message += fmt.Sprintf("; isolation failed: %v", e.IsolationErr)
	}
	return message
}

func (e *policyApplyError) Unwrap() error { return e.Cause }

func (c *controller) applyPolicy(bridge, member string, policy PortPolicy) error {
	normalized, err := Normalize(policy)
	if err != nil {
		return err
	}
	previous, err := c.inspectMember(bridge, member)
	if err != nil {
		return err
	}

	stage, applyErr := c.applyNormalizedPolicy(bridge, member, normalized)
	if applyErr == nil {
		return nil
	}

	rollbackErr := c.restoreMember(bridge, member, previous)
	var isolationErr error
	if rollbackErr != nil {
		isolationErr = c.isolateMember(bridge, member)
	}
	return &policyApplyError{
		Bridge: bridge, Member: member, Stage: stage, Cause: applyErr,
		RollbackErr: rollbackErr, IsolationErr: isolationErr,
	}
}

func (c *controller) applyNormalizedPolicy(bridge, member string, policy PortPolicy) (string, error) {
	operations := []struct {
		stage string
		run   func() error
	}{
		{"clear_tagged_vlans", func() error { return c.backend.SetMemberTaggedVLANs(bridge, member, nil) }},
		{"clear_pvid", func() error { return c.backend.SetMemberPVID(bridge, member, 0) }},
		{"disable_qinq", func() error { return c.backend.SetMemberQinQ(bridge, member, false) }},
		{"set_8021q", func() error { return c.backend.SetMemberVLANProtocol(bridge, member, vlanProtocol8021Q) }},
	}
	if policy.UntaggedVLAN != nil {
		operations = append(operations, struct {
			stage string
			run   func() error
		}{"set_pvid", func() error { return c.backend.SetMemberPVID(bridge, member, *policy.UntaggedVLAN) }})
	}
	operations = append(operations, struct {
		stage string
		run   func() error
	}{"set_tagged_vlans", func() error { return c.backend.SetMemberTaggedVLANs(bridge, member, policy.TaggedVLANs) }})

	for _, operation := range operations {
		if err := operation.run(); err != nil {
			return operation.stage, err
		}
	}
	state, err := c.inspectMember(bridge, member)
	if err != nil {
		return "verify", err
	}
	if err := verifyPolicyState(state, policy); err != nil {
		return "verify", err
	}
	return "", nil
}

func verifyPolicyState(state memberState, policy PortPolicy) error {
	expectedPVID := 0
	if policy.UntaggedVLAN != nil {
		expectedPVID = *policy.UntaggedVLAN
	}
	if state.PVID != expectedPVID || state.QinQ || state.VLANProtocol != vlanProtocol8021Q || !slices.Equal(state.TaggedVLANs, policy.TaggedVLANs) {
		return fmt.Errorf("%w: observed pvid=%d tagged=%v protocol=%#x qinq=%t", ErrMemberPolicyDrift, state.PVID, state.TaggedVLANs, state.VLANProtocol, state.QinQ)
	}
	return nil
}

func (c *controller) MemberPolicyMatches(bridge, member string, policy PortPolicy) (bool, error) {
	normalized, err := Normalize(policy)
	if err != nil {
		return false, err
	}
	matched := false
	err = c.withBridgeLock(bridge, func() error {
		state, err := c.inspectMember(bridge, member)
		if err != nil {
			return err
		}
		matched = verifyPolicyState(state, normalized) == nil
		return nil
	})
	return matched, err
}

func (c *controller) SetMemberPrivate(bridge, member string, enabled bool) error {
	return c.withBridgeLock(bridge, func() error {
		state, err := c.inspectMember(bridge, member)
		if err != nil {
			return err
		}
		if state.Private == enabled {
			return nil
		}
		if err := c.backend.SetMemberPrivate(bridge, member, enabled); err != nil {
			return fmt.Errorf("set bridge %q member %q private=%t: %w", bridge, member, enabled, err)
		}
		state, err = c.inspectMember(bridge, member)
		if err != nil {
			return err
		}
		if state.Private != enabled {
			return fmt.Errorf("%w: bridge %s member %s private=%t, expected %t", ErrMemberPrivateDrift, bridge, member, state.Private, enabled)
		}
		return nil
	})
}

func (c *controller) isolateMember(bridge, member string) error {
	return errors.Join(
		c.backend.SetMemberTaggedVLANs(bridge, member, nil),
		c.backend.SetMemberPVID(bridge, member, 0),
		c.backend.SetMemberQinQ(bridge, member, false),
		c.backend.SetMemberVLANProtocol(bridge, member, vlanProtocol8021Q),
	)
}

func (c *controller) isolateAndVerifyMember(bridge, member string) error {
	if err := c.isolateMember(bridge, member); err != nil {
		return fmt.Errorf("isolate bridge %q member %q: %w", bridge, member, err)
	}
	state, err := c.inspectMember(bridge, member)
	if err != nil {
		return err
	}
	if state.PVID != 0 || len(state.TaggedVLANs) != 0 || state.QinQ || state.VLANProtocol != vlanProtocol8021Q {
		return fmt.Errorf("verify isolated bridge %q member %q: %#v", bridge, member, state)
	}
	return nil
}

func (c *controller) restoreMember(bridge, member string, state memberState) error {
	if err := c.isolateMember(bridge, member); err != nil {
		return err
	}
	if err := c.backend.SetMemberVLANProtocol(bridge, member, state.VLANProtocol); err != nil {
		return err
	}
	if err := c.backend.SetMemberQinQ(bridge, member, state.QinQ); err != nil {
		return err
	}
	if err := c.backend.SetMemberPVID(bridge, member, state.PVID); err != nil {
		return err
	}
	if err := c.backend.SetMemberTaggedVLANs(bridge, member, state.TaggedVLANs); err != nil {
		return err
	}
	observed, err := c.inspectMember(bridge, member)
	if err != nil {
		return err
	}
	if observed.PVID != state.PVID || observed.QinQ != state.QinQ || observed.Private != state.Private ||
		observed.VLANProtocol != state.VLANProtocol || !slices.Equal(observed.TaggedVLANs, state.TaggedVLANs) {
		return fmt.Errorf("restored member state does not match snapshot")
	}
	return nil
}

func (c *controller) RemoveMember(bridge, member string) error {
	return c.withBridgeLock(bridge, func() error {
		isolateErr := c.isolateAndVerifyMember(bridge, member)
		removeErr := c.backend.RemoveMember(bridge, member)
		if err := errors.Join(isolateErr, removeErr); err != nil {
			return fmt.Errorf("remove bridge %q member %q: %w", bridge, member, err)
		}
		return nil
	})
}

func (c *controller) ConfigureMember(
	bridge, member string,
	expectedDefaultAccessVLAN *int,
	policy PortPolicy,
) error {
	normalized, err := Normalize(policy)
	if err != nil {
		return err
	}
	return c.withBridgeLock(bridge, func() error {
		if _, err := c.ValidateFilteredBridge(bridge, expectedDefaultAccessVLAN); err != nil {
			return err
		}

		added := false
		if err := c.backend.AddMember(bridge, member); err != nil {
			if !errors.Is(err, syscall.EEXIST) {
				return fmt.Errorf("add member %q to bridge %q: %w", member, bridge, err)
			}
		} else {
			added = true
		}
		if !added {
			state, err := c.inspectMember(bridge, member)
			if err != nil {
				return err
			}
			if verifyPolicyState(state, normalized) == nil {
				return nil
			}
			return c.applyPolicy(bridge, member, normalized)
		}
		if err := c.isolateAndVerifyMember(bridge, member); err != nil {
			return errors.Join(err, c.backend.RemoveMember(bridge, member))
		}
		if err := c.applyPolicy(bridge, member, normalized); err != nil {
			return errors.Join(err, c.backend.RemoveMember(bridge, member))
		}
		return nil
	})
}

func (c *controller) withBridgeLock(bridge string, operation func() error) error {
	value, _ := bridgeOperationLocks.LoadOrStore(bridge, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	return operation()
}
