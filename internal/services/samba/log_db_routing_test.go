// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"testing"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetAuditLogsReadsFromTelemetryDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{})

	now := time.Now().UTC()

	if err := mainDB.Create(&sambaModels.SambaAuditLog{
		ID:        1,
		Share:     "legacy-main",
		User:      "legacy-user",
		IP:        "10.0.0.1",
		Action:    "mkdirat",
		Result:    "ok",
		Path:      "/legacy",
		Folder:    "legacy",
		CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed main db audit log row: %v", err)
	}

	if err := telemetryDB.Create(&sambaModels.SambaAuditLog{
		ID:        2,
		Share:     "telemetry",
		User:      "telemetry-user",
		IP:        "10.0.0.2",
		Action:    "create_file",
		Result:    "ok",
		Path:      "/telemetry",
		Folder:    "telemetry",
		CreatedAt: now.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("failed to seed telemetry db audit log row: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	resp, err := svc.GetAuditLogs(1, 100, "id", "ASC")
	if err != nil {
		t.Fatalf("GetAuditLogs returned error: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected one telemetry row, got %d", len(resp.Data))
	}

	if resp.Data[0].Share != "telemetry" {
		t.Fatalf("expected telemetry row to be returned, got share=%q", resp.Data[0].Share)
	}
}

func TestGetGlobalConfigStillReadsFromMainDB(t *testing.T) {
	mainDB := testutil.NewSQLiteTestDB(t, &sambaModels.SambaSettings{})
	telemetryDB := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{})

	expected := sambaModels.SambaSettings{
		ID:                 1,
		UnixCharset:        "UTF-8",
		Workgroup:          "WORKGROUP",
		ServerString:       "Sylve SMB Server",
		Interfaces:         "lo0",
		BindInterfacesOnly: false,
	}

	if err := mainDB.Create(&expected).Error; err != nil {
		t.Fatalf("failed to seed main db samba settings: %v", err)
	}

	svc := &Service{
		DB:          mainDB,
		TelemetryDB: telemetryDB,
	}

	got, err := svc.GetGlobalConfig()
	if err != nil {
		t.Fatalf("GetGlobalConfig returned error: %v", err)
	}

	if got.Workgroup != expected.Workgroup {
		t.Fatalf("expected workgroup %q, got %q", expected.Workgroup, got.Workgroup)
	}
}
