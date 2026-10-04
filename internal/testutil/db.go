// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"golang.org/x/crypto/ssh"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func NewSQLiteTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if len(migrateModels) > 0 {
		if err := db.AutoMigrate(migrateModels...); err != nil {
			t.Fatalf("failed to migrate test tables: %v", err)
		}
	}
	if db.Migrator().HasTable("replication_policies") {
		replicationguard.MarkPolicySchemaReady(db)
	}
	if db.Migrator().HasTable("replication_guest_operations") {
		replicationguard.MarkGuestOperationSchemaReady(db)
	}

	return db
}

func SSHHostKey(t *testing.T) string {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}
