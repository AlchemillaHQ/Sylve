// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"errors"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"gorm.io/gorm"
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

func TestEnsureRestoredStandardSwitch(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t, &networkModels.StandardSwitch{})

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed standard switch: %v", err)
	}

	t.Run("found by name", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
		id, created, err := svc.ensureRestoredStandardSwitch(tx, 1, 0, 0, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != lan.ID {
			t.Fatalf("expected switch ID %d, got %d", lan.ID, id)
		}
		if created {
			t.Fatal("expected created=false for found switch")
		}
	})

	t.Run("found by bridge when name empty", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.StandardSwitch{BridgeName: "bridge-lan"}
		id, created, err := svc.ensureRestoredStandardSwitch(tx, 1, 0, 0, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != lan.ID {
			t.Fatalf("expected switch ID %d, got %d", lan.ID, id)
		}
		if created {
			t.Fatal("expected created=false")
		}
	})

	t.Run("not found returns ErrSwitchNotFound", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.StandardSwitch{Name: "wan", BridgeName: "bridge-wan"}
		_, _, err := svc.ensureRestoredStandardSwitch(tx, 1, 0, 0, meta)
		if !errors.Is(err, ErrSwitchNotFound) {
			t.Fatalf("expected ErrSwitchNotFound, got %v", err)
		}
	})

	t.Run("nil metadata returns ErrSwitchNotFound", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		_, _, err := svc.ensureRestoredStandardSwitch(tx, 1, 0, 0, nil)
		if !errors.Is(err, ErrSwitchNotFound) {
			t.Fatalf("expected ErrSwitchNotFound, got %v", err)
		}
	})
}

func TestEnsureRestoredManualSwitch(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t, &networkModels.ManualSwitch{})

	home := networkModels.ManualSwitch{Name: "home-net", Bridge: "re0"}
	if err := db.Create(&home).Error; err != nil {
		t.Fatalf("failed to seed manual switch: %v", err)
	}

	t.Run("found by name", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.ManualSwitch{Name: "home-net", Bridge: "re0"}
		id, err := svc.ensureRestoredManualSwitch(tx, 1, 0, 0, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != home.ID {
			t.Fatalf("expected switch ID %d, got %d", home.ID, id)
		}
	})

	t.Run("found by bridge when name empty", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.ManualSwitch{Bridge: "re0"}
		id, err := svc.ensureRestoredManualSwitch(tx, 1, 0, 0, meta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != home.ID {
			t.Fatalf("expected switch ID %d, got %d", home.ID, id)
		}
	})

	t.Run("not found returns ErrSwitchNotFound", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		meta := &networkModels.ManualSwitch{Name: "other-net", Bridge: "em0"}
		_, err := svc.ensureRestoredManualSwitch(tx, 1, 0, 0, meta)
		if !errors.Is(err, ErrSwitchNotFound) {
			t.Fatalf("expected ErrSwitchNotFound, got %v", err)
		}
	})

	t.Run("nil metadata returns ErrSwitchNotFound", func(t *testing.T) {
		tx := db.Begin()
		defer tx.Rollback()

		_, err := svc.ensureRestoredManualSwitch(tx, 1, 0, 0, nil)
		if !errors.Is(err, ErrSwitchNotFound) {
			t.Fatalf("expected ErrSwitchNotFound, got %v", err)
		}
	})
}

func TestNormalizeRestoredJailNetworksSkipsUnresolved(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed lan switch: %v", err)
	}
	mgmt := networkModels.ManualSwitch{Name: "mgmt", Bridge: "re0"}
	if err := db.Create(&mgmt).Error; err != nil {
		t.Fatalf("failed to seed mgmt switch: %v", err)
	}

	networks := []jailModels.Network{
		{
			Name:       "lan-net",
			SwitchType: "standard",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "lan",
				BridgeName: "bridge-lan",
			},
		},
		{
			Name:       "mgmt-net",
			SwitchType: "manual",
			ManualSwitch: &networkModels.ManualSwitch{
				Name:   "mgmt",
				Bridge: "re0",
			},
		},
		{
			Name:       "dmz-net",
			SwitchType: "standard",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "dmz",
				BridgeName: "bridge-dmz",
			},
		},
	}

	tx := db.Begin()
	defer tx.Rollback()

	resolved, requiresSync, err := svc.normalizeRestoredJailNetworks(tx, 100, 200, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requiresSync {
		t.Fatal("expected requiresSync=false")
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 networks (lan + mgmt), dmz should be skipped, got %d", len(resolved))
	}

	var foundLan, foundMgmt bool
	for _, n := range resolved {
		switch n.Name {
		case "lan-net":
			foundLan = true
			if n.SwitchID != lan.ID || n.SwitchType != "standard" {
				t.Fatalf("lan-net switch mismatch: id=%d type=%s", n.SwitchID, n.SwitchType)
			}
		case "mgmt-net":
			foundMgmt = true
			if n.SwitchID != mgmt.ID || n.SwitchType != "manual" {
				t.Fatalf("mgmt-net switch mismatch: id=%d type=%s", n.SwitchID, n.SwitchType)
			}
		}
	}
	if !foundLan {
		t.Fatal("lan-net should be in resolved networks")
	}
	if !foundMgmt {
		t.Fatal("mgmt-net should be in resolved networks")
	}
}

func TestNormalizeRestoredJailNetworksAllResolved(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed lan switch: %v", err)
	}
	wifi := networkModels.StandardSwitch{Name: "wifi", BridgeName: "bridge-wifi"}
	if err := db.Create(&wifi).Error; err != nil {
		t.Fatalf("failed to seed wifi switch: %v", err)
	}

	networks := []jailModels.Network{
		{
			Name: "nic0",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "lan",
				BridgeName: "bridge-lan",
			},
		},
		{
			Name: "nic1",
			StandardSwitch: &networkModels.StandardSwitch{
				Name:       "wifi",
				BridgeName: "bridge-wifi",
			},
		},
	}

	tx := db.Begin()
	defer tx.Rollback()

	resolved, requiresSync, err := svc.normalizeRestoredJailNetworks(tx, 100, 200, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requiresSync {
		t.Fatal("expected requiresSync=false")
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved, got %d", len(resolved))
	}

	switchIDs := map[string]uint{}
	for _, n := range resolved {
		switchIDs[n.Name] = n.SwitchID
	}
	if switchIDs["nic0"] != lan.ID {
		t.Fatalf("nic0 expected switch %d, got %d", lan.ID, switchIDs["nic0"])
	}
	if switchIDs["nic1"] != wifi.ID {
		t.Fatalf("nic1 expected switch %d, got %d", wifi.ID, switchIDs["nic1"])
	}
}

func newTestZeltaServiceWithDB(t *testing.T, models ...any) (*Service, *gorm.DB) {
	t.Helper()
	db := newZeltaServiceTestDB(t, models...)
	return newTestZeltaService(db), db
}
