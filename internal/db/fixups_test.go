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
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestNormalizeDownloadUncategorizedType(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &utilitiesModels.Downloads{})
	download := utilitiesModels.Downloads{
		UUID:     "legacy-download-category",
		Path:     "/tmp/legacy-download-category.img",
		Name:     "legacy-download-category.img",
		Type:     utilitiesModels.DownloadTypePath,
		URL:      "/tmp/legacy-download-source.img",
		Progress: 100,
		Status:   utilitiesModels.DownloadStatusDone,
		UType:    utilitiesModels.DownloadUType("uncategoried"),
	}
	if err := dbConn.Create(&download).Error; err != nil {
		t.Fatalf("seed legacy download: %v", err)
	}

	if err := normalizeDownloadUncategorizedType(dbConn); err != nil {
		t.Fatalf("normalize download category: %v", err)
	}
	if err := normalizeDownloadUncategorizedType(dbConn); err != nil {
		t.Fatalf("repeat download category migration: %v", err)
	}

	var stored utilitiesModels.Downloads
	if err := dbConn.First(&stored, download.ID).Error; err != nil {
		t.Fatalf("load normalized download: %v", err)
	}
	if stored.UType != utilitiesModels.DownloadUTypeOther {
		t.Fatalf("download category=%q want %q", stored.UType, utilitiesModels.DownloadUTypeOther)
	}

	var migrationCount int64
	if err := dbConn.Model(&models.Migrations{}).
		Where("name = ?", "normalize_download_uncategorized_type_1").
		Count(&migrationCount).Error; err != nil {
		t.Fatalf("count download category migration: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count=%d want 1", migrationCount)
	}
}

func TestRenameLegacyARCMaxTunable(t *testing.T) {
	t.Run("legacy override renamed to canonical", func(t *testing.T) {
		dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemTunable{})
		legacy := models.SystemTunable{Name: models.SystemTunableLegacyARCMaxOID, Value: "17179869184"}
		if err := dbConn.Create(&legacy).Error; err != nil {
			t.Fatalf("seed legacy arc_max tunable: %v", err)
		}

		if err := renameLegacyARCMaxTunable(dbConn); err != nil {
			t.Fatalf("rename legacy arc_max tunable: %v", err)
		}
		if err := renameLegacyARCMaxTunable(dbConn); err != nil {
			t.Fatalf("repeat legacy arc_max tunable migration: %v", err)
		}

		var stored models.SystemTunable
		if err := dbConn.Where("name = ?", models.SystemTunableARCMaxOID).First(&stored).Error; err != nil {
			t.Fatalf("load renamed tunable: %v", err)
		}
		if stored.Value != "17179869184" {
			t.Fatalf("renamed tunable value=%q want 17179869184", stored.Value)
		}

		var legacyCount int64
		if err := dbConn.Model(&models.SystemTunable{}).
			Where("name = ?", models.SystemTunableLegacyARCMaxOID).
			Count(&legacyCount).Error; err != nil {
			t.Fatalf("count legacy tunables: %v", err)
		}
		if legacyCount != 0 {
			t.Fatalf("legacy arc_max rows=%d want 0", legacyCount)
		}
	})

	t.Run("both names present keeps canonical", func(t *testing.T) {
		dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemTunable{})
		canonical := models.SystemTunable{Name: models.SystemTunableARCMaxOID, Value: "8589934592"}
		if err := dbConn.Create(&canonical).Error; err != nil {
			t.Fatalf("seed canonical tunable: %v", err)
		}
		legacy := models.SystemTunable{Name: models.SystemTunableLegacyARCMaxOID, Value: "17179869184"}
		if err := dbConn.Create(&legacy).Error; err != nil {
			t.Fatalf("seed legacy tunable: %v", err)
		}

		if err := renameLegacyARCMaxTunable(dbConn); err != nil {
			t.Fatalf("rename legacy arc_max tunable: %v", err)
		}

		var stored models.SystemTunable
		if err := dbConn.Where("name = ?", models.SystemTunableARCMaxOID).First(&stored).Error; err != nil {
			t.Fatalf("load canonical tunable: %v", err)
		}
		if stored.Value != "8589934592" {
			t.Fatalf("canonical value=%q want 8589934592", stored.Value)
		}

		var legacyCount int64
		if err := dbConn.Model(&models.SystemTunable{}).
			Where("name = ?", models.SystemTunableLegacyARCMaxOID).
			Count(&legacyCount).Error; err != nil {
			t.Fatalf("count legacy tunables: %v", err)
		}
		if legacyCount != 0 {
			t.Fatalf("legacy arc_max rows=%d want 0", legacyCount)
		}
	})

	t.Run("canonical only is untouched", func(t *testing.T) {
		dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &models.SystemTunable{})
		canonical := models.SystemTunable{Name: models.SystemTunableARCMaxOID, Value: "8589934592"}
		if err := dbConn.Create(&canonical).Error; err != nil {
			t.Fatalf("seed canonical tunable: %v", err)
		}

		if err := renameLegacyARCMaxTunable(dbConn); err != nil {
			t.Fatalf("rename legacy arc_max tunable: %v", err)
		}

		var total int64
		if err := dbConn.Model(&models.SystemTunable{}).Count(&total).Error; err != nil {
			t.Fatalf("count tunables: %v", err)
		}
		if total != 1 {
			t.Fatalf("tunable rows=%d want 1", total)
		}
	})
}

func TestDeduplicateMdnsRecordsBeforeIdentityIndex(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{})
	if err := dbConn.Exec(`
		CREATE TABLE mdns_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			type TEXT,
			port INTEGER
		)
	`).Error; err != nil {
		t.Fatalf("create legacy mDNS records table: %v", err)
	}
	if err := dbConn.Exec(`
		INSERT INTO mdns_records (id, name, type, port) VALUES
			(1, 'printer', '_ipp._tcp', 631),
			(2, 'printer', '_ipp._tcp', 8631),
			(3, 'printer', '_printer._tcp', 9100),
			(4, 'printer', '_ipp._tcp', 10631)
	`).Error; err != nil {
		t.Fatalf("seed duplicate mDNS records: %v", err)
	}

	deduplicateMdnsRecords(dbConn)
	deduplicateMdnsRecords(dbConn)

	var rows []struct {
		ID   uint
		Name string
		Type string
		Port int
	}
	if err := dbConn.Table("mdns_records").Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("list deduplicated mDNS records: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two unique records, got %+v", rows)
	}
	if rows[0].ID != 1 || rows[0].Port != 631 || rows[1].ID != 3 {
		t.Fatalf("deduplication did not retain the oldest identities: %+v", rows)
	}

	var migrationCount int64
	if err := dbConn.Model(&models.Migrations{}).
		Where("name = ?", "deduplicate_mdns_record_identity_1").
		Count(&migrationCount).Error; err != nil {
		t.Fatalf("count mDNS deduplication migration: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected one migration marker, got %d", migrationCount)
	}

	if err := dbConn.AutoMigrate(&mdnsModels.MdnsRecord{}); err != nil {
		t.Fatalf("apply mDNS identity index: %v", err)
	}
	duplicate := mdnsModels.MdnsRecord{Name: "printer", Type: "_ipp._tcp", Port: 9999}
	if err := dbConn.Create(&duplicate).Error; err == nil {
		t.Fatal("expected the identity index to reject a duplicate mDNS record")
	}
}

func TestClearReplicatedManagedBackupTargetKeyPaths(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&models.Migrations{}, &clusterModels.BackupTarget{}, &clusterModels.BackupTargetNodeReadiness{},
	)
	target := clusterModels.BackupTarget{
		ID: 7, Name: "legacy-managed", SSHHost: "root@backup", SSHPort: 22,
		SSHKeyPath: "/leader/local/target-7_id", SSHKey: "managed-key", BackupRoot: "tank/backups",
	}
	if err := dbConn.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := dbConn.Create(&clusterModels.BackupTargetNodeReadiness{
		TargetID: target.ID, NodeID: "node-1", TargetFingerprint: "legacy", LastVerifiedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed readiness: %v", err)
	}

	if err := clearReplicatedManagedBackupTargetKeyPaths(dbConn); err != nil {
		t.Fatalf("fixup: %v", err)
	}
	var stored clusterModels.BackupTarget
	if err := dbConn.First(&stored, target.ID).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if stored.SSHKeyPath != "" || stored.SSHKey != "managed-key" {
		t.Fatalf("managed target not normalized: %+v", stored)
	}
	var readinessCount, migrationCount int64
	if err := dbConn.Model(&clusterModels.BackupTargetNodeReadiness{}).Count(&readinessCount).Error; err != nil || readinessCount != 0 {
		t.Fatalf("readiness count=%d err=%v", readinessCount, err)
	}
	if err := dbConn.Model(&models.Migrations{}).
		Where("name = ?", "clear_replicated_managed_backup_target_key_paths_1").
		Count(&migrationCount).Error; err != nil || migrationCount != 1 {
		t.Fatalf("migration count=%d err=%v", migrationCount, err)
	}
	if err := clearReplicatedManagedBackupTargetKeyPaths(dbConn); err != nil {
		t.Fatalf("idempotent fixup: %v", err)
	}
}

func TestMigrateReplicationTransitionEvents(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t,
		&models.Migrations{},
		&clusterModels.ReplicationEvent{},
		&clusterModels.ReplicationTransitionEvent{},
	)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	rows := []clusterModels.ReplicationEvent{
		{ID: 1, EventType: "replication", Status: "success", StartedAt: now},
		{ID: 2, TransitionRunID: "transition-2", EventType: "failover", Status: "success", StartedAt: now},
		{ID: 3, EventType: "failover", Status: "failed", Message: "ambiguous legacy warning", StartedAt: now},
	}
	if err := dbConn.Create(&rows).Error; err != nil {
		t.Fatalf("seed legacy events: %v", err)
	}

	if err := migrateReplicationTransitionEvents(dbConn); err != nil {
		t.Fatalf("migrate transition events: %v", err)
	}
	if err := migrateReplicationTransitionEvents(dbConn); err != nil {
		t.Fatalf("repeat transition migration: %v", err)
	}

	var localEvents []clusterModels.ReplicationEvent
	if err := dbConn.Order("id ASC").Find(&localEvents).Error; err != nil {
		t.Fatalf("load local events: %v", err)
	}
	if len(localEvents) != 2 || localEvents[0].ID != 1 || localEvents[1].ID != 3 {
		t.Fatalf("local event migration mismatch: %+v", localEvents)
	}

	var transitions []clusterModels.ReplicationTransitionEvent
	if err := dbConn.Order("id ASC").Find(&transitions).Error; err != nil {
		t.Fatalf("load transition events: %v", err)
	}
	if len(transitions) != 1 || transitions[0].ID != 2 ||
		transitions[0].TransitionRunID != "transition-2" {
		t.Fatalf("transition event migration mismatch: %+v", transitions)
	}

	var migrationCount int64
	if err := dbConn.Model(&models.Migrations{}).
		Where("name = ?", "split_replication_transition_events_1").
		Count(&migrationCount).Error; err != nil {
		t.Fatalf("count migration marker: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration marker count=%d, want 1", migrationCount)
	}
}

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

func TestDropSambaSharePathUniqueIndexDropsLegacyPathUniqueness(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.Migrations{}, &sambaModels.SambaShare{})

	if err := dbConn.Exec(`CREATE UNIQUE INDEX idx_samba_shares_path_legacy ON samba_shares(path)`).Error; err != nil {
		t.Fatalf("failed creating legacy samba_shares path unique index: %v", err)
	}

	dropSambaSharePathUniqueIndex(dbConn)

	var migrationCount int64
	if err := dbConn.Table("migrations").Where("name = ?", "drop_samba_share_path_unique_index_1").Count(&migrationCount).Error; err != nil {
		t.Fatalf("failed checking migration row: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("expected migration row to be recorded once, got %d", migrationCount)
	}

	first := sambaModels.SambaShare{Name: "share-one", Dataset: "dataset-one", Path: "/mnt/dup"}
	if err := dbConn.Create(&first).Error; err != nil {
		t.Fatalf("failed creating first samba share: %v", err)
	}

	second := sambaModels.SambaShare{Name: "share-two", Dataset: "dataset-two", Path: "/mnt/dup"}
	if err := dbConn.Create(&second).Error; err != nil {
		t.Fatalf("expected duplicate path to be allowed after fixup, got: %v", err)
	}
}

func TestEnforceBasicSettingsSingletonRepairsAndConstrainsRows(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})

	settings := []models.BasicSettings{
		{ID: 1, Pools: []string{"old"}, Initialized: false, Restarted: false},
		{ID: 2, Pools: []string{"zroot"}, Initialized: true, Restarted: false},
		{ID: 3, Pools: []string{"tank"}, Initialized: true, Restarted: true},
	}
	if err := dbConn.Create(&settings).Error; err != nil {
		t.Fatalf("failed creating duplicate basic settings fixtures: %v", err)
	}

	if err := enforceBasicSettingsSingleton(dbConn); err != nil {
		t.Fatalf("failed enforcing basic settings singleton: %v", err)
	}

	var repaired []models.BasicSettings
	if err := dbConn.Find(&repaired).Error; err != nil {
		t.Fatalf("failed reading repaired basic settings: %v", err)
	}
	if len(repaired) != 1 {
		t.Fatalf("expected one repaired basic settings row, got %d", len(repaired))
	}
	if repaired[0].ID != 1 {
		t.Fatalf("expected repaired row to use ID 1, got %d", repaired[0].ID)
	}
	if !repaired[0].Initialized || !repaired[0].Restarted {
		t.Fatalf("expected the most complete settings row to be retained, got %+v", repaired[0])
	}
	if len(repaired[0].Pools) != 1 || repaired[0].Pools[0] != "tank" {
		t.Fatalf("expected the selected row's settings to be retained, got %v", repaired[0].Pools)
	}

	duplicate := models.BasicSettings{ID: 2, Initialized: true}
	if err := dbConn.Create(&duplicate).Error; err == nil {
		t.Fatal("expected singleton index to reject a second basic settings row")
	}

	if err := enforceBasicSettingsSingleton(dbConn); err != nil {
		t.Fatalf("expected singleton enforcement to be idempotent, got %v", err)
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
