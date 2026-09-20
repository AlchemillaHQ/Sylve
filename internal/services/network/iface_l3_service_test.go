// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
)

type fakeHostInterfaceState struct {
	object *iface.Interface
	fail   func(args []string) error
}

func newFakeHostInterface(t *testing.T, name string, ether string, mtu int) *fakeHostInterfaceState {
	t.Helper()

	state := &fakeHostInterfaceState{
		object: &iface.Interface{
			Name:   name,
			Ether:  ether,
			Driver: "em",
			MTU:    mtu,
			Flags:  iface.Flags{Desc: []string{"UP"}},
			ND6:    iface.ND6{Flags: iface.Flags{Raw: hostInterfaceL3ND6AcceptRTAdv | hostInterfaceL3ND6AutoLinkLocal | 0x01}},
		},
	}

	originalGet := syncIfaceGet
	originalRun := syncRunCommand
	t.Cleanup(func() {
		syncIfaceGet = originalGet
		syncRunCommand = originalRun
	})

	syncIfaceGet = func(requested string) (*iface.Interface, error) {
		if requested != name {
			return nil, &interfaceMissingError{name: requested}
		}
		return state.object, nil
	}
	syncRunCommand = func(command string, args ...string) (string, error) {
		if state.fail != nil {
			if err := state.fail(args); err != nil {
				return "", err
			}
		}
		if err := state.handle(args); err != nil {
			return "", err
		}
		return "", nil
	}

	return state
}

func (s *fakeHostInterfaceState) handle(args []string) error {
	if len(args) < 2 {
		return nil
	}

	switch {
	case args[1] == "inet":
		return s.handleIPv4(args)
	case args[1] == "inet6":
		return s.handleIPv6(args)
	case args[1] == "mtu" && len(args) >= 3:
		value, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		s.object.MTU = value
	case args[1] == "metric" && len(args) >= 3:
		value, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		s.object.Metric = value
	case args[1] == "up":
		s.object.Flags.Desc = []string{"UP"}
	case args[1] == "down":
		s.object.Flags.Desc = nil
	}
	return nil
}

func (s *fakeHostInterfaceState) handleIPv4(args []string) error {
	if len(args) >= 4 && args[3] == "delete" {
		for index, address := range s.object.IPv4 {
			if address.IP.String() == args[2] {
				s.object.IPv4 = append(s.object.IPv4[:index], s.object.IPv4[index+1:]...)
				return nil
			}
		}
		return nil
	}
	if len(args) < 3 {
		return nil
	}

	prefix, err := netip.ParsePrefix(args[2])
	if err != nil {
		return err
	}
	mask := net.CIDRMask(prefix.Bits(), 32)
	s.object.IPv4 = append(s.object.IPv4, iface.IPv4{
		IP:      prefix.Addr().AsSlice(),
		Netmask: net.IP(mask).String(),
	})
	return nil
}

func (s *fakeHostInterfaceState) handleIPv6(args []string) error {
	if len(args) >= 4 && args[3] == "delete" {
		for index, address := range s.object.IPv6 {
			if address.IP.String() == strings.TrimSuffix(args[2], "%"+s.object.Name) {
				s.object.IPv6 = append(s.object.IPv6[:index], s.object.IPv6[index+1:]...)
				return nil
			}
		}
		return nil
	}

	if s.applyND6Flag(args) {
		return nil
	}

	if len(args) < 3 {
		return nil
	}
	prefix, err := netip.ParsePrefix(args[2])
	if err != nil {
		return err
	}
	s.object.IPv6 = append(s.object.IPv6, iface.IPv6{
		IP:           prefix.Addr().AsSlice(),
		PrefixLength: prefix.Bits(),
	})
	return nil
}

func (s *fakeHostInterfaceState) applyND6Flag(args []string) bool {
	handled := false
	update := func(set string, clear string, mask uint32) {
		if containsString(args, set) {
			s.object.ND6.Raw |= mask
			handled = true
		} else if containsString(args, clear) {
			s.object.ND6.Raw &^= mask
			handled = true
		}
	}

	update("no_radr", "-no_radr", hostInterfaceL3ND6NoRADR)
	update("accept_rtadv", "-accept_rtadv", hostInterfaceL3ND6AcceptRTAdv)
	update("ifdisabled", "-ifdisabled", hostInterfaceL3ND6IfDisabled)
	update("auto_linklocal", "-auto_linklocal", hostInterfaceL3ND6AutoLinkLocal)

	return handled
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stubHostInterfaceL3Clock(t *testing.T, start time.Time) *time.Time {
	t.Helper()

	current := start
	original := hostInterfaceL3Now
	t.Cleanup(func() {
		hostInterfaceL3Now = original
	})
	hostInterfaceL3Now = func() time.Time {
		return current
	}
	return &current
}

func standardHostInterfaceRequest(addresses ...string) networkServiceInterfaces.HostInterfaceL3UpdateRequest {
	request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{}
	for _, address := range addresses {
		request.Addresses = append(request.Addresses, networkServiceInterfaces.HostInterfaceL3AddressInput{
			Address: address,
		})
	}
	return request
}

func TestSaveAndConfirmHostInterfaceL3PromotesRow(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if entry.Phase != networkModels.PendingApplyPhaseApplied || entry.Kind != networkModels.PendingApplyKindInterface {
		t.Fatalf("unexpected pending entry %+v", entry)
	}

	var pendingCount int64
	if err := db.Model(&networkModels.PendingApply{}).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("expected one pending row, got %d", pendingCount)
	}
	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Count(&rowCount).Error; err != nil {
		t.Fatalf("count host rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("row must not be promoted before confirmation, got %d", rowCount)
	}

	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}

	var row networkModels.HostInterfaceL3
	if err := db.Preload("Addresses").Where("interface = ?", "em0").First(&row).Error; err != nil {
		t.Fatalf("load promoted row: %v", err)
	}
	if row.Revision != 1 || row.IPv6Mode != networkModels.HostInterfaceL3IPv6ModeInherit {
		t.Fatalf("unexpected promoted row %+v", row)
	}
	if len(row.Addresses) != 1 || row.Addresses[0].Address != "10.0.0.5" || row.Addresses[0].PrefixLength != 24 {
		t.Fatalf("unexpected promoted addresses %+v", row.Addresses)
	}
	if len(row.AppliedState.Addresses) != 1 || row.AppliedState.Addresses[0].Address != "10.0.0.5/24" {
		t.Fatalf("unexpected applied state %+v", row.AppliedState)
	}

	if err := db.Model(&networkModels.PendingApply{}).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows after confirm: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("pending rows must be removed on confirm, got %d", pendingCount)
	}
}

func TestSaveHostInterfaceL3RejectsRevisionMismatchAndPending(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em0", Revision: 3}).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	_, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_revision_mismatch" {
		t.Fatalf("expected revision mismatch, got %v", err)
	}

	request := standardHostInterfaceRequest("10.0.0.5/24")
	request.ExpectedRevision = 3
	entry, err := svc.SaveHostInterfaceL3("em0", request)
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}

	_, err = svc.SaveHostInterfaceL3("em0", request)
	if err == nil || HostInterfaceL3ErrorCode(err) != "host_interface_l3_pending_conflict" {
		t.Fatalf("expected pending conflict, got %v", err)
	}

	if err := svc.RevertHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("RevertHostInterfaceL3: %v", err)
	}
}

func TestExpireHostInterfaceL3RevertsPendingOperation(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	now := stubHostInterfaceL3Clock(t, time.Now())

	_, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if len(state.object.IPv4) != 1 {
		t.Fatalf("expected the address to be applied, got %+v", state.object.IPv4)
	}

	expired := now.Add(HostInterfaceL3ConfirmationWindow + time.Second)
	if err := svc.ExpireHostInterfaceL3Pending(expired); err != nil {
		t.Fatalf("ExpireHostInterfaceL3Pending: %v", err)
	}

	if len(state.object.IPv4) != 0 {
		t.Fatalf("expected the address to be reverted, got %+v", state.object.IPv4)
	}
	var pendingCount int64
	if err := db.Model(&networkModels.PendingApply{}).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("expected the expired pending row to be removed, got %d", pendingCount)
	}
	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Count(&rowCount).Error; err != nil {
		t.Fatalf("count host rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected no promoted row, got %d", rowCount)
	}
}

func TestExpireHostInterfaceL3RestoresPreSaveRuntimeDrift(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	now := stubHostInterfaceL3Clock(t, time.Now())

	managedMTU := uint(9000)
	request := standardHostInterfaceRequest("10.0.0.5/24")
	request.MTU = &managedMTU
	entry, err := svc.SaveHostInterfaceL3("em0", request)
	if err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("initial confirm: %v", err)
	}

	state.object.MTU = 1500
	state.object.IPv4 = nil
	request.ExpectedRevision = 1
	entry, err = svc.SaveHostInterfaceL3("em0", request)
	if err != nil {
		t.Fatalf("repairing save: %v", err)
	}
	if state.object.MTU != 9000 || len(state.object.IPv4) != 1 {
		t.Fatalf("expected drift to be repaired before confirmation, got MTU %d and %+v", state.object.MTU, state.object.IPv4)
	}

	if err := svc.ExpireHostInterfaceL3Pending(now.Add(HostInterfaceL3ConfirmationWindow + time.Second)); err != nil {
		t.Fatalf("expire repaired save: %v", err)
	}
	if state.object.MTU != 1500 || len(state.object.IPv4) != 0 {
		t.Fatalf("expected exact pre-save runtime state, got MTU %d and %+v", state.object.MTU, state.object.IPv4)
	}
}

func TestExpireHostInterfaceL3DeleteRestoresPreDeletePrefix(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	now := stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	state.object.IPv4 = []iface.IPv4{{
		IP: mustParseIP(t, "10.0.0.5"), Netmask: "255.255.255.255",
	}}

	if _, err := svc.DeleteHostInterfaceL3("em0", 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(state.object.IPv4) != 0 {
		t.Fatalf("expected delete to remove the drifted address, got %+v", state.object.IPv4)
	}
	if err := svc.ExpireHostInterfaceL3Pending(now.Add(HostInterfaceL3ConfirmationWindow + time.Second)); err != nil {
		t.Fatalf("expire delete: %v", err)
	}
	if len(state.object.IPv4) != 1 {
		t.Fatalf("expected the pre-delete address to be restored, got %+v", state.object.IPv4)
	}
	prefix, ok := interfaceIPv4Prefix(state.object.IPv4[0])
	if !ok || prefix.String() != "10.0.0.5/32" {
		t.Fatalf("expected exact pre-delete /32, got %+v", state.object.IPv4[0])
	}
}

func TestRecoverHostInterfaceL3RollsBackPreparedOperation(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)

	initialIPv4 := iface.IPv4{IP: mustParseIP(t, "10.0.0.5"), Netmask: "255.255.255.0"}
	live, _ := syncIfaceGet("em0")
	live.IPv4 = []iface.IPv4{initialIPv4}

	pending := networkModels.PendingApply{
		ID:                     "prepared-op",
		Interface:              "em0",
		Kind:                   networkModels.PendingApplyKindInterface,
		Phase:                  networkModels.PendingApplyPhasePrepared,
		RuntimeRestoreRequired: true,
		CandidatePayload: networkModels.HostInterfaceL3Spec{
			Addresses: []networkModels.HostInterfaceL3AddressSpec{
				{Family: "inet", Address: "10.0.0.6", PrefixLength: 24},
			},
		},
		CandidateAppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{
				{Family: "inet", Address: "10.0.0.6/24"},
			},
		},
		RuntimeSnapshot: networkModels.HostInterfaceL3Baseline{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{Family: "inet", Address: "10.0.0.5/24"}},
		},
		CreatedAt: time.Now(),
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	live.IPv4 = append(live.IPv4, iface.IPv4{IP: mustParseIP(t, "10.0.0.6"), Netmask: "255.255.255.0"})

	if err := svc.RecoverHostInterfaceL3(); err != nil {
		t.Fatalf("RecoverHostInterfaceL3: %v", err)
	}

	foundBaseline := false
	for _, address := range live.IPv4 {
		if address.IP.String() == "10.0.0.6" {
			t.Fatalf("expected the unconfirmed address to be removed, got %+v", live.IPv4)
		}
		if address.IP.String() == "10.0.0.5" {
			foundBaseline = true
		}
	}
	if !foundBaseline {
		t.Fatalf("expected the baseline address to remain, got %+v", live.IPv4)
	}
	var pendingCount int64
	if err := db.Model(&networkModels.PendingApply{}).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("expected the recovered operation to be discarded, got %d", pendingCount)
	}
}

func TestDeleteAndConfirmHostInterfaceL3RemovesRow(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}

	deleteEntry, err := svc.DeleteHostInterfaceL3("em0", 1)
	if err != nil {
		t.Fatalf("DeleteHostInterfaceL3: %v", err)
	}
	if len(state.object.IPv4) != 0 {
		t.Fatalf("expected the address to be removed during delete, got %+v", state.object.IPv4)
	}

	if err := svc.ConfirmHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3(delete): %v", err)
	}

	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Count(&rowCount).Error; err != nil {
		t.Fatalf("count host rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected the row to be deleted, got %d", rowCount)
	}
}

func TestReapplyHostInterfaceL3RestoresMissingAddress(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}

	state.object.IPv4 = nil

	if err := svc.ReapplyHostInterfaceL3("em0"); err != nil {
		t.Fatalf("ReapplyHostInterfaceL3: %v", err)
	}
	if len(state.object.IPv4) != 1 || state.object.IPv4[0].IP.String() != "10.0.0.5" {
		t.Fatalf("expected the address to be restored, got %+v", state.object.IPv4)
	}

	var row networkModels.HostInterfaceL3
	if err := db.Preload("Addresses").Where("interface = ?", "em0").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Revision != 1 {
		t.Fatalf("reapply must not bump the revision, got %d", row.Revision)
	}
}

func TestReapplyHostInterfaceL3RejectsTentativeKeptIPv6(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("2001:db8::5/64"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	state.object.IPv6[0].Tentative = true
	originalWait := hostInterfaceL3DADWait
	hostInterfaceL3DADWait = func(time.Duration) {}
	t.Cleanup(func() { hostInterfaceL3DADWait = originalWait })

	err = svc.ReapplyHostInterfaceL3("em0")
	if err == nil || !strings.Contains(err.Error(), "tentative") {
		t.Fatalf("expected kept IPv6 DAD failure, got %v", err)
	}
}

func TestCheckStandardSwitchPortsForHostInterfaceL3(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em0"}).Error; err != nil {
		t.Fatalf("seed host row: %v", err)
	}
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em1.100", VLANParent: "em1", VLANTag: 100}).Error; err != nil {
		t.Fatalf("seed VLAN host row: %v", err)
	}

	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em0"}); err == nil {
		t.Fatal("expected the port with Host IP configuration to be refused")
	}
	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em1"}); err == nil {
		t.Fatal("expected a port with a Host IP VLAN child to be refused")
	}
	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em2"}); err != nil {
		t.Fatalf("unrelated ports must pass: %v", err)
	}
	pending := networkModels.PendingApply{
		ID:        "pending-host-ip",
		Interface: "em2.100",
		Kind:      networkModels.PendingApplyKindInterface,
		Phase:     networkModels.PendingApplyPhaseApplied,
		RuntimeSnapshot: networkModels.HostInterfaceL3Baseline{
			VLANParent: "em2",
			VLANTag:    100,
		},
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("seed pending Host IP operation: %v", err)
	}
	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em2"}); err == nil {
		t.Fatal("expected a port reserved as a pending VLAN parent to be refused")
	}
}

func TestConfirmHostInterfaceL3RevalidatesMembership(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := db.Create(&networkModels.NetworkPort{Name: "em0", SwitchID: 1}).Error; err != nil {
		t.Fatalf("seed concurrent switch membership: %v", err)
	}

	err = svc.ConfirmHostInterfaceL3(entry.ID)
	if err == nil || HostInterfaceL3ErrorCode(err) != networkServiceInterfaces.HostInterfaceL3ConflictStandardSwitchPort {
		t.Fatalf("expected confirmation to refuse the new membership, got %v", err)
	}
}

func TestConfirmHostInterfaceL3RefusesVLANRetag(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0.100", "aa:bb:cc:dd:ee:ff", 1500)
	state.object.VLANParent = "em0"
	state.object.VLANTag = 100
	childGet := syncIfaceGet
	syncIfaceGet = func(name string) (*iface.Interface, error) {
		if name == "em0" {
			return &iface.Interface{Name: "em0", Ether: "aa:bb:cc:dd:ee:ff", Driver: "em", MTU: 1500}, nil
		}
		return childGet(name)
	}
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0.100", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	state.object.VLANTag = 200

	err = svc.ConfirmHostInterfaceL3(entry.ID)
	if err == nil || HostInterfaceL3ErrorCode(err) != networkServiceInterfaces.HostInterfaceL3ConflictVLANIdentity {
		t.Fatalf("expected confirmation to refuse a retagged VLAN, got %v", err)
	}
}

func TestDeleteHostInterfaceL3KeepsPendingWhenCompensationFails(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24", "10.0.0.6/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	state.fail = func(args []string) error {
		if len(args) >= 4 && args[2] == "10.0.0.6" && args[3] == "delete" {
			return errors.New("second delete failed")
		}
		if len(args) >= 3 && args[2] == "10.0.0.5/24" {
			return errors.New("restore failed")
		}
		return nil
	}

	if _, err := svc.DeleteHostInterfaceL3("em0", 1); err == nil {
		t.Fatal("expected delete and compensation to fail")
	}
	var count int64
	if err := db.Model(&networkModels.PendingApply{}).Count(&count).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the pending record to remain, got %d", count)
	}
}

func TestReapplyHostInterfaceL3RejectsPrefixOwnerDrift(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state.object.IPv4 = []iface.IPv4{
		{IP: mustParseIP(t, "10.0.0.5"), Netmask: "255.255.255.0"},
		{IP: mustParseIP(t, "10.0.0.1"), Netmask: "255.255.255.0"},
	}
	row := networkModels.HostInterfaceL3{
		Interface: "em0", IdentityMAC: state.object.Ether, Revision: 1,
		AppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{{
				Family: "inet", Address: "10.0.0.5/24",
			}},
		},
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if err := db.Create(&networkModels.HostInterfaceL3Address{
		InterfaceL3ID: row.ID, Family: "inet", Address: "10.0.0.5", PrefixLength: 24,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	err := svc.ReapplyHostInterfaceL3("em0")
	if err == nil || HostInterfaceL3ErrorCode(err) != networkServiceInterfaces.HostInterfaceL3ConflictPrefixOwnerChanged {
		t.Fatalf("expected prefix-owner conflict, got %v", err)
	}
}

func TestHostInterfaceL3IdentityCapturedAndMismatchDeleteSkipsRuntime(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}

	state.object.Ether = "bb:bb:bb:bb:bb:bb"
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err == nil ||
		HostInterfaceL3ErrorCode(err) != "host_interface_l3_identity_mismatch" {
		t.Fatalf("expected confirmation to refuse replacement hardware, got %v", err)
	}
	var pending networkModels.PendingApply
	if err := db.Where("id = ?", entry.ID).First(&pending).Error; err != nil {
		t.Fatalf("load refused pending operation: %v", err)
	}
	if pending.Phase != networkModels.PendingApplyPhaseApplied {
		t.Fatalf("expected refused confirmation to remain revertible, got %q", pending.Phase)
	}
	state.object.Ether = "aa:bb:cc:dd:ee:ff"
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}

	var row networkModels.HostInterfaceL3
	if err := db.Where("interface = ?", "em0").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.IdentityMAC != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("expected the pre-apply MAC to be stored, got %q", row.IdentityMAC)
	}

	state.object.Ether = "bb:bb:bb:bb:bb:bb"
	addressesBeforeDelete := len(state.object.IPv4)
	deleteEntry, err := svc.DeleteHostInterfaceL3("em0", 1)
	if err != nil {
		t.Fatalf("DeleteHostInterfaceL3: %v", err)
	}
	if len(state.object.IPv4) != addressesBeforeDelete {
		t.Fatalf("replacement hardware was mutated during delete: %+v", state.object.IPv4)
	}
	if err := svc.ConfirmHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3(delete): %v", err)
	}
	var count int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Where("interface = ?", "em0").Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the Host IP row to be removed, got %d", count)
	}
}

func TestHostInterfaceL3BaselineSurvivesSecondEdit(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	state.object.Flags.Desc = nil
	stubHostInterfaceL3Clock(t, time.Now())

	mtu := uint(9000)
	req := standardHostInterfaceRequest("10.0.0.5/24")
	req.MTU = &mtu

	entry, err := svc.SaveHostInterfaceL3("em0", req)
	if err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm1: %v", err)
	}

	req.ExpectedRevision = 1
	entry2, err := svc.SaveHostInterfaceL3("em0", req)
	if err != nil {
		t.Fatalf("save2: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry2.ID); err != nil {
		t.Fatalf("confirm2: %v", err)
	}

	var row networkModels.HostInterfaceL3
	if err := db.Where("interface = ?", "em0").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.AdoptionBaseline.MTU == nil || *row.AdoptionBaseline.MTU != 1500 {
		t.Fatalf("expected the adoption baseline JSON to stay 1500, got %+v", row.AdoptionBaseline.MTU)
	}
	if row.AppliedState.Up == nil || !*row.AppliedState.Up {
		t.Fatalf("expected link-state ownership to survive the second edit, got %+v", row.AppliedState.Up)
	}
	deleteEntry, err := svc.DeleteHostInterfaceL3("em0", row.Revision)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if interfaceIsUp(state.object) {
		t.Fatal("expected delete to restore the original down state")
	}
	if err := svc.ConfirmHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
}

func TestRevertConfigOnlyHostInterfaceL3DeleteDoesNotTouchReplacement(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	state.object.Ether = "bb:bb:bb:bb:bb:bb"
	addressesBeforeDelete := len(state.object.IPv4)
	deleteEntry, err := svc.DeleteHostInterfaceL3("em0", 1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.RevertHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("revert config-only delete: %v", err)
	}
	if len(state.object.IPv4) != addressesBeforeDelete {
		t.Fatalf("replacement hardware was mutated: %+v", state.object.IPv4)
	}
	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Where("interface = ?", "em0").Count(&rowCount).Error; err != nil {
		t.Fatalf("count Host IP rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected reverted delete to keep the Host IP row, got %d", rowCount)
	}
}
