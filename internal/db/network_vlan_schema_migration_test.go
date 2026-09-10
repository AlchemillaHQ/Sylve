// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestNetworkVLANSchemaMigrationKeepsLegacyRowsCompatible(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t)
	if err := dbConn.AutoMigrate(
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&jailModels.Network{},
	); err != nil {
		t.Fatalf("create current schema fixture: %v", err)
	}

	statements := []string{
		`ALTER TABLE jail_networks ADD COLUMN vlan integer DEFAULT 0`,
		`ALTER TABLE standard_switches DROP COLUMN vlan_filtering`,
		`ALTER TABLE standard_switches DROP COLUMN default_access_vlan`,
		`ALTER TABLE standard_switches DROP COLUMN host_vlan`,
		`ALTER TABLE network_ports DROP COLUMN vlan_mode`,
		`ALTER TABLE network_ports DROP COLUMN vlan_untagged_vlan`,
		`ALTER TABLE network_ports DROP COLUMN vlan_tagged_vlans`,
		`ALTER TABLE jail_networks DROP COLUMN vlan_mode`,
		`ALTER TABLE jail_networks DROP COLUMN vlan_untagged_vlan`,
		`ALTER TABLE jail_networks DROP COLUMN vlan_tagged_vlans`,
		`INSERT INTO standard_switches (id, name, bridge_name, mtu, vlan, disable_ipv6)
		 VALUES (1, 'legacy-standard', 'bridge-standard', 9000, 17, true)`,
		`INSERT INTO network_ports (id, name, switch_id)
		 VALUES (1, 'igb0', 1)`,
		`INSERT INTO jail_networks (id, jid, name, switch_id, switch_type, vlan)
		 VALUES (1, 10, 'vnet0', 1, 'standard', 23)`,
	}
	for _, statement := range statements {
		if err := dbConn.Exec(statement).Error; err != nil {
			t.Fatalf("prepare pre-feature schema: %v", err)
		}
	}

	if err := dbConn.AutoMigrate(
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.StandardSwitch{},
		&networkModels.NetworkPort{},
		&jailModels.Network{},
	); err != nil {
		t.Fatalf("apply VLAN-filtering model migration: %v", err)
	}

	var standard networkModels.StandardSwitch
	if err := dbConn.First(&standard, 1).Error; err != nil {
		t.Fatalf("load migrated standard switch: %v", err)
	}
	if standard.VLANFiltering || standard.DefaultAccessVLAN != nil || standard.HostVLAN != nil ||
		standard.VLAN != 17 || standard.MTU != 9000 {
		t.Fatalf("legacy standard switch semantics changed: %#v", standard)
	}

	var port networkModels.NetworkPort
	if err := dbConn.First(&port, 1).Error; err != nil {
		t.Fatalf("load migrated physical port: %v", err)
	}
	if port.VLANPolicy.Mode != "" || port.VLANPolicy.UntaggedVLAN != nil || len(port.VLANPolicy.TaggedVLANs) != 0 {
		t.Fatalf("legacy physical port gained a policy: %#v", port.VLANPolicy)
	}

	var jailNetwork jailModels.Network
	if err := dbConn.First(&jailNetwork, 1).Error; err != nil {
		t.Fatalf("load migrated jail network: %v", err)
	}
	if jailNetwork.VLANPolicy.Mode != "" || jailNetwork.VLANPolicy.UntaggedVLAN != nil ||
		len(jailNetwork.VLANPolicy.TaggedVLANs) != 0 {
		t.Fatalf("ordinary jail network gained a policy: %#v", jailNetwork.VLANPolicy)
	}
	var legacyJailVLAN int
	if err := dbConn.Raw(`SELECT vlan FROM jail_networks WHERE id = 1`).Scan(&legacyJailVLAN).Error; err != nil {
		t.Fatalf("read inert legacy jail VLAN column: %v", err)
	}
	if legacyJailVLAN != 23 {
		t.Fatalf("inert legacy jail VLAN column changed: got %d, want 23", legacyJailVLAN)
	}
}
