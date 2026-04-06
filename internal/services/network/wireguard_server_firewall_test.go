// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func stubWireGuardServerRuntime(t *testing.T) {
	t.Helper()

	previousRunCommand := wireGuardRunCommand
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	runtime := newFakeWireGuardRuntime()
	wireGuardRunCommand = runtime.runCommand
	wireGuardConfigureWithWGCtrl = func(string, wgtypes.Config) error {
		return nil
	}
	wireGuardInterfaceHasAddress = func(string, string) (bool, error) {
		return true, nil
	}
}

func TestWireGuardServerSyncsManagedHiddenFirewallRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardServerRuntime(t)

	if err := svc.InitWireGuardServer(&InitWireGuardServerRequest{
		Port:                    61820,
		Addresses:               []string{"172.29.100.1/24", "fd8b:d8b6:e92a::1/48"},
		AllowWireGuardPort:      true,
		MasqueradeIPv4Interface: "bridge0",
		MasqueradeIPv6Interface: "bridge0",
	}); err != nil {
		t.Fatalf("expected wireguard server init to succeed: %v", err)
	}

	var trafficRules []networkModels.FirewallTrafficRule
	if err := db.Order("priority asc, id asc").Find(&trafficRules).Error; err != nil {
		t.Fatalf("failed loading traffic rules: %v", err)
	}
	if len(trafficRules) != 1 {
		t.Fatalf("expected exactly one managed traffic rule, got %d", len(trafficRules))
	}
	if trafficRules[0].Visible {
		t.Fatalf("expected managed traffic rule to be hidden")
	}
	if trafficRules[0].Name != wireGuardManagedAllowRuleName || trafficRules[0].Priority != 1 {
		t.Fatalf("unexpected managed traffic rule: %+v", trafficRules[0])
	}

	var natRules []networkModels.FirewallNATRule
	if err := db.Order("priority asc, id asc").Find(&natRules).Error; err != nil {
		t.Fatalf("failed loading nat rules: %v", err)
	}
	if len(natRules) != 2 {
		t.Fatalf("expected two managed nat rules, got %d", len(natRules))
	}
	if natRules[0].Visible || natRules[1].Visible {
		t.Fatalf("expected managed nat rules to be hidden: %+v", natRules)
	}
	if natRules[0].Name != wireGuardManagedMasqV4RuleName || natRules[0].Priority != 1 {
		t.Fatalf("unexpected v4 nat rule: %+v", natRules[0])
	}
	if natRules[0].SourceRaw != "172.29.100.0/24" {
		t.Fatalf("expected v4 source subnet 172.29.100.0/24, got %q", natRules[0].SourceRaw)
	}
	if natRules[1].Name != wireGuardManagedMasqV6RuleName || natRules[1].Priority != 2 {
		t.Fatalf("unexpected v6 nat rule: %+v", natRules[1])
	}
	if natRules[1].SourceRaw != "fd8b:d8b6:e92a::/48" {
		t.Fatalf("expected v6 source subnet fd8b:d8b6:e92a::/48, got %q", natRules[1].SourceRaw)
	}

	if err := svc.EditWireGuardServer(InitWireGuardServerRequest{
		Port:                    61820,
		Addresses:               []string{"172.29.100.1/24", "fd8b:d8b6:e92a::1/48"},
		AllowWireGuardPort:      false,
		MasqueradeIPv4Interface: "",
		MasqueradeIPv6Interface: "",
	}); err != nil {
		t.Fatalf("expected wireguard server edit to succeed: %v", err)
	}

	var trafficCount int64
	if err := db.Model(&networkModels.FirewallTrafficRule{}).Count(&trafficCount).Error; err != nil {
		t.Fatalf("failed counting traffic rules: %v", err)
	}
	if trafficCount != 0 {
		t.Fatalf("expected managed traffic rule deletion after disable, got count=%d", trafficCount)
	}

	var natCount int64
	if err := db.Model(&networkModels.FirewallNATRule{}).Count(&natCount).Error; err != nil {
		t.Fatalf("failed counting nat rules: %v", err)
	}
	if natCount != 0 {
		t.Fatalf("expected managed nat rule deletion after disable, got count=%d", natCount)
	}
}
