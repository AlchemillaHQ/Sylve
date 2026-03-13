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

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestSaveConfigAllowsDottedDomain(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPConfig{},
	)

	cfg := networkModels.DHCPConfig{
		Domain:     "example.local",
		DNSServers: []string{},
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("failed to seed dhcp config: %v", err)
	}
	if err := db.Model(&cfg).Update("expand_hosts", false).Error; err != nil {
		t.Fatalf("failed to force expand_hosts=false: %v", err)
	}

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		StandardSwitches: []uint{},
		ManualSwitches:   []uint{},
		DNSServers:       []string{},
		Domain:           "example.local",
		ExpandHosts:      boolPtr(false),
	})
	if err == nil {
		t.Fatal("expected no_changes_detected error, got nil")
	}
	if err.Error() != "no_changes_detected" {
		t.Fatalf("expected no_changes_detected error, got %q", err.Error())
	}
}

func TestSaveConfigOmittedExpandHostsIsTreatedAsUnchanged(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StandardSwitch{},
		&networkModels.ManualSwitch{},
		&networkModels.DHCPConfig{},
	)

	cfg := networkModels.DHCPConfig{
		Domain:     "lab.local",
		DNSServers: []string{},
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("failed to seed dhcp config: %v", err)
	}
	if err := db.Model(&cfg).Update("expand_hosts", false).Error; err != nil {
		t.Fatalf("failed to force expand_hosts=false: %v", err)
	}

	err := svc.SaveConfig(&networkServiceInterfaces.ModifyDHCPConfigRequest{
		StandardSwitches: []uint{},
		ManualSwitches:   []uint{},
		DNSServers:       []string{},
		Domain:           "lab.local",
		ExpandHosts:      nil,
	})
	if err == nil {
		t.Fatal("expected no_changes_detected error, got nil")
	}
	if err.Error() != "no_changes_detected" {
		t.Fatalf("expected no_changes_detected error, got %q", err.Error())
	}

	var refreshed networkModels.DHCPConfig
	if err := db.First(&refreshed, cfg.ID).Error; err != nil {
		t.Fatalf("failed to reload dhcp config: %v", err)
	}
	if refreshed.ExpandHosts {
		t.Fatalf("expected ExpandHosts=false to remain unchanged, got %v", refreshed.ExpandHosts)
	}
}
