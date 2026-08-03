// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestGetJailLogsReturnsLast512Lines(t *testing.T) {
	previousConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{DataPath: t.TempDir()}
	t.Cleanup(func() {
		config.ParsedConfig = previousConfig
	})

	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	jail := jailModels.Jail{CTID: 42, Name: "test-jail"}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("create jail: %v", err)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		t.Fatalf("get jails path: %v", err)
	}
	logDir := filepath.Join(jailsPath, "42")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("create log directory: %v", err)
	}

	var fullLog strings.Builder
	var expected strings.Builder
	for line := 1; line <= 600; line++ {
		entry := fmt.Sprintf("line-%03d\n", line)
		fullLog.WriteString(entry)
		if line > 88 {
			expected.WriteString(entry)
		}
	}
	if err := os.WriteFile(filepath.Join(logDir, "42.log"), []byte(fullLog.String()), 0644); err != nil {
		t.Fatalf("write jail log: %v", err)
	}

	logs, err := (&Service{DB: db}).GetJailLogs(42)
	if err != nil {
		t.Fatalf("GetJailLogs: %v", err)
	}
	if logs != expected.String() {
		t.Fatalf("GetJailLogs returned %d lines, want 512", strings.Count(logs, "\n"))
	}
}
