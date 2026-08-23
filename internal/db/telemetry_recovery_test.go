// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

func TestOpenTelemetryDatabaseQuarantinesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	corruptContents := []byte("this is not a sqlite database")
	if err := os.WriteFile(path, corruptContents, 0600); err != nil {
		t.Fatalf("write corrupt telemetry database: %v", err)
	}

	ormConfig := &gorm.Config{
		Logger:                                   gormLogger.Default.LogMode(gormLogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
	}
	telemetryDB, sqlDB, quarantinedPath, err := openTelemetryDatabase(path, ormConfig, true)
	if err != nil {
		t.Fatalf("recover telemetry database: %v", err)
	}
	defer sqlDB.Close()

	if quarantinedPath == "" {
		t.Fatal("expected corrupt telemetry database to be quarantined")
	}
	quarantinedContents, err := os.ReadFile(quarantinedPath)
	if err != nil {
		t.Fatalf("read quarantined telemetry database: %v", err)
	}
	if !bytes.Equal(quarantinedContents, corruptContents) {
		t.Fatalf("quarantined contents changed: got %q", quarantinedContents)
	}

	if !telemetryDB.Migrator().HasTable(&infoModels.CPU{}) {
		t.Fatal("replacement telemetry database was not migrated")
	}
	if err := checkTelemetryDatabaseIntegrity(sqlDB); err != nil {
		t.Fatalf("replacement telemetry database failed integrity check: %v", err)
	}
}

func TestOpenTelemetryDatabaseDoesNotQuarantineWhenRecoveryDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	corruptContents := []byte("this is not a sqlite database")
	if err := os.WriteFile(path, corruptContents, 0600); err != nil {
		t.Fatalf("write corrupt telemetry database: %v", err)
	}

	ormConfig := &gorm.Config{Logger: gormLogger.Default.LogMode(gormLogger.Silent)}
	_, sqlDB, quarantinedPath, err := openTelemetryDatabase(path, ormConfig, false)
	if sqlDB != nil {
		_ = sqlDB.Close()
	}
	if err == nil {
		t.Fatal("expected corrupt telemetry database to fail without recovery")
	}
	if quarantinedPath != "" {
		t.Fatalf("unexpected quarantine path: %s", quarantinedPath)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read original corrupt telemetry database: %v", readErr)
	}
	if !bytes.Equal(contents, corruptContents) {
		t.Fatalf("original corrupt database changed: got %q", contents)
	}
}

func TestQuarantineTelemetryDatabaseKeepsSidecarsTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	suffixes := []string{"", "-wal", "-shm", "-journal"}
	for _, suffix := range suffixes {
		if err := os.WriteFile(path+suffix, []byte("contents"+suffix), 0600); err != nil {
			t.Fatalf("write telemetry file %s: %v", suffix, err)
		}
	}

	existingQuarantine := path + ".corrupt-20260804T123456.000000789Z"
	if err := os.WriteFile(existingQuarantine, []byte("older quarantine"), 0600); err != nil {
		t.Fatalf("write existing quarantine: %v", err)
	}

	quarantinedPath, err := quarantineTelemetryDatabase(
		path,
		time.Date(2026, time.August, 4, 12, 34, 56, 789, time.UTC),
	)
	if err != nil {
		t.Fatalf("quarantine telemetry database: %v", err)
	}
	if quarantinedPath != existingQuarantine+"-1" {
		t.Fatalf("quarantine path = %q, want %q", quarantinedPath, existingQuarantine+"-1")
	}
	if contents, err := os.ReadFile(existingQuarantine); err != nil || string(contents) != "older quarantine" {
		t.Fatalf("existing quarantine was changed: contents=%q err=%v", contents, err)
	}

	for _, suffix := range suffixes {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("original telemetry file %s still exists or returned unexpected error: %v", suffix, err)
		}
		contents, err := os.ReadFile(quarantinedPath + suffix)
		if err != nil {
			t.Fatalf("read quarantined telemetry file %s: %v", suffix, err)
		}
		if want := "contents" + suffix; string(contents) != want {
			t.Fatalf("quarantined telemetry file %s = %q, want %q", suffix, contents, want)
		}
	}
}
