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

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestSambaShareRetentionMigrationDefaultsExistingRows(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t)
	if err := dbConn.Exec(`
		CREATE TABLE samba_shares (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			dataset TEXT,
			path TEXT
		)
	`).Error; err != nil {
		t.Fatalf("create legacy Samba shares table: %v", err)
	}
	if err := dbConn.Exec(`
		INSERT INTO samba_shares (id, name, dataset, path)
		VALUES (1, 'legacy', 'legacy-guid', '/legacy')
	`).Error; err != nil {
		t.Fatalf("seed legacy Samba share: %v", err)
	}

	if err := dbConn.AutoMigrate(&sambaModels.SambaShare{}); err != nil {
		t.Fatalf("migrate Samba share retention: %v", err)
	}

	var share sambaModels.SambaShare
	if err := dbConn.First(&share, 1).Error; err != nil {
		t.Fatalf("load migrated Samba share: %v", err)
	}
	if got := sambaModels.AuditRetentionDaysValue(share.AuditRetentionDays); got != sambaModels.DefaultAuditRetentionDays {
		t.Fatalf("audit retention days=%d want %d", got, sambaModels.DefaultAuditRetentionDays)
	}

	forever := sambaModels.SambaShare{
		Name:               "forever",
		Dataset:            "forever-guid",
		Path:               "/forever",
		AuditRetentionDays: sambaModels.AuditRetentionDaysPointer(0),
	}
	if err := dbConn.Create(&forever).Error; err != nil {
		t.Fatalf("create Samba share with unlimited retention: %v", err)
	}
	if err := dbConn.First(&forever, forever.ID).Error; err != nil {
		t.Fatalf("reload Samba share with unlimited retention: %v", err)
	}
	if got := sambaModels.AuditRetentionDaysValue(forever.AuditRetentionDays); got != 0 {
		t.Fatalf("unlimited audit retention days=%d want 0", got)
	}
}

func TestSambaAuditLogRetentionMigrationDefaultsExistingRows(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t)
	if err := dbConn.Exec(`
		CREATE TABLE samba_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			share TEXT,
			user TEXT,
			ip TEXT,
			action TEXT,
			result TEXT,
			path TEXT,
			target TEXT,
			folder TEXT,
			created_at DATETIME
		)
	`).Error; err != nil {
		t.Fatalf("create legacy Samba audit table: %v", err)
	}
	if err := dbConn.Exec(`
		INSERT INTO samba_audit_logs (
			id, share, user, ip, action, result, path, target, folder, created_at
		) VALUES (
			1, 'legacy', 'alice', '192.0.2.1', 'connect', 'ok', '/legacy', '', 'legacy', CURRENT_TIMESTAMP
		)
	`).Error; err != nil {
		t.Fatalf("seed legacy Samba audit row: %v", err)
	}

	if err := dbConn.AutoMigrate(&sambaModels.SambaAuditLog{}); err != nil {
		t.Fatalf("migrate Samba audit retention: %v", err)
	}

	var entry sambaModels.SambaAuditLog
	if err := dbConn.First(&entry, 1).Error; err != nil {
		t.Fatalf("load migrated Samba audit row: %v", err)
	}
	if entry.Occurrences != 1 {
		t.Fatalf("occurrences=%d want 1", entry.Occurrences)
	}
	if got := sambaModels.AuditRetentionDaysValue(entry.RetentionDays); got != sambaModels.DefaultAuditRetentionDays {
		t.Fatalf("retention days=%d want %d", got, sambaModels.DefaultAuditRetentionDays)
	}

	forever := sambaModels.SambaAuditLog{
		Share:         "forever",
		User:          "alice",
		Action:        "connect",
		Result:        "ok",
		RetentionDays: sambaModels.AuditRetentionDaysPointer(0),
	}
	if err := dbConn.Create(&forever).Error; err != nil {
		t.Fatalf("create Samba audit row with unlimited retention: %v", err)
	}
	if err := dbConn.First(&forever, forever.ID).Error; err != nil {
		t.Fatalf("reload Samba audit row with unlimited retention: %v", err)
	}
	if got := sambaModels.AuditRetentionDaysValue(forever.RetentionDays); got != 0 {
		t.Fatalf("unlimited retention days=%d want 0", got)
	}
}
