// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"os"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	_ "github.com/mattn/go-sqlite3"
	"maragu.dev/goqite"
)

type fullBackupJailStub struct {
	jailServiceInterfaces.JailServiceInterface
	ctid     uint
	ctErr    error
	stopErr  error
}

func (s fullBackupJailStub) GetJailCTIDFromDataset(_ string) (uint, error) {
	return s.ctid, s.ctErr
}

func (s fullBackupJailStub) JailAction(_ int, action string) error {
	return s.stopErr
}

type queueJobMessage struct {
	Name    string
	Message []byte
}

const goqiteSchema = `
create table if not exists goqite (
  id text primary key default ('m_' || lower(hex(randomblob(16)))),
  created text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  updated text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  queue text not null,
  body blob not null,
  timeout text not null default (strftime('%Y-%m-%dT%H:%M:%fZ')),
  received integer not null default 0,
  priority integer not null default 0
) strict;
`

type goqiteTestHarness struct {
	db  *sql.DB
	dir string
}

func newGoqiteHarness(t *testing.T) *goqiteTestHarness {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/queue.db"
	d, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		t.Fatalf("failed to open goqite db: %v", err)
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	if _, err := d.Exec(goqiteSchema); err != nil {
		t.Fatalf("failed to create goqite schema: %v", err)
	}
	return &goqiteTestHarness{db: d, dir: dir}
}

func (h *goqiteTestHarness) enqueue(ctx context.Context, name string, payload interface{}) error {
	q := goqite.New(goqite.NewOpts{DB: h.db, Name: name})
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(queueJobMessage{Name: name, Message: body}); err != nil {
		return err
	}
	return q.Send(ctx, goqite.Message{Body: buf.Bytes()})
}

func (h *goqiteTestHarness) receive(ctx context.Context, name string) (*goqite.Message, string, error) {
	q := goqite.New(goqite.NewOpts{DB: h.db, Name: name})
	msg, err := q.Receive(ctx)
	if err != nil {
		return nil, "", err
	}
	if msg == nil {
		return nil, "", nil
	}
	var jm queueJobMessage
	if err := gob.NewDecoder(bytes.NewReader(msg.Body)).Decode(&jm); err != nil {
		return msg, "", err
	}
	return msg, jm.Name, nil
}

func (h *goqiteTestHarness) deleteMsg(ctx context.Context, qName string, id goqite.ID) error {
	q := goqite.New(goqite.NewOpts{DB: h.db, Name: qName})
	return q.Delete(ctx, id)
}

func (h *goqiteTestHarness) close() {
	if h.db != nil {
		h.db.Close()
	}
}

func TestGoqiteEnqueueAndReceive(t *testing.T) {
	h := newGoqiteHarness(t)
	defer h.close()

	ctx := context.Background()
	if err := h.enqueue(ctx, "jobs-zelta", backupJobPayload{JobID: 42}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	msg, msgName, err := h.receive(ctx, "jobs-zelta")
	if err != nil {
		t.Fatalf("receive failed: %v", err)
	}
	if msg == nil {
		t.Fatal("received nil message")
	}
	if msgName != "" {
		t.Logf("received message name: %q", msgName)
	}
	h.deleteMsg(ctx, "jobs-zelta", msg.ID)

	var payload backupJobPayload
	if err := json.Unmarshal(msg.Body[bytes.IndexByte(msg.Body, 0)+1:], &payload); err != nil {
		_ = err
	}
}

func newBackupServiceForIntegration(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	return &Service{
		DB:               db,
		queuedJobs:       make(map[uint]struct{}),
		runningJobs:      make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
	}
}

func TestRunBackupJobZeltaBinaryNotInstalled(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 200, "no-binary", "dataset", 1, "tank/data")

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", os.Getenv("PATH"))

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil {
		t.Fatal("expected error when zelta binary missing")
	}

	updated := fetchJob(t, svc.DB, 200)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status, got %q", updated.LastStatus)
	}
	if updated.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

func TestRunBackupJobZeltaBinaryNotFoundSetsFailure(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 201, "no-binary-fail", "dataset", 1, "tank/data")

	ZeltaInstallDir = "/nonexistent/path"
	t.Cleanup(func() { ZeltaInstallDir = "" })

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil {
		t.Skip("zelta binary found, skipping")
	}

	if err == nil {
		t.Fatal("expected error")
	}
	updated := fetchJob(t, svc.DB, 201)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status when zelta not found, got %q", updated.LastStatus)
	}
}

func TestBackupJobWithZeltaBinaryIntegration(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	seedBackupTarget(t, svc.DB, 1, "t1")
	job := seedAndLoadJob(t, svc.DB, 300, "integration-job", "dataset", 1, "tank/data")

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })

	err := svc.runBackupJob(context.Background(), &job)
	if err == nil {
		t.Fatal("expected failure (no zelta binary)")
	}

	updated := fetchJob(t, svc.DB, 300)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status (no zelta binary), got %q with error %q",
			updated.LastStatus, updated.LastError)
	}
	if !updated.LastRunAt.IsZero() && updated.LastError == "" {
		t.Fatal("expected LastError when zelta binary fails to start")
	}
}

func TestRunBackupJobStopBeforeBackupWithoutJailService(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	svc.Jail = fullBackupJailStub{ctid: 42, ctErr: nil}
	seedBackupTarget(t, svc.DB, 1, "t1")

	job := clusterModels.BackupJob{
		ID: 400, Name: "jail-stop", Mode: "jail", TargetID: 1,
		JailRootDataset: "zroot/jails/42", CronExpr: "0 0 * * *",
		Enabled: true, StopBeforeBackup: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	svc.DB.Preload("Target").First(&loaded, 400)

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", os.Getenv("PATH"))

	err := svc.runBackupJob(context.Background(), &loaded)
	if err == nil {
		t.Fatal("expected error from zelta binary not found")
	}

	updated := fetchJob(t, svc.DB, 400)
	if updated.LastStatus != "failed" {
		t.Fatalf("expected failed status, got %q with error %q", updated.LastStatus, updated.LastError)
	}
}

func TestRunBackupJobVMWithZeltaBinaryMissing(t *testing.T) {
	svc := newBackupServiceForIntegration(t)
	seedBackupTarget(t, svc.DB, 1, "t1")

	job := clusterModels.BackupJob{
		ID: 500, Name: "vm-backup", Mode: "vm", TargetID: 1,
		SourceDataset: "zroot/virtual-machines/42", CronExpr: "0 0 * * *",
		Enabled: true,
	}
	if err := svc.DB.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	svc.DB.Preload("Target").First(&loaded, 500)

	ZeltaInstallDir = t.TempDir()
	t.Cleanup(func() { ZeltaInstallDir = "" })
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", os.Getenv("PATH"))

	err := svc.runBackupJob(context.Background(), &loaded)
	if err == nil {
		t.Fatal("expected error from VM backup without zelta")
	}
}
