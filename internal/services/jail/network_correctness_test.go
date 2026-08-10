// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestLoadJailNetworkRejectsCrossJailMembership(t *testing.T) {
	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Network{},
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)
	first := jailModels.Jail{CTID: 7101, Name: "first", Type: jailModels.JailTypeFreeBSD}
	second := jailModels.Jail{CTID: 7102, Name: "second", Type: jailModels.JailTypeFreeBSD}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("seed first jail: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed second jail: %v", err)
	}
	switchRow := networkModels.StandardSwitch{Name: "membership-switch", BridgeName: "vm-membership"}
	if err := db.Create(&switchRow).Error; err != nil {
		t.Fatalf("seed switch: %v", err)
	}
	network := jailModels.Network{JailID: second.ID, Name: "second-net", SwitchID: switchRow.ID, SwitchType: "standard"}
	if err := db.Create(&network).Error; err != nil {
		t.Fatalf("seed network: %v", err)
	}

	_, err := loadJailNetwork(db, first.ID, network.ID)
	if err == nil || !strings.Contains(err.Error(), "network_not_found") {
		t.Fatalf("expected scoped network_not_found, got %v", err)
	}
}

func TestJailNetworkRCConfReplacementPreservesUnmanagedConfiguration(t *testing.T) {
	original := strings.Join([]string{
		`hostname="jail.example"`,
		`ifconfig_em0="DHCP"`,
		jailNetworkRCConfStart,
		`ifconfig_old="SYNCDHCP"`,
		jailNetworkRCConfEnd,
		`sshd_enable="YES"`,
		"",
	}, "\n")
	updated := replaceJailNetworkRCConfBlock(original, []string{`ifconfig_new="SYNCDHCP"`})
	for _, expected := range []string{`hostname="jail.example"`, `ifconfig_em0="DHCP"`, `sshd_enable="YES"`, `ifconfig_new="SYNCDHCP"`} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("expected %q to be preserved in %q", expected, updated)
		}
	}
	if strings.Contains(updated, `ifconfig_old="SYNCDHCP"`) {
		t.Fatalf("old managed block remained in %q", updated)
	}
}

func TestJailNetworkHookCleanupPreservesNonNetworkLines(t *testing.T) {
	svc := &Service{}
	original := strings.Join([]string{
		"#!/bin/sh",
		"rctl -a jail:test:memoryuse:deny=1G",
		"### Start Sylve-Managed Network ###",
		"ifconfig epair0a up",
		"### End Sylve-Managed Network ###",
		"cpuset -l 0 -j test",
		"",
	}, "\n")
	cleaned := svc.RemoveSylveNetworkFromHook(original)
	if strings.Contains(cleaned, "ifconfig epair0a up") {
		t.Fatalf("managed network command remained in %q", cleaned)
	}
	for _, expected := range []string{"rctl -a jail:test:memoryuse:deny=1G", "cpuset -l 0 -j test"} {
		if !strings.Contains(cleaned, expected) {
			t.Fatalf("expected non-network hook line %q to remain in %q", expected, cleaned)
		}
	}
}

func TestNormalizeJailNetworkValueRejectsWrongFamiliesAndNonEthernetMAC(t *testing.T) {
	for _, test := range []struct {
		role  jailNetworkObjectRole
		value string
	}{
		{role: jailNetworkIPv4GW, value: "2001:db8::1"},
		{role: jailNetworkIPv6GW, value: "192.0.2.1"},
		{role: jailNetworkMAC, value: "01:02:03:04:05:06:07:08"},
	} {
		if _, err := normalizeJailNetworkValue(test.role, test.value); err == nil {
			t.Fatalf("expected %s value %q to be rejected", test.role, test.value)
		}
	}
}
