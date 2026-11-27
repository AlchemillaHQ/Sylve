// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package samba

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func auditTestLine(stamp, user, action, path string) string {
	return fmt.Sprintf("%s host smbd_audit[42]: sylve-smb-al|%s|192.0.2.5|client|documents|/mnt/documents|%s|ok|%s\n", stamp, user, action, path)
}

func useAuditLogFile(t *testing.T, contents string) string {
	t.Helper()
	previous := auditLogPath
	auditLogPath = filepath.Join(t.TempDir(), "audit.log")
	t.Cleanup(func() { auditLogPath = previous })
	if err := os.WriteFile(auditLogPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return auditLogPath
}

func appendAuditLog(t *testing.T, text string) {
	t.Helper()
	file, err := os.OpenFile(auditLogPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func newAuditTestService(db *gorm.DB) *Service {
	return &Service{DB: db, recentMkdirs: map[string]time.Time{}}
}

func auditRowCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&sambaModels.SambaAuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func parseAudit(t *testing.T, service *Service) {
	t.Helper()
	if err := service.ParseAuditLogs(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditImportResumesAfterRestartWithoutDuplicates(t *testing.T) {
	useAuditLogFile(t,
		auditTestLine("Nov 20 10:00:00", "alice", "write", "/a.txt")+
			auditTestLine("Nov 20 10:00:05", "bob", "read", "/b.txt"))
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})

	parseAudit(t, newAuditTestService(db))
	if count := auditRowCount(t, db); count != 2 {
		t.Fatalf("first run imported %d rows; want 2", count)
	}

	restarted := newAuditTestService(db)
	parseAudit(t, restarted)
	if count := auditRowCount(t, db); count != 2 {
		t.Fatalf("a restart re-imported the log: %d rows; want 2", count)
	}
	appendAuditLog(t, auditTestLine("Nov 20 10:01:00", "carol", "write", "/c.txt"))
	parseAudit(t, restarted)
	if count := auditRowCount(t, db); count != 3 {
		t.Fatalf("after a new line there are %d rows; want 3", count)
	}
}

func TestAuditImportSkipsContentAnOlderSylveAlreadyImported(t *testing.T) {
	useAuditLogFile(t, auditTestLine("Nov 20 10:00:00", "alice", "write", "/a.txt"))
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})
	if err := db.Create(&sambaModels.SambaAuditLog{Share: "documents", User: "alice", Action: "write", Path: "/a.txt"}).Error; err != nil {
		t.Fatal(err)
	}

	service := newAuditTestService(db)
	parseAudit(t, service)
	if count := auditRowCount(t, db); count != 1 {
		t.Fatalf("an upgrade re-imported old lines: %d rows; want 1", count)
	}
	appendAuditLog(t, auditTestLine("Nov 20 10:02:00", "bob", "read", "/b.txt"))
	parseAudit(t, service)
	if count := auditRowCount(t, db); count != 2 {
		t.Fatalf("a line written after the upgrade was missed: %d rows; want 2", count)
	}
}

func TestAuditImportReadsARotatedFileFromItsStart(t *testing.T) {
	path := useAuditLogFile(t, auditTestLine("Nov 20 10:00:00", "alice", "write", "/a.txt"))
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})
	parseAudit(t, newAuditTestService(db))

	if err := os.Rename(path, path+".0"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(auditTestLine("Nov 20 11:00:00", "bob", "read", "/b.txt")), 0600); err != nil {
		t.Fatal(err)
	}
	parseAudit(t, newAuditTestService(db))
	if count := auditRowCount(t, db); count != 2 {
		t.Fatalf("after rotation there are %d rows; want 2", count)
	}
}

func TestAuditImportReadsLargeBacklogsInBoundedChunks(t *testing.T) {
	previous := auditReadChunkBytes
	auditReadChunkBytes = 300
	t.Cleanup(func() { auditReadChunkBytes = previous })

	var backlog strings.Builder
	for i := range 20 {
		backlog.WriteString(auditTestLine(fmt.Sprintf("Nov 20 10:%02d:00", i), "alice", "write", fmt.Sprintf("/file-%02d.txt", i)))
	}
	backlog.WriteString(strings.Repeat("x", 700) + "\n")
	backlog.WriteString(auditTestLine("Nov 20 11:00:00", "bob", "read", "/after-long-line.txt"))
	useAuditLogFile(t, backlog.String())
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})

	parseAudit(t, newAuditTestService(db))
	if count := auditRowCount(t, db); count != 21 {
		t.Fatalf("bounded reads imported %d rows; want 21", count)
	}
}

func TestAuditEntriesUseTheirSyslogTimeAndMergeOnlyWithinASecond(t *testing.T) {
	useAuditLogFile(t,
		auditTestLine("Nov 20 10:00:00", "alice", "write", "/a.txt")+
			auditTestLine("Nov 20 10:00:00", "alice", "write", "/a.txt")+
			auditTestLine("Nov 20 10:00:07", "alice", "write", "/a.txt"))
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaShare{}, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})
	parseAudit(t, newAuditTestService(db))

	var logs []sambaModels.SambaAuditLog
	if err := db.Order("created_at").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].Occurrences != 2 || logs[1].Occurrences != 1 {
		t.Fatalf("rows = %+v; want the same-second pair merged and the later event separate", logs)
	}
	if got := logs[0].CreatedAt.Local(); got.Month() != time.November || got.Day() != 20 || got.Hour() != 10 || got.Second() != 0 {
		t.Fatalf("createdAt = %s; want the syslog time", got)
	}
}

func TestParseSyslogTime(t *testing.T) {
	now := time.Date(2027, time.January, 1, 0, 30, 0, 0, time.Local)
	stamp, ok := parseSyslogTime([]byte("Dec 31 23:59:30 host smbd_audit[1]: x"), now)
	if !ok || stamp.Year() != 2026 {
		t.Fatalf("a December line read in January parsed as %s; want the previous year", stamp)
	}
	stamp, ok = parseSyslogTime([]byte("Jan  1 00:10:00 host smbd_audit[1]: x"), now)
	if !ok || stamp.Year() != 2027 || stamp.Day() != 1 {
		t.Fatalf("a space-padded day parsed as %s", stamp)
	}
	stamp, ok = parseSyslogTime([]byte("2026-11-20T10:00:00.5+05:30 host smbd_audit[1]: x"), now)
	if !ok || !stamp.Equal(time.Date(2026, 11, 20, 4, 30, 0, 500_000_000, time.UTC)) {
		t.Fatalf("an RFC 5424 time parsed as %s", stamp)
	}
	if _, ok := parseSyslogTime([]byte("garbage"), now); ok {
		t.Fatal("garbage parsed as a time")
	}
}

func TestGetAuditLogsFiltersAndCapsPageSize(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	rows := []sambaModels.SambaAuditLog{
		{Share: "documents", User: "Alice", Action: "write", Path: "/reports/q1_100%.txt", CreatedAt: base},
		{Share: "documents", User: "bob", Client: "Studio-Mac", IP: "192.0.2.10", Action: "read", Path: "/reports/q2.txt", CreatedAt: base.Add(time.Hour)},
		{Share: "media", User: "alice", Action: "unlinkat", Path: "/movie.mkv", CreatedAt: base.Add(2 * time.Hour)},
		{Share: "media", User: "carol", Action: "renameat", Path: "/old.mkv", Target: "/reports-archive.mkv", CreatedAt: base.Add(3 * time.Hour)},
	}
	for i := range 600 {
		rows = append(rows, sambaModels.SambaAuditLog{Share: "bulk", User: "dave", Action: "read", Path: fmt.Sprintf("/bulk/%d", i), CreatedAt: base.Add(-time.Hour)})
	}
	if err := db.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db}
	from, to := base.Add(30*time.Minute), base.Add(150*time.Minute)

	for name, tc := range map[string]struct {
		query sambaServiceInterfaces.AuditLogQuery
		want  int64
	}{
		"share":                  {sambaServiceInterfaces.AuditLogQuery{Share: "documents"}, 2},
		"user ignores case":      {sambaServiceInterfaces.AuditLogQuery{User: "ALICE"}, 2},
		"actions":                {sambaServiceInterfaces.AuditLogQuery{Actions: []string{"unlinkat", "renameat"}}, 2},
		"search path and target": {sambaServiceInterfaces.AuditLogQuery{Search: "reports"}, 3},
		"search is literal":      {sambaServiceInterfaces.AuditLogQuery{Search: "100%"}, 1},
		"underscore is literal":  {sambaServiceInterfaces.AuditLogQuery{Search: "q1_"}, 1},
		"search user":            {sambaServiceInterfaces.AuditLogQuery{Search: "CAROL"}, 1},
		"search share":           {sambaServiceInterfaces.AuditLogQuery{Search: "medi"}, 2},
		"search client":          {sambaServiceInterfaces.AuditLogQuery{Search: "studio"}, 1},
		"search ip":              {sambaServiceInterfaces.AuditLogQuery{Search: "192.0.2"}, 1},
		"time range":             {sambaServiceInterfaces.AuditLogQuery{From: &from, To: &to}, 2},
		"combined":               {sambaServiceInterfaces.AuditLogQuery{Share: "media", User: "alice"}, 1},
	} {
		response, err := service.GetAuditLogs(tc.query)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if response.Total != tc.want || int64(len(response.Data)) != tc.want {
			t.Errorf("%s: total=%d rows=%d; want %d", name, response.Total, len(response.Data), tc.want)
		}
	}

	response, err := service.GetAuditLogs(sambaServiceInterfaces.AuditLogQuery{Size: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != MaxAuditLogPageSize || response.Total != 604 || response.LastPage != 2 {
		t.Fatalf("an oversized page returned %d rows (total %d, last page %d); want %d rows", len(response.Data), response.Total, response.LastPage, MaxAuditLogPageSize)
	}
}

type statFileInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (info statFileInfo) Sys() any    { return info.stat }
func (info statFileInfo) Size() int64 { return int64(info.stat.Size) }

func TestAuditCursorSurvivesDeviceNumbersAboveTheSignedRange(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &sambaModels.SambaAuditLog{}, &sambaModels.SambaAuditCursor{})
	s := &Service{DB: db}
	info := statFileInfo{stat: &syscall.Stat_t{Dev: 12224085360050818663, Ino: 149, Size: 4096}}
	device, inode := auditFileIdentity(info)
	if err := s.insertAuditEntries(nil, 0, 0, auditCursor(device, inode, 2637)); err != nil {
		t.Fatalf("saving the read position for a ZFS device number failed: %v", err)
	}
	if offset, err := s.loadAuditCursor(info); err != nil || offset != 2637 {
		t.Fatalf("loadAuditCursor = %d, %v; want the saved position 2637", offset, err)
	}
}
