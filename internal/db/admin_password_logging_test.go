// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

func captureAdminSetupLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var output bytes.Buffer
	previous := logger.L
	logger.L = zerolog.New(&output)
	t.Cleanup(func() {
		logger.L = previous
	})

	return &output
}

func newAdminSetupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(t, &models.User{})
}

func createConfiguredAdminTestUser(t *testing.T, database *gorm.DB, password string) models.User {
	t.Helper()

	hashed, err := utils.HashPassword(password)
	if err != nil {
		t.Fatalf("failed_to_hash_test_password: %v", err)
	}
	user := models.User{
		Username: "admin",
		Email:    "old@example.test",
		Password: hashed,
		Source:   "local",
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatalf("failed_to_create_test_admin: %v", err)
	}
	return user
}

func TestSetupConfiguredAdminAppliesAndVerifiesForceReset(t *testing.T) {
	logs := captureAdminSetupLogs(t)
	database := newAdminSetupTestDB(t)
	oldUser := createConfiguredAdminTestUser(t, database, "old-admin-secret")
	const newPassword = "new-admin-secret"

	err := setupConfiguredAdmin(database, internal.BaseConfigAdmin{
		Email:              "admin@sylve.local",
		Password:           newPassword,
		ForcePasswordReset: true,
	})
	if err != nil {
		t.Fatalf("expected force reset to succeed: %v", err)
	}

	var persisted models.User
	if err := database.Where("username = ?", "admin").First(&persisted).Error; err != nil {
		t.Fatalf("failed_to_reload_admin: %v", err)
	}
	if !utils.CheckPasswordHash(newPassword, persisted.Password) {
		t.Fatal("persisted admin password does not match reset password")
	}
	if persisted.Email != "admin@sylve.local" || !persisted.Admin {
		t.Fatalf("expected admin metadata update, got email=%q admin=%t", persisted.Email, persisted.Admin)
	}

	output := logs.String()
	if !strings.Contains(output, `"outcome":"force_reset_applied"`) {
		t.Fatalf("expected verified reset log, got: %s", output)
	}
	if strings.Contains(output, newPassword) || strings.Contains(output, oldUser.Password) || strings.Contains(output, persisted.Password) {
		t.Fatalf("admin credential material was written to logs: %s", output)
	}
}

func TestSetupConfiguredAdminLogsAlreadyMatchingForceReset(t *testing.T) {
	logs := captureAdminSetupLogs(t)
	database := newAdminSetupTestDB(t)
	const password = "matching-admin-secret"
	createConfiguredAdminTestUser(t, database, password)

	if err := setupConfiguredAdmin(database, internal.BaseConfigAdmin{
		Email:              "old@example.test",
		Password:           password,
		ForcePasswordReset: true,
	}); err != nil {
		t.Fatalf("expected matching reset verification to succeed: %v", err)
	}

	if !strings.Contains(logs.String(), `"outcome":"force_reset_already_matched"`) {
		t.Fatalf("expected already-matched reset log, got: %s", logs.String())
	}
}

func TestSetupConfiguredAdminLogsIgnoredPasswordWithoutForceReset(t *testing.T) {
	logs := captureAdminSetupLogs(t)
	database := newAdminSetupTestDB(t)
	createConfiguredAdminTestUser(t, database, "stored-admin-secret")

	if err := setupConfiguredAdmin(database, internal.BaseConfigAdmin{
		Email:    "old@example.test",
		Password: "different-config-secret",
	}); err != nil {
		t.Fatalf("expected admin setup to succeed: %v", err)
	}

	var persisted models.User
	if err := database.Where("username = ?", "admin").First(&persisted).Error; err != nil {
		t.Fatalf("failed_to_reload_admin: %v", err)
	}
	if !utils.CheckPasswordHash("stored-admin-secret", persisted.Password) {
		t.Fatal("admin password changed without force reset")
	}
	if !strings.Contains(logs.String(), `"outcome":"ignored_force_reset_disabled"`) {
		t.Fatalf("expected ignored-password log, got: %s", logs.String())
	}
}

func TestSetupConfiguredAdminRejectsEmptyForcedPassword(t *testing.T) {
	logs := captureAdminSetupLogs(t)
	database := newAdminSetupTestDB(t)
	createConfiguredAdminTestUser(t, database, "stored-admin-secret")

	err := setupConfiguredAdmin(database, internal.BaseConfigAdmin{ForcePasswordReset: true})
	if err == nil || !strings.Contains(err.Error(), "non-empty configured password") {
		t.Fatalf("expected empty forced password error, got: %v", err)
	}
	if !strings.Contains(logs.String(), `"outcome":"rejected_empty_password"`) {
		t.Fatalf("expected rejected reset log, got: %s", logs.String())
	}
}

func TestSetupConfiguredAdminLogsInitialCreation(t *testing.T) {
	logs := captureAdminSetupLogs(t)
	database := newAdminSetupTestDB(t)
	const password = "initial-admin-secret"

	if err := setupConfiguredAdmin(database, internal.BaseConfigAdmin{
		Email:    "admin@sylve.local",
		Password: password,
	}); err != nil {
		t.Fatalf("expected initial admin creation to succeed: %v", err)
	}

	var persisted models.User
	if err := database.Where("username = ?", "admin").First(&persisted).Error; err != nil {
		t.Fatalf("failed_to_load_created_admin: %v", err)
	}
	if !utils.CheckPasswordHash(password, persisted.Password) || !persisted.Admin {
		t.Fatal("created admin credentials or privileges are invalid")
	}
	if !strings.Contains(logs.String(), `"outcome":"created"`) {
		t.Fatalf("expected creation log, got: %s", logs.String())
	}
}
