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
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"gorm.io/gorm"
)

type restoredJailHardwareNormalizerStub struct {
	jailServiceInterfaces.JailServiceInterface
	calls  int
	cpuSet []int
}

func (s *restoredJailHardwareNormalizerStub) NormalizeRestoredJailHardware(data *jailModels.Jail) error {
	s.calls++
	data.CPUSet = append([]int(nil), s.cpuSet...)
	return nil
}

func TestNormalizeRestoredJailHooks(t *testing.T) {
	hooks := normalizeRestoredJailHooks(42, jailModels.JailTypeFreeBSD, []jailModels.JailHooks{
		{Phase: "prestart", Enabled: true, Script: "/bin/echo"},
		{Phase: "start", Enabled: true, Script: "/bin/sh /etc/rc"},
		{Phase: "poststop", Enabled: false, Script: "/bin/true"},
	})
	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}
	if hooks[0].JailID != 42 || hooks[1].JailID != 42 || hooks[2].JailID != 42 {
		t.Fatal("jail ID should be set on all hooks")
	}
	if hooks[0].Phase != "prestart" {
		t.Fatalf("expected prestart, got %q", hooks[0].Phase)
	}
	if hooks[1].Enabled || hooks[1].Script != "" {
		t.Fatalf("legacy FreeBSD start hook was not normalized: %+v", hooks[1])
	}
	if hooks[2].Enabled {
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

func TestUpsertRestoredJailStateNormalizesHardwareBeforePersistence(t *testing.T) {
	service, db := newTestZeltaServiceWithDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
	)
	normalizer := &restoredJailHardwareNormalizerStub{cpuSet: []int{1}}
	service.Jail = normalizer
	enabled := true

	reconciled, err := service.upsertRestoredJailState(
		t.Context(),
		"tank/sylve/jails/23",
		&restoredJailMetadata{Jail: jailModels.Jail{
			CTID:           23,
			Name:           "jail-23",
			Type:           jailModels.JailTypeFreeBSD,
			ResourceLimits: &enabled,
			Cores:          1,
			Memory:         1024 * 1024 * 1024,
		}},
		false,
		false,
	)
	if err != nil {
		t.Fatalf("upsertRestoredJailState failed: %v", err)
	}
	if normalizer.calls != 1 || len(reconciled.CPUSet) != 1 || reconciled.CPUSet[0] != 1 {
		t.Fatalf("normalizer calls=%d reconciled CPU set=%v", normalizer.calls, reconciled.CPUSet)
	}

	var persisted jailModels.Jail
	if err := db.Where("ct_id = ?", 23).First(&persisted).Error; err != nil {
		t.Fatalf("load reconciled jail: %v", err)
	}
	if len(persisted.CPUSet) != 1 || persisted.CPUSet[0] != 1 {
		t.Fatalf("persisted CPU set = %v, want [1]", persisted.CPUSet)
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

func TestNormalizeRestoredJailNetworksRejectsUnresolved(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)

	lan := networkModels.StandardSwitch{Name: "lan", BridgeName: "bridge-lan"}
	if err := db.Create(&lan).Error; err != nil {
		t.Fatalf("failed to seed lan switch: %v", err)
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

	resolved, err := svc.normalizeRestoredJailNetworks(tx, 100, 200, networks)
	if !errors.Is(err, ErrSwitchNotFound) {
		t.Fatalf("expected ErrSwitchNotFound, got resolved=%v err=%v", resolved, err)
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

	resolved, err := svc.normalizeRestoredJailNetworks(tx, 100, 200, networks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestNormalizeRestoredJailNetworksValidatesTargetVLANPolicy(t *testing.T) {
	svc, db := newTestZeltaServiceWithDB(t,
		&networkModels.StandardSwitch{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&jailModels.Network{},
	)
	defaultVLAN := 10
	target := networkModels.StandardSwitch{
		Name:              "filtered-lan",
		BridgeName:        "bridge-filtered-lan",
		VLANFiltering:     true,
		DefaultAccessVLAN: &defaultVLAN,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed filtered switch: %v", err)
	}
	accessVLAN := 20
	policy := bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &accessVLAN}
	attachment := networkAttachment.Contract{
		Version: networkAttachment.CurrentVersion, Kind: networkAttachment.KindJail,
		SwitchName: target.Name, SwitchType: "standard", VLANFiltering: true,
		VLANPolicy: &policy,
	}
	network := jailModels.Network{
		Name:       "vnet0",
		SwitchType: "standard",
		Attachment: &attachment,
		VLANPolicy: policy,
	}

	tx := db.Begin()
	defer tx.Rollback()
	resolved, err := svc.normalizeRestoredJailNetworks(tx, 100, 200, []jailModels.Network{network})
	if err != nil {
		t.Fatalf("compatible policy rejected: %v", err)
	}
	if len(resolved) != 1 || resolved[0].VLANPolicy.Mode != bridgevlan.ModeAccess ||
		resolved[0].VLANPolicy.UntaggedVLAN == nil || *resolved[0].VLANPolicy.UntaggedVLAN != accessVLAN {
		t.Fatalf("restored VLAN policy = %#v", resolved)
	}

	if err := tx.Model(&networkModels.StandardSwitch{}).
		Where("id = ?", target.ID).
		Updates(map[string]any{"vlan_filtering": false, "default_access_vlan": nil}).Error; err != nil {
		t.Fatalf("disable target filtering: %v", err)
	}
	_, err = svc.normalizeRestoredJailNetworks(tx, 100, 200, []jailModels.Network{network})
	if err == nil || !strings.Contains(err.Error(), "jail_network_vlan_mode_mismatch") {
		t.Fatalf("incompatible target accepted: %v", err)
	}
}

func newTestZeltaServiceWithDB(t *testing.T, models ...any) (*Service, *gorm.DB) {
	t.Helper()
	db := newZeltaServiceTestDB(t, models...)
	return newTestZeltaService(db), db
}
