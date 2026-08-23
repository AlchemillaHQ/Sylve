// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

func stubWireGuardServerRuntime(t *testing.T) *fakeWireGuardRuntime {
	t.Helper()

	previousRunCommand := wireGuardRunCommand
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousHasAddress := wireGuardInterfaceHasAddress
	previousListInterfaces := wireGuardListInterfaces
	previousRuntimeOS := wireGuardRuntimeOS
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardInterfaceHasAddress = previousHasAddress
		wireGuardListInterfaces = previousListInterfaces
		wireGuardRuntimeOS = previousRuntimeOS
	})

	runtime := newFakeWireGuardRuntime()
	wireGuardRunCommand = runtime.runCommand
	wireGuardConfigureWithWGCtrl = func(string, wgtypes.Config) error {
		return nil
	}
	wireGuardInterfaceHasAddress = func(string, string) (bool, error) {
		return true, nil
	}
	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "bridge0"}}, nil
	}
	wireGuardRuntimeOS = "linux"
	return runtime
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
	svc.wireGuardUDPPortInUse = func(int) bool { return false }

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

func TestWireGuardServerInitRejectsOccupiedUDPPort(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
	)
	seedWireGuardServiceEnabled(t, db)

	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to reserve UDP port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	port := listener.LocalAddr().(*net.UDPAddr).Port
	err = svc.InitWireGuardServer(&InitWireGuardServerRequest{
		Port:      uint(port),
		Addresses: []string{"172.29.100.1/24"},
	})
	if err == nil || err.Error() != "wireguard_port_already_in_use" {
		t.Fatalf("expected occupied UDP port to be rejected, got %v", err)
	}
}

func TestWireGuardServerEnabledStateControlsManagedFirewallRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardServerRuntime(t)
	svc.wireGuardUDPPortInUse = func(int) bool { return false }

	if err := svc.InitWireGuardServer(&InitWireGuardServerRequest{
		Port:                    61820,
		Addresses:               []string{"172.29.100.1/24"},
		AllowWireGuardPort:      true,
		MasqueradeIPv4Interface: "bridge0",
	}); err != nil {
		t.Fatalf("initialize wireguard server: %v", err)
	}

	assertManagedWireGuardRuleCounts(t, db, 1, 1)
	if err := svc.SetWireGuardServerEnabled(false); err != nil {
		t.Fatalf("disable wireguard server: %v", err)
	}
	assertManagedWireGuardRuleCounts(t, db, 0, 0)
	if err := svc.SetWireGuardServerEnabled(false); err != nil {
		t.Fatalf("repeat disabled state: %v", err)
	}

	if err := svc.SetWireGuardServerEnabled(true); err != nil {
		t.Fatalf("enable wireguard server: %v", err)
	}
	assertManagedWireGuardRuleCounts(t, db, 1, 1)
	if err := svc.SetWireGuardServerEnabled(true); err != nil {
		t.Fatalf("repeat enabled state: %v", err)
	}
}

func TestWireGuardServiceDisableRemovesManagedFirewallRules(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardServerRuntime(t)
	svc.wireGuardUDPPortInUse = func(int) bool { return false }

	if err := svc.InitWireGuardServer(&InitWireGuardServerRequest{
		Port:                    61820,
		Addresses:               []string{"172.29.100.1/24"},
		AllowWireGuardPort:      true,
		MasqueradeIPv4Interface: "bridge0",
	}); err != nil {
		t.Fatalf("initialize wireguard server: %v", err)
	}
	assertManagedWireGuardRuleCounts(t, db, 1, 1)

	var basic models.BasicSettings
	if err := db.First(&basic).Error; err != nil {
		t.Fatal(err)
	}
	basic.Services = []models.AvailableService{}
	if err := db.Save(&basic).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableWireGuardService(context.Background()); err != nil {
		t.Fatalf("disable wireguard service: %v", err)
	}
	assertManagedWireGuardRuleCounts(t, db, 0, 0)

	var stored networkModels.WireGuardServer
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || !stored.AllowWireGuardPort || stored.MasqueradeIPv4Interface != "bridge0" {
		t.Fatalf("global service disable changed the saved server configuration: %+v", stored)
	}
}

func TestWireGuardServerEditRuntimeFailureRestoresPreviousState(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)
	seedWireGuardServiceEnabled(t, db)
	runtime := stubWireGuardServerRuntime(t)
	svc.wireGuardUDPPortInUse = func(int) bool { return false }

	if err := svc.InitWireGuardServer(&InitWireGuardServerRequest{
		Port:      61820,
		Addresses: []string{"172.29.100.1/24"},
	}); err != nil {
		t.Fatalf("initialize wireguard server: %v", err)
	}
	var previous networkModels.WireGuardServer
	if err := db.First(&previous).Error; err != nil {
		t.Fatal(err)
	}

	previousConfigure := wireGuardConfigureWithWGCtrl
	previousResolveBinary := wireGuardResolveWGBinaryPath
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigure
		wireGuardResolveWGBinaryPath = previousResolveBinary
	})
	failedNewConfig := false
	wireGuardConfigureWithWGCtrl = func(_ string, cfg wgtypes.Config) error {
		if cfg.ListenPort != nil && *cfg.ListenPort == 61999 && !failedNewConfig {
			failedNewConfig = true
			return errors.New("injected runtime apply failure")
		}
		return nil
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "", errors.New("wg binary unavailable")
	}

	err := svc.EditWireGuardServer(InitWireGuardServerRequest{
		Port:      61999,
		Addresses: []string{"172.29.200.1/24"},
	})
	if err == nil || !failedNewConfig {
		t.Fatalf("expected injected edit failure, got %v", err)
	}

	var restored networkModels.WireGuardServer
	if err := db.First(&restored, previous.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.Port != previous.Port ||
		len(restored.Addresses) != 1 || restored.Addresses[0] != previous.Addresses[0] ||
		restored.PrivateKey != previous.PrivateKey || restored.PublicKey != previous.PublicKey ||
		restored.MTU != previous.MTU || restored.Enabled != previous.Enabled {
		t.Fatalf("wireguard server was not restored after runtime failure: before=%+v after=%+v", previous, restored)
	}
	if !runtime.interfaceExists(wireGuardServerInterfaceName) {
		t.Fatal("previous wireguard runtime was not restored")
	}
}

func TestValidateWireGuardServerConfig(t *testing.T) {
	previousListInterfaces := wireGuardListInterfaces
	t.Cleanup(func() { wireGuardListInterfaces = previousListInterfaces })
	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "bridge0"}}, nil
	}

	key := mustGenerateWireGuardPrivateKey(t).String()
	valid := func() networkModels.WireGuardServer {
		return networkModels.WireGuardServer{
			Port:       51820,
			Addresses:  []string{"10.210.0.1/24", "fd00::1/64"},
			PrivateKey: key,
			MTU:        1420,
		}
	}
	tests := []struct {
		name   string
		mutate func(*networkModels.WireGuardServer)
		code   string
	}{
		{name: "invalid port", mutate: func(server *networkModels.WireGuardServer) { server.Port = 0 }, code: "wireguard_invalid_port"},
		{name: "addresses required", mutate: func(server *networkModels.WireGuardServer) { server.Addresses = nil }, code: "wireguard_addresses_required"},
		{name: "invalid mtu", mutate: func(server *networkModels.WireGuardServer) { server.MTU = 575 }, code: "wireguard_invalid_mtu"},
		{name: "invalid key", mutate: func(server *networkModels.WireGuardServer) { server.PrivateKey = "bad" }, code: "wireguard_invalid_private_key"},
		{name: "invalid address", mutate: func(server *networkModels.WireGuardServer) { server.Addresses = []string{"not-a-cidr"} }, code: "wireguard_invalid_address"},
		{name: "ipv6 mtu too small", mutate: func(server *networkModels.WireGuardServer) { server.MTU = 1200 }, code: "wireguard_ipv6_mtu_too_small"},
		{name: "ipv4 masquerade requires ipv4", mutate: func(server *networkModels.WireGuardServer) {
			server.Addresses = []string{"fd00::1/64"}
			server.MasqueradeIPv4Interface = "bridge0"
		}, code: "wireguard_masquerade_ipv4_requires_server_ipv4_cidr"},
		{name: "ipv6 masquerade requires ipv6", mutate: func(server *networkModels.WireGuardServer) {
			server.Addresses = []string{"10.210.0.1/24"}
			server.MasqueradeIPv6Interface = "bridge0"
		}, code: "wireguard_masquerade_ipv6_requires_server_ipv6_cidr"},
		{name: "invalid masquerade interface", mutate: func(server *networkModels.WireGuardServer) {
			server.MasqueradeIPv4Interface = "bad interface"
		}, code: "wireguard_invalid_masquerade_interface"},
		{name: "server cannot masquerade itself", mutate: func(server *networkModels.WireGuardServer) {
			server.MasqueradeIPv4Interface = wireGuardServerInterfaceName
		}, code: "wireguard_masquerade_interface_cannot_be_server"},
		{name: "masquerade interface must exist", mutate: func(server *networkModels.WireGuardServer) {
			server.MasqueradeIPv4Interface = "em9"
		}, code: "wireguard_masquerade_interface_not_found"},
	}

	svc := &Service{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := valid()
			test.mutate(&server)
			err := svc.validateWireGuardServerConfig(&server)
			if !errors.Is(err, ErrInvalidWireGuardServer) || WireGuardErrorCode(err) != test.code {
				t.Fatalf("expected %s, got %v (%s)", test.code, err, WireGuardErrorCode(err))
			}
		})
	}

	server := valid()
	server.MasqueradeIPv4Interface = "bridge0"
	server.MasqueradeIPv6Interface = "bridge0"
	if err := svc.validateWireGuardServerConfig(&server); err != nil {
		t.Fatalf("valid server configuration rejected: %v", err)
	}
}

func assertManagedWireGuardRuleCounts(t *testing.T, db *gorm.DB, trafficWant, natWant int64) {
	t.Helper()
	var trafficCount int64
	if err := db.Model(&networkModels.FirewallTrafficRule{}).
		Where("visible = ? AND name = ?", false, wireGuardManagedAllowRuleName).
		Count(&trafficCount).Error; err != nil {
		t.Fatal(err)
	}
	var natCount int64
	if err := db.Model(&networkModels.FirewallNATRule{}).
		Where("visible = ? AND name IN ?", false, []string{wireGuardManagedMasqV4RuleName, wireGuardManagedMasqV6RuleName}).
		Count(&natCount).Error; err != nil {
		t.Fatal(err)
	}
	if trafficCount != trafficWant || natCount != natWant {
		t.Fatalf("managed rule counts: traffic=%d want=%d nat=%d want=%d", trafficCount, trafficWant, natCount, natWant)
	}
}
