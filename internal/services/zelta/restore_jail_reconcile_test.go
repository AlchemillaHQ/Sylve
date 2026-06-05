// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

func TestNormalizeRestoredSwitchType(t *testing.T) {
	if normalizeRestoredSwitchType("manual") != "manual" {
		t.Fatal("manual should stay manual")
	}
	if normalizeRestoredSwitchType("MANUAL") != "manual" {
		t.Fatal("case-insensitive manual")
	}
	if normalizeRestoredSwitchType("standard") != "standard" {
		t.Fatal("standard should stay standard")
	}
	if normalizeRestoredSwitchType("") != "standard" {
		t.Fatal("empty should default to standard")
	}
	if normalizeRestoredSwitchType("unknown") != "standard" {
		t.Fatal("unknown should default to standard")
	}
}

func TestNormalizeRestoredSwitchMTU(t *testing.T) {
	if normalizeRestoredSwitchMTU(1500) != 1500 {
		t.Fatal("valid MTU preserved")
	}
	if normalizeRestoredSwitchMTU(9000) != 9000 {
		t.Fatal("jumbo MTU preserved")
	}
	if normalizeRestoredSwitchMTU(0) != 1500 {
		t.Fatal("zero defaults to 1500")
	}
	if normalizeRestoredSwitchMTU(-1) != 1500 {
		t.Fatal("negative defaults to 1500")
	}
}

func TestNormalizeRestoredJailHooks(t *testing.T) {
	hooks := normalizeRestoredJailHooks(42, []jailModels.JailHooks{
		{Phase: "prestart", Enabled: true, Script: "/bin/echo"},
		{Phase: "poststop", Enabled: false, Script: "/bin/true"},
	})
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].JailID != 42 || hooks[1].JailID != 42 {
		t.Fatal("jail ID should be set on all hooks")
	}
	if hooks[0].Phase != "prestart" {
		t.Fatalf("expected prestart, got %q", hooks[0].Phase)
	}
	if hooks[1].Enabled {
		t.Fatal("poststop should stay disabled")
	}
}

func TestNormalizeRestoredJailStorages(t *testing.T) {
	storages := normalizeRestoredJailStorages(100, []jailModels.Storage{
		{Pool: "tank", GUID: "guid-1", Name: "Data", IsBase: false},
	}, "zroot", "guid-base")
	if len(storages) != 2 {
		t.Fatalf("expected 2 storages (has no base, so auto-added), got %d", len(storages))
	}
	foundBase := false
	for _, s := range storages {
		if s.IsBase {
			foundBase = true
			if s.Pool != "zroot" || s.GUID != "guid-base" {
				t.Fatalf("base storage pool/guid mismatch: %+v", s)
			}
			if s.Name != "Base Filesystem" {
				t.Fatalf("base default name: %q", s.Name)
			}
		}
	}
	if !foundBase {
		t.Fatal("expected base storage to be created")
	}

	storages = normalizeRestoredJailStorages(200, []jailModels.Storage{
		{Pool: "tank", GUID: "guid-1", Name: "Base Filesystem", IsBase: true},
	}, "zroot", "guid-base")
	if len(storages) != 1 {
		t.Fatalf("expected 1 storage (has base), got %d", len(storages))
	}
	if !storages[0].IsBase || storages[0].Pool != "zroot" {
		t.Fatalf("base pool should be overridden: %+v", storages[0])
	}
}

func TestObjectIDPtr(t *testing.T) {
	obj := &networkModels.Object{ID: 5}
	ptr := objectIDPtr(obj)
	if ptr == nil || *ptr != 5 {
		t.Fatalf("expected ptr to 5, got %v", ptr)
	}

	ptr = objectIDPtr(&networkModels.Object{ID: 0})
	if ptr != nil {
		t.Fatal("zero ID should return nil")
	}
}

func TestRestoredSwitchResolutionKey(t *testing.T) {
	net := jailModels.Network{
		SwitchType: "standard",
		SwitchID:   10,
		StandardSwitch: &networkModels.StandardSwitch{
			Name:       "myswitch",
			BridgeName: "bridge0",
		},
	}
	key := restoredSwitchResolutionKey(net)
	expected := "standard:10:myswitch:bridge0"
	if key != expected {
		t.Fatalf("expected %q, got %q", expected, key)
	}

	netManual := jailModels.Network{
		SwitchType: "manual",
		SwitchID:   5,
		ManualSwitch: &networkModels.ManualSwitch{
			Name:   "home-net",
			Bridge: "re0",
		},
	}
	key = restoredSwitchResolutionKey(netManual)
	if key != "manual:5:home-net:re0" {
		t.Fatalf("manual key: %q", key)
	}
}

func TestRestoredObjectEntriesMatch(t *testing.T) {
	metaNil := func() *networkModels.Object { return nil }
	makeObj := func(typ string, values ...string) *networkModels.Object {
		entries := make([]networkModels.ObjectEntry, len(values))
		for i, v := range values {
			entries[i] = networkModels.ObjectEntry{Value: v}
		}
		return &networkModels.Object{Type: typ, Entries: entries}
	}

	if !restoredObjectEntriesMatch(nil, metaNil()) {
		t.Fatal("nil metadata should match")
	}
	if restoredObjectEntriesMatch(nil, makeObj("HOST", "10.0.0.1")) {
		t.Fatal("non-nil metadata with nil existing should not match")
	}

	existing := makeObj("HOST", "10.0.0.1", "10.0.0.2")
	if !restoredObjectEntriesMatch(existing, makeObj("HOST", "10.0.0.1")) {
		t.Fatal("subset match should pass")
	}
	if restoredObjectEntriesMatch(existing, makeObj("HOST", "10.0.0.1", "10.0.0.3")) {
		t.Fatal("metadata with extra entry not in existing should fail")
	}
	if restoredObjectEntriesMatch(existing, makeObj("NETWORK", "10.0.0.1")) {
		t.Fatal("type mismatch should fail")
	}

	if !restoredObjectEntriesMatch(existing, metaNil()) {
		t.Fatal("nil metadata should always match")
	}
}

func TestRestoredPortsCompatible(t *testing.T) {
	existing := []networkModels.NetworkPort{
		{Name: "eth0"}, {Name: "eth1"},
	}
	if !restoredPortsCompatible(existing, nil) {
		t.Fatal("nil metadata ports should be compatible")
	}
	if !restoredPortsCompatible(existing, []networkModels.NetworkPort{}) {
		t.Fatal("empty metadata should be compatible")
	}
	if restoredPortsCompatible(existing, []networkModels.NetworkPort{{Name: "eth2"}}) {
		t.Fatal("missing port should not be compatible")
	}
	if !restoredPortsCompatible(existing, []networkModels.NetworkPort{{Name: "ETH0"}}) {
		t.Fatal("case-insensitive match should be compatible")
	}
}

func TestRestoredStandardSwitchCompatible(t *testing.T) {
	existing := &networkModels.StandardSwitch{Name: "sw1", BridgeName: "bridge0"}
	metadata := &networkModels.StandardSwitch{Name: "sw1", BridgeName: "bridge0"}
	if !restoredStandardSwitchCompatible(existing, metadata) {
		t.Fatal("identical should be compatible")
	}
	metadataDiff := &networkModels.StandardSwitch{Name: "sw1", BridgeName: "other"}
	if restoredStandardSwitchCompatible(existing, metadataDiff) {
		t.Fatal("different bridge should not be compatible")
	}

	if restoredStandardSwitchCompatible(existing, nil) {
		t.Fatal("nil metadata with existing should not be compatible")
	}
	if restoredStandardSwitchCompatible(nil, metadata) {
		t.Fatal("nil existing should not be compatible")
	}
}
