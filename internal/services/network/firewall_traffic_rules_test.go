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
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func TestValidateFirewallTrafficRuleRequestRejectsAmbiguousSelectorsAndUnsafeInterfaces(t *testing.T) {
	objectID := uint(1)
	tests := []struct {
		name string
		req  networkServiceInterfaces.UpsertFirewallTrafficRuleRequest
	}{
		{
			name: "raw and object selector",
			req: networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
				Name: "ambiguous", Action: "pass", Direction: "in", Protocol: "any", Family: "any",
				SourceRaw: "any", SourceObjID: &objectID,
			},
		},
		{
			name: "unsafe interface",
			req: networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
				Name: "unsafe", Action: "pass", Direction: "in", Protocol: "any", Family: "any",
				IngressInterfaces: []string{"em0\npass all"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{}
			err := svc.validateFirewallTrafficRuleRequest(&tt.req)
			if !errors.Is(err, ErrInvalidFirewallTrafficRule) {
				t.Fatalf("expected invalid traffic rule error, got %v", err)
			}
		})
	}
}

func TestValidateFirewallTrafficRuleRequestAcceptsTCPUDPWithPorts(t *testing.T) {
	svc := &Service{}
	err := svc.validateFirewallTrafficRuleRequest(&networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name:        "dns",
		Action:      "pass",
		Direction:   "out",
		Protocol:    "tcp_udp",
		Family:      "any",
		DstPortsRaw: "53",
	})
	if err != nil {
		t.Fatalf("expected tcp_udp traffic rule with ports to validate, got %v", err)
	}
}

func TestNormalizeFirewallTrafficRuleIDsRejectsInvalidSets(t *testing.T) {
	tooMany := make([]uint, MaxFirewallTrafficRuleDeleteItems+1)
	for i := range tooMany {
		tooMany[i] = uint(i + 1)
	}

	for _, ids := range [][]uint{nil, {}, {0}, {1, 1}, tooMany} {
		if _, err := normalizeFirewallTrafficRuleIDs(ids); !errors.Is(err, ErrInvalidFirewallTrafficRule) {
			t.Fatalf("ids=%v: expected invalid traffic rule error, got %v", ids, err)
		}
	}
}

func TestDeleteFirewallTrafficRulesPreflightsBeforeMutation(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	rules := []networkModels.FirewallTrafficRule{
		{Name: "visible-one", Visible: true, Enabled: true, Priority: 10, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "managed", Visible: true, Enabled: true, Priority: 20, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "visible-two", Visible: true, Enabled: true, Priority: 30, Action: "block", Direction: "out", Protocol: "any", Family: "any"},
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
	if err := svc.deleteFirewallTrafficRules([]uint{rules[0].ID, 999999}, apply); !errors.Is(err, ErrFirewallTrafficRuleNotFound) {
		t.Fatalf("expected missing-rule preflight error, got %v", err)
	}
	if err := svc.deleteFirewallTrafficRules([]uint{rules[0].ID, rules[1].ID}, apply); !errors.Is(err, ErrHiddenFirewallRuleMutation) {
		t.Fatalf("expected managed-rule preflight error, got %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("preflight failures invoked apply %d times", applyCalls)
	}

	var count int64
	if err := db.Model(&networkModels.FirewallTrafficRule{}).Count(&count).Error; err != nil || count != int64(len(rules)) {
		t.Fatalf("preflight failures changed rows: count=%d err=%v", count, err)
	}
}

func TestDeleteFirewallTrafficRulesDeletesOnlyRequestedRulesAndAppliesOnce(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	rules := []networkModels.FirewallTrafficRule{
		{Name: "delete-one", Visible: true, Enabled: true, Priority: 10, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "keep-one", Visible: true, Enabled: true, Priority: 20, Action: "pass", Direction: "out", Protocol: "any", Family: "any"},
		{Name: "delete-two", Visible: true, Enabled: true, Priority: 30, Action: "block", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "keep-two", Visible: true, Enabled: false, Priority: 40, Action: "block", Direction: "out", Protocol: "icmp", Family: "inet6"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	applyCalls := 0
	if err := svc.deleteFirewallTrafficRules([]uint{rules[0].ID, rules[2].ID}, func() error {
		applyCalls++
		return nil
	}); err != nil {
		t.Fatalf("delete traffic rules: %v", err)
	}
	if applyCalls != 1 {
		t.Fatalf("expected exactly one firewall apply, got %d", applyCalls)
	}

	var remaining []networkModels.FirewallTrafficRule
	if err := db.Order("priority asc").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 || remaining[0].ID != rules[1].ID || remaining[0].Priority != 20 || remaining[1].ID != rules[3].ID || remaining[1].Priority != 40 {
		t.Fatalf("unexpected remaining rules or priorities: %+v", remaining)
	}
}

func TestDeleteFirewallTrafficRulesRestoresCompleteSnapshotAfterApplyFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	rules := []networkModels.FirewallTrafficRule{
		{Name: "keep", Description: "first", Visible: true, Enabled: true, Log: true, Quick: true, Priority: 10, Action: "pass", Direction: "in", Protocol: "tcp_udp", IngressInterfaces: []string{"em0"}, Family: "inet", SourceRaw: "192.0.2.0/24", DestRaw: "any", DstPortsRaw: "53"},
		{Name: "delete-one", Description: "second", Visible: true, Enabled: false, Priority: 25, Action: "block", Direction: "out", Protocol: "udp", EgressInterfaces: []string{"em1"}, Family: "inet6", SourceRaw: "any", DestRaw: "2001:db8::1", SrcPortsRaw: "1024:65535"},
		{Name: "delete-two", Description: "third", Visible: true, Enabled: true, Log: true, Priority: 90, Action: "pass", Direction: "in", Protocol: "icmp", Family: "any", SourceRaw: "any", DestRaw: "any"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	before, err := snapshotFirewallTrafficRules(db)
	if err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("forced apply failure")
	applyCalls := 0
	err = svc.deleteFirewallTrafficRules([]uint{rules[1].ID, rules[2].ID}, func() error {
		applyCalls++
		if applyCalls == 1 {
			return applyErr
		}
		return nil
	})
	if !errors.Is(err, applyErr) || applyCalls != 2 {
		t.Fatalf("unexpected apply/rollback result: err=%v applyCalls=%d", err, applyCalls)
	}

	after, err := snapshotFirewallTrafficRules(db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("traffic rule snapshot was not restored exactly:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestReorderFirewallTrafficRulesUsesVisiblePositionsAfterManagedRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	hidden := networkModels.FirewallTrafficRule{
		Name: "managed", Visible: true, Enabled: true, Priority: 1,
		Action: "pass", Direction: "in", Protocol: "any", Family: "any",
	}
	visibleA := networkModels.FirewallTrafficRule{
		Name: "visible-a", Visible: true, Enabled: true, Priority: 2,
		Action: "pass", Direction: "in", Protocol: "any", Family: "any",
	}
	visibleB := networkModels.FirewallTrafficRule{
		Name: "visible-b", Visible: true, Enabled: true, Priority: 3,
		Action: "pass", Direction: "in", Protocol: "any", Family: "any",
	}
	if err := db.Create(&[]networkModels.FirewallTrafficRule{hidden, visibleA, visibleB}).Error; err != nil {
		t.Fatal(err)
	}
	var rules []networkModels.FirewallTrafficRule
	if err := db.Order("id asc").Find(&rules).Error; err != nil {
		t.Fatal(err)
	}
	hidden, visibleA, visibleB = rules[0], rules[1], rules[2]
	if err := db.Model(&hidden).Update("visible", false).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{
		{ID: visibleB.ID, Priority: 1},
		{ID: visibleA.ID, Priority: 2},
	})
	if err != nil {
		t.Fatalf("reorder failed: %v", err)
	}

	for id, expectedPriority := range map[uint]int{
		hidden.ID:   1,
		visibleB.ID: 2,
		visibleA.ID: 3,
	} {
		var rule networkModels.FirewallTrafficRule
		if err := db.First(&rule, id).Error; err != nil {
			t.Fatal(err)
		}
		if rule.Priority != expectedPriority {
			t.Fatalf("rule %d priority=%d, want %d", id, rule.Priority, expectedPriority)
		}
	}
}

func TestReorderFirewallTrafficRulesRejectsIncompleteVisibleSet(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	rules := []networkModels.FirewallTrafficRule{
		{Name: "one", Visible: true, Enabled: true, Priority: 1, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "two", Visible: true, Enabled: true, Priority: 2, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.ReorderFirewallTrafficRules([]networkServiceInterfaces.FirewallReorderRequest{{ID: rules[0].ID, Priority: 1}})
	if !errors.Is(err, ErrFirewallTrafficRuleConflict) {
		t.Fatalf("expected reorder conflict, got %v", err)
	}
}

func TestRestoreFirewallTrafficRulesAfterApplyFailureRestoresExactSnapshot(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.FirewallTrafficRule{})
	rules := []networkModels.FirewallTrafficRule{
		{Name: "managed", Visible: true, Enabled: true, Priority: 1, Action: "pass", Direction: "in", Protocol: "any", Family: "any"},
		{Name: "visible", Visible: true, Enabled: false, Priority: 2, Action: "block", Direction: "out", Protocol: "any", Family: "inet"},
	}
	if err := db.Create(&rules).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[0]).Update("visible", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&rules[1]).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	snapshot, err := snapshotFirewallTrafficRules(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&networkModels.FirewallTrafficRule{}, rules[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&networkModels.FirewallTrafficRule{
		Name: "unexpected", Visible: true, Enabled: true, Priority: 9,
		Action: "pass", Direction: "in", Protocol: "any", Family: "any",
	}).Error; err != nil {
		t.Fatal(err)
	}

	reapplyCalls := 0
	applyErr := errors.New("apply failed")
	err = svc.restoreFirewallTrafficRulesAfterApplyFailure(snapshot, applyErr, func() error {
		reapplyCalls++
		return nil
	})
	if !errors.Is(err, applyErr) || reapplyCalls != 1 {
		t.Fatalf("unexpected rollback result: err=%v reapplyCalls=%d", err, reapplyCalls)
	}

	var restored []networkModels.FirewallTrafficRule
	if err := db.Order("id asc").Find(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || restored[0].Name != "managed" || restored[0].Visible || restored[1].Name != "visible" || restored[1].Enabled {
		t.Fatalf("snapshot was not restored exactly: %+v", restored)
	}
}
