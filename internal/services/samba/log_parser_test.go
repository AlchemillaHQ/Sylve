// SPDX-License-Identifier: BSD-2-Clause

package samba

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestParseAuditLineSupportsEverySelectableOperation(t *testing.T) {
	tests := []struct {
		action string
		args   string
		path   string
		target string
	}{
		{action: "connect", args: "documents", path: "/zroot/documents"},
		{action: "disconnect", args: "documents", path: "/zroot/documents"},
		{action: "mkdirat", args: "dirfd|/documents/new-folder", path: "/documents/new-folder"},
		{action: "unlinkat", args: "dirfd|/documents/old.txt", path: "/documents/old.txt"},
		{action: "renameat", args: "/documents/old.txt|/documents/new.txt", path: "/documents/old.txt", target: "/documents/new.txt"},
		{action: "create_file", args: "0x0|create|/documents/new.txt", path: "/documents/new.txt"},
		{action: "openat", args: "dirfd|0x0|/documents/open.txt", path: "/documents/open.txt"},
		{action: "close", args: "fd|/documents/close.txt", path: "/documents/close.txt"},
		{action: "read", args: "4096|0|/documents/read.txt", path: "/documents/read.txt"},
		{action: "write", args: "4096|0|/documents/write.txt", path: "/documents/write.txt"},
	}

	service := &Service{recentMkdirs: make(map[string]time.Time)}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			line := []byte("Aug 19 12:00:00 host smbd_audit[42]: sylve-smb-al|alice|192.0.2.5|client|documents|/zroot/documents|" + test.action + "|ok|" + test.args)
			entry, ok := service.parseAuditLine(line)
			if !ok {
				t.Fatalf("operation %q was rejected", test.action)
			}
			if entry.User != "alice" || entry.Client != "client" || entry.IP != "192.0.2.5" || entry.Share != "documents" {
				t.Fatalf("actor fields were not parsed: %+v", entry)
			}
			if entry.Action != test.action || entry.Path != test.path || entry.Target != test.target {
				t.Fatalf("entry = %+v, want action=%q path=%q target=%q", entry, test.action, test.path, test.target)
			}
		})
	}
}

func TestParseAuditLineMatchesObservedSambaOutput(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		user   string
		ip     string
		client string
		share  string
		action string
		path   string
	}{
		{
			name:   "connect",
			line:   "Aug 19 03:10:06 ares smbd_audit[53204]: sylve-smb-al|hayzam|10.10.30.105|mac|projects|/zroot/projects|connect|ok|projects",
			user:   "hayzam",
			ip:     "10.10.30.105",
			client: "mac",
			share:  "projects",
			action: "connect",
			path:   "/zroot/projects",
		},
		{
			name:   "create file",
			line:   "Aug 19 03:14:45 ares smbd_audit[54276]: sylve-smb-al|zed|10.10.30.105|mac|fruit|/zroot/fruit|create_file|ok|0x100080|dir|open|/zroot/fruit",
			user:   "zed",
			ip:     "10.10.30.105",
			client: "mac",
			share:  "fruit",
			action: "create_file",
			path:   "/zroot/fruit",
		},
	}

	service := &Service{recentMkdirs: make(map[string]time.Time)}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry, ok := service.parseAuditLine([]byte(test.line))
			if !ok {
				t.Fatal("observed Samba audit line was rejected")
			}
			if entry.User != test.user || entry.Client != test.client || entry.IP != test.ip || entry.Share != test.share || entry.Action != test.action || entry.Path != test.path {
				t.Fatalf("entry = %+v", entry)
			}
		})
	}
}

func TestParseAuditLineClassifiesObservedCreateFileFields(t *testing.T) {
	service := &Service{recentMkdirs: make(map[string]time.Time)}
	line := []byte("Aug 19 03:14:45 ares smbd_audit[54276]: sylve-smb-al|zed|10.10.30.105|mac|fruit|/zroot/fruit|create_file|ok|0x81|dir|create|/zroot/fruit/new folder")
	entry, ok := service.parseAuditLine(line)
	if !ok {
		t.Fatal("observed create_file line was rejected")
	}
	if entry.ObjectType != "dir" || entry.Disposition != "create" || entry.Path != "/zroot/fruit/new folder" {
		t.Fatalf("create_file metadata = %+v", entry)
	}
}

func TestParseAuditLogsRetainsIncompleteLine(t *testing.T) {
	previousPath := auditLogPath
	auditLogPath = filepath.Join(t.TempDir(), "audit.log")
	t.Cleanup(func() { auditLogPath = previousPath })

	prefix := "Aug 19 12:00:00 host smbd_audit[42]: sylve-smb-al|alice|192.0.2.5|client|documents|42|write|ok|4096|0|"
	if err := os.WriteFile(auditLogPath, []byte(prefix), 0600); err != nil {
		t.Fatalf("write incomplete audit line: %v", err)
	}
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{})
	service := &Service{DB: dbConn, recentMkdirs: make(map[string]time.Time)}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse incomplete audit line: %v", err)
	}
	if service.auditFileOffset != 0 {
		t.Fatalf("offset advanced past incomplete line: %d", service.auditFileOffset)
	}

	f, err := os.OpenFile(auditLogPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open audit log for append: %v", err)
	}
	if _, err := f.WriteString("/documents/write.txt\n"); err != nil {
		f.Close()
		t.Fatalf("finish audit line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close audit log: %v", err)
	}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse completed audit line: %v", err)
	}
	var logs []sambaModels.SambaAuditLog
	if err := dbConn.Find(&logs).Error; err != nil {
		t.Fatalf("load parsed audit logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Action != "write" || logs[0].Path != "/documents/write.txt" {
		t.Fatalf("unexpected parsed logs: %+v", logs)
	}
}

func TestParseAuditLogsHandlesRotationAndAggregatesRepeats(t *testing.T) {
	previousPath := auditLogPath
	auditLogPath = filepath.Join(t.TempDir(), "audit.log")
	t.Cleanup(func() { auditLogPath = previousPath })
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{})
	service := &Service{DB: dbConn, recentMkdirs: make(map[string]time.Time)}
	line := "Aug 19 12:00:00 host smbd_audit[42]: sylve-smb-al|alice|192.0.2.5|client|documents|/documents|write|ok|4096|0|/documents/file.txt\n"
	if err := os.WriteFile(auditLogPath, []byte(line), 0600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse audit log: %v", err)
	}
	f, err := os.OpenFile(auditLogPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open audit log for repeat marker: %v", err)
	}
	if _, err := f.WriteString("Aug 19 12:00:00 host syslogd: last message repeated 2 times\n"); err != nil {
		f.Close()
		t.Fatalf("append repeat marker: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close audit log after repeat marker: %v", err)
	}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse delayed repeat marker: %v", err)
	}
	if err := os.Rename(auditLogPath, auditLogPath+".0"); err != nil {
		t.Fatalf("rotate audit log: %v", err)
	}
	connect := "Aug 19 12:00:01 host smbd_audit[43]: sylve-smb-al|bob|192.0.2.6|laptop|documents|/documents|connect|ok|documents\n"
	if err := os.WriteFile(auditLogPath, []byte(connect), 0600); err != nil {
		t.Fatalf("write replacement audit log: %v", err)
	}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse rotated audit log: %v", err)
	}
	var logs []sambaModels.SambaAuditLog
	if err := dbConn.Order("id").Find(&logs).Error; err != nil {
		t.Fatalf("load audit logs: %v", err)
	}
	if len(logs) != 2 || logs[0].Occurrences != 3 || logs[1].Action != "connect" || logs[1].User != "bob" {
		t.Fatalf("unexpected audit logs after rotation: %+v", logs)
	}
}

func TestParseAuditLogsStoresCreatesButSkipsOpenProbes(t *testing.T) {
	previousPath := auditLogPath
	auditLogPath = filepath.Join(t.TempDir(), "audit.log")
	t.Cleanup(func() { auditLogPath = previousPath })
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{})
	share := sambaModels.SambaShare{
		Name:               "fruit",
		Dataset:            "fruit-guid",
		AuditRetentionDays: sambaModels.AuditRetentionDaysPointer(14),
	}
	if err := dbConn.Create(&share).Error; err != nil {
		t.Fatalf("create share: %v", err)
	}
	prefix := "Aug 19 03:14:45 ares smbd_audit[42]: sylve-smb-al|zed|10.10.30.105|mac|fruit|/zroot/fruit|create_file|ok|"
	data := prefix + "0x100080|dir|open|/zroot/fruit\n" +
		prefix + "0x81|dir|create|/zroot/fruit/new folder\n" +
		strings.Replace(prefix, "|ok|", "|fail (Permission denied)|", 1) + "0x81|file|open|/zroot/fruit/private\n"
	if err := os.WriteFile(auditLogPath, []byte(data), 0600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	service := &Service{DB: dbConn, recentMkdirs: make(map[string]time.Time)}
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatalf("parse audit log: %v", err)
	}
	var logs []sambaModels.SambaAuditLog
	if err := dbConn.Find(&logs).Error; err != nil {
		t.Fatalf("load audit logs: %v", err)
	}
	if len(logs) != 2 || logs[0].Path != "/zroot/fruit/new folder" || logs[1].Result != "fail (Permission denied)" || logs[0].ShareID != uint(share.ID) || sambaModels.AuditRetentionDaysValue(logs[0].RetentionDays) != 14 {
		t.Fatalf("unexpected high-signal audit logs: %+v", logs)
	}
}

func TestPruneAuditLogsHonorsPerRowRetentionAndForever(t *testing.T) {
	dbConn := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{})
	now := time.Now().UTC()
	logs := []sambaModels.SambaAuditLog{
		{Share: "expired", RetentionDays: sambaModels.AuditRetentionDaysPointer(70), CreatedAt: now.AddDate(0, 0, -71)},
		{Share: "recent", RetentionDays: sambaModels.AuditRetentionDaysPointer(70), CreatedAt: now.AddDate(0, 0, -69)},
		{Share: "forever", RetentionDays: sambaModels.AuditRetentionDaysPointer(0), CreatedAt: now.AddDate(-5, 0, 0)},
	}
	if err := dbConn.Create(&logs).Error; err != nil {
		t.Fatalf("create audit fixtures: %v", err)
	}
	service := &Service{DB: dbConn}
	if err := service.PruneAuditLogs(now); err != nil {
		t.Fatalf("prune audit logs: %v", err)
	}
	var remaining []sambaModels.SambaAuditLog
	if err := dbConn.Order("id").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining logs: %v", err)
	}
	if len(remaining) != 2 || remaining[0].Share != "recent" || remaining[1].Share != "forever" {
		t.Fatalf("unexpected remaining logs: %+v", remaining)
	}
}
