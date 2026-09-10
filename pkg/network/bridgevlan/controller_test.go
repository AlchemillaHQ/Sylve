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
	"testing"
	"time"
)

type fakeBackend struct {
	bridge     BridgeState
	member     memberState
	memberUp   bool
	calls      []string
	callCounts map[string]int
	failAt     map[string]int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		bridge:     BridgeState{VLANFiltering: true},
		member:     memberState{VLANProtocol: vlanProtocol8021Q, TaggedVLANs: []int{}},
		memberUp:   true,
		callCounts: map[string]int{},
		failAt:     map[string]int{},
	}
}

func (f *fakeBackend) mutate(call string, apply func()) error {
	f.calls = append(f.calls, call)
	f.callCounts[call]++
	if f.failAt[call] == f.callCounts[call] {
		return fmt.Errorf("injected failure at %s", call)
	}
	apply()
	return nil
}

func (f *fakeBackend) BridgeState(string) (BridgeState, error) { return f.bridge, nil }
func (f *fakeBackend) SetVLANFiltering(_ string, enabled bool) error {
	return f.mutate(fmt.Sprintf("filter:%t", enabled), func() { f.bridge.VLANFiltering = enabled })
}
func (f *fakeBackend) SetDefaultPVID(_ string, pvid int) error {
	return f.mutate(fmt.Sprintf("default:%d", pvid), func() { f.bridge.DefaultPVID = pvid })
}
func (f *fakeBackend) SetDefaultQinQ(_ string, enabled bool) error {
	return f.mutate(fmt.Sprintf("default-qinq:%t", enabled), func() { f.bridge.DefaultQinQ = enabled })
}
func (f *fakeBackend) memberState(_, _ string) (memberState, error) {
	if !f.memberUp {
		return memberState{}, syscall.ENOENT
	}
	state := f.member
	state.TaggedVLANs = append([]int(nil), state.TaggedVLANs...)
	return state, nil
}
func (f *fakeBackend) AddMember(_, _ string) error {
	if f.memberUp {
		return syscall.EEXIST
	}
	return f.mutate("add", func() { f.memberUp = true })
}
func (f *fakeBackend) RemoveMember(_, _ string) error {
	return f.mutate("remove", func() { f.memberUp = false })
}
func (f *fakeBackend) SetMemberPVID(_, _ string, pvid int) error {
	return f.mutate(fmt.Sprintf("pvid:%d", pvid), func() { f.member.PVID = pvid })
}
func (f *fakeBackend) SetMemberTaggedVLANs(_, _ string, vlans []int) error {
	copy := append([]int(nil), vlans...)
	return f.mutate(fmt.Sprintf("tagged:%v", copy), func() { f.member.TaggedVLANs = copy })
}
func (f *fakeBackend) SetMemberVLANProtocol(_, _ string, protocol uint16) error {
	return f.mutate(fmt.Sprintf("protocol:%#x", protocol), func() { f.member.VLANProtocol = protocol })
}
func (f *fakeBackend) SetMemberQinQ(_, _ string, enabled bool) error {
	return f.mutate(fmt.Sprintf("qinq:%t", enabled), func() { f.member.QinQ = enabled })
}
func (f *fakeBackend) SetMemberPrivate(_, _ string, enabled bool) error {
	return f.mutate(fmt.Sprintf("private:%t", enabled), func() { f.member.Private = enabled })
}

func TestSetMemberPrivateIsIdempotentAndVerified(t *testing.T) {
	backend := newFakeBackend()
	controller := newController(backend)

	if err := controller.SetMemberPrivate("bridge0", "epair0a", true); err != nil {
		t.Fatalf("set private: %v", err)
	}
	if !backend.member.Private || !slices.Equal(backend.calls, []string{"private:true"}) {
		t.Fatalf("private state = %t, calls = %v", backend.member.Private, backend.calls)
	}
	if err := controller.SetMemberPrivate("bridge0", "epair0a", true); err != nil {
		t.Fatalf("repeat private: %v", err)
	}
	if !slices.Equal(backend.calls, []string{"private:true"}) {
		t.Fatalf("idempotent call mutated member: %v", backend.calls)
	}
}

func TestApplyPolicyUsesFailClosedOperationOrder(t *testing.T) {
	backend := newFakeBackend()
	backend.member = memberState{
		PVID: 99, TaggedVLANs: []int{100}, VLANProtocol: 0x88a8, QinQ: true, Private: true,
	}
	controller := newController(backend)
	native := 10
	policy := PortPolicy{Mode: ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{30, 20}}

	if err := controller.applyPolicy("bridge0", "epair0a", policy); err != nil {
		t.Fatalf("apply policy: %v", err)
	}
	wantCalls := []string{
		"tagged:[]", "pvid:0", "qinq:false", "protocol:0x8100",
		"pvid:10", "tagged:[20 30]",
	}
	if !slices.Equal(backend.calls, wantCalls) {
		t.Fatalf("operation order = %v, want %v", backend.calls, wantCalls)
	}
	if err := verifyPolicyState(backend.member, PortPolicy{
		Mode: ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{20, 30},
	}); err != nil {
		t.Fatalf("final policy: %v", err)
	}
	if !backend.member.Private {
		t.Fatal("VLAN policy reconciliation cleared private-port isolation")
	}
}

func TestApplyPolicyRestoresExactSnapshotAfterFailure(t *testing.T) {
	backend := newFakeBackend()
	backend.member = memberState{
		PVID: 7, TaggedVLANs: []int{8, 9}, VLANProtocol: vlanProtocol8021Q,
	}
	before := backend.member
	backend.failAt["tagged:[20]"] = 1
	controller := newController(backend)
	native := 10

	err := controller.applyPolicy("bridge0", "epair0a", PortPolicy{
		Mode: ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{20},
	})
	var applyErr *policyApplyError
	if !errors.As(err, &applyErr) || applyErr.Stage != "set_tagged_vlans" {
		t.Fatalf("expected tagged-set policyApplyError, got %v", err)
	}
	if applyErr.RollbackErr != nil || applyErr.IsolationErr != nil {
		t.Fatalf("unexpected recovery errors: %#v", applyErr)
	}
	if backend.member.PVID != before.PVID || backend.member.QinQ != before.QinQ ||
		backend.member.VLANProtocol != before.VLANProtocol ||
		!slices.Equal(backend.member.TaggedVLANs, before.TaggedVLANs) {
		t.Fatalf("member state = %#v, want snapshot %#v", backend.member, before)
	}
}

func TestApplyPolicyIsolatesMemberWhenRollbackFails(t *testing.T) {
	backend := newFakeBackend()
	backend.member = memberState{
		PVID: 7, TaggedVLANs: []int{8}, VLANProtocol: vlanProtocol8021Q,
	}
	backend.failAt["tagged:[20]"] = 1
	backend.failAt["protocol:0x8100"] = 2
	controller := newController(backend)
	native := 10

	err := controller.applyPolicy("bridge0", "epair0a", PortPolicy{
		Mode: ModeTrunk, UntaggedVLAN: &native, TaggedVLANs: []int{20},
	})
	var applyErr *policyApplyError
	if !errors.As(err, &applyErr) || applyErr.RollbackErr == nil {
		t.Fatalf("expected rollback failure, got %v", err)
	}
	if applyErr.IsolationErr != nil {
		t.Fatalf("final isolation failed: %v", applyErr.IsolationErr)
	}
	if backend.member.PVID != 0 || len(backend.member.TaggedVLANs) != 0 ||
		backend.member.QinQ || backend.member.VLANProtocol != vlanProtocol8021Q {
		t.Fatalf("member was not left isolated: %#v", backend.member)
	}
}

func TestConfigureMemberValidatesBridgeBeforeAttachment(t *testing.T) {
	backend := newFakeBackend()
	backend.bridge.VLANFiltering = false
	backend.memberUp = false
	controller := newController(backend)
	access := 10

	err := controller.ConfigureMember("bridge0", "epair0a", nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &access,
	})
	if !errors.Is(err, ErrBridgeNotFiltered) {
		t.Fatalf("expected bridge validation failure, got %v", err)
	}
	if len(backend.calls) != 0 || backend.memberUp {
		t.Fatalf("member changed before bridge validation: calls=%v up=%t", backend.calls, backend.memberUp)
	}
}

func TestConfigureMemberLeavesMatchingExistingMemberUntouched(t *testing.T) {
	backend := newFakeBackend()
	access := 10
	backend.member = memberState{
		PVID: access, TaggedVLANs: []int{}, VLANProtocol: vlanProtocol8021Q,
	}
	controller := newController(backend)

	if err := controller.ConfigureMember("bridge0", "epair0a", nil, PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &access,
	}); err != nil {
		t.Fatalf("configure matching member: %v", err)
	}
	if len(backend.calls) != 0 {
		t.Fatalf("matching member was mutated: %v", backend.calls)
	}
}

func TestMemberPolicyMatchesIsReadOnly(t *testing.T) {
	backend := newFakeBackend()
	access := 10
	backend.member = memberState{
		PVID: access, TaggedVLANs: []int{}, VLANProtocol: vlanProtocol8021Q,
	}
	controller := newController(backend)

	matched, err := controller.MemberPolicyMatches("bridge0", "epair0a", PortPolicy{
		Mode: ModeAccess, UntaggedVLAN: &access,
	})
	if err != nil {
		t.Fatalf("inspect matching member: %v", err)
	}
	if !matched {
		t.Fatal("matching member reported drift")
	}
	if len(backend.calls) != 0 {
		t.Fatalf("member inspection mutated state: %v", backend.calls)
	}
}

func TestBridgeOperationsSerializePerBridgeAcrossControllers(t *testing.T) {
	first, second := newController(nil), newController(nil)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(release) })
	defer releaseFirst()
	firstDone := make(chan error, 1)
	wantErr := errors.New("operation failed")
	go func() {
		firstDone <- first.withBridgeLock(t.Name(), func() error {
			close(entered)
			<-release
			return wantErr
		})
	}()
	<-entered

	sameBridgeDone, otherBridgeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		sameBridgeDone <- second.withBridgeLock(t.Name(), func() error { return nil })
	}()
	go func() {
		otherBridgeDone <- second.withBridgeLock(t.Name()+"-other", func() error { return nil })
	}()
	select {
	case err := <-otherBridgeDone:
		if err != nil {
			t.Fatalf("unrelated bridge operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("an unrelated bridge was blocked")
	}
	select {
	case <-sameBridgeDone:
		t.Fatal("same-bridge operations overlapped")
	case <-time.After(25 * time.Millisecond):
	}

	releaseFirst()
	if err := <-firstDone; !errors.Is(err, wantErr) {
		t.Fatalf("operation error = %v, want %v", err, wantErr)
	}
	select {
	case err := <-sameBridgeDone:
		if err != nil {
			t.Fatalf("next bridge operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge remained locked after a failed operation")
	}
}
