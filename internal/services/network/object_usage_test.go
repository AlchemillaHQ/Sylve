// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"slices"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetObjectsUsedByIncludesEveryReferenceColumn(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.FirewallNATRule{},
		&networkModels.DHCPStaticLease{},
		&networkModels.StaticRoute{},
	)
	if err := db.Create(&networkServiceTestVM{ID: 10, RID: 101, Name: "build-vm"}).Error; err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	if err := db.Create(&networkServiceTestJail{ID: 20, CTID: 202, Name: "web-jail"}).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	want := make(map[uint]networkModels.ObjectUsage)
	for _, source := range []struct {
		table   string
		usage   networkModels.ObjectUsage
		row     map[string]any
		columns []string
	}{
		{
			table: "firewall_traffic_rules",
			usage: networkModels.ObjectUsage{Type: "firewall-traffic", ID: 1, Name: "allow-web"},
			row:   map[string]any{"id": 1, "name": "allow-web", "action": "pass"},
			columns: []string{
				"source_obj_id", "dest_obj_id", "src_port_obj_id", "dst_port_obj_id",
			},
		},
		{
			table: "firewall_nat_rules",
			usage: networkModels.ObjectUsage{Type: "firewall-nat", ID: 2, Name: "web-nat"},
			row:   map[string]any{"id": 2, "name": "web-nat"},
			columns: []string{
				"source_obj_id", "dest_obj_id", "translate_to_obj_id", "dnat_target_obj_id",
				"dst_port_obj_id", "redirect_port_obj_id",
			},
		},
		{
			table: "dhcp_static_leases",
			usage: networkModels.ObjectUsage{Type: "dhcp", ID: 3, Name: "client"},
			row:   map[string]any{"id": 3, "hostname": "client"},
			columns: []string{
				"ip_object_id", "mac_object_id", "d_uid_object_id",
			},
		},
		{
			table:   "vm_networks",
			usage:   networkModels.ObjectUsage{Type: "vm", ID: 10, Name: "build-vm"},
			row:     map[string]any{"id": 4, "vm_id": 10, "switch_id": 1},
			columns: []string{"mac_id"},
		},
		{
			table: "jail_networks",
			usage: networkModels.ObjectUsage{Type: "jail", ID: 20, Name: "web-jail"},
			row:   map[string]any{"id": 5, "jid": 20, "name": "vnet0", "switch_id": 1},
			columns: []string{
				"mac_id", "ipv4_id", "ipv4_gw_id", "ipv6_id", "ipv6_gw_id",
			},
		},
		{
			table: "standard_switches",
			usage: networkModels.ObjectUsage{Type: "switch", ID: 6, Name: "lan"},
			row:   map[string]any{"id": 6, "name": "lan", "bridge_name": "vm-lan"},
			columns: []string{
				"address_object_id", "address6_object_id", "network_object_id", "network6_object_id",
				"gateway_address_object_id", "gateway6_address_object_id", "bridge_mac_object_id",
			},
		},
		{
			table: "static_routes",
			usage: networkModels.ObjectUsage{Type: "route", ID: 7, Name: "remote"},
			row: map[string]any{
				"id": 7, "name": "remote", "enabled": true, "destination_type": "host",
				"destination": "192.0.2.20", "family": "inet", "next_hop_mode": "gateway",
			},
			columns: []string{"destination_obj_id", "gateway_obj_id"},
		},
	} {
		for _, column := range source.columns {
			object := networkModels.Object{Name: source.table + "-" + column, Type: "Host"}
			if err := db.Create(&object).Error; err != nil {
				t.Fatalf("seed %s object: %v", column, err)
			}
			source.row[column] = object.ID
			want[object.ID] = source.usage
		}
		if err := db.Table(source.table).Create(source.row).Error; err != nil {
			t.Fatalf("seed %s reference: %v", source.table, err)
		}
	}
	unused := networkModels.Object{Name: "unused", Type: "Host"}
	if err := db.Create(&unused).Error; err != nil {
		t.Fatalf("seed unused object: %v", err)
	}

	objects, err := svc.GetObjects()
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(objects) != len(want)+1 {
		t.Fatalf("object count=%d, want %d", len(objects), len(want)+1)
	}
	for _, object := range objects {
		if object.ID == unused.ID {
			if object.IsUsed || object.IsUsedBy != "" || object.UsedBy == nil || len(object.UsedBy) != 0 {
				t.Fatalf("unused object should have an empty usage array: %+v", object)
			}
			continue
		}
		if !object.IsUsed || !slices.Equal(object.UsedBy, []networkModels.ObjectUsage{want[object.ID]}) {
			t.Errorf("%s usage=%+v, want %+v", object.Name, object.UsedBy, want[object.ID])
		}
	}
}

func TestGetObjectsUsedByDeduplicatesConsumersAndRetainsSharedUsage(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.FirewallTrafficRule{},
		&networkModels.DHCPStaticLease{},
	)
	object := networkModels.Object{Name: "shared-mac", Type: "Mac"}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("seed object: %v", err)
	}
	for _, guest := range []networkServiceTestVM{
		{ID: 10, RID: 101, Name: "same-name"},
		{ID: 11, RID: 102, Name: "same-name"},
	} {
		if err := db.Create(&guest).Error; err != nil {
			t.Fatalf("seed VM: %v", err)
		}
		for range 2 {
			if err := db.Table("vm_networks").Create(map[string]any{
				"vm_id": guest.ID, "switch_id": 1, "mac_id": object.ID,
			}).Error; err != nil {
				t.Fatalf("seed VM adapter: %v", err)
			}
		}
	}
	if err := db.Create(&networkModels.FirewallTrafficRule{
		ID: 1, Name: "same-name", Action: "pass", SourceObjID: &object.ID, DestObjID: &object.ID,
	}).Error; err != nil {
		t.Fatalf("seed traffic rule: %v", err)
	}
	if err := db.Create(&networkModels.DHCPStaticLease{
		ID: 1, Hostname: "client", MACObjectID: &object.ID,
	}).Error; err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	objects, err := svc.GetObjects()
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	want := []networkModels.ObjectUsage{
		{Type: "dhcp", ID: 1, Name: "client"},
		{Type: "firewall-traffic", ID: 1, Name: "same-name"},
		{Type: "vm", ID: 10, Name: "same-name"},
		{Type: "vm", ID: 11, Name: "same-name"},
	}
	if len(objects) != 1 || !slices.Equal(objects[0].UsedBy, want) {
		t.Fatalf("shared usage=%+v, want %+v", objects, want)
	}
	if !objects[0].IsUsed || objects[0].IsUsedBy != "dhcp" {
		t.Fatalf("legacy DHCP ownership changed: %+v", objects[0])
	}

	if err := db.Table("vms").Where("id = ?", 10).Update("name", "renamed").Error; err != nil {
		t.Fatalf("rename VM: %v", err)
	}
	objects, err = svc.GetObjects()
	if err != nil {
		t.Fatalf("refresh objects: %v", err)
	}
	want[2].Name = "renamed"
	if !slices.Equal(objects[0].UsedBy, want) {
		t.Fatalf("usage did not reflect consumer rename: %+v", objects[0].UsedBy)
	}
}

func TestGetObjectsUsedByToleratesMissingUsageMetadata(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.Object{}, &networkModels.ObjectEntry{})
	for _, statement := range []string{
		"CREATE TABLE vm_networks (id INTEGER PRIMARY KEY, vm_id INTEGER, mac_id INTEGER)",
		"CREATE TABLE firewall_traffic_rules (id INTEGER PRIMARY KEY, source_obj_id INTEGER)",
		"INSERT INTO vm_networks (id, vm_id, mac_id) VALUES (1, 42, 1)",
		"INSERT INTO firewall_traffic_rules (id, source_obj_id) VALUES (2, 1)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed partial schema: %v", err)
		}
	}
	object := networkModels.Object{ID: 1, Name: "partial-schema", Type: "Mac"}
	if err := db.Create(&object).Error; err != nil {
		t.Fatalf("seed object: %v", err)
	}
	svc := &Service{DB: db}
	objects, err := svc.GetObjects()
	if err != nil {
		t.Fatalf("list objects with missing consumer names/guest table: %v", err)
	}
	want := []networkModels.ObjectUsage{
		{Type: "firewall-traffic", ID: 2},
		{Type: "vm", ID: 42},
	}
	if len(objects) != 1 || !objects[0].IsUsed || !slices.Equal(objects[0].UsedBy, want) {
		t.Fatalf("partial schema usage=%+v, want %+v", objects, want)
	}
}
