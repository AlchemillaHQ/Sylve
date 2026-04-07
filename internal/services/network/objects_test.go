// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
)

func TestEditObject_UsedFirewallListUpdatesEntriesAndResolutions(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			_, _ = w.Write([]byte("198.51.100.1\n"))
		case "/new":
			_, _ = w.Write([]byte("203.0.113.2\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	listID, err := svc.CreateObject("firewall-list", "List", []string{server.URL + "/old"})
	if err != nil {
		t.Fatalf("failed to create list object: %v", err)
	}

	sourceObjID := listID
	rule := networkModels.FirewallTrafficRule{
		Name:        "uses-list-object",
		Enabled:     true,
		Priority:    1000,
		Action:      "block",
		Direction:   "in",
		Protocol:    "any",
		Family:      "inet",
		SourceObjID: &sourceObjID,
		DestRaw:     "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create firewall rule using list object: %v", err)
	}

	if err := svc.EditObject(listID, "firewall-list", "List", []string{server.URL + "/new"}); err != nil {
		t.Fatalf("expected list object edit to succeed: %v", err)
	}

	var updated networkModels.Object
	if err := db.Preload("Entries").Preload("Resolutions").First(&updated, listID).Error; err != nil {
		t.Fatalf("failed to load updated list object: %v", err)
	}
	if err := svc.hydrateListSnapshotResolutions(&updated); err != nil {
		t.Fatalf("failed to hydrate list snapshot values: %v", err)
	}

	if len(updated.Entries) != 1 || updated.Entries[0].Value != server.URL+"/new" {
		t.Fatalf("expected list entries to be updated, got: %+v", updated.Entries)
	}

	if len(updated.Resolutions) != 1 || updated.Resolutions[0].ResolvedValue != "203.0.113.2" {
		t.Fatalf("expected refreshed list resolutions to be updated, got: %+v", updated.Resolutions)
	}
}

func TestEditObject_UsedFirewallPortUpdatesEntries(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)

	portID, err := svc.CreateObject("web-port", "Port", []string{"80"})
	if err != nil {
		t.Fatalf("failed to create port object: %v", err)
	}

	dstPortObjID := portID
	rule := networkModels.FirewallTrafficRule{
		Name:         "uses-port-object",
		Enabled:      true,
		Priority:     1000,
		Action:       "pass",
		Direction:    "in",
		Protocol:     "tcp",
		Family:       "inet",
		SourceRaw:    "any",
		DestRaw:      "any",
		DstPortObjID: &dstPortObjID,
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create firewall rule using port object: %v", err)
	}

	if err := svc.EditObject(portID, "web-port", "Port", []string{"443"}); err != nil {
		t.Fatalf("expected port object edit to succeed: %v", err)
	}

	var updated networkModels.Object
	if err := db.Preload("Entries").First(&updated, portID).Error; err != nil {
		t.Fatalf("failed to load updated port object: %v", err)
	}

	if len(updated.Entries) != 1 || updated.Entries[0].Value != "443" {
		t.Fatalf("expected port entries to be updated, got: %+v", updated.Entries)
	}
}

func TestCreateObject_PortSupportsSinglesAndRanges(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	id, err := svc.CreateObject("mixed-port-object", "Port", []string{"80", "8000:9000", "443"})
	if err != nil {
		t.Fatalf("failed to create mixed port object: %v", err)
	}

	var created networkModels.Object
	if err := db.Preload("Entries").First(&created, id).Error; err != nil {
		t.Fatalf("failed to load created port object: %v", err)
	}

	got := make(map[string]struct{}, len(created.Entries))
	for _, entry := range created.Entries {
		got[entry.Value] = struct{}{}
	}

	want := []string{"80", "8000:9000", "443"}
	for _, value := range want {
		if _, ok := got[value]; !ok {
			t.Fatalf("expected created port object to include %q, got entries: %+v", value, created.Entries)
		}
	}
}

func TestCreateObject_PortRejectsGroupedValues(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	tests := []struct {
		name   string
		values []string
	}{
		{
			name:   "comma grouped",
			values: []string{"80,443"},
		},
		{
			name:   "space grouped",
			values: []string{"80 443"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateObject("invalid-port-"+tt.name, "Port", tt.values)
			if err == nil {
				t.Fatalf("expected grouped port value to be rejected for %q", tt.name)
			}
			if !strings.Contains(err.Error(), "invalid port value") {
				t.Fatalf("expected invalid port value error, got: %v", err)
			}
		})
	}
}

func TestCreateObject_DynamicRefreshFailureRollsBackObject(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	_, err := svc.CreateObject("bad-list", "List", []string{"http://127.0.0.1:1/list.txt"})
	if err == nil {
		t.Fatal("expected create to fail when list refresh fails")
	}

	var count int64
	if err := db.Model(&networkModels.Object{}).Where("name = ?", "bad-list").Count(&count).Error; err != nil {
		t.Fatalf("failed to count objects: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected failed create to rollback object row, count=%d", count)
	}
}

func TestEditObject_DynamicRefreshFailureRollsBackEntriesAndResolutions(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("198.51.100.11\n"))
	}))
	defer server.Close()

	listID, err := svc.CreateObject("list-refresh", "List", []string{server.URL})
	if err != nil {
		t.Fatalf("failed to create list object: %v", err)
	}

	sourceObjID := listID
	rule := networkModels.FirewallTrafficRule{
		Name:        "uses-list-object",
		Enabled:     true,
		Priority:    1,
		Action:      "block",
		Direction:   "in",
		Protocol:    "any",
		Family:      "inet",
		SourceObjID: &sourceObjID,
		DestRaw:     "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create firewall rule using list object: %v", err)
	}

	if err := svc.EditObject(listID, "list-refresh", "List", []string{"http://127.0.0.1:1/new.txt"}); err == nil {
		t.Fatal("expected list edit to fail when refresh fails")
	}

	var updated networkModels.Object
	if err := db.Preload("Entries").Preload("Resolutions").First(&updated, listID).Error; err != nil {
		t.Fatalf("failed to load list object: %v", err)
	}
	if err := svc.hydrateListSnapshotResolutions(&updated); err != nil {
		t.Fatalf("failed to hydrate list snapshot values: %v", err)
	}

	if len(updated.Entries) != 1 || updated.Entries[0].Value != server.URL {
		t.Fatalf("expected list entries to rollback to previous value, got: %+v", updated.Entries)
	}

	if len(updated.Resolutions) != 1 || updated.Resolutions[0].ResolvedValue != "198.51.100.11" {
		t.Fatalf("expected resolutions to rollback to previous values, got: %+v", updated.Resolutions)
	}
}

func TestEditObject_FirewallApplyFailureRollsBackObject(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.FirewallAdvancedSettings{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
	)

	settings := models.BasicSettings{
		Services: []models.AvailableService{models.Firewall},
	}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatalf("failed to create basic settings: %v", err)
	}
	if err := db.Create(&networkModels.FirewallAdvancedSettings{PreRules: "", PostRules: ""}).Error; err != nil {
		t.Fatalf("failed to seed firewall advanced settings: %v", err)
	}

	object := networkModels.Object{
		Name: "web-port",
		Type: "Port",
		Entries: []networkModels.ObjectEntry{
			{Value: "80"},
		},
	}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("failed to seed object: %v", err)
	}

	previousRCPath := firewallRCConfPath
	firewallRCConfPath = filepath.Join(t.TempDir(), "rc.conf")
	t.Cleanup(func() {
		firewallRCConfPath = previousRCPath
	})

	previousRunCommand := firewallRunCommand
	firewallRunCommand = func(command string, args ...string) (string, error) {
		switch command {
		case "/sbin/kldstat":
			return "", nil
		case "/sbin/pfctl":
			if len(args) > 0 && args[0] == "-nf" {
				return "", fmt.Errorf("command execution failed: exit status 1, output: /tmp/pf.conf:1: syntax error")
			}
			if len(args) > 0 && args[0] == "-si" {
				return "", fmt.Errorf("pf disabled")
			}
			return "", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		firewallRunCommand = previousRunCommand
	})

	if err := svc.EditObject(object.ID, "web-port", "Port", []string{"443"}); err == nil {
		t.Fatal("expected edit to fail when firewall apply fails")
	}

	var updated networkModels.Object
	if err := db.Preload("Entries").First(&updated, object.ID).Error; err != nil {
		t.Fatalf("failed to reload object: %v", err)
	}
	if len(updated.Entries) != 1 || updated.Entries[0].Value != "80" {
		t.Fatalf("expected object entries to rollback after apply failure, got: %+v", updated.Entries)
	}
}

func TestGetObjects_WithPartialSchema_DoesNotFail(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
	)

	obj := networkModels.Object{
		Name: "partial-schema-object",
		Type: "Port",
		Entries: []networkModels.ObjectEntry{
			{Value: "443"},
		},
	}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatalf("failed to create object: %v", err)
	}

	objects, err := svc.GetObjects()
	if err != nil {
		t.Fatalf("expected GetObjects to succeed with partial schema, got: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected one object, got %d", len(objects))
	}
}

func TestGetObjects_UsageLabelingPrefersDHCPForMacAndKeepsHostLegacyOwnerEmpty(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&networkModels.ObjectListSnapshot{},
		&networkModels.DHCPRange{},
		&networkModels.DHCPStaticLease{},
		&networkModels.FirewallTrafficRule{},
	)

	macObj := networkModels.Object{
		Name: "mac-used-by-dhcp-and-firewall",
		Type: "Mac",
		Entries: []networkModels.ObjectEntry{
			{Value: "02:00:00:00:00:01"},
		},
	}
	if err := db.Create(&macObj).Error; err != nil {
		t.Fatalf("failed to create mac object: %v", err)
	}

	hostObj := networkModels.Object{
		Name: "host-used-by-dhcp",
		Type: "Host",
		Entries: []networkModels.ObjectEntry{
			{Value: "198.51.100.25"},
		},
	}
	if err := db.Create(&hostObj).Error; err != nil {
		t.Fatalf("failed to create host object: %v", err)
	}

	rng := networkModels.DHCPRange{
		Type:    "ipv4",
		StartIP: "198.51.100.10",
		EndIP:   "198.51.100.200",
	}
	if err := db.Create(&rng).Error; err != nil {
		t.Fatalf("failed to create dhcp range: %v", err)
	}

	macID := macObj.ID
	hostID := hostObj.ID
	lease := networkModels.DHCPStaticLease{
		Hostname:    "lease1",
		MACObjectID: &macID,
		IPObjectID:  &hostID,
		DHCPRangeID: rng.ID,
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatalf("failed to create dhcp static lease: %v", err)
	}

	rule := networkModels.FirewallTrafficRule{
		Name:        "mac-used-in-firewall",
		Enabled:     true,
		Priority:    1000,
		Action:      "block",
		Direction:   "in",
		Protocol:    "any",
		Family:      "inet",
		SourceObjID: &macID,
		DestRaw:     "any",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create firewall traffic rule: %v", err)
	}

	objects, err := svc.GetObjects()
	if err != nil {
		t.Fatalf("expected GetObjects to succeed, got: %v", err)
	}

	byID := map[uint]networkModels.Object{}
	for _, object := range objects {
		byID[object.ID] = object
	}

	macLoaded, ok := byID[macObj.ID]
	if !ok {
		t.Fatalf("missing mac object %d in list", macObj.ID)
	}
	if !macLoaded.IsUsed {
		t.Fatal("expected mac object to be marked used")
	}
	if macLoaded.IsUsedBy != "dhcp" {
		t.Fatalf("expected mac object owner to prefer dhcp, got %q", macLoaded.IsUsedBy)
	}

	hostLoaded, ok := byID[hostObj.ID]
	if !ok {
		t.Fatalf("missing host object %d in list", hostObj.ID)
	}
	if !hostLoaded.IsUsed {
		t.Fatal("expected host object to be marked used")
	}
	if hostLoaded.IsUsedBy != "" {
		t.Fatalf("expected host object owner to remain empty for ip-object dhcp usage, got %q", hostLoaded.IsUsedBy)
	}
}
