// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alchemillahq/sylve/internal/bootstrap"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/network/bridgevlan"
	"github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/rs/zerolog"
)

func TestBootstrapCreatesSwitchWithMissingPort(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{}, &networkModels.ObjectEntry{})
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(string) (*iface.Interface, error) { return nil, errors.New("interface not found") },
		createBridge: func(sw networkModels.StandardSwitch) error {
			if len(sw.Ports) != 0 || !sw.DHCP || !sw.DefaultRoute {
				t.Fatalf("bootstrap lost switch configuration: %+v", sw)
			}
			return nil
		},
	})
	var logs bytes.Buffer
	originalLogger := logger.L
	logger.L = zerolog.New(&logs).Level(zerolog.WarnLevel)
	t.Cleanup(func() { logger.L = originalLogger })
	engine := bootstrap.NewService(nil, svc)
	source := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(source, []byte(`{"version":1,"switches":[{"type":"standard","standard":{
		"name":"missing-uplink","ports":["missing0"],"dhcp":true,"defaultRoute":true,
		"bridgeMac":{"mode":"port","port":"missing0"}}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	report := engine.Apply(t.Context(), source)
	if report.Failed() || !report.Archived || engine.SwitchStatus().Attempts != 1 {
		t.Fatalf("missing port prevented bootstrap: %+v", report)
	}
	switches, err := svc.GetStandardSwitches()
	if err != nil || len(switches) != 1 || switches[0].Ports == nil || len(switches[0].Ports) != 0 {
		t.Fatalf("portless switch not listed: %+v, err=%v", switches, err)
	}
	mac, err := desiredStandardSwitchMAC(switches[0])
	if err != nil {
		t.Fatal(err)
	}
	if hardware, err := net.ParseMAC(mac); err != nil || hardware[0]&3 != 2 {
		t.Fatalf("generated MAC is not locally administered unicast: %q, err=%v", mac, err)
	}
	encoded, err := json.Marshal(switches[0])
	if err != nil || !bytes.Contains(encoded, []byte(`"ports":[]`)) {
		t.Fatalf("empty ports not serialized as an array: %s, err=%v", encoded, err)
	}
	var event struct {
		Level       string   `json:"level"`
		Message     string   `json:"message"`
		SwitchID    uint     `json:"switchID"`
		Switch      string   `json:"switch"`
		PrunedPorts []string `json:"prunedPorts"`
		Ports       []string `json:"ports"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatalf("decode pruning warning: %v, logs=%s", err, logs.String())
	}
	if event.Level != "warn" || event.Message != "bootstrap_standard_switch_ports_pruned" ||
		event.SwitchID != switches[0].ID || event.Switch != "missing-uplink" ||
		!slices.Equal(event.PrunedPorts, []string{"missing0"}) || event.Ports == nil || len(event.Ports) != 0 {
		t.Fatalf("unexpected pruning warning: %+v", event)
	}
}

func TestNewStandardSwitchPrunesMissingPorts(t *testing.T) {
	for _, test := range []struct {
		name       string
		ports      []string
		sourcePort string
		objectMAC  bool
		filtered   bool
		wantPorts  []string
		wantSource string
	}{
		{name: "mixed implicit source", ports: []string{"missing0", "em1", "em0"}, wantPorts: []string{"em1", "em0"}, wantSource: "em1"},
		{name: "pruned source", ports: []string{"missing0", "em0"}, sourcePort: "missing0", wantPorts: []string{"em0"}, wantSource: "em0"},
		{name: "retained explicit source", ports: []string{"missing0", "em0", "em1"}, sourcePort: "em1", wantPorts: []string{"em0", "em1"}, wantSource: "em1"},
		{name: "retained object source", ports: []string{"missing0"}, objectMAC: true},
		{name: "existing ports unchanged", ports: []string{"em0", "em1"}, sourcePort: "em1", wantPorts: []string{"em0", "em1"}, wantSource: "em1"},
		{name: "filtered policies pruned", ports: []string{"missing0", "em0"}, filtered: true, wantPorts: []string{"em0"}, wantSource: "em0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{}, &networkModels.ObjectEntry{})
			stubSyncFunctions(t, syncStubSet{
				ifaceGet: func(name string) (*iface.Interface, error) {
					if name == "em0" || name == "em1" {
						return &iface.Interface{Name: name, Ether: testStandardSwitchMAC}, nil
					}
					return nil, errors.New("interface not found")
				},
				createBridge: func(networkModels.StandardSwitch) error { return nil },
			})
			config := StandardSwitchConfig{Ports: test.ports, DisableIPv6: true}
			if test.sourcePort != "" {
				config.MACSource = networkModels.StandardSwitchMACSource{Mode: "port", Port: test.sourcePort}
			}
			if test.objectMAC {
				config.MACSource = createTestStandardSwitchMACSource(t, svc)
			}
			if test.filtered {
				vlan := 20
				config.VLANConfig = networkModels.StandardSwitchVLANConfig{Filtering: true, PortPolicies: map[string]bridgevlan.PortPolicy{}}
				for _, port := range test.ports {
					config.VLANConfig.PortPolicies[port] = bridgevlan.PortPolicy{Mode: bridgevlan.ModeAccess, UntaggedVLAN: &vlan}
				}
			}
			id, err := svc.NewStandardSwitchWithContext(t.Context(), CreateStandardSwitchRequest{
				Name: "bootstrap-switch", PruneMissingPorts: true, StandardSwitchConfig: config,
			})
			if err != nil {
				t.Fatalf("create pruned switch: %v", err)
			}
			stored, err := svc.GetStandardSwitch(id)
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(stored.Ports))
			for _, port := range stored.Ports {
				names = append(names, port.Name)
				if test.filtered && port.VLANPolicy.Mode != bridgevlan.ModeAccess {
					t.Fatalf("surviving port lost its VLAN policy: %+v", port)
				}
			}
			if !slices.Equal(names, test.wantPorts) || stored.BridgeMACSourcePort != test.wantSource {
				t.Fatalf("ports=%v source=%q, want ports=%v source=%q", names, stored.BridgeMACSourcePort, test.wantPorts, test.wantSource)
			}
			mac, err := desiredStandardSwitchMAC(stored)
			if err != nil || mac != testStandardSwitchMAC {
				t.Fatalf("pruning changed the MAC source: mac=%q err=%v", mac, err)
			}
			if test.objectMAC && standardSwitchOptionalIDValue(stored.BridgeMACObjectID) != config.MACSource.MACObjectID {
				t.Fatal("pruning replaced an explicit MAC object")
			}
		})
	}
}

func TestBootstrapPrunesPortsOnExistingSwitch(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{}, &networkModels.ObjectEntry{})
	portPresent := true
	bridgePresent := false
	bridgeName := adoptionBridgeName("existing-bootstrap")
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if (name == "em0" && portPresent) || (name == bridgeName && bridgePresent) {
				return &iface.Interface{Name: name, Ether: testStandardSwitchMAC}, nil
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(networkModels.StandardSwitch) error {
			bridgePresent = true
			return nil
		},
		editBridge: func(networkModels.StandardSwitch, networkModels.StandardSwitch) error { return nil },
	})
	request := CreateStandardSwitchRequest{Name: "existing-bootstrap", PruneMissingPorts: true,
		StandardSwitchConfig: StandardSwitchConfig{Ports: []string{"em0"}, DisableIPv6: true,
			MACSource: networkModels.StandardSwitchMACSource{Mode: "port", Port: "em0"}}}
	id, err := svc.NewStandardSwitch(request)
	if err != nil {
		t.Fatal(err)
	}
	portPresent = false
	if again, err := svc.NewStandardSwitch(request); err != nil || again != id {
		t.Fatalf("bootstrap could not prune an existing switch: id=%d err=%v", again, err)
	}
	stored, err := svc.GetStandardSwitch(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Ports) != 0 || stored.BridgeMACMode != "object" || stored.BridgeMACObjectID == nil {
		t.Fatalf("existing switch did not receive a portless identity: %+v", stored)
	}
}

func TestPruneMissingSwitchPortsPropagatesInventoryErrors(t *testing.T) {
	inventoryErr := errors.New("inventory permission denied")
	stubSyncFunctions(t, syncStubSet{ifaceGet: func(string) (*iface.Interface, error) {
		return nil, inventoryErr
	}})
	_, _, err := pruneStandardSwitchMissingPorts(StandardSwitchConfig{Ports: []string{"em0"}})
	if !errors.Is(err, inventoryErr) {
		t.Fatalf("inventory failure was pruned: %v", err)
	}
}

func TestPortlessStandardSwitchKeepsMACAcrossRestartAndAttach(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.ManualSwitch{}, &networkModels.NetworkPort{}, &networkModels.ObjectEntry{})
	creates, edits := 0, 0
	bridgePresent := false
	var runtime networkModels.StandardSwitch
	stubSyncFunctions(t, syncStubSet{
		ifaceGet: func(name string) (*iface.Interface, error) {
			if name == "em0" {
				return &iface.Interface{Name: name, Ether: "02:00:00:00:00:99"}, nil
			}
			if bridgePresent && name == runtime.BridgeName {
				mac, err := desiredStandardSwitchMAC(runtime)
				return &iface.Interface{Name: name, Ether: mac}, err
			}
			return nil, errors.New("interface not found")
		},
		createBridge: func(sw networkModels.StandardSwitch) error {
			creates++
			runtime = sw
			bridgePresent = true
			return nil
		},
		editBridge: func(_, sw networkModels.StandardSwitch) error {
			edits++
			runtime = sw
			return nil
		},
	})
	config := StandardSwitchConfig{DisableIPv6: true, DHCP: true}
	request := CreateStandardSwitchRequest{Name: "portless", StandardSwitchConfig: config}
	id, err := svc.NewStandardSwitch(request)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.GetStandardSwitch(id)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := desiredStandardSwitchMAC(stored)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := svc.NewStandardSwitch(request); err != nil || again != id {
		t.Fatalf("repeat create: id=%d err=%v", again, err)
	}
	svc = &Service{DB: db}
	bridgePresent = false
	if err := svc.SyncStandardSwitches(); err != nil {
		t.Fatal(err)
	}
	config.Ports = []string{"em0"}
	if err := svc.EditStandardSwitch(UpdateStandardSwitchRequest{ID: id, StandardSwitchConfig: config}); err != nil {
		t.Fatalf("attach without a MAC source: %v", err)
	}
	if err := svc.SyncStandardSwitches(); err != nil {
		t.Fatal(err)
	}
	if creates != 2 || edits != 2 || len(runtime.Ports) != 1 || runtime.Ports[0].Name != "em0" || !runtime.DHCP {
		t.Fatalf("restart/attach lost desired state: creates=%d edits=%d runtime=%+v", creates, edits, runtime)
	}
	if got, err := desiredStandardSwitchMAC(runtime); err != nil || got != mac ||
		standardSwitchOptionalIDValue(runtime.BridgeMACObjectID) != standardSwitchOptionalIDValue(stored.BridgeMACObjectID) {
		t.Fatalf("restart/attach changed MAC: %q -> %q, err=%v", mac, got, err)
	}
}
