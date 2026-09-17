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
	update := func(present bool, set string, clear string, mask uint32) {
		if containsString(args, set) {
			s.object.ND6.Raw |= mask
			handled = true
		} else if containsString(args, clear) {
			s.object.ND6.Raw &^= mask
			handled = true
		}
	}

	update(true, "no_radr", "-no_radr", hostInterfaceL3ND6NoRADR)
	update(true, "accept_rtadv", "-accept_rtadv", hostInterfaceL3ND6AcceptRTAdv)
	update(true, "ifdisabled", "-ifdisabled", hostInterfaceL3ND6IfDisabled)
	update(true, "auto_linklocal", "-auto_linklocal", hostInterfaceL3ND6AutoLinkLocal)

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

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
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
	_ = entry
}

func TestRecoverHostInterfaceL3RevertsPreparedOperation(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	_ = state

	initialIPv4 := iface.IPv4{IP: mustParseIP(t, "10.0.0.5"), Netmask: "255.255.255.0"}
	live, _ := syncIfaceGet("em0")
	live.IPv4 = []iface.IPv4{initialIPv4}

	pending := networkModels.PendingApply{
		ID:    "prepared-op",
		Kind:  networkModels.PendingApplyKindInterface,
		Phase: networkModels.PendingApplyPhasePrepared,
		CandidatePayload: networkModels.HostInterfaceL3Spec{
			Addresses: []networkModels.HostInterfaceL3AddressSpec{
				{Family: "inet", Address: "10.0.0.6", PrefixLength: 24},
			},
		},
		CandidateAppliedState: networkModels.HostInterfaceL3AppliedState{
			Addresses: []networkModels.HostInterfaceL3AppliedAddress{
				{Family: "inet", Address: "10.0.0.5/24", PrefixLength: 24},
				{Family: "inet", Address: "10.0.0.6/24", PrefixLength: 24, Alias: true},
			},
		},
		RuntimeSnapshot: networkModels.HostInterfaceL3Baseline{},
		CreatedAt:       time.Now(),
	}
	target := networkModels.PendingApplyTarget{
		PendingID: "prepared-op", TargetKind: "interface", TargetID: "em0",
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	live.IPv4 = append(live.IPv4, iface.IPv4{IP: mustParseIP(t, "10.0.0.6"), Netmask: "255.255.255.0"})

	if err := svc.RecoverHostInterfaceL3(); err != nil {
		t.Fatalf("RecoverHostInterfaceL3: %v", err)
	}

	for _, address := range live.IPv4 {
		if address.IP.String() == "10.0.0.6" {
			t.Fatalf("expected the prepared address to be reverted, got %+v", live.IPv4)
		}
	}
	var pendingCount int64
	if err := db.Model(&networkModels.PendingApply{}).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("expected the prepared operation to be discarded, got %d", pendingCount)
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

func TestCheckStandardSwitchPortsForHostInterfaceL3(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	if err := db.Create(&networkModels.HostInterfaceL3{Interface: "em0"}).Error; err != nil {
		t.Fatalf("seed host row: %v", err)
	}

	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em0"}, nil); err == nil {
		t.Fatal("expected the port with Host IP configuration to be refused")
	}
	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em0"}, []string{"em0"}); err != nil {
		t.Fatalf("unchanged ports must not be refused: %v", err)
	}
	if err := svc.checkStandardSwitchPortsForHostInterfaceL3([]string{"em1"}, nil); err != nil {
		t.Fatalf("unrelated ports must pass: %v", err)
	}
}

func TestHostInterfaceL3IdentityCapturedBeforeApplyAndEnforcedOnDelete(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}

	// Hardware swapped before confirmation: the captured identity must win.
	state.object.Ether = "bb:bb:bb:bb:bb:bb"
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

	if _, err := svc.DeleteHostInterfaceL3("em0", 1); err == nil ||
		HostInterfaceL3ErrorCode(err) != "host_interface_l3_identity_mismatch" {
		t.Fatalf("expected delete to refuse replacement hardware, got %v", err)
	}
}

func TestHostInterfaceL3AliasPromotionThroughSaveFlow(t *testing.T) {
	svc, _ := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24", "10.0.0.6/24"))
	if err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("confirm1: %v", err)
	}

	// Drop the /24 owner so the former /32 alias is promoted to owner.
	req := standardHostInterfaceRequest("10.0.0.6/24")
	req.ExpectedRevision = 1
	entry2, err := svc.SaveHostInterfaceL3("em0", req)
	if err != nil {
		t.Fatalf("save2 (alias promotion): %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(entry2.ID); err != nil {
		t.Fatalf("confirm2: %v", err)
	}
}

func TestHostInterfaceL3RevertKeepsPendingRecordOnFailure(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	state := newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
	stubHostInterfaceL3Clock(t, time.Now())

	entry, err := svc.SaveHostInterfaceL3("em0", standardHostInterfaceRequest("10.0.0.5/24"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	state.fail = func(args []string) error {
		if len(args) >= 4 && args[3] == "delete" {
			return errors.New("ifconfig delete failed")
		}
		return nil
	}

	if err := svc.RevertHostInterfaceL3(entry.ID); err == nil {
		t.Fatal("expected the revert to fail")
	}

	var pendingCount int64
	if err := db.Model(&networkModels.PendingApply{}).Where("id = ?", entry.ID).Count(&pendingCount).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("expected the pending record to be retained for retry, got %d", pendingCount)
	}
}

func TestHostInterfaceL3BaselineSurvivesSecondEdit(t *testing.T) {
	svc, db := hostInterfaceL3TestDB(t)
	stubHostInterfaceL3Interfaces(t, []*iface.Interface{})
	_ = newFakeHostInterface(t, "em0", "aa:bb:cc:dd:ee:ff", 1500)
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
	if row.MTUBaseline == nil || *row.MTUBaseline != 1500 {
		t.Fatalf("expected the adoption baseline (1500) to be preserved, got %+v", row.MTUBaseline)
	}
	if row.AdoptionBaseline.MTU == nil || *row.AdoptionBaseline.MTU != 1500 {
		t.Fatalf("expected the adoption baseline JSON to stay 1500, got %+v", row.AdoptionBaseline.MTU)
	}
}
