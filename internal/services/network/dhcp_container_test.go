// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"errors"
	"os"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func TestDHCPContainerScopedBindings(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "loopback-only", true: "selected-switch"}[selected], func(t *testing.T) {
			svc, db := newDHCPServiceForTest(t)
			config := seedDHCPConfig(t, db, "host.lan", []string{"192.0.2.53"}, true)
			wantInterface := "lo0"
			if selected {
				sw := networkModels.ManualSwitch{Name: "host", Bridge: "bridge9"}
				if err := db.Create(&sw).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&config).Association("ManualSwitches").Append(&sw); err != nil {
					t.Fatal(err)
				}
				wantInterface = sw.Bridge
			}
			path := configureDHCPRuntimeForTest(t, svc, "old", func() error { return nil })
			if err := svc.WriteDHCPConfig(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range []string{"interface=" + wantInterface + "\n", "bind-interfaces\n", "except-interface=sylc*\n", "server=192.0.2.53\n", "domain=host.lan\n", "expand-hosts\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in %s", want, text)
				}
			}
			if strings.Contains(text, "port=0") || strings.Contains(text, "pid-file=") || strings.Contains(text, "dhcp-leasefile=") || strings.Count(text, "except-interface=") != 1 {
				t.Fatalf("changed rc/DNS ownership: %s", text)
			}
		})
	}
}

func TestDHCPContainerBridgeSelectionFailsBeforeApply(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	seedDHCPConfig(t, db, "lan", nil, false)
	sw := networkModels.ManualSwitch{Name: "container", Bridge: "sylc01234567890"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	svc.dhcpRuntime.restart = func() error { t.Fatal("must not restart"); return nil }
	err := svc.SaveConfig(&networkInterfaces.ModifyDHCPConfigRequest{ManualSwitches: []uint{sw.ID}})
	if !errors.Is(err, ErrDHCPConfigConflict) || DHCPConfigErrorCode(err) != "dhcp_container_bridge_reserved" {
		t.Fatal(err)
	}
}

func TestDHCPContainerBridgeCannotBeSelectedThroughExistingRange(t *testing.T) {
	svc, db := newDHCPServiceForTest(t)
	config := seedDHCPConfig(t, db, "lan", nil, false)
	sw := networkModels.ManualSwitch{Name: "owned", Bridge: "sylc01234567890"}
	if err := db.Create(&sw).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&config).Association("ManualSwitches").Append(&sw); err != nil {
		t.Fatal(err)
	}
	svc.dhcpRuntime.restart = func() error { t.Fatal("container bridge range must not restart host dnsmasq"); return nil }
	expiry := uint(60)
	_, err := svc.CreateRange(&networkInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "10.77.0.10", EndIP: "10.77.0.20", ManualSwitch: &sw.ID, Expiry: &expiry})
	if !errors.Is(err, ErrDHCPConfigConflict) || DHCPConfigErrorCode(err) != "dhcp_container_bridge_reserved" {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&networkModels.DHCPRange{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("rejected range persisted", count, err)
	}
}
