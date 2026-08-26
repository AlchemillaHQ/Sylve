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
	"reflect"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func validFirewallNATRuleRequest(name string) networkServiceInterfaces.UpsertFirewallNATRuleRequest {
	return networkServiceInterfaces.UpsertFirewallNATRuleRequest{
		Name:             name,
		NATType:          "snat",
		EgressInterfaces: []string{"em0"},
		Family:           "inet",
		Protocol:         "any",
		SourceRaw:        "any",
		DestRaw:          "any",
		TranslateMode:    "interface",
	}
}

func TestValidateFirewallNATRuleRequestRejectsAmbiguousSelectorsAndUnsafeInterfaces(t *testing.T) {
	objectID := uint(1)
	tests := []struct {
		name string
		req  networkServiceInterfaces.UpsertFirewallNATRuleRequest
	}{
		{
			name: "raw and object selector",
			req: func() networkServiceInterfaces.UpsertFirewallNATRuleRequest {
				req := validFirewallNATRuleRequest("ambiguous")
				req.SourceObjID = &objectID
				return req
			}(),
		},
		{
			name: "unsafe interface",
			req: func() networkServiceInterfaces.UpsertFirewallNATRuleRequest {
				req := validFirewallNATRuleRequest("unsafe")
				req.EgressInterfaces = []string{"em0\nnat on em1"}
				return req
			}(),
		},
		{
			name: "oversized name",
			req:  validFirewallNATRuleRequest(strings.Repeat("n", MaxFirewallNATRuleNameBytes+1)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{}
			err := svc.validateFirewallNATRuleRequest(&tt.req)
			if !errors.Is(err, ErrInvalidFirewallNATRule) {
				t.Fatalf("expected invalid NAT rule error, got %v", err)
			}
		})
	}

	svc := &Service{}
	if err := svc.validateFirewallNATRuleRequest(nil); !errors.Is(err, ErrInvalidFirewallNATRule) {
		t.Fatalf("expected nil request to be rejected, got %v", err)
	}
}

func TestValidateFirewallNATRuleRequestRejectsTCPUDP(t *testing.T) {
	req := validFirewallNATRuleRequest("combined-protocol")
	req.Protocol = "tcp_udp"
	if err := (&Service{}).validateFirewallNATRuleRequest(&req); !errors.Is(err, ErrInvalidFirewallNATRule) {
		t.Fatalf("expected tcp_udp to remain invalid for NAT rules, got %v", err)
	}
}

func TestNormalizeFirewallNATRuleIDsRejectsInvalidSets(t *testing.T) {
	tooMany := make([]uint, MaxFirewallNATRuleDeleteItems+1)
	for i := range tooMany {
		tooMany[i] = uint(i + 1)
	}

	for _, ids := range [][]uint{nil, {}, {0}, {1, 1}, tooMany} {
		if _, err := normalizeFirewallNATRuleIDs(ids); !errors.Is(err, ErrInvalidFirewallNATRule) {
			t.Fatalf("ids=%v: expected invalid NAT rule error, got %v", ids, err)
		}
	}
}

func TestValidateFirewallNATRuleRequestRequiresSingleAddressHostTargets(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.Object{}, &networkModels.ObjectEntry{})
	objects := []networkModels.Object{
		{Name: "target.example", Type: "FQDN"},
		{Name: "multiple-hosts", Type: "Host"},
		{Name: "single-host", Type: "Host"},
	}
	if err := db.Create(&objects).Error; err != nil {
		t.Fatal(err)
	}
	fqdn, multiHost, singleHost := objects[0], objects[1], objects[2]
	entries := []networkModels.ObjectEntry{
		{ObjectID: fqdn.ID, Value: "target.example"},
		{ObjectID: multiHost.ID, Value: "192.0.2.10"},
		{ObjectID: multiHost.ID, Value: "192.0.2.11"},
		{ObjectID: singleHost.ID, Value: "192.0.2.12"},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}

	for name, objectID := range map[string]uint{
		"fqdn":       fqdn.ID,
		"multi-host": multiHost.ID,
	} {
		t.Run(name, func(t *testing.T) {
			req := validFirewallNATRuleRequest(name)
			req.TranslateMode = "address"
			req.TranslateToObjID = &objectID
			if err := svc.validateFirewallNATRuleRequest(&req); !errors.Is(err, ErrInvalidFirewallNATRule) {
				t.Fatalf("expected invalid target object, got %v", err)
			}
		})
	}

	req := validFirewallNATRuleRequest("single")
	req.TranslateMode = "address"
	req.TranslateToObjID = &singleHost.ID
	if err := svc.validateFirewallNATRuleRequest(&req); err != nil {
		t.Fatalf("expected single-address Host object to be accepted, got %v", err)
	}
}

func TestDeleteFirewallNATRulesPreflightsBeforeMutation(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "visible-one", Visible: true, Enabled: true, Priority: 10, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "managed", Visible: true, Enabled: true, Priority: 20, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.10"},
		{Name: "visible-two", Visible: true, Enabled: true, Priority: 30, NATType: "binat", IngressInterfaces: []string{"em2"}, Family: "inet", Protocol: "any", TranslateToRaw: "198.51.100.10", DNATTargetRaw: "10.0.0.10"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[1]).Update("visible", false).Error; err != nil {
		t.Fatal(err)
	}

	applyCalls := 0
	apply := func() error {
		applyCalls++
		return nil
	}
	if err := svc.deleteFirewallNATRules([]uint{rules[0].ID, 999999}, apply); !errors.Is(err, ErrFirewallNATRuleNotFound) {
		t.Fatalf("expected missing-rule preflight error, got %v", err)
	}
	if err := svc.deleteFirewallNATRules([]uint{rules[0].ID, rules[1].ID}, apply); !errors.Is(err, ErrHiddenFirewallRuleMutation) {
		t.Fatalf("expected managed-rule preflight error, got %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("preflight failures invoked apply %d times", applyCalls)
	}

	var count int64
	if err := db.Model(&networkModels.FirewallNATRule{}).Count(&count).Error; err != nil || count != int64(len(rules)) {
		t.Fatalf("preflight failures changed rows: count=%d err=%v", count, err)
	}
}

func TestDeleteFirewallNATRulesDeletesOnlyRequestedRulesAndAppliesOnce(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "delete-one", Visible: true, Enabled: true, Priority: 10, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "keep-one", Visible: true, Enabled: true, Priority: 20, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.10", DstPortsRaw: "443"},
		{Name: "delete-two", Visible: true, Enabled: false, Priority: 35, NATType: "binat", IngressInterfaces: []string{"em2"}, Family: "inet", Protocol: "any", TranslateToRaw: "198.51.100.10", DNATTargetRaw: "10.0.0.10"},
		{Name: "keep-two", Visible: true, Enabled: false, Priority: 90, NATType: "snat", EgressInterfaces: []string{"em3"}, Family: "inet6", Protocol: "udp", TranslateMode: "address", TranslateToRaw: "2001:db8::10"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	applyCalls := 0
	if err := svc.deleteFirewallNATRules([]uint{rules[0].ID, rules[2].ID}, func() error {
		applyCalls++
		return nil
	}); err != nil {
		t.Fatalf("delete NAT rules: %v", err)
	}
	if applyCalls != 1 {
		t.Fatalf("expected exactly one firewall apply, got %d", applyCalls)
	}

	var remaining []networkModels.FirewallNATRule
	if err := db.Order("priority asc").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].ID != rules[1].ID || remaining[0].Priority != 20 || remaining[1].ID != rules[3].ID || remaining[1].Priority != 90 {
		t.Fatalf("unexpected remaining rules or priorities: %+v", remaining)
	}
}

func TestDeleteFirewallNATRulesRestoresCompleteSnapshotAfterApplyFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "keep", Description: "first", Visible: true, Enabled: false, Log: false, Priority: 10, NATType: "snat", PolicyRoutingEnabled: false, EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", SourceRaw: "10.0.0.0/24", DestRaw: "any", TranslateMode: "interface"},
		{Name: "delete-one", Description: "second", Visible: true, Enabled: true, Log: true, Priority: 25, NATType: "dnat", PolicyRoutingEnabled: true, PolicyRouteGateway: "192.0.2.1", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", SourceRaw: "any", DestRaw: "198.51.100.20", DNATTargetRaw: "10.0.0.20", DstPortsRaw: "443", RedirectPortsRaw: "8443"},
		{Name: "delete-two", Description: "third", Visible: true, Enabled: false, Log: false, Priority: 90, NATType: "binat", IngressInterfaces: []string{"em2"}, EgressInterfaces: []string{"em3"}, Family: "inet6", Protocol: "udp", SourceRaw: "2001:db8:1::/64", DestRaw: "2001:db8:2::10", TranslateToRaw: "2001:db8:3::10", DNATTargetRaw: "2001:db8:4::10"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	before, err := snapshotFirewallNATRules(db)
	if err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("forced apply failure")
	applyCalls := 0
	err = svc.deleteFirewallNATRules([]uint{rules[1].ID, rules[2].ID}, func() error {
		applyCalls++
		if applyCalls == 1 {
			return applyErr
		}
		return nil
	})
	if !errors.Is(err, applyErr) || applyCalls != 2 {
		t.Fatalf("unexpected apply/rollback result: err=%v applyCalls=%d", err, applyCalls)
	}

	after, err := snapshotFirewallNATRules(db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("NAT rule snapshot was not restored exactly:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestReorderFirewallNATRulesUsesVisiblePositionsAfterManagedRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "managed", Visible: true, Enabled: true, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "visible-a", Visible: true, Enabled: true, Priority: 2, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "visible-b", Visible: true, Enabled: true, Priority: 3, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[0]).Update("visible", false).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.ReorderFirewallNATRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: rules[2].ID, Priority: 1},
		{ID: rules[1].ID, Priority: 2},
	})
	if err != nil {
		t.Fatalf("reorder failed: %v", err)
	}

	for id, expectedPriority := range map[uint]int{
		rules[0].ID: 1,
		rules[2].ID: 2,
		rules[1].ID: 3,
	} {
		var rule networkModels.FirewallNATRule
		if err := db.First(&rule, id).Error; err != nil {
			t.Fatal(err)
		}
		if rule.Priority != expectedPriority {
			t.Fatalf("rule %d priority=%d, want %d", id, rule.Priority, expectedPriority)
		}
	}
}

func TestReorderFirewallNATRulesRejectsIncompleteVisibleSet(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "one", Visible: true, Enabled: true, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "two", Visible: true, Enabled: true, Priority: 2, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.ReorderFirewallNATRules([]networkServiceInterfaces.FirewallReorderRequest{{ID: rules[0].ID, Priority: 1}})
	if !errors.Is(err, ErrFirewallNATRuleConflict) {
		t.Fatalf("expected reorder conflict, got %v", err)
	}
}

func TestRestoreFirewallNATRulesAfterApplyFailureRestoresExactSnapshot(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallNATRule{})
	rules := []networkModels.FirewallNATRule{
		{Name: "managed", Visible: true, Enabled: true, Log: false, Priority: 1, NATType: "snat", EgressInterfaces: []string{"em0"}, Family: "inet", Protocol: "any", TranslateMode: "interface"},
		{Name: "visible", Visible: true, Enabled: false, Log: false, Priority: 2, NATType: "dnat", IngressInterfaces: []string{"em1"}, Family: "inet", Protocol: "tcp", DNATTargetRaw: "192.0.2.20", DstPortsRaw: "443"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[0]).Update("visible", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[1]).Updates(map[string]any{"enabled": false, "log": false}).Error; err != nil {
		t.Fatal(err)
	}

	snapshot, err := snapshotFirewallNATRules(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&networkModels.FirewallNATRule{}, rules[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&networkModels.FirewallNATRule{
		Name: "unexpected", Visible: true, Enabled: true, Priority: 9,
		NATType: "snat", EgressInterfaces: []string{"em9"}, Family: "inet", Protocol: "any", TranslateMode: "interface",
	}).Error; err != nil {
		t.Fatal(err)
	}

	reapplyCalls := 0
	applyErr := errors.New("apply failed")
	err = svc.restoreFirewallNATRulesAfterApplyFailure(snapshot, applyErr, func() error {
		reapplyCalls++
		return nil
	})
	if !errors.Is(err, applyErr) || reapplyCalls != 1 {
		t.Fatalf("unexpected rollback result: err=%v reapplyCalls=%d", err, reapplyCalls)
	}

	restored, err := snapshotFirewallNATRules(db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, snapshot) {
		t.Fatalf("snapshot was not restored exactly:\nbefore=%+v\nafter=%+v", snapshot, restored)
	}
}
