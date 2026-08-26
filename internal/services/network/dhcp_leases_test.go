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
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

const (
	testDHCPMAC  = "aa:bb:cc:dd:ee:ff"
	testDHCPDUID = "00:01:00:01:2a:bc:de:f0"
)

type dhcpLeaseServiceFixture struct {
	svc        *Service
	db         *gorm.DB
	configPath string
	leasePath  string
	ipv4Range  networkModels.DHCPRange
	ipv6Range  networkModels.DHCPRange
	ipv4Object networkModels.Object
	ipv6Object networkModels.Object
	macObject  networkModels.Object
	duidObject networkModels.Object
}

func setupDHCPLeaseService(t *testing.T, restart func() error) dhcpLeaseServiceFixture {
	t.Helper()
	svc, db := newDHCPServiceForTest(t)
	standard := networkModels.StandardSwitch{Name: "primary", BridgeName: "bridge0"}
	if err := db.Create(&standard).Error; err != nil {
		t.Fatalf("seed standard switch: %v", err)
	}
	config := seedDHCPConfig(t, db, "lan", []string{}, true)
	if err := db.Model(&config).Association("StandardSwitches").Append(&standard); err != nil {
		t.Fatalf("associate standard switch: %v", err)
	}

	fixture := dhcpLeaseServiceFixture{svc: svc, db: db}
	fixture.ipv4Range = networkModels.DHCPRange{
		Type:             "ipv4",
		StartIP:          "192.0.2.10",
		EndIP:            "192.0.2.100",
		StandardSwitchID: &standard.ID,
	}
	fixture.ipv6Range = networkModels.DHCPRange{
		Type:             "ipv6",
		StartIP:          "2001:db8::10",
		EndIP:            "2001:db8::100",
		StandardSwitchID: &standard.ID,
	}
	for _, dhcpRange := range []*networkModels.DHCPRange{&fixture.ipv4Range, &fixture.ipv6Range} {
		if err := db.Create(dhcpRange).Error; err != nil {
			t.Fatalf("seed %s range: %v", dhcpRange.Type, err)
		}
	}

	fixture.ipv4Object = createDHCPLeaseObject(t, db, "host-v4", "Host", "192.0.2.20")
	fixture.ipv6Object = createDHCPLeaseObject(t, db, "host-v6", "Host", "2001:db8::20")
	fixture.macObject = createDHCPLeaseObject(t, db, "mac", "Mac", testDHCPMAC)
	fixture.duidObject = createDHCPLeaseObject(t, db, "duid", "DUID", testDHCPDUID)

	fixture.configPath = configureDHCPRuntimeForTest(t, svc, "old config\n", restart)
	fixture.leasePath = filepath.Join(t.TempDir(), "dnsmasq.leases")
	svc.dhcpRuntime.leasePath = fixture.leasePath
	return fixture
}

func createDHCPLeaseObject(t *testing.T, db *gorm.DB, name string, objectType string, value string) networkModels.Object {
	t.Helper()
	object := networkModels.Object{
		Name: name,
		Type: objectType,
		Entries: []networkModels.ObjectEntry{
			{Value: value},
		},
	}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("seed %s object: %v", objectType, err)
	}
	return object
}

func validIPv4StaticMapRequest(fixture dhcpLeaseServiceFixture) networkServiceInterfaces.CreateStaticMapRequest {
	return networkServiceInterfaces.CreateStaticMapRequest{
		Hostname:    "client-a",
		Comments:    "test lease",
		IPObjectID:  &fixture.ipv4Object.ID,
		MACObjectID: &fixture.macObject.ID,
		DHCPRangeID: fixture.ipv4Range.ID,
	}
}

func TestParseDHCPFileLeasesSkipsServerDUIDAndMalformedRows(t *testing.T) {
	leases, err := parseDHCPFileLeases([]byte(strings.Join([]string{
		"duid 00:01:00:01:aa:bb:cc:dd",
		"0 aa:bb:cc:dd:ee:ff 192.0.2.20 client-a *",
		"42 00000001 2001:db8::20 client-v6 00:01:00:01:2a:bc:de:f0",
		"not-a-lease",
		"invalid aa:bb:cc:dd:ee:00 192.0.2.30 ignored *",
	}, "\n")))
	if err != nil {
		t.Fatalf("parse leases: %v", err)
	}
	if len(leases) != 2 {
		t.Fatalf("expected two client leases, got %#v", leases)
	}
	if leases[0].Expiry != 0 || leases[0].MAC != testDHCPMAC || leases[0].IP != "192.0.2.20" || leases[0].Hostname != "client-a" {
		t.Fatalf("unexpected IPv4 lease: %#v", leases[0])
	}
	if leases[1].Expiry != 42 || leases[1].IAID != "00000001" || leases[1].DUID != testDHCPDUID || leases[1].IP != "2001:db8::20" {
		t.Fatalf("unexpected IPv6 lease: %#v", leases[1])
	}
}

func TestStaticMapValidationAndConflictsReturnTypedErrors(t *testing.T) {
	fixture := setupDHCPLeaseService(t, func() error { return nil })
	request := validIPv4StaticMapRequest(fixture)

	badHostname := request
	badHostname.Hostname = "-invalid"
	if _, err := fixture.svc.CreateStaticMap(&badHostname); !errors.Is(err, ErrInvalidDHCPLease) || DHCPLeaseErrorCode(err) != "invalid_dhcp_lease_hostname" {
		t.Fatalf("expected hostname validation error, got %v", err)
	}

	wrongFamily := request
	wrongFamily.IPObjectID = &fixture.ipv6Object.ID
	if _, err := fixture.svc.CreateStaticMap(&wrongFamily); !errors.Is(err, ErrInvalidDHCPLease) || DHCPLeaseErrorCode(err) != "dhcp_ip_object_family_mismatch" {
		t.Fatalf("expected family validation error, got %v", err)
	}

	missingRange := request
	missingRange.DHCPRangeID = 999
	if _, err := fixture.svc.CreateStaticMap(&missingRange); !errors.Is(err, ErrDHCPLeaseNotFound) || DHCPLeaseErrorCode(err) != "dhcp_range_not_found" {
		t.Fatalf("expected missing-range error, got %v", err)
	}

	seeded := networkModels.DHCPStaticLease{
		Hostname:    "CLIENT-A",
		IPObjectID:  &fixture.ipv4Object.ID,
		MACObjectID: &fixture.macObject.ID,
		DHCPRangeID: fixture.ipv4Range.ID,
	}
	if err := fixture.db.Create(&seeded).Error; err != nil {
		t.Fatalf("seed conflicting lease: %v", err)
	}
	if _, err := fixture.svc.CreateStaticMap(&request); !errors.Is(err, ErrDHCPLeaseConflict) || DHCPLeaseErrorCode(err) != "duplicate_hostname" {
		t.Fatalf("expected case-insensitive hostname conflict, got %v", err)
	}
}

func TestStaticMapCreateUpdateAndDeleteApplyRenderedConfig(t *testing.T) {
	restarts := 0
	fixture := setupDHCPLeaseService(t, func() error {
		restarts++
		return nil
	})
	request := validIPv4StaticMapRequest(fixture)
	id, err := fixture.svc.CreateStaticMap(&request)
	if err != nil || id == 0 {
		t.Fatalf("create static lease: id=%d error=%v", id, err)
	}
	assertFileContains(t, fixture.configPath, "dhcp-host="+testDHCPMAC+",192.0.2.20,client-a,infinite")

	modify := networkServiceInterfaces.ModifyStaticMapRequest{
		Hostname:     "client-v6",
		Comments:     "updated lease",
		IPObjectID:   &fixture.ipv6Object.ID,
		DUIDObjectID: &fixture.duidObject.ID,
		DHCPRangeID:  fixture.ipv6Range.ID,
	}
	if err := fixture.svc.ModifyStaticMap(id, &modify); err != nil {
		t.Fatalf("update static lease: %v", err)
	}
	var persisted networkModels.DHCPStaticLease
	if err := fixture.db.First(&persisted, id).Error; err != nil {
		t.Fatalf("load updated lease: %v", err)
	}
	if persisted.MACObjectID != nil || persisted.DUIDObjectID == nil || *persisted.DUIDObjectID != fixture.duidObject.ID {
		t.Fatalf("update did not replace identifier fields: %#v", persisted)
	}
	assertFileContains(t, fixture.configPath, "dhcp-host=id:"+testDHCPDUID+",[2001:db8::20],client-v6,infinite")

	if err := fixture.svc.DeleteStaticMap(id); err != nil {
		t.Fatalf("delete static lease: %v", err)
	}
	if err := fixture.db.First(&networkModels.DHCPStaticLease{}, id).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted lease remains in database: %v", err)
	}
	if restarts != 3 {
		t.Fatalf("expected one restart per mutation, got %d", restarts)
	}
}

func TestStaticMapCreateRollsBackDatabaseAndConfigOnRestartFailure(t *testing.T) {
	restartCalls := 0
	fixture := setupDHCPLeaseService(t, func() error {
		restartCalls++
		if restartCalls == 1 {
			return errors.New("restart failed")
		}
		return nil
	})
	request := validIPv4StaticMapRequest(fixture)
	if _, err := fixture.svc.CreateStaticMap(&request); err == nil {
		t.Fatal("expected restart failure")
	}
	var count int64
	if err := fixture.db.Model(&networkModels.DHCPStaticLease{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed create survived rollback: count=%d error=%v", count, err)
	}
	data, err := os.ReadFile(fixture.configPath)
	if err != nil || string(data) != "old config\n" {
		t.Fatalf("config snapshot was not restored: data=%q error=%v", data, err)
	}
	if restartCalls != 2 {
		t.Fatalf("expected failed apply and restoration restarts, got %d", restartCalls)
	}
}

func TestDeleteDynamicLeaseMatchesExactIdentifierAndIPPair(t *testing.T) {
	fixture := setupDHCPLeaseService(t, func() error { return nil })
	original := strings.Join([]string{
		"duid 00:01:00:01:aa:bb:cc:dd",
		"100 aa:bb:cc:dd:ee:ff 192.0.2.20 target *",
		"101 aa:bb:cc:dd:ee:ff 192.0.2.21 same-mac *",
		"102 aa:bb:cc:dd:ee:00 192.0.2.20 same-ip *",
		"103 00000001 2001:db8::20 target-v6 00:01:00:01:2a:bc:de:f0",
		"",
	}, "\n")
	if err := os.WriteFile(fixture.leasePath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed lease file: %v", err)
	}

	if err := fixture.svc.DeleteDynamicLease(&networkServiceInterfaces.DeleteDynamicLeaseRequest{
		Identifier: testDHCPMAC,
		IP:         "192.0.2.20",
	}); err != nil {
		t.Fatalf("delete exact IPv4 lease: %v", err)
	}
	data, err := os.ReadFile(fixture.leasePath)
	if err != nil {
		t.Fatalf("read updated lease file: %v", err)
	}
	updated := string(data)
	if strings.Contains(updated, "192.0.2.20 target *") || !strings.Contains(updated, "192.0.2.21 same-mac *") || !strings.Contains(updated, "192.0.2.20 same-ip *") {
		t.Fatalf("dynamic deletion did not preserve non-matching rows:\n%s", updated)
	}
	if !strings.Contains(updated, "duid 00:01:00:01:aa:bb:cc:dd") {
		t.Fatalf("dynamic deletion removed the server DUID row:\n%s", updated)
	}

	if err := fixture.svc.DeleteDynamicLease(&networkServiceInterfaces.DeleteDynamicLeaseRequest{
		Identifier: testDHCPDUID,
		IP:         "2001:db8::20",
	}); err != nil {
		t.Fatalf("delete exact IPv6 lease: %v", err)
	}
}

func TestDeleteDynamicLeaseRestoresFileOnRestartFailure(t *testing.T) {
	restartCalls := 0
	fixture := setupDHCPLeaseService(t, func() error {
		restartCalls++
		if restartCalls == 1 {
			return errors.New("restart failed")
		}
		return nil
	})
	original := []byte("100 aa:bb:cc:dd:ee:ff 192.0.2.20 target *\n")
	if err := os.WriteFile(fixture.leasePath, original, 0o644); err != nil {
		t.Fatalf("seed lease file: %v", err)
	}

	err := fixture.svc.DeleteDynamicLease(&networkServiceInterfaces.DeleteDynamicLeaseRequest{
		Identifier: testDHCPMAC,
		IP:         "192.0.2.20",
	})
	if err == nil {
		t.Fatal("expected restart failure")
	}
	restored, readErr := os.ReadFile(fixture.leasePath)
	if readErr != nil || string(restored) != string(original) {
		t.Fatalf("lease snapshot was not restored: data=%q error=%v", restored, readErr)
	}
	if restartCalls != 2 {
		t.Fatalf("expected failed apply and restoration restarts, got %d", restartCalls)
	}
}

func TestGetLeasesReturnsConcreteCollectionsWhenLeaseFileIsMissing(t *testing.T) {
	fixture := setupDHCPLeaseService(t, func() error { return nil })
	leases, err := fixture.svc.GetLeases()
	if err != nil {
		t.Fatalf("get leases: %v", err)
	}
	if leases.File == nil || leases.DB == nil || len(leases.File) != 0 || len(leases.DB) != 0 {
		t.Fatalf("expected empty concrete collections, got %#v", leases)
	}
}

func TestMapDBErrMapsDuplicateConstraintErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "sqlite mac unique",
			err:  fmt.Errorf("UNIQUE constraint failed: dhcp_static_leases.mac_object_id, dhcp_static_leases.dhcp_range_id"),
			want: "duplicate_mac_in_range",
		},
		{
			name: "sqlite duid unique",
			err:  fmt.Errorf("UNIQUE constraint failed: dhcp_static_leases.d_uid_object_id, dhcp_static_leases.dhcp_range_id"),
			want: "duplicate_duid_in_range",
		},
		{
			name: "named ip constraint",
			err:  fmt.Errorf(`duplicate key value violates unique constraint "uniq_l_ip_per_range"`),
			want: "duplicate_ip_in_range",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mapDBErr(test.err)
			if got == nil || DHCPLeaseErrorCode(got) != test.want {
				t.Fatalf("expected %q, got %v (%q)", test.want, got, DHCPLeaseErrorCode(got))
			}
		})
	}
}

func assertFileContains(t *testing.T, path string, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), expected) {
		t.Fatalf("expected %q in rendered config:\n%s", expected, data)
	}
}
