// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestFixJailNetworkNameIndexScopesUniquenessByJail(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{})

	if err := dbConn.Exec(`
		CREATE TABLE jail_networks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			jid INTEGER,
			name TEXT NOT NULL,
			switch_id INTEGER NOT NULL,
			switch_type TEXT NOT NULL DEFAULT 'standard',
			mac_id INTEGER,
			ipv4_id INTEGER,
			ipv4_gw_id INTEGER,
			ipv6_id INTEGER,
			ipv6_gw_id INTEGER,
			default_gateway BOOLEAN DEFAULT false,
			dhcp BOOLEAN DEFAULT false,
			sla_ac BOOLEAN DEFAULT false
		)
	`).Error; err != nil {
		t.Fatalf("failed creating legacy jail_networks table: %v", err)
	}

	if err := dbConn.Exec(`CREATE UNIQUE INDEX idx_jail_network_name ON jail_networks(name)`).Error; err != nil {
		t.Fatalf("failed creating legacy unique index: %v", err)
	}

	fixJailNetworkNameIndex(dbConn)

	var migrationCount int64
	if err := dbConn.Table("migrations").Where("name = ?", "jail_network_name_scope_index_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to be recorded, got %d", migrationCount)
	}

	if err := dbConn.Exec(`
		INSERT INTO jail_networks (jid, name, switch_id, switch_type)
		VALUES (1, 'LAN', 1, 'manual')
	`).Error; err != nil {
		t.Fatalf("failed inserting first jail network: %v", err)
	}

	if err := dbConn.Exec(`
		INSERT INTO jail_networks (jid, name, switch_id, switch_type)
		VALUES (2, 'LAN', 1, 'manual')
	`).Error; err != nil {
		t.Fatalf("expected same network name on a different jail to succeed, got: %v", err)
	}

	if err := dbConn.Exec(`
		INSERT INTO jail_networks (jid, name, switch_id, switch_type)
		VALUES (1, 'LAN', 1, 'manual')
	`).Error; err == nil {
		t.Fatal("expected duplicate (jid, name) to fail, got nil")
	}
}

func TestFixJailNetworkNameIndexAfterAutoMigrateOrdering(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{})

	if err := dbConn.Exec(`
		CREATE TABLE jail_networks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			jid INTEGER,
			name TEXT NOT NULL,
			switch_id INTEGER NOT NULL,
			switch_type TEXT NOT NULL DEFAULT 'standard',
			mac_id INTEGER,
			ipv4_id INTEGER,
			ipv4_gw_id INTEGER,
			ipv6_id INTEGER,
			ipv6_gw_id INTEGER,
			default_gateway BOOLEAN DEFAULT false,
			dhcp BOOLEAN DEFAULT false,
			sla_ac BOOLEAN DEFAULT false
		)
	`).Error; err != nil {
		t.Fatalf("failed creating legacy jail_networks table: %v", err)
	}

	if err := dbConn.Exec(`CREATE UNIQUE INDEX idx_jail_network_name ON jail_networks(name)`).Error; err != nil {
		t.Fatalf("failed creating legacy unique index: %v", err)
	}

	// Simulate SetupDatabase order:
	// 1) AutoMigrate models
	// 2) Run Fixups()
	if err := dbConn.AutoMigrate(&jailModels.Network{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	fixJailNetworkNameIndex(dbConn)

	var indexes []string
	if err := dbConn.Raw(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='jail_networks'`).Scan(&indexes).Error; err != nil {
		t.Fatalf("failed listing indexes: %v", err)
	}

	hasLegacy := false
	hasScoped := false
	for _, idx := range indexes {
		if idx == "idx_jail_network_name" {
			hasLegacy = true
		}
		if idx == "idx_jail_network_name_per_jail" {
			hasScoped = true
		}
	}

	if hasLegacy {
		t.Fatal("legacy global unique index idx_jail_network_name should be dropped")
	}
	if !hasScoped {
		t.Fatal("scoped index idx_jail_network_name_per_jail should exist")
	}

	if err := dbConn.Exec(`
		INSERT INTO jail_networks (jid, name, switch_id, switch_type)
		VALUES (10, 'LAN', 1, 'manual')
	`).Error; err != nil {
		t.Fatalf("failed inserting first jail network: %v", err)
	}

	if err := dbConn.Exec(`
		INSERT INTO jail_networks (jid, name, switch_id, switch_type)
		VALUES (11, 'LAN', 1, 'manual')
	`).Error; err != nil {
		t.Fatalf("expected same network name on different jail to succeed, got: %v", err)
	}
}

func TestCleanupLegacyDevdEventsTableDropsLegacyTableAndRecordsMigration(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{})

	if err := dbConn.Exec(`
		CREATE TABLE devd_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			payload TEXT
		)
	`).Error; err != nil {
		t.Fatalf("failed creating legacy devd_events table: %v", err)
	}

	cleanupLegacyDevdEventsTable(dbConn)

	if dbConn.Migrator().HasTable("devd_events") {
		t.Fatal("expected legacy devd_events table to be dropped")
	}

	var migrationCount int64
	if err := dbConn.Table("migrations").Where("name = ?", "drop_legacy_devd_events_table_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to be recorded once, got %d", migrationCount)
	}
}

func TestCleanupLegacyDevdEventsTableRecordsMigrationWhenTableAbsent(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{})

	cleanupLegacyDevdEventsTable(dbConn)

	var migrationCount int64
	if err := dbConn.Table("migrations").Where("name = ?", "drop_legacy_devd_events_table_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to be recorded once, got %d", migrationCount)
	}
}

func TestBackfillTemplateSourceGuestIDsBackfillsOnlyUnambiguousMatches(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(
		t,
		&models.Migrations{},
		&vmModels.VM{},
		&vmModels.VMTemplate{},
		&jailModels.Jail{},
		&jailModels.JailTemplate{},
	)

	vms := []vmModels.VM{
		{RID: 101, Name: "vm-web"},
		{RID: 102, Name: "vm-db"},
		{RID: 103, Name: "vm-dup"},
		{RID: 104, Name: "vm-dup"},
	}
	if err := dbConn.Create(&vms).Error; err != nil {
		t.Fatalf("failed creating vm fixtures: %v", err)
	}

	jails := []jailModels.Jail{
		{CTID: 201, Name: "jail-web"},
		{CTID: 202, Name: "jail-db"},
		{CTID: 203, Name: "jail-cache"},
	}
	if err := dbConn.Create(&jails).Error; err != nil {
		t.Fatalf("failed creating jail fixtures: %v", err)
	}

	vmTemplates := []vmModels.VMTemplate{
		{Name: "tpl-vm-web", SourceVMName: "vm-web", SourceVMRID: 0},
		{Name: "tpl-vm-dup", SourceVMName: "vm-dup", SourceVMRID: 0},
		{Name: "tpl-vm-miss", SourceVMName: "vm-missing", SourceVMRID: 0},
		{Name: "tpl-vm-set", SourceVMName: "vm-db", SourceVMRID: 999},
	}
	if err := dbConn.Create(&vmTemplates).Error; err != nil {
		t.Fatalf("failed creating vm template fixtures: %v", err)
	}

	jailTemplates := []jailModels.JailTemplate{
		{Name: "tpl-jail-web", SourceJailName: "jail-web", SourceJailCTID: 0, Pool: "zroot", RootDataset: "zroot/templates/jail-web"},
		{Name: "tpl-jail-db", SourceJailName: "jail-db", SourceJailCTID: 0, Pool: "zroot", RootDataset: "zroot/templates/jail-db"},
		{Name: "tpl-jail-miss", SourceJailName: "jail-missing", SourceJailCTID: 0, Pool: "zroot", RootDataset: "zroot/templates/jail-miss"},
		{Name: "tpl-jail-set", SourceJailName: "jail-db", SourceJailCTID: 999, Pool: "zroot", RootDataset: "zroot/templates/jail-set"},
	}
	if err := dbConn.Create(&jailTemplates).Error; err != nil {
		t.Fatalf("failed creating jail template fixtures: %v", err)
	}

	backfillTemplateSourceGuestIDs(dbConn)

	var refreshedVMTemplates []vmModels.VMTemplate
	if err := dbConn.Order("name asc").Find(&refreshedVMTemplates).Error; err != nil {
		t.Fatalf("failed reading vm templates: %v", err)
	}

	vmSourceByName := map[string]uint{}
	for _, tpl := range refreshedVMTemplates {
		vmSourceByName[tpl.Name] = tpl.SourceVMRID
	}

	if vmSourceByName["tpl-vm-web"] != 101 {
		t.Fatalf("expected tpl-vm-web source rid 101, got %d", vmSourceByName["tpl-vm-web"])
	}
	if vmSourceByName["tpl-vm-dup"] != 0 {
		t.Fatalf("expected ambiguous vm template to remain unset, got %d", vmSourceByName["tpl-vm-dup"])
	}
	if vmSourceByName["tpl-vm-miss"] != 0 {
		t.Fatalf("expected missing vm template to remain unset, got %d", vmSourceByName["tpl-vm-miss"])
	}
	if vmSourceByName["tpl-vm-set"] != 999 {
		t.Fatalf("expected pre-set vm template source rid to remain unchanged, got %d", vmSourceByName["tpl-vm-set"])
	}

	var refreshedJailTemplates []jailModels.JailTemplate
	if err := dbConn.Order("name asc").Find(&refreshedJailTemplates).Error; err != nil {
		t.Fatalf("failed reading jail templates: %v", err)
	}

	jailSourceByName := map[string]uint{}
	for _, tpl := range refreshedJailTemplates {
		jailSourceByName[tpl.Name] = tpl.SourceJailCTID
	}

	if jailSourceByName["tpl-jail-web"] != 201 {
		t.Fatalf("expected tpl-jail-web source ctid 201, got %d", jailSourceByName["tpl-jail-web"])
	}
	if jailSourceByName["tpl-jail-db"] != 202 {
		t.Fatalf("expected tpl-jail-db source ctid 202, got %d", jailSourceByName["tpl-jail-db"])
	}
	if jailSourceByName["tpl-jail-miss"] != 0 {
		t.Fatalf("expected missing jail template to remain unset, got %d", jailSourceByName["tpl-jail-miss"])
	}
	if jailSourceByName["tpl-jail-set"] != 999 {
		t.Fatalf("expected pre-set jail template source ctid to remain unchanged, got %d", jailSourceByName["tpl-jail-set"])
	}

	var migrationCount int64
	if err := dbConn.Table("migrations").Where("name = ?", "template_source_guest_id_backfill_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to be recorded once, got %d", migrationCount)
	}

	backfillTemplateSourceGuestIDs(dbConn)
	if err := dbConn.Table("migrations").Where("name = ?", "template_source_guest_id_backfill_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row after second run: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to stay single after rerun, got %d", migrationCount)
	}
}
