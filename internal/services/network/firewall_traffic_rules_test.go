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
