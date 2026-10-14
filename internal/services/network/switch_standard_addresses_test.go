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
	"net"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/pkg/network/iface"
)

func TestNormalizeStandardSwitchCIDRs(t *testing.T) {
	tooMany := make([]string, MaxStandardSwitchAddresses+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("192.0.2.%d/24", i+1)
	}
	for _, test := range []struct {
		name   string
		family int
		values []string
		want   []string
		code   string
	}{
		{"ULA and GUA", 6, []string{" fd00::1/64 ", "2001:db8::1/64"}, []string{"fd00::1/64", "2001:db8::1/64"}, ""},
		{"IPv4 same and different subnets", 4, []string{"192.0.2.10/24", "192.0.2.11/24", "198.51.100.10/24"}, []string{"192.0.2.10/24", "192.0.2.11/24", "198.51.100.10/24"}, ""},
		{"canonical duplicates", 6, []string{"fd00:0:0::1/64", "fd00::1/64"}, []string{"fd00::1/64"}, ""},
		{"prefix conflict", 4, []string{"192.0.2.10/24", "192.0.2.10/32"}, nil, "standard_switch_address_prefix_conflict"},
		{"wrong family", 6, []string{"192.0.2.10/24"}, nil, "invalid_standard_switch_network6_manual"},
		{"empty alias", 4, []string{""}, nil, "invalid_standard_switch_network4_manual"},
		{"IPv4 network base", 4, []string{"192.0.2.0/24"}, nil, "invalid_standard_switch_network4_manual"},
		{"too many", 4, tooMany, nil, "standard_switch_too_many_addresses"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeStandardSwitchCIDRs(test.values, test.family)
			if test.code != "" {
				if !errors.Is(err, ErrInvalidStandardSwitch) || StandardSwitchErrorCode(err) != test.code {
					t.Fatalf("error = %v, want %s", err, test.code)
				}
				return
			}
			if err != nil || !slices.Equal(got, test.want) {
				t.Fatalf("addresses = %v, error = %v, want %v", got, err, test.want)
			}
		})
	}
}

func TestStandardSwitchNetworksResolvesAddressSources(t *testing.T) {
	objectID := uint(7)
	sw := networkModels.StandardSwitch{
		Addresses: standardSwitchAddresses(0,
			[]networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}, {CIDR: "192.0.2.11/24"}},
			[]networkModels.StandardSwitchAddressSource{{ObjectID: objectID}, {CIDR: "fd00::10/64"}, {CIDR: "fd00::11/64"}}),
		Network6Manual: "fd00::99/64",
	}
	sw.Addresses[2].Object = &networkModels.Object{Entries: []networkModels.ObjectEntry{
		{Value: "2001:db8::10/64"}, {Value: "fd00:0:0::10/64"},
	}}
	if got := sw.Networks(4); !slices.Equal(got, []string{"192.0.2.10/24", "192.0.2.11/24"}) {
		t.Fatalf("IPv4 addresses = %v", got)
	}
	if got := sw.Networks(6); !slices.Equal(got, []string{"2001:db8::10/64", "fd00::10/64", "fd00::11/64"}) {
		t.Fatalf("IPv6 addresses = %v", got)
	}
	if sw.Network(6) != "2001:db8::10/64" {
		t.Fatal("aliases changed the primary route CIDR")
	}
}

func TestStandardSwitchSourcesNormalizeAndRespectAutomaticModes(t *testing.T) {
	sources := []networkModels.StandardSwitchAddressSource{{ObjectID: 7}, {CIDR: "fd00:0:0::10/64"}, {CIDR: "fd00::10/64"}, {ObjectID: 7}}
	normalized, err := normalizeStandardSwitchAddressSources(sources, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(normalized, []networkModels.StandardSwitchAddressSource{{ObjectID: 7}, {CIDR: "fd00::10/64"}}) {
		t.Fatalf("normalized sources = %#v", normalized)
	}
	for _, mode := range []standardSwitchInput{
		{dhcp: true, slaac: true, addresses4: sources, addresses6: sources},
		{dhcp: true, disableIPv6: true, addresses4: sources, addresses6: sources},
	} {
		got, err := normalizeStandardSwitchInput(mode)
		if err != nil || len(got.addresses4) != 0 || len(got.addresses6) != 0 {
			t.Fatalf("automatic mode retained static sources: %#v, %v", got, err)
		}
	}
}

func TestStandardSwitchRejectsInvalidAddressSources(t *testing.T) {
	for _, sources := range [][]networkModels.StandardSwitchAddressSource{
		{{}},
		{{ObjectID: 7, CIDR: "192.0.2.10/24"}},
		{{CIDR: " "}},
	} {
		if _, err := normalizeStandardSwitchAddressSources(sources, 4); StandardSwitchErrorCode(err) != "invalid_standard_switch_address_source" {
			t.Fatalf("sources = %#v, error = %v", sources, err)
		}
	}
}

func TestStandardSwitchAddressListsOverrideLegacyInputs(t *testing.T) {
	legacy := standardSwitchInput{network6ID: 7, manual: networkModels.StandardSwitchManualAddresses{Network4: "192.0.2.10/24"}}
	got, err := normalizeStandardSwitchInput(legacy)
	if err != nil || !slices.Equal(got.addresses4, []networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}}) ||
		!slices.Equal(got.addresses6, []networkModels.StandardSwitchAddressSource{{ObjectID: 7}}) {
		t.Fatalf("legacy inputs = %#v, %v", got, err)
	}
	legacy.addresses4 = []networkModels.StandardSwitchAddressSource{}
	legacy.addresses6 = []networkModels.StandardSwitchAddressSource{}
	got, err = normalizeStandardSwitchInput(legacy)
	if err != nil || len(got.addresses4) != 0 || len(got.addresses6) != 0 || got.network6ID != 0 || got.manual.Network4 != "" {
		t.Fatalf("explicit empty address lists retained legacy settings: %#v, %v", got, err)
	}
}

func TestStandardSwitchAdoptionAcceptsConfiguredAliases(t *testing.T) {
	sw := networkModels.StandardSwitch{
		Addresses: standardSwitchAddresses(0,
			[]networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}, {CIDR: "192.0.2.11/24"}},
			[]networkModels.StandardSwitchAddressSource{{CIDR: "fd00::10/64"}, {CIDR: "2001:db8::10/64"}}),
	}
	live := &iface.Interface{
		IPv4: []iface.IPv4{
			{IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0"},
			{IP: net.ParseIP("192.0.2.11"), Netmask: "255.255.255.0"},
		},
		IPv6: []iface.IPv6{
			{IP: net.ParseIP("fd00::10"), PrefixLength: 64},
			{IP: net.ParseIP("2001:db8::10"), PrefixLength: 64},
		},
	}
	if err := validateStandardSwitchAdoptionLayer3(sw, live, "vm-test"); err != nil {
		t.Fatalf("configured aliases blocked adoption: %v", err)
	}
	live.IPv6 = append(live.IPv6, iface.IPv6{IP: net.ParseIP("2001:db8::11"), PrefixLength: 64})
	if err := validateStandardSwitchAdoptionLayer3(sw, live, "vm-test"); StandardSwitchErrorCode(err) != "standard_switch_runtime_address_conflict" {
		t.Fatalf("unmanaged address was not rejected: %v", err)
	}
}

func TestStandardSwitchMultipleAddressPersistenceAndRollback(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{})
	runtimeExists := false
	var applied networkModels.StandardSwitch
	var applyError error
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if runtimeExists {
				return &iface.Interface{Name: name}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error { applied = sw; return nil },
		editBridge: func(previous, desired networkModels.StandardSwitch) error {
			applied = desired
			return applyError
		},
		deleteBridge: func(networkModels.StandardSwitch) error { return nil },
	})
	config := StandardSwitchConfig{
		MTU: 1500, MACSource: createTestStandardSwitchMACSource(t, svc),
		Addresses4: []networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}, {CIDR: "192.0.2.11/24"}},
		Addresses6: []networkModels.StandardSwitchAddressSource{{CIDR: "fd00::10/64"}, {CIDR: "2001:db8::10/64"}},
	}
	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "multiple-addresses", StandardSwitchConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	assertAddresses := func(sw networkModels.StandardSwitch, alias4, alias6 string) {
		t.Helper()
		if !slices.Equal(sw.Networks(4), []string{"192.0.2.10/24", alias4}) ||
			!slices.Equal(sw.Networks(6), []string{"fd00::10/64", alias6}) {
			t.Fatalf("addresses = %v / %v", sw.Networks(4), sw.Networks(6))
		}
	}
	assertAddresses(applied, "192.0.2.11/24", "2001:db8::10/64")
	runtimeExists = true
	config.Addresses4[1].CIDR = "198.51.100.10/24"
	config.Addresses6[1].CIDR = "2001:db8::11/64"
	if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{ID: id, StandardSwitchConfig: config}); err != nil {
		t.Fatal(err)
	}
	stored, err := loadStandardSwitch(db, id)
	if err != nil {
		t.Fatal(err)
	}
	assertAddresses(stored, "198.51.100.10/24", "2001:db8::11/64")

	applyError = errors.New("forced alias apply failure")
	config.Addresses6[1].CIDR = "2001:db8::12/64"
	if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{ID: id, StandardSwitchConfig: config}); !errors.Is(err, applyError) {
		t.Fatalf("edit error = %v", err)
	}
	stored, err = loadStandardSwitch(db, id)
	if err != nil {
		t.Fatal(err)
	}
	assertAddresses(stored, "198.51.100.10/24", "2001:db8::11/64")
	assertAddresses(applied, "198.51.100.10/24", "2001:db8::11/64")

	applyError, runtimeExists = nil, false
	if err := svc.SyncStandardSwitches(); err != nil {
		t.Fatal(err)
	}
	assertAddresses(applied, "198.51.100.10/24", "2001:db8::11/64")

	runtimeExists = true
	config.Addresses4 = config.Addresses4[:1]
	config.Addresses6 = config.Addresses6[:1]
	if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{ID: id, StandardSwitchConfig: config}); err != nil {
		t.Fatal(err)
	}
	stored, err = loadStandardSwitch(db, id)
	if err != nil || len(stored.Addresses) != 2 {
		t.Fatalf("aliases were not cleared: %#v, %v", stored, err)
	}
}

func TestStandardSwitchAcceptsMultiCIDRObjectsAndValidatesEveryEntry(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{})
	var applied networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{createBridge: func(sw networkModels.StandardSwitch) error { applied = sw; return nil }})
	macSource := createTestStandardSwitchMACSource(t, svc)
	object := networkModels.Object{Name: "dual-ipv6", Type: "Network", Entries: []networkModels.ObjectEntry{
		{Value: "fd00::10/64"}, {Value: "2001:db8::10/64"},
	}}
	if err := db.Create(&object).Error; err != nil {
		t.Fatal(err)
	}
	config := StandardSwitchConfig{MTU: 1500, MACSource: macSource,
		Addresses6: []networkModels.StandardSwitchAddressSource{{ObjectID: object.ID}, {CIDR: "fd00::11/64"}},
	}
	if _, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "object-addresses", StandardSwitchConfig: config}); err != nil {
		t.Fatal(err)
	}
	if len(applied.Networks(6)) != 3 {
		t.Fatalf("not all object entries and aliases were applied: %v", applied.Networks(6))
	}
	if err := db.Create(&networkModels.ObjectEntry{ObjectID: object.ID, Value: "192.0.2.10/24"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "invalid-object-addresses", StandardSwitchConfig: config}); StandardSwitchErrorCode(err) != "invalid_standard_switch_network6_object" {
		t.Fatalf("mixed-family object error = %v", err)
	}
}

func TestStandardSwitchMultipleObjectsAndManualAddresses(t *testing.T) {
	svc, db := newDHCPObjectEditServiceForTest(t)
	objects := []networkModels.Object{
		{Name: "ipv4-primary", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "192.0.2.10/24"}, {Value: "192.0.2.11/24"}}},
		{Name: "ipv4-secondary", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "192.0.2.11/24"}, {Value: "198.51.100.10/24"}}},
		{Name: "ipv6-ula", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "fd00::10/64"}, {Value: "fd00::11/64"}}},
		{Name: "ipv6-gua", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "2001:db8::10/64"}, {Value: "2001:db8::11/64"}}},
	}
	for i := range objects {
		if err := db.Create(&objects[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	config := StandardSwitchConfig{
		MTU: 1500, MACSource: createTestStandardSwitchMACSource(t, svc),
		Addresses4: []networkModels.StandardSwitchAddressSource{{ObjectID: objects[0].ID}, {CIDR: "203.0.113.10/24"}, {ObjectID: objects[1].ID}},
		Addresses6: []networkModels.StandardSwitchAddressSource{{ObjectID: objects[2].ID}, {ObjectID: objects[3].ID}, {CIDR: "2001:db8::12/64"}},
	}
	var applied networkModels.StandardSwitch
	runtimeExists := false
	var applyError error
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if runtimeExists {
				return &iface.Interface{Name: name}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error { applied = sw; return nil },
		editBridge: func(previous, desired networkModels.StandardSwitch) error {
			applied = desired
			err := applyError
			applyError = nil
			return err
		},
		deleteBridge: func(networkModels.StandardSwitch) error { return nil },
	})
	id, err := svc.NewStandardSwitch(CreateStandardSwitchRequest{Name: "multiple-objects", StandardSwitchConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	assertConfiguration := func(sw networkModels.StandardSwitch) {
		t.Helper()
		if !slices.Equal(sw.AddressSources(4), config.Addresses4) || !slices.Equal(sw.AddressSources(6), config.Addresses6) {
			t.Fatalf("address sources were flattened or reordered: %v / %v", sw.AddressSources(4), sw.AddressSources(6))
		}
		if !slices.Equal(sw.Networks(4), []string{"192.0.2.10/24", "192.0.2.11/24", "203.0.113.10/24", "198.51.100.10/24"}) ||
			!slices.Equal(sw.Networks(6), []string{"fd00::10/64", "fd00::11/64", "2001:db8::10/64", "2001:db8::11/64", "2001:db8::12/64"}) {
			t.Fatalf("resolved addresses = %v / %v", sw.Networks(4), sw.Networks(6))
		}
	}
	assertConfiguration(applied)
	listed, err := svc.GetStandardSwitches()
	if err != nil || len(listed) != 1 {
		t.Fatalf("list switches = %v, %v", listed, err)
	}
	assertConfiguration(listed[0])
	if listed[0].NetworkID != nil || listed[0].Network6ID != nil || listed[0].NetworkManual != "" || listed[0].Network6Manual != "" {
		t.Fatal("new configuration was stored in legacy address fields")
	}
	for _, object := range objects {
		used, _, err := svc.IsObjectUsed(object.ID)
		if err != nil || !used {
			t.Fatalf("object %s is not marked used: %t, %v", object.Name, used, err)
		}
		if err := svc.DeleteObject(object.ID); NetworkObjectErrorCode(err) != "network_object_in_use" {
			t.Fatalf("deleting referenced object %s: %v", object.Name, err)
		}
	}

	runtimeExists = true
	failure := errors.New("forced object selection apply failure")
	applyError = failure
	changed := config
	changed.Addresses6 = []networkModels.StandardSwitchAddressSource{{CIDR: "fd00::20/64"}}
	if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{ID: id, StandardSwitchConfig: changed}); !errors.Is(err, failure) {
		t.Fatalf("edit error = %v", err)
	}
	stored, err := loadStandardSwitch(db, id)
	if err != nil {
		t.Fatal(err)
	}
	assertConfiguration(stored)
	assertConfiguration(applied)
	runtimeExists = false
	if err := svc.SyncStandardSwitches(); err != nil {
		t.Fatal(err)
	}
	assertConfiguration(applied)

	if err := svc.DeleteStandardSwitch(id); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := db.Model(&networkModels.StandardSwitchAddress{}).Where("switch_id = ?", id).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("deleted switch retained %d address sources: %v", remaining, err)
	}
	if err := svc.BulkDeleteObjects([]uint{objects[0].ID, objects[1].ID, objects[2].ID, objects[3].ID}); err != nil {
		t.Fatalf("deleted switch retained object references: %v", err)
	}
}

func TestStandardSwitchSecondaryObjectEdits(t *testing.T) {
	for _, family := range []int{4, 6} {
		t.Run(fmt.Sprintf("IPv%d", family), func(t *testing.T) {
			svc, db := newDHCPObjectEditServiceForTest(t)
			primary, secondary, manual, updated, conflict, wrongFamily := "192.0.2.10/24", "198.51.100.10/24", "203.0.113.10/24", "198.51.100.20/24", "203.0.113.10/32", "fd00::10/64"
			if family == 6 {
				primary, secondary, manual, updated, conflict, wrongFamily = "fd00::10/64", "2001:db8::10/64", "2001:db8::11/64", "2001:db8::20/64", "2001:db8::11/128", "192.0.2.10/24"
			}
			first := networkModels.Object{Name: "primary", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: primary}}}
			second := networkModels.Object{Name: "secondary", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: secondary}, {Value: primary}}}
			for _, object := range []*networkModels.Object{&first, &second} {
				if err := db.Create(object).Error; err != nil {
					t.Fatal(err)
				}
			}
			sources := []networkModels.StandardSwitchAddressSource{{ObjectID: first.ID}, {ObjectID: second.ID}, {CIDR: manual}}
			sw := networkModels.StandardSwitch{Name: "secondary-object", BridgeName: "vm-test", MTU: 1500}
			if family == 4 {
				sw.Addresses = standardSwitchAddresses(0, sources, nil)
			} else {
				sw.Addresses = standardSwitchAddresses(0, nil, sources)
			}
			if err := db.Create(&sw).Error; err != nil {
				t.Fatal(err)
			}
			var applied networkModels.StandardSwitch
			var applyError error
			transitions := 0
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) { return &iface.Interface{Name: name}, nil },
				editBridge: func(previous, desired networkModels.StandardSwitch) error {
					transitions++
					if previous.Network(family) != primary || desired.Network(family) != primary {
						t.Fatal("secondary object edit changed the primary route address")
					}
					applied = desired
					err := applyError
					applyError = nil
					return err
				},
			})
			if err := svc.EditObject(second.ID, second.Name, second.Type, []string{updated, primary}); err != nil {
				t.Fatal(err)
			}
			want := []string{primary, updated, manual}
			if transitions != 1 || !slices.Equal(applied.Networks(family), want) || !slices.Equal(applied.AddressSources(family), sources) {
				t.Fatalf("secondary object edit = %v, sources = %v, transitions = %d", applied.Networks(family), applied.AddressSources(family), transitions)
			}
			failure := errors.New("forced secondary object apply failure")
			applyError = failure
			if err := svc.EditObject(second.ID, second.Name, second.Type, []string{secondary}); !errors.Is(err, failure) {
				t.Fatalf("object edit error = %v", err)
			}
			restored, err := loadStandardSwitch(db, sw.ID)
			if err != nil || !slices.Equal(restored.Networks(family), want) || !slices.Equal(applied.Networks(family), want) || transitions != 3 {
				t.Fatalf("secondary object rollback = %v / %v, transitions = %d, %v", restored.Networks(family), applied.Networks(family), transitions, err)
			}
			for _, value := range []string{conflict, wrongFamily} {
				if err := svc.EditObject(second.ID, second.Name, second.Type, []string{value}); NetworkObjectErrorCode(err) != "network_object_switch_invalid_addresses" {
					t.Fatalf("invalid secondary object address %s was accepted: %v", value, err)
				}
			}
			if err := svc.EditObject(second.ID, second.Name, "Host", []string{"192.0.2.10"}); NetworkObjectErrorCode(err) != "network_object_type_in_use" {
				t.Fatalf("referenced secondary object changed type: %v", err)
			}
			if transitions != 3 {
				t.Fatalf("invalid edit changed switch runtime: %d transitions", transitions)
			}
			full := make([]string, MaxStandardSwitchAddresses)
			for i := range full {
				full[i] = fmt.Sprintf("198.51.100.%d/24", i+1)
				if family == 6 {
					full[i] = fmt.Sprintf("2001:db8::%x/64", i+1)
				}
			}
			if err := svc.EditObject(second.ID, second.Name, second.Type, full); NetworkObjectErrorCode(err) != "network_object_switch_invalid_addresses" {
				t.Fatalf("secondary object edit exceeded the combined address limit: %v", err)
			}
		})
	}
}

func TestStandardSwitchCombinedAddressValidation(t *testing.T) {
	svc, db := newNetworkServiceForTest(t)
	macSource := createTestStandardSwitchMACSource(t, svc)
	single := networkModels.Object{Name: "single-address", Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "fd00::1/64"}}}
	full := networkModels.Object{Name: "full-address-list", Type: "Network"}
	for i := 1; i <= MaxStandardSwitchAddresses; i++ {
		full.Entries = append(full.Entries, networkModels.ObjectEntry{Value: fmt.Sprintf("fd00::%x/64", i)})
	}
	for _, object := range []*networkModels.Object{&single, &full} {
		if err := db.Create(object).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name     string
		objectID uint
		aliases  []string
		code     string
	}{
		{"full object without aliases", full.ID, nil, ""},
		{"combined limit", full.ID, []string{"2001:db8::1/64"}, "standard_switch_too_many_addresses"},
		{"combined prefix conflict", single.ID, []string{"fd00::1/128"}, "standard_switch_address_prefix_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := []networkModels.StandardSwitchAddressSource{{ObjectID: test.objectID}}
			for _, value := range test.aliases {
				sources = append(sources, networkModels.StandardSwitchAddressSource{CIDR: value})
			}
			_, err := svc.validateStandardSwitchInput(0, "vm-test", standardSwitchInput{
				addresses6: sources, macSource: macSource,
			})
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if StandardSwitchErrorCode(err) != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestReconcileStandardSwitchStaticAddresses(t *testing.T) {
	for _, family := range []int{4, 6} {
		t.Run(fmt.Sprintf("IPv%d", family), func(t *testing.T) {
			var commands []string
			stubSyncFunctions(t, syncStubSet{runCommand: func(command string, args ...string) (string, error) {
				commands = append(commands, strings.Join(append([]string{command}, args...), " "))
				return "", nil
			}})
			af, primary, retained, removed, added := "inet", "192.0.2.10/24", "192.0.2.11/24", "198.51.100.10/24", "203.0.113.10/24"
			live := &iface.Interface{IPv4: []iface.IPv4{
				{IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0"},
				{IP: net.ParseIP("192.0.2.11"), Netmask: "255.255.255.0"},
				{IP: net.ParseIP("198.51.100.10"), Netmask: "255.255.255.0"},
			}}
			if family == 6 {
				af, primary, retained, removed, added = "inet6", "fd00::10/64", "2001:db8::10/64", "2001:db8:1::10/64", "2001:db8:2::10/64"
				live = &iface.Interface{IPv6: []iface.IPv6{
					{IP: net.ParseIP("fe80::10"), PrefixLength: 64},
					{IP: net.ParseIP("fd00::10"), PrefixLength: 64},
					{IP: net.ParseIP("2001:db8::10"), PrefixLength: 64},
					{IP: net.ParseIP("2001:db8:1::10"), PrefixLength: 64},
				}}
			}
			desired := []string{primary, retained, added}
			if err := reconcileStandardSwitchStaticAddresses("vm-test", family, live, []string{primary, retained, removed}, desired, false); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"/sbin/ifconfig vm-test " + af + " " + removed + " delete",
				"/sbin/ifconfig vm-test " + af + " " + added + " alias",
			}
			if !slices.Equal(commands, want) {
				t.Fatalf("commands = %v, want %v", commands, want)
			}
			commands = nil
			if err := reconcileStandardSwitchStaticAddresses("vm-test", family, &iface.Interface{}, nil, desired, false); err != nil {
				t.Fatal(err)
			}
			if len(commands) != 3 || commands[0] != "/sbin/ifconfig vm-test "+af+" "+primary ||
				commands[1] != "/sbin/ifconfig vm-test "+af+" "+retained+" alias" {
				t.Fatalf("create commands = %v", commands)
			}
			commands = nil
			if err := reconcileStandardSwitchStaticAddresses("vm-test", family, live, desired, []string{primary, retained, removed}, false); err != nil {
				t.Fatal(err)
			}
			if len(commands) != 0 {
				t.Fatalf("existing static addresses were reapplied: %v", commands)
			}
		})
	}
}

func TestEditStandardBridgeAliasesDoNotChangeRoutes(t *testing.T) {
	previous := withTestStandardSwitchMAC(networkModels.StandardSwitch{
		Name: "alias-routes", BridgeName: "vm-test", MTU: 1500,
		NetworkManual: "192.0.2.10/24", GatewayManual: "192.0.2.1", DefaultRoute: true,
		Network6Manual: "fd00::10/64", Gateway6Manual: "fe80::1", DefaultRoute6: true,
	})
	desired := previous
	desired.Addresses = standardSwitchAddresses(0,
		[]networkModels.StandardSwitchAddressSource{{CIDR: previous.Network(4)}, {CIDR: "192.0.2.11/24"}},
		[]networkModels.StandardSwitchAddressSource{{CIDR: previous.Network(6)}, {CIDR: "2001:db8::10/64"}})
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{Name: name, Description: previous.Name, MTU: 1500, Flags: iface.Flags{Raw: 1},
				IPv4: []iface.IPv4{{IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0"}},
				IPv6: []iface.IPv6{{IP: net.ParseIP("fd00::10"), PrefixLength: 64}},
			}, nil
		},
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})
	if err := editStandardBridge(previous, desired); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.HasPrefix(command, "/sbin/route") {
			t.Fatalf("alias-only edit changed routes: %v", commands)
		}
	}
	for _, want := range []string{"/sbin/ifconfig vm-test inet 192.0.2.11/24 alias", "/sbin/ifconfig vm-test inet6 2001:db8::10/64 alias"} {
		if !slices.Contains(commands, want) {
			t.Fatalf("missing %q: %v", want, commands)
		}
	}
}

func TestStandardSwitchStaticLinkLocalAddress(t *testing.T) {
	live := &iface.Interface{IPv6: []iface.IPv6{
		{IP: net.ParseIP("fe80::1"), PrefixLength: 64},
		{IP: net.ParseIP("fe80::2"), PrefixLength: 64},
	}}
	var commands []string
	stubSyncFunctions(t, syncStubSet{runCommand: func(command string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{command}, args...), " "))
		return "", nil
	}})
	if err := reconcileStandardSwitchStaticAddresses("vm-test", 6, live, []string{"fe80::2/64"}, []string{"fe80::2/64"}, true); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatalf("existing configured link-local address was reapplied: %v", commands)
	}
	if err := reconcileStandardSwitchStaticAddresses("vm-test", 6, live, []string{"fe80::2/64"}, []string{"fd00::10/64"}, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(commands, []string{
		"/sbin/ifconfig vm-test inet6 fe80::2%vm-test delete",
		"/sbin/ifconfig vm-test inet6 fd00::10/64",
	}) {
		t.Fatalf("managed link-local address change = %v", commands)
	}
}

func TestStandardSwitchObjectAliasEditAndRollback(t *testing.T) {
	svc, db := newDHCPObjectEditServiceForTest(t)
	object := networkModels.Object{Name: "switch-v6-addresses", Type: "Network", Entries: []networkModels.ObjectEntry{
		{Value: "2001:db8::10/64"}, {Value: "fd00::10/64"},
	}}
	if err := db.Create(&object).Error; err != nil {
		t.Fatal(err)
	}
	sw := networkModels.StandardSwitch{Name: "object-aliases", BridgeName: "vm-test", MTU: 1500, Network6ID: &object.ID}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	var applied networkModels.StandardSwitch
	var applyError error
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) { return &iface.Interface{Name: name}, nil },
		editBridge: func(previous, desired networkModels.StandardSwitch) error {
			if standardSwitchModelsMatch(previous, desired) {
				t.Fatal("secondary object entry change was treated as unchanged")
			}
			applied = desired
			err := applyError
			applyError = nil
			return err
		},
	})
	if err := svc.EditObject(object.ID, object.Name, object.Type, []string{"2001:db8::10/64", "fd00::11/64"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(applied.Networks(6), []string{"2001:db8::10/64", "fd00::11/64"}) {
		t.Fatalf("object edit addresses = %v", applied.Networks(6))
	}
	failure := errors.New("forced object alias failure")
	applyError = failure
	if err := svc.EditObject(object.ID, object.Name, object.Type, []string{"2001:db8::10/64", "fd00::12/64"}); !errors.Is(err, failure) {
		t.Fatalf("object edit error = %v", err)
	}
	restored, err := loadStandardSwitch(db, sw.ID)
	if err != nil || !slices.Equal(restored.Networks(6), []string{"2001:db8::10/64", "fd00::11/64"}) ||
		!slices.Equal(applied.Networks(6), restored.Networks(6)) {
		t.Fatalf("object alias rollback = %v / %v, %v", restored.Networks(6), applied.Networks(6), err)
	}
	if err := svc.EditObject(object.ID, object.Name, object.Type, []string{"192.0.2.10/24", "192.0.2.11/24"}); NetworkObjectErrorCode(err) != "network_object_switch_invalid_addresses" {
		t.Fatalf("wrong-family object edit error = %v", err)
	}
}

func TestFilteredStandardSwitchHostKeepsAllConfiguredAddresses(t *testing.T) {
	hostVLAN := 20
	sw := networkModels.StandardSwitch{
		BridgeName: "vm-test", MTU: 1500, VLANFiltering: true, HostVLAN: &hostVLAN,
		Addresses: standardSwitchAddresses(0,
			[]networkModels.StandardSwitchAddressSource{{CIDR: "192.0.2.10/24"}, {CIDR: "192.0.2.11/24"}},
			[]networkModels.StandardSwitchAddressSource{{CIDR: "fd00::10/64"}, {CIDR: "2001:db8::10/64"}}),
	}
	var commands []string
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			return &iface.Interface{
				Name: name, MTU: 1496, Groups: []string{standardSwitchHostVLANGroup}, VLANParent: sw.BridgeName, VLANTag: hostVLAN,
				IPv4: []iface.IPv4{
					{IP: net.ParseIP("192.0.2.10"), Netmask: "255.255.255.0"},
					{IP: net.ParseIP("192.0.2.11"), Netmask: "255.255.255.0"},
				},
				IPv6: []iface.IPv6{
					{IP: net.ParseIP("fe80::10"), PrefixLength: 64},
					{IP: net.ParseIP("fd00::10"), PrefixLength: 64},
					{IP: net.ParseIP("2001:db8::10"), PrefixLength: 64},
				},
			}, nil
		},
		stopDhclient: func(string) error { return nil },
		runCommand: func(command string, args ...string) (string, error) {
			commands = append(commands, strings.Join(append([]string{command}, args...), " "))
			return "", nil
		},
	})
	if err := reconcileFilteredStandardSwitchHost(sw, sw); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.Contains(command, " delete") || strings.Contains(command, " alias") {
			t.Fatalf("configured aliases or link-local address were changed: %v", commands)
		}
	}
	desired := sw
	desired.Addresses = slices.Clone(sw.Addresses)
	desired.Addresses[1].CIDR = "192.0.2.12/24"
	desired.Addresses[3].CIDR = "2001:db8::12/64"
	commands = nil
	if err := reconcileFilteredStandardSwitchHost(sw, desired); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/sbin/ifconfig vm-test.20 inet 192.0.2.11 delete",
		"/sbin/ifconfig vm-test.20 inet 192.0.2.12/24 alias",
		"/sbin/ifconfig vm-test.20 inet6 2001:db8::10 delete",
		"/sbin/ifconfig vm-test.20 inet6 2001:db8::12/64 alias -no_dad",
	} {
		if !slices.Contains(commands, want) {
			t.Fatalf("missing %q: %v", want, commands)
		}
	}
}
