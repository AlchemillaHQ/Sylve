// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

func boolPtr(v bool) *bool {
	return &v
}

func newDHCPServiceForTest(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	return newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPConfig{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
	)
}

func seedDHCPConfig(t *testing.T, db *gorm.DB, domain string, dnsServers []string, expandHosts bool) networkModels.DHCPConfig {
	t.Helper()
	config := networkModels.DHCPConfig{
		Domain:      domain,
		DNSServers:  append([]string{}, dnsServers...),
		ExpandHosts: expandHosts,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("seed DHCP config: %v", err)
	}
	return config
}

func configureDHCPRuntimeForTest(t *testing.T, svc *Service, initial string, restart func() error) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnsmasq.conf")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed dnsmasq config: %v", err)
	}
	svc.dhcpRuntime.configPath = path
	svc.dhcpRuntime.restart = restart
	return path
}

func TestGetConfigReturnsStableCollections(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	seedDHCPConfig(t, db, "lan", nil, true)

	config, err := svc.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if config.StandardSwitches == nil || config.ManualSwitches == nil || config.DNSServers == nil {
		t.Fatalf("expected non-nil collections, got %#v", config)
	}
}

func TestGetConfigPreloadsStandardSwitchPorts(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	config := seedDHCPConfig(t, db, "lan", nil, true)
	withPort := networkModels.StandardSwitch{Name: "with-port", BridgeName: "bridge0"}
	withoutPorts := networkModels.StandardSwitch{Name: "without-ports", BridgeName: "bridge1"}
	if err := db.Create(&withPort).Error; err != nil {
		t.Fatalf("seed standard switch with port: %v", err)
	}
	if err := db.Create(&withoutPorts).Error; err != nil {
		t.Fatalf("seed standard switch without ports: %v", err)
	}
	port := networkModels.NetworkPort{Name: "em0", SwitchID: withPort.ID}
	if err := db.Create(&port).Error; err != nil {
		t.Fatalf("seed standard switch port: %v", err)
	}
	if err := db.Model(&config).Association("StandardSwitches").Replace([]networkModels.StandardSwitch{withPort, withoutPorts}); err != nil {
		t.Fatalf("associate DHCP standard switches: %v", err)
	}

	got, err := svc.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if len(got.StandardSwitches) != 2 {
		t.Fatalf("expected two standard switches, got %d", len(got.StandardSwitches))
	}

	portsBySwitch := make(map[string][]networkModels.NetworkPort, len(got.StandardSwitches))
	for _, sw := range got.StandardSwitches {
		portsBySwitch[sw.Name] = sw.Ports
	}
	if len(portsBySwitch["with-port"]) != 1 || portsBySwitch["with-port"][0].Name != "em0" {
		t.Fatalf("expected preloaded port, got %#v", portsBySwitch["with-port"])
	}
	if portsBySwitch["without-ports"] == nil || len(portsBySwitch["without-ports"]) != 0 {
		t.Fatalf("expected an empty non-nil port collection, got %#v", portsBySwitch["without-ports"])
	}
}

func TestDHCPEmbeddedStandardSwitchesPreloadPorts(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	standard := networkModels.StandardSwitch{Name: "standard", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	port := networkModels.NetworkPort{Name: "em0", SwitchID: standard.ID}
	if err := db.Create(&port).Error; err != nil {
		t.Fatalf("seed standard switch port: %v", err)
	}
	rng := networkModels.DHCPRange{
		Type:             "ipv4",
		StartIP:          "192.0.2.10",
		EndIP:            "192.0.2.20",
		StandardSwitchID: &standard.ID,
	}
	if err := db.Create(&rng).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}
	lease := networkModels.DHCPStaticLease{Hostname: "client", DHCPRangeID: rng.ID}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("seed DHCP lease: %v", err)
	}

	ranges, err := svc.GetRanges()
	if err != nil {
		t.Fatalf("GetRanges returned error: %v", err)
	}
	if len(ranges) != 1 || ranges[0].StandardSwitch == nil || len(ranges[0].StandardSwitch.Ports) != 1 {
		t.Fatalf("expected range switch ports to be preloaded, got %#v", ranges)
	}

	leases, err := svc.GetLeases()
	if err != nil {
		t.Fatalf("GetLeases returned error: %v", err)
	}
	if len(leases.DB) != 1 || leases.DB[0].DHCPRange == nil || leases.DB[0].DHCPRange.StandardSwitch == nil || len(leases.DB[0].DHCPRange.StandardSwitch.Ports) != 1 {
		t.Fatalf("expected lease range switch ports to be preloaded, got %#v", leases.DB)
	}
}

func TestGetConfigRejectsMissingSingleton(t *testing.T) {
	svc, _ := newDHCPServiceForTest(t)
	if _, err := svc.GetConfig(); err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected record-not-found invariant error, got %v", err)
	}
}

func TestSaveConfigNoChangeSkipsRuntimeApply(t *testing.T) {
	tests := []struct {
		name        string
		expandHosts *bool
	}{
		{name: "explicit value", expandHosts: boolPtr(false)},
		{name: "omitted value", expandHosts: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db := newDHCPServiceForTest(t)
			seedDHCPConfig(t, db, "example.local", []string{}, false)
			svc.dhcpRuntime.atomicWriteFile = func(string, []byte, os.FileMode) error {
				t.Fatal("no-change update attempted to write dnsmasq config")
				return nil
			}
			svc.dhcpRuntime.restart = func() error {
				t.Fatal("no-change update attempted to restart dnsmasq")
				return nil
			}

			err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
				StandardSwitches: []uint{},
				ManualSwitches:   []uint{},
				DNSServers:       []string{},
				Domain:           "example.local",
				ExpandHosts:      test.expandHosts,
			})
			if err != nil {
				t.Fatalf("expected idempotent success, got %v", err)
			}
		})
	}
}

func TestSaveConfigNormalizesAndDeduplicatesInput(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	standard := networkModels.StandardSwitch{Name: "standard", BridgeName: "bridge0"}
	manual := networkModels.ManualSwitch{Name: "manual", Bridge: "bridge1"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual switch: %v", err)
	}
	config := seedDHCPConfig(t, db, "old.local", []string{"1.1.1.1"}, false)

	restartCalls := 0
	configPath := configureDHCPRuntimeForTest(t, svc, "old config\n", func() error {
		restartCalls++
		return nil
	})

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		StandardSwitches: []uint{standard.ID, standard.ID},
		ManualSwitches:   []uint{manual.ID, manual.ID},
		DNSServers:       []string{" 1.1.1.1 ", "2001:0db8::1", "2001:db8::1"},
		Domain:           " Example.Local ",
		ExpandHosts:      boolPtr(true),
	})
	if err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}
	if restartCalls != 1 {
		t.Fatalf("expected one dnsmasq restart, got %d", restartCalls)
	}

	var refreshed networkModels.DHCPConfig
	if err := db.Preload("StandardSwitches").Preload("ManualSwitches").First(&refreshed, config.ID).Error; err != nil {
		t.Fatalf("reload DHCP config: %v", err)
	}
	if refreshed.Domain != "example.local" || !refreshed.ExpandHosts {
		t.Fatalf("unexpected normalized scalar config: %#v", refreshed)
	}
	if !slices.Equal(refreshed.DNSServers, []string{"1.1.1.1", "2001:db8::1"}) {
		t.Fatalf("unexpected normalized DNS servers: %#v", refreshed.DNSServers)
	}
	if len(refreshed.StandardSwitches) != 1 || len(refreshed.ManualSwitches) != 1 {
		t.Fatalf("expected one association of each type, got standard=%d manual=%d", len(refreshed.StandardSwitches), len(refreshed.ManualSwitches))
	}

	rendered, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read rendered config: %v", err)
	}
	for _, expected := range []string{"interface=bridge0", "interface=bridge1", "server=2001:db8::1", "domain=example.local"} {
		if !strings.Contains(string(rendered), expected) {
			t.Errorf("rendered config missing %q:\n%s", expected, rendered)
		}
	}
}

func TestSaveConfigReturnsStableValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		req  networkServiceInterfaces.ModifyDHCPConfigRequest
		code string
	}{
		{
			name: "zero standard switch ID",
			req:  networkServiceInterfaces.ModifyDHCPConfigRequest{StandardSwitches: []uint{0}},
			code: "invalid_dhcp_standard_switch_id",
		},
		{
			name: "missing manual switch",
			req:  networkServiceInterfaces.ModifyDHCPConfigRequest{ManualSwitches: []uint{999}},
			code: "dhcp_manual_switch_not_found",
		},
		{
			name: "invalid DNS server",
			req:  networkServiceInterfaces.ModifyDHCPConfigRequest{DNSServers: []string{"not-an-ip"}},
			code: "invalid_dhcp_dns_server",
		},
		{
			name: "too many DNS servers",
			req: networkServiceInterfaces.ModifyDHCPConfigRequest{
				DNSServers: func() []string {
					servers := make([]string, MaxDHCPConfigDNSServers+1)
					for i := range servers {
						servers[i] = fmt.Sprintf("192.0.2.%d", i+1)
					}
					return servers
				}(),
			},
			code: "too_many_dhcp_dns_servers",
		},
		{
			name: "invalid domain",
			req:  networkServiceInterfaces.ModifyDHCPConfigRequest{Domain: "bad_domain"},
			code: "invalid_dhcp_domain",
		},
		{
			name: "domain too long",
			req:  networkServiceInterfaces.ModifyDHCPConfigRequest{Domain: strings.Repeat("a", MaxDHCPConfigDomainBytes+1)},
			code: "dhcp_domain_too_long",
		},
		{
			name: "too many switches",
			req: networkServiceInterfaces.ModifyDHCPConfigRequest{
				StandardSwitches: func() []uint {
					ids := make([]uint, MaxDHCPConfigSwitches+1)
					for i := range ids {
						ids[i] = uint(i + 1)
					}
					return ids
				}(),
			},
			code: "too_many_dhcp_config_switches",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db := newDHCPServiceForTest(t)
			seedDHCPConfig(t, db, "lan", []string{}, true)
			err := svc.SaveConfig(&test.req)
			if !errors.Is(err, ErrInvalidDHCPConfig) {
				t.Fatalf("expected validation error, got %v", err)
			}
			if code := DHCPConfigErrorCode(err); code != test.code {
				t.Fatalf("expected code %q, got %q", test.code, code)
			}
		})
	}
}

func TestSaveConfigRejectsRemovingSwitchReferencedByRange(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	standard := networkModels.StandardSwitch{Name: "standard", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	config := seedDHCPConfig(t, db, "lan", []string{}, true)
	if err := db.Model(&config).Association("StandardSwitches").Append(&standard); err != nil {
		t.Fatalf("associate DHCP switch: %v", err)
	}
	rangeModel := networkModels.DHCPRange{
		Type:             "ipv4",
		StartIP:          "192.0.2.10",
		EndIP:            "192.0.2.20",
		StandardSwitchID: &standard.ID,
	}
	if err := db.Create(&rangeModel).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		StandardSwitches: []uint{},
		ManualSwitches:   []uint{},
		DNSServers:       []string{},
		Domain:           "lan",
		ExpandHosts:      boolPtr(true),
	})
	if !errors.Is(err, ErrDHCPConfigConflict) || DHCPConfigErrorCode(err) != "dhcp_switch_has_ranges" {
		t.Fatalf("expected range-reference conflict, got %v", err)
	}

	var persisted networkModels.DHCPConfig
	if err := db.Preload("StandardSwitches").First(&persisted, config.ID).Error; err != nil {
		t.Fatalf("reload DHCP config: %v", err)
	}
	if len(persisted.StandardSwitches) != 1 || persisted.StandardSwitches[0].ID != standard.ID {
		t.Fatalf("referenced switch association changed: %#v", persisted.StandardSwitches)
	}
}

func TestSaveConfigRollsBackDatabaseWhenWriteFails(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	config := seedDHCPConfig(t, db, "old.local", []string{"1.1.1.1"}, false)
	configPath := configureDHCPRuntimeForTest(t, svc, "old config\n", func() error {
		t.Fatal("write failure should not restart dnsmasq")
		return nil
	})
	svc.dhcpRuntime.atomicWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("write failed")
	}

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		DNSServers:  []string{"8.8.8.8"},
		Domain:      "new.local",
		ExpandHosts: boolPtr(true),
	})
	if err == nil || !strings.Contains(err.Error(), "write_dhcp_config") {
		t.Fatalf("expected write error, got %v", err)
	}
	assertDHCPConfigUnchanged(t, db, config.ID, "old.local", []string{"1.1.1.1"}, false)
	data, readErr := os.ReadFile(configPath)
	if readErr != nil || string(data) != "old config\n" {
		t.Fatalf("expected old runtime config, data=%q err=%v", data, readErr)
	}
}

func TestSaveConfigRestoresDatabaseAndRuntimeWhenRestartFails(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	oldSwitch := networkModels.StandardSwitch{Name: "old-switch", BridgeName: "bridge0"}
	newSwitch := networkModels.StandardSwitch{Name: "new-switch", BridgeName: "bridge1"}
	if err := db.Create(&oldSwitch).Error; err != nil {
		t.Fatalf("seed old standard switch: %v", err)
	}
	if err := db.Create(&newSwitch).Error; err != nil {
		t.Fatalf("seed new standard switch: %v", err)
	}
	config := seedDHCPConfig(t, db, "old.local", []string{"1.1.1.1"}, false)
	if err := db.Model(&config).Association("StandardSwitches").Append(&oldSwitch); err != nil {
		t.Fatalf("seed old DHCP switch association: %v", err)
	}
	restartCalls := 0
	configPath := configureDHCPRuntimeForTest(t, svc, "old config\n", func() error {
		restartCalls++
		if restartCalls == 1 {
			return errors.New("restart failed")
		}
		return nil
	})

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		StandardSwitches: []uint{newSwitch.ID},
		DNSServers:       []string{"8.8.8.8"},
		Domain:           "new.local",
		ExpandHosts:      boolPtr(true),
	})
	if err == nil || !strings.Contains(err.Error(), "restart_dnsmasq") {
		t.Fatalf("expected restart error, got %v", err)
	}
	if restartCalls != 2 {
		t.Fatalf("expected failed candidate restart plus restored restart, got %d calls", restartCalls)
	}
	assertDHCPConfigUnchanged(t, db, config.ID, "old.local", []string{"1.1.1.1"}, false)
	var restored networkModels.DHCPConfig
	if err := db.Preload("StandardSwitches").First(&restored, config.ID).Error; err != nil {
		t.Fatalf("reload restored DHCP associations: %v", err)
	}
	if len(restored.StandardSwitches) != 1 || restored.StandardSwitches[0].ID != oldSwitch.ID {
		t.Fatalf("expected old switch association to be restored, got %#v", restored.StandardSwitches)
	}
	data, readErr := os.ReadFile(configPath)
	if readErr != nil || string(data) != "old config\n" {
		t.Fatalf("expected restored runtime config, data=%q err=%v", data, readErr)
	}
}

func assertDHCPConfigUnchanged(t *testing.T, db *gorm.DB, id uint, domain string, dnsServers []string, expandHosts bool) {
	t.Helper()
	var config networkModels.DHCPConfig
	if err := db.First(&config, id).Error; err != nil {
		t.Fatalf("reload DHCP config: %v", err)
	}
	if config.Domain != domain || config.ExpandHosts != expandHosts || !slices.Equal(config.DNSServers, dnsServers) {
		t.Fatalf("DHCP config changed unexpectedly: %#v", config)
	}
}
