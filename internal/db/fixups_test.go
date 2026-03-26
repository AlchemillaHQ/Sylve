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
