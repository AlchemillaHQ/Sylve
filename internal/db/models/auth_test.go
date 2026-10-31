// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package models_test

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestPAMOwnershipMigrationPreservesManagedAccounts(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	for _, query := range []string{
		"CREATE TABLE users (id integer PRIMARY KEY, username text, source text, uid integer)",
		"INSERT INTO users (id, username, source, uid) VALUES (1, 'alice', 'pam', 1001)",
		"CREATE TABLE groups (id integer PRIMARY KEY, name text)",
		"INSERT INTO groups (id, name) VALUES (1, 'staff')",
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&models.User{}, &models.Group{}); err != nil {
		t.Fatal(err)
	}
	var user models.User
	var group models.Group
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&group, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.Username != "alice" || user.Source != "pam" || user.UID != 1001 || user.ExternallyManaged || group.ExternallyManaged {
		t.Fatalf("migration changed existing ownership: user=%+v group=%+v", user, group)
	}
}
