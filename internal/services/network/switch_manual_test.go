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
	"fmt"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"gorm.io/gorm"
)

func setupManualSwitchService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	svc, db := newNetworkServiceForTest(t,
		&networkModels.ManualSwitch{},
		&networkModels.StandardSwitch{},
	)

	statements := []string{
		`CREATE TABLE dhcp_manual_switches (dhcp_config_id integer, manual_switch_id integer)`,
		`CREATE TABLE dhcp_ranges (id integer primary key, manual_switch_id integer)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create manual switch fixture table: %v", err)
		}
	}

	stubManualSwitchInterface(t, func(name string) (*iface.Interface, error) {
		return &iface.Interface{Name: name, Groups: []string{"bridge"}}, nil
	})
	originalInspect := syncInspectBridgeVLAN
	syncInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{}, nil
	}
	t.Cleanup(func() { syncInspectBridgeVLAN = originalInspect })
	return svc, db
}

func stubManualSwitchInterface(t *testing.T, get func(string) (*iface.Interface, error)) {
	t.Helper()
	original := manualSwitchIfaceGet
	manualSwitchIfaceGet = get
	t.Cleanup(func() {
		manualSwitchIfaceGet = original
	})
}

func seedManualSwitch(t *testing.T, db *gorm.DB, name, bridge string) networkModels.ManualSwitch {
	t.Helper()
	switchModel := networkModels.ManualSwitch{Name: name, Bridge: bridge}
	if err := db.Create(&switchModel).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}
	return switchModel
}

func TestCreateManualSwitchValidatesAndNormalizesInput(t *testing.T) {
	svc, db := setupManualSwitchService(t)

	created, err := svc.CreateManualSwitch("  uplink_1  ", "  bridge10  ")
	if err != nil {
		t.Fatalf("create manual switch: %v", err)
	}
	if created.ID == 0 || created.Name != "uplink_1" || created.Bridge != "bridge10" {
		t.Fatalf("unexpected created switch: %#v", created)
	}

	var persisted networkModels.ManualSwitch
	if err := db.First(&persisted, created.ID).Error; err != nil {
		t.Fatalf("load created switch: %v", err)
	}
	if persisted.Name != created.Name || persisted.Bridge != created.Bridge {
		t.Fatalf("persisted switch = %#v, created = %#v", persisted, created)
	}

	for _, test := range []struct {
		name      string
		bridge    string
		errorCode string
	}{
		{name: "bad name", bridge: "bridge11", errorCode: "invalid_manual_switch_name"},
		{name: strings.Repeat("a", MaxManualSwitchNameBytes+1), bridge: "bridge11", errorCode: "invalid_manual_switch_name"},
		{name: "valid", bridge: "", errorCode: "invalid_manual_switch_bridge"},
		{name: "valid", bridge: strings.Repeat("b", MaxManualSwitchBridgeBytes+1), errorCode: "invalid_manual_switch_bridge"},
	} {
		_, err := svc.CreateManualSwitch(test.name, test.bridge)
		if !errors.Is(err, ErrInvalidManualSwitch) || ManualSwitchErrorCode(err) != test.errorCode {
			t.Fatalf("name=%q bridge=%q error=%v code=%q", test.name, test.bridge, err, ManualSwitchErrorCode(err))
		}
	}
}

func TestCreateManualSwitchValidatesTheHostBridge(t *testing.T) {
	t.Run("missing interface", func(t *testing.T) {
		svc, _ := setupManualSwitchService(t)
		stubManualSwitchInterface(t, func(name string) (*iface.Interface, error) {
			return nil, fmt.Errorf("interface %s not found", name)
		})

		_, err := svc.CreateManualSwitch("uplink", "bridge20")
		if !errors.Is(err, ErrInvalidManualSwitch) || ManualSwitchErrorCode(err) != "manual_switch_bridge_not_found" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("non-bridge interface", func(t *testing.T) {
		svc, _ := setupManualSwitchService(t)
		stubManualSwitchInterface(t, func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name}, nil
		})

		_, err := svc.CreateManualSwitch("uplink", "em0")
		if !errors.Is(err, ErrInvalidManualSwitch) || ManualSwitchErrorCode(err) != "manual_switch_interface_not_bridge" {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestManualSwitchRejectsCrossTypeNameAndBridgeConflicts(t *testing.T) {
	tests := []struct {
		name       string
		standard   networkModels.StandardSwitch
		createName string
		bridge     string
		code       string
	}{
		{
			name:       "standard name",
			standard:   networkModels.StandardSwitch{Name: "shared", BridgeName: "vm-standard-name"},
			createName: "shared",
			bridge:     "bridge30",
			code:       "manual_switch_name_conflict",
		},
		{
			name:       "standard managed bridge",
			standard:   networkModels.StandardSwitch{Name: "standard", BridgeName: "bridge31"},
			createName: "manual",
			bridge:     "bridge31",
			code:       "manual_switch_bridge_conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db := setupManualSwitchService(t)
			if err := db.Create(&test.standard).Error; err != nil {
				t.Fatalf("seed standard switch: %v", err)
			}

			_, err := svc.CreateManualSwitch(test.createName, test.bridge)
			if !errors.Is(err, ErrManualSwitchConflict) || ManualSwitchErrorCode(err) != test.code {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDeleteManualSwitchRejectsEveryKnownReference(t *testing.T) {
	tests := []struct {
		name      string
		insert    string
		errorCode string
	}{
		{name: "VM", insert: `INSERT INTO vm_networks (switch_id, switch_type) VALUES (?, 'manual')`, errorCode: "manual_switch_in_use_by_vm"},
		{name: "jail", insert: `INSERT INTO jail_networks (name, switch_id, switch_type) VALUES ('vnet0', ?, 'manual')`, errorCode: "manual_switch_in_use_by_jail"},
		{name: "DHCP config", insert: `INSERT INTO dhcp_manual_switches (dhcp_config_id, manual_switch_id) VALUES (1, ?)`, errorCode: "manual_switch_in_use_by_dhcp_config"},
		{name: "DHCP range", insert: `INSERT INTO dhcp_ranges (manual_switch_id) VALUES (?)`, errorCode: "manual_switch_in_use_by_dhcp_range"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db := setupManualSwitchService(t)
			switchModel := seedManualSwitch(t, db, "in-use", "bridge40")
			if err := db.Exec(test.insert, switchModel.ID).Error; err != nil {
				t.Fatalf("seed %s reference: %v", test.name, err)
			}

			err := svc.DeleteManualSwitch(switchModel.ID)
			if !errors.Is(err, ErrManualSwitchInUse) || ManualSwitchErrorCode(err) != test.errorCode {
				t.Fatalf("unexpected error: %v", err)
			}
			if err := db.First(&networkModels.ManualSwitch{}, switchModel.ID).Error; err != nil {
				t.Fatalf("referenced switch was deleted: %v", err)
			}
		})
	}
}

func TestManualSwitchDeleteAndUpdateReturnStableErrors(t *testing.T) {
	svc, db := setupManualSwitchService(t)
	if err := svc.DeleteManualSwitch(999999); !errors.Is(err, ErrManualSwitchNotFound) || ManualSwitchErrorCode(err) != "manual_switch_not_found" {
		t.Fatalf("unexpected missing-delete error: %v", err)
	}

	switchModel := seedManualSwitch(t, db, "editable", "bridge50")
	if err := db.Exec(`INSERT INTO dhcp_manual_switches (dhcp_config_id, manual_switch_id) VALUES (1, ?)`, switchModel.ID).Error; err != nil {
		t.Fatalf("seed DHCP config reference: %v", err)
	}
	if _, err := svc.UpdateManualSwitch(switchModel.ID, "renamed", "bridge51"); !errors.Is(err, ErrManualSwitchInUse) || ManualSwitchErrorCode(err) != "manual_switch_in_use_by_dhcp_config" {
		t.Fatalf("unexpected referenced-update error: %v", err)
	}
}

func TestManualSwitchReportsLiveVLANStateWithoutPersistingIt(t *testing.T) {
	svc, db := setupManualSwitchService(t)
	liveState := bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 10}
	syncInspectBridgeVLAN = func(bridge string) (bridgevlan.BridgeState, error) {
		if bridge != "bridge60" {
			t.Fatalf("inspected bridge = %q, want bridge60", bridge)
		}
		return liveState, nil
	}

	created, err := svc.CreateManualSwitch("filtered", "bridge60")
	if err != nil {
		t.Fatalf("create filtered manual switch: %v", err)
	}
	if !created.VLANStateAvailable || !created.VLANFiltering ||
		created.DefaultAccessVLAN == nil || *created.DefaultAccessVLAN != 10 {
		t.Fatalf("created live VLAN state = %#v", created)
	}
	if db.Migrator().HasColumn("manual_switches", "vlan_filtering") ||
		db.Migrator().HasColumn("manual_switches", "default_access_vlan") {
		t.Fatal("manual switch VLAN status was added to the database schema")
	}

	var persisted networkModels.ManualSwitch
	if err := db.First(&persisted, created.ID).Error; err != nil {
		t.Fatalf("load manual switch: %v", err)
	}
	if persisted.VLANFiltering || persisted.DefaultAccessVLAN != nil {
		t.Fatalf("database load unexpectedly contained runtime VLAN state: %#v", persisted)
	}

	liveState = bridgevlan.BridgeState{}
	switches, err := svc.GetManualSwitches()
	if err != nil {
		t.Fatalf("list manual switches: %v", err)
	}
	if len(switches) != 1 || !switches[0].VLANStateAvailable ||
		switches[0].VLANFiltering || switches[0].DefaultAccessVLAN != nil {
		t.Fatalf("manual switch did not reflect changed live state: %#v", switches)
	}
}

func TestGetManualSwitchesIsolatesVLANInspectionFailures(t *testing.T) {
	svc, db := setupManualSwitchService(t)
	seedManualSwitch(t, db, "healthy", "bridge-ok")
	seedManualSwitch(t, db, "broken", "bridge-broken")
	syncInspectBridgeVLAN = func(bridge string) (bridgevlan.BridgeState, error) {
		if bridge == "bridge-broken" {
			return bridgevlan.BridgeState{}, errors.New("ioctl failed")
		}
		return bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 30}, nil
	}

	switches, err := svc.GetManualSwitches()
	if err != nil {
		t.Fatalf("one failed inspection broke the list: %v", err)
	}
	if len(switches) != 2 {
		t.Fatalf("switch count = %d, want 2", len(switches))
	}
	byName := map[string]networkModels.ManualSwitch{}
	for _, sw := range switches {
		byName[sw.Name] = sw
	}
	healthy := byName["healthy"]
	if !healthy.VLANStateAvailable || !healthy.VLANFiltering ||
		healthy.DefaultAccessVLAN == nil || *healthy.DefaultAccessVLAN != 30 ||
		healthy.VLANStateError != "" {
		t.Fatalf("healthy state = %#v", healthy)
	}
	broken := byName["broken"]
	if broken.VLANStateAvailable || broken.VLANFiltering || broken.DefaultAccessVLAN != nil ||
		broken.VLANStateError != "manual_switch_vlan_state_unavailable" {
		t.Fatalf("broken state = %#v", broken)
	}
}

func TestManualSwitchVLANInspectionFailureStillCreatesObservationOnlyImport(t *testing.T) {
	svc, db := setupManualSwitchService(t)
	syncInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
		return bridgevlan.BridgeState{}, errors.New("ioctl failed")
	}

	created, err := svc.CreateManualSwitch("broken", "bridge61")
	if err != nil {
		t.Fatalf("create observation-only import: %v", err)
	}
	if created.VLANStateAvailable || created.VLANStateError != "manual_switch_vlan_state_unavailable" {
		t.Fatalf("unexpected unavailable state: %#v", created)
	}
	var count int64
	if err := db.Model(&networkModels.ManualSwitch{}).Count(&count).Error; err != nil {
		t.Fatalf("count manual switches: %v", err)
	}
	if count != 1 {
		t.Fatalf("manual switch import count = %d, want 1", count)
	}
}

func TestManualSwitchImportsUnsupportedLiveVLANStateAsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name  string
		state bridgevlan.BridgeState
		code  string
	}{
		{
			name:  "Q-in-Q",
			state: bridgevlan.BridgeState{VLANFiltering: true, DefaultQinQ: true},
			code:  "filtered_switch_qinq_unsupported",
		},
		{
			name:  "reserved default PVID",
			state: bridgevlan.BridgeState{VLANFiltering: true, DefaultPVID: 4095},
			code:  "invalid_manual_switch_default_pvid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, db := setupManualSwitchService(t)
			syncInspectBridgeVLAN = func(string) (bridgevlan.BridgeState, error) {
				return test.state, nil
			}

			created, err := svc.CreateManualSwitch("unsupported", "bridge62")
			if err != nil {
				t.Fatalf("create observation-only import: %v", err)
			}
			if created.VLANStateAvailable || created.VLANStateError != test.code {
				t.Fatalf("unexpected unavailable state: %#v", created)
			}
			var count int64
			if err := db.Model(&networkModels.ManualSwitch{}).Count(&count).Error; err != nil {
				t.Fatalf("count manual switches: %v", err)
			}
			if count != 1 {
				t.Fatalf("manual switch import count = %d, want 1", count)
			}
		})
	}
}
