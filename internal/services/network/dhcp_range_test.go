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
	"os"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

func dhcpUintPtr(value uint) *uint {
	return &value
}

func setupDHCPRangeService(
	t *testing.T,
	restart func() error,
) (*Service, *gorm.DB, networkModels.StandardSwitch, string) {
	t.Helper()
	svc, db := newDHCPServiceForTest(t)
	standard := networkModels.StandardSwitch{Name: "primary", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	config := seedDHCPConfig(t, db, "lan", []string{}, true)
	if err := db.Model(&config).Association("StandardSwitches").Append(&standard); err != nil {
		t.Fatalf("associate standard switch with DHCP config: %v", err)
	}
	path := configureDHCPRuntimeForTest(t, svc, "old config\n", restart)
	return svc, db, standard, path
}

func validCreateDHCPRangeRequest(switchID uint) networkServiceInterfaces.CreateDHCPRangeRequest {
	expiry := uint(43200)
	return networkServiceInterfaces.CreateDHCPRangeRequest{
		Type:           "ipv4",
		StartIP:        "192.0.2.10",
		EndIP:          "192.0.2.20",
		StandardSwitch: &switchID,
		Expiry:         &expiry,
	}
}

func validModifyDHCPRangeRequest(switchID uint) networkServiceInterfaces.ModifyDHCPRangeRequest {
	create := validCreateDHCPRangeRequest(switchID)
	return networkServiceInterfaces.ModifyDHCPRangeRequest{
		Type:           create.Type,
		StartIP:        create.StartIP,
		EndIP:          create.EndIP,
		StandardSwitch: create.StandardSwitch,
		Expiry:         create.Expiry,
	}
}

func TestNormalizeDHCPRangeRequestValidatesFamiliesAndRequiredFields(t *testing.T) {
	standardID := uint(1)
	manualID := uint(2)
	expiry := uint(0)
	trueValue := true

	tests := []struct {
		name string
		req  networkServiceInterfaces.CreateDHCPRangeRequest
		code string
	}{
		{name: "missing switch", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "192.0.2.1", EndIP: "192.0.2.2", Expiry: &expiry}, code: "dhcp_range_switch_required"},
		{name: "multiple switches", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "192.0.2.1", EndIP: "192.0.2.2", StandardSwitch: &standardID, ManualSwitch: &manualID, Expiry: &expiry}, code: "dhcp_range_multiple_switches"},
		{name: "missing expiry", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "192.0.2.1", EndIP: "192.0.2.2", StandardSwitch: &standardID}, code: "invalid_dhcp_range_expiry"},
		{name: "reversed IPv4", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "192.0.2.2", EndIP: "192.0.2.1", StandardSwitch: &standardID, Expiry: &expiry}, code: "invalid_dhcp_ipv4_range"},
		{name: "IPv4 with RA flag", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv4", StartIP: "192.0.2.1", EndIP: "192.0.2.2", StandardSwitch: &standardID, Expiry: &expiry, RAOnly: &trueValue}, code: "dhcp_ipv4_ra_flags_not_allowed"},
		{name: "half IPv6 range", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv6", StartIP: "2001:db8::1", StandardSwitch: &standardID, Expiry: &expiry}, code: "invalid_dhcp_ipv6_range"},
		{name: "equal IPv6 range", req: networkServiceInterfaces.CreateDHCPRangeRequest{Type: "ipv6", StartIP: "2001:db8::1", EndIP: "2001:db8::1", StandardSwitch: &standardID, Expiry: &expiry}, code: "invalid_dhcp_ipv6_range"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeDHCPRangeRequest(
				test.req.Type,
				test.req.StartIP,
				test.req.EndIP,
				test.req.StandardSwitch,
				test.req.ManualSwitch,
				test.req.Expiry,
				test.req.RAOnly,
				test.req.SLAAC,
			)
			if !errors.Is(err, ErrInvalidDHCPRange) || DHCPRangeErrorCode(err) != test.code {
				t.Fatalf("expected %q validation error, got %v (%q)", test.code, err, DHCPRangeErrorCode(err))
			}
		})
	}

	tooLarge := MaxDHCPRangeExpirySeconds + 1
	if uint64(uint(tooLarge)) == tooLarge {
		expiry := uint(tooLarge)
		_, err := normalizeDHCPRangeRequest("ipv4", "192.0.2.1", "192.0.2.2", &standardID, nil, &expiry, nil, nil)
		if !errors.Is(err, ErrInvalidDHCPRange) || DHCPRangeErrorCode(err) != "invalid_dhcp_range_expiry" {
			t.Fatalf("expected expiry-bound validation error, got %v", err)
		}
	}

	constructor, err := normalizeDHCPRangeRequest("ipv6", "", "", &standardID, nil, &expiry, &trueValue, &trueValue)
	if err != nil || constructor.startIP != "" || constructor.endIP != "" || constructor.expiry != 0 {
		t.Fatalf("expected valid infinite IPv6 constructor range, range=%#v error=%v", constructor, err)
	}
}

func TestCreateDHCPRangeRequiresConfiguredSwitchAndRejectsFamilyConflict(t *testing.T) {
	svc, db, configured, _ := setupDHCPRangeService(t, func() error { return nil })
	unconfigured := networkModels.StandardSwitch{Name: "other", BridgeName: "bridge1"}
	if err := db.Create(&unconfigured).Error; err != nil {
		t.Fatalf("seed unconfigured switch: %v", err)
	}

	if _, err := svc.CreateRange(&networkServiceInterfaces.CreateDHCPRangeRequest{
		Type:           "ipv4",
		StartIP:        "192.0.2.10",
		EndIP:          "192.0.2.20",
		StandardSwitch: &unconfigured.ID,
		Expiry:         dhcpUintPtr(0),
	}); !errors.Is(err, ErrDHCPRangeConflict) || DHCPRangeErrorCode(err) != "dhcp_switch_not_enabled" {
		t.Fatalf("expected unconfigured-switch conflict, got %v", err)
	}

	request := validCreateDHCPRangeRequest(configured.ID)
	id, err := svc.CreateRange(&request)
	if err != nil || id == 0 {
		t.Fatalf("create configured range: id=%d error=%v", id, err)
	}
	if _, err := svc.CreateRange(&request); !errors.Is(err, ErrDHCPRangeConflict) || DHCPRangeErrorCode(err) != "dhcp_switch_family_range_exists" {
		t.Fatalf("expected same-family conflict, got %v", err)
	}
}

func TestCreateDHCPRangePersistsExplicitInfiniteExpiry(t *testing.T) {
	svc, db, standard, _ := setupDHCPRangeService(t, func() error { return nil })
	request := validCreateDHCPRangeRequest(standard.ID)
	request.Expiry = dhcpUintPtr(0)

	id, err := svc.CreateRange(&request)
	if err != nil {
		t.Fatalf("create infinite range: %v", err)
	}
	var persisted networkModels.DHCPRange
	if err := db.First(&persisted, id).Error; err != nil {
		t.Fatalf("load created range: %v", err)
	}
	if persisted.Expiry != 0 {
		t.Fatalf("expected explicit infinite expiry to persist as zero, got %d", persisted.Expiry)
	}
}

func TestModifyDHCPRangeFamilyIsImmutableAndNoChangeSkipsRuntime(t *testing.T) {
	svc, db, standard, _ := setupDHCPRangeService(t, func() error { return nil })
	request := validCreateDHCPRangeRequest(standard.ID)
	rangeModel := networkModels.DHCPRange{
		Type:             request.Type,
		StartIP:          request.StartIP,
		EndIP:            request.EndIP,
		StandardSwitchID: request.StandardSwitch,
		Expiry:           *request.Expiry,
	}
	if err := db.Create(&rangeModel).Error; err != nil {
		t.Fatalf("seed DHCP range: %v", err)
	}

	modify := validModifyDHCPRangeRequest(standard.ID)
	modify.Type = "ipv6"
	modify.StartIP = ""
	modify.EndIP = ""
	if err := svc.ModifyRange(rangeModel.ID, &modify); !errors.Is(err, ErrDHCPRangeConflict) || DHCPRangeErrorCode(err) != "dhcp_range_type_immutable" {
		t.Fatalf("expected immutable-family conflict, got %v", err)
	}

	svc.dhcpRuntime.atomicWriteFile = func(string, []byte, os.FileMode) error {
		t.Fatal("no-change range update attempted to write dnsmasq config")
		return nil
	}
	svc.dhcpRuntime.restart = func() error {
		t.Fatal("no-change range update attempted to restart dnsmasq")
		return nil
	}
	modify = validModifyDHCPRangeRequest(standard.ID)
	if err := svc.ModifyRange(rangeModel.ID, &modify); err != nil {
		t.Fatalf("no-change update returned error: %v", err)
	}
}

func TestDHCPRangeMutationsRollBackDatabaseWhenRuntimeApplyFails(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		restartCalls := 0
		svc, db, standard, configPath := setupDHCPRangeService(t, func() error {
			restartCalls++
			if restartCalls == 1 {
				return errors.New("restart failed")
			}
			return nil
		})

		request := validCreateDHCPRangeRequest(standard.ID)
		if _, err := svc.CreateRange(&request); err == nil {
			t.Fatal("expected create runtime failure")
		}
		var count int64
		if err := db.Model(&networkModels.DHCPRange{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("created range survived rollback: count=%d error=%v", count, err)
		}
		assertDHCPRangeRuntimeRestored(t, configPath, restartCalls)
	})

	t.Run("modify", func(t *testing.T) {
		restartCalls := 0
		svc, db, standard, configPath := setupDHCPRangeService(t, func() error {
			restartCalls++
			if restartCalls == 1 {
				return errors.New("restart failed")
			}
			return nil
		})
		request := validCreateDHCPRangeRequest(standard.ID)
		rangeModel := networkModels.DHCPRange{
			Type:             request.Type,
			StartIP:          request.StartIP,
			EndIP:            request.EndIP,
			StandardSwitchID: request.StandardSwitch,
			Expiry:           *request.Expiry,
		}
		if err := db.Create(&rangeModel).Error; err != nil {
			t.Fatalf("seed DHCP range: %v", err)
		}

		modify := validModifyDHCPRangeRequest(standard.ID)
		modify.StartIP = "192.0.2.30"
		modify.EndIP = "192.0.2.40"
		if err := svc.ModifyRange(rangeModel.ID, &modify); err == nil {
			t.Fatal("expected modify runtime failure")
		}
		var persisted networkModels.DHCPRange
		if err := db.First(&persisted, rangeModel.ID).Error; err != nil {
			t.Fatalf("reload rolled-back range: %v", err)
		}
		if persisted.StartIP != request.StartIP || persisted.EndIP != request.EndIP {
			t.Fatalf("modified range survived rollback: %#v", persisted)
		}
		assertDHCPRangeRuntimeRestored(t, configPath, restartCalls)
	})

	t.Run("delete with leases", func(t *testing.T) {
		restartCalls := 0
		svc, db, standard, configPath := setupDHCPRangeService(t, func() error {
			restartCalls++
			if restartCalls == 1 {
				return errors.New("restart failed")
			}
			return nil
		})
		request := validCreateDHCPRangeRequest(standard.ID)
		rangeModel := networkModels.DHCPRange{
			Type:             request.Type,
			StartIP:          request.StartIP,
			EndIP:            request.EndIP,
			StandardSwitchID: request.StandardSwitch,
			Expiry:           *request.Expiry,
		}
		if err := db.Create(&rangeModel).Error; err != nil {
			t.Fatalf("seed DHCP range: %v", err)
		}
		lease := networkModels.DHCPStaticLease{Hostname: "client", DHCPRangeID: rangeModel.ID}
		if err := db.Create(&lease).Error; err != nil {
			t.Fatalf("seed DHCP lease: %v", err)
		}

		if err := svc.DeleteRange(rangeModel.ID); err == nil {
			t.Fatal("expected delete runtime failure")
		}
		if err := db.First(&networkModels.DHCPRange{}, rangeModel.ID).Error; err != nil {
			t.Fatalf("range deletion was not rolled back: %v", err)
		}
		if err := db.First(&networkModels.DHCPStaticLease{}, lease.ID).Error; err != nil {
			t.Fatalf("lease deletion was not rolled back: %v", err)
		}
		assertDHCPRangeRuntimeRestored(t, configPath, restartCalls)
	})
}

func assertDHCPRangeRuntimeRestored(t *testing.T, path string, restartCalls int) {
	t.Helper()
	if restartCalls != 2 {
		t.Fatalf("expected failed candidate restart and restored restart, got %d", restartCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old config\n" {
		t.Fatalf("expected restored runtime config, data=%q error=%v", data, err)
	}
}
