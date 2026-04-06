// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/system"
	"gorm.io/gorm"
)

func Fixups(db *gorm.DB) error {
	runNetworkDeltaMigration(db)
	fixJailNetworkNameIndex(db)
	backfillVMStorageEnableDefaults(db)
	backfillTemplateSourceGuestIDs(db)
	createSylveUnixGroup(db)
	cleanupInvalidTokenRows(db)
	cleanupInvalidAuditUserIDs(db)
	cleanupLegacyDevdEventsTable(db)
	dropSambaSharePathUniqueIndex(db)
	backfillFirewallRuleVisibilityDefaults(db)

	return nil
}

func PreMigrationFixups(db *gorm.DB) {
	deduplicateJailHooks(db)
}

func runNetworkDeltaMigration(db *gorm.DB) {
	const name = "network_interface_delta_migration_2"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed")
		return
	}

	if count > 0 {
		return
	}

	// network_interfaces moved to telemetry.db and are migrated there now.
	// Keep this historical fixup idempotent without deleting legacy rows before
	// telemetry migration can copy them.
	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func fixJailNetworkNameIndex(db *gorm.DB) {
	const name = "jail_network_name_scope_index_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for fix_jail_network_name_index")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("jail_networks") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	if db.Migrator().HasIndex(&jailModels.Network{}, "idx_jail_network_name") {
		if err := db.Migrator().DropIndex(&jailModels.Network{}, "idx_jail_network_name"); err != nil {
			logger.L.Err(err).Msg("failed dropping legacy jail network global name index")
			return
		}
	}

	if !db.Migrator().HasIndex(&jailModels.Network{}, "idx_jail_network_name_per_jail") {
		if err := db.Migrator().CreateIndex(&jailModels.Network{}, "idx_jail_network_name_per_jail"); err != nil {
			logger.L.Err(err).Msg("failed creating jail network scoped name index")
			return
		}
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func backfillVMStorageEnableDefaults(db *gorm.DB) {
	const name = "vm_storage_enable_default_true_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for backfill_vm_storage_enable_defaults")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("vm_storages") || !db.Migrator().HasColumn("vm_storages", "enable") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	if err := db.Exec(`UPDATE vm_storages SET enable = 1`).Error; err != nil {
		logger.L.Err(err).Msg("failed to backfill vm_storages.enable default values")
		return
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func createSylveUnixGroup(db *gorm.DB) {
	var count int64
	if err := db.
		Table("groups").
		Where("name = ?", "sylve_g").
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("failed checking for sylve unix group")
		return
	}

	if count > 0 {
		exists := system.UnixGroupExists("sylve_g")
		if !exists {
			if err := system.CreateUnixGroup("sylve_g"); err != nil {
				logger.L.Err(err).Msg("failed creating sylve unix group")
			}
		}

		return
	}

	if err := db.Exec(`INSERT INTO groups (name, notes, created_at, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "sylve_g", "Default sylve unix group").Error; err != nil {
		logger.L.Err(err).Msg("failed creating sylve unix group")
		return
	}

	exists := system.UnixGroupExists("sylve_g")
	if !exists {
		if err := system.CreateUnixGroup("sylve_g"); err != nil {
			logger.L.Err(err).Msg("failed creating sylve unix group")
		}
	}
}

func cleanupInvalidTokenRows(db *gorm.DB) {
	const name = "cleanup_invalid_token_rows_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for cleanup_invalid_token_rows")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("tokens") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	query := `DELETE FROM tokens WHERE user_id < 0`
	args := []any{}
	if db.Migrator().HasTable("pam_identities") {
		query = `DELETE FROM tokens WHERE user_id < 0 OR (auth_type = ? AND user_id NOT IN (SELECT id FROM pam_identities))`
		args = append(args, "pam")
	}

	result := db.Exec(query, args...)
	if result.Error != nil {
		logger.L.Err(result.Error).Msg("failed cleaning up invalid token rows")
		return
	}

	if result.RowsAffected > 0 {
		logger.L.Warn().Msgf("Deleted %d invalid token rows", result.RowsAffected)
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func cleanupInvalidAuditUserIDs(db *gorm.DB) {
	const name = "cleanup_invalid_audit_user_ids_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for cleanup_invalid_audit_user_ids")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("audit_records") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	result := db.Exec(`UPDATE audit_records SET user_id = NULL WHERE user_id < 0`)
	if result.Error != nil {
		logger.L.Err(result.Error).Msg("failed cleaning up invalid audit user IDs")
		return
	}

	if result.RowsAffected > 0 {
		logger.L.Warn().Msgf("Nullified %d invalid audit user IDs", result.RowsAffected)
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func cleanupLegacyDevdEventsTable(db *gorm.DB) {
	const name = "drop_legacy_devd_events_table_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for cleanup_legacy_devd_events_table")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("devd_events") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	if err := db.Migrator().DropTable("devd_events"); err != nil {
		logger.L.Err(err).Msg("failed dropping legacy devd_events table")
		return
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func dropSambaSharePathUniqueIndex(db *gorm.DB) {
	const name = "drop_samba_share_path_unique_index_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for drop_samba_share_path_unique_index")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable(&sambaModels.SambaShare{}) {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	indexes, err := db.Migrator().GetIndexes(&sambaModels.SambaShare{})
	if err != nil {
		logger.L.Err(err).Msg("failed retrieving samba_shares indexes")
		return
	}

	for _, idx := range indexes {
		unique, ok := idx.Unique()
		if !ok || !unique {
			continue
		}

		columns := idx.Columns()
		if len(columns) != 1 || !strings.EqualFold(columns[0], "path") {
			continue
		}

		if err := db.Migrator().DropIndex(&sambaModels.SambaShare{}, idx.Name()); err != nil {
			logger.L.Err(err).Str("index", idx.Name()).Msg("failed dropping samba_shares path unique index")
			return
		}
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func backfillFirewallRuleVisibilityDefaults(db *gorm.DB) {
	const name = "backfill_firewall_rule_visibility_defaults_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for backfill_firewall_rule_visibility_defaults")
		return
	}
	if count > 0 {
		return
	}

	if db.Migrator().HasTable("firewall_traffic_rules") && db.Migrator().HasColumn("firewall_traffic_rules", "visible") {
		if err := db.Exec(`UPDATE firewall_traffic_rules SET visible = 1`).Error; err != nil {
			logger.L.Err(err).Msg("failed to backfill firewall_traffic_rules.visible")
			return
		}
	}
	if db.Migrator().HasTable("firewall_nat_rules") && db.Migrator().HasColumn("firewall_nat_rules", "visible") {
		if err := db.Exec(`UPDATE firewall_nat_rules SET visible = 1`).Error; err != nil {
			logger.L.Err(err).Msg("failed to backfill firewall_nat_rules.visible")
			return
		}
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

func backfillTemplateSourceGuestIDs(db *gorm.DB) {
	const name = "template_source_guest_id_backfill_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for backfill_template_source_guest_ids")
		return
	}

	if count > 0 {
		return
	}

	if db.Migrator().HasTable(&vmModels.VMTemplate{}) && db.Migrator().HasTable(&vmModels.VM{}) {
		var vms []vmModels.VM
		if err := db.Model(&vmModels.VM{}).Select("name", "rid").Find(&vms).Error; err != nil {
			logger.L.Err(err).Msg("failed loading vm sources for template source id backfill")
			return
		}

		ridByName := make(map[string]uint, len(vms))
		ambiguous := make(map[string]struct{})
		for _, vm := range vms {
			sourceName := strings.TrimSpace(vm.Name)
			if sourceName == "" || vm.RID == 0 {
				continue
			}

			if existing, exists := ridByName[sourceName]; exists && existing != vm.RID {
				ambiguous[sourceName] = struct{}{}
				continue
			}

			ridByName[sourceName] = vm.RID
		}

		var templates []vmModels.VMTemplate
		if err := db.Model(&vmModels.VMTemplate{}).
			Select("id", "source_vm_name", "source_vm_rid").
			Where("source_vm_rid = ?", 0).
			Find(&templates).Error; err != nil {
			logger.L.Err(err).Msg("failed loading vm templates for source id backfill")
			return
		}

		for _, tpl := range templates {
			sourceName := strings.TrimSpace(tpl.SourceVMName)
			if sourceName == "" {
				continue
			}

			if _, exists := ambiguous[sourceName]; exists {
				continue
			}

			rid, exists := ridByName[sourceName]
			if !exists || rid == 0 {
				continue
			}

			if err := db.Model(&vmModels.VMTemplate{}).
				Where("id = ?", tpl.ID).
				Update("source_vm_rid", rid).Error; err != nil {
				logger.L.Err(err).Uint("template_id", tpl.ID).Msg("failed backfilling vm template source rid")
				return
			}
		}
	}

	if db.Migrator().HasTable(&jailModels.JailTemplate{}) && db.Migrator().HasTable(&jailModels.Jail{}) {
		var jails []jailModels.Jail
		if err := db.Model(&jailModels.Jail{}).Select("name", "ct_id").Find(&jails).Error; err != nil {
			logger.L.Err(err).Msg("failed loading jail sources for template source id backfill")
			return
		}

		ctidByName := make(map[string]uint, len(jails))
		ambiguous := make(map[string]struct{})
		for _, jail := range jails {
			sourceName := strings.TrimSpace(jail.Name)
			if sourceName == "" || jail.CTID == 0 {
				continue
			}

			if existing, exists := ctidByName[sourceName]; exists && existing != jail.CTID {
				ambiguous[sourceName] = struct{}{}
				continue
			}

			ctidByName[sourceName] = jail.CTID
		}

		var templates []jailModels.JailTemplate
		if err := db.Model(&jailModels.JailTemplate{}).
			Select("id", "source_jail_name", "source_jail_ctid").
			Where("source_jail_ctid = ?", 0).
			Find(&templates).Error; err != nil {
			logger.L.Err(err).Msg("failed loading jail templates for source id backfill")
			return
		}

		for _, tpl := range templates {
			sourceName := strings.TrimSpace(tpl.SourceJailName)
			if sourceName == "" {
				continue
			}

			if _, exists := ambiguous[sourceName]; exists {
				continue
			}

			ctid, exists := ctidByName[sourceName]
			if !exists || ctid == 0 {
				continue
			}

			if err := db.Model(&jailModels.JailTemplate{}).
				Where("id = ?", tpl.ID).
				Update("source_jail_ctid", ctid).Error; err != nil {
				logger.L.Err(err).Uint("template_id", tpl.ID).Msg("failed backfilling jail template source ctid")
				return
			}
		}
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})
}

/**
 * A previous migration omitted a primary key on the jail_hooks table,
 * which caused runaway duplicate entries during association updates and
 * made it impossible to target specific hooks.
 * * This fixup deduplicates the existing entries so GORM can safely apply
 * an auto-incrementing primary key during the subsequent AutoMigrate pass.
 */
func deduplicateJailHooks(db *gorm.DB) {
	const name = "deduplicate_jail_hooks_1"

	var count int64
	if err := db.
		Table("migrations").
		Where("name = ?", name).
		Count(&count).Error; err != nil {
		logger.L.Err(err).Msg("migration check failed for deduplicate_jail_hooks")
		return
	}

	if count > 0 {
		return
	}

	if !db.Migrator().HasTable("jail_hooks") {
		db.Table("migrations").Create(map[string]any{"name": name})
		return
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			CREATE TABLE jail_hooks_temp (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				jid INTEGER,
				phase TEXT,
				enabled BOOLEAN,
				script TEXT
			)
		`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO jail_hooks_temp (jid, phase, enabled, script)
			SELECT DISTINCT jid, phase, enabled, script FROM jail_hooks
		`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`DROP TABLE jail_hooks`).Error; err != nil {
			return err
		}

		if err := tx.Exec(`ALTER TABLE jail_hooks_temp RENAME TO jail_hooks`).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		logger.L.Err(err).Msg("failed deduplicating jail hooks")
		return
	}

	db.Table("migrations").Create(map[string]any{
		"name": name,
	})

	logger.L.Info().Msg("Deduplicated jail hooks and migrated schema")
}
