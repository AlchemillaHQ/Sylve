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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func extractZeltaToTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ZeltaInstallDir = dir
	t.Cleanup(func() { ZeltaInstallDir = "" })
	if err := EnsureZeltaInstalled(); err != nil {
		t.Fatalf("failed to extract zelta: %v", err)
	}
	return dir
}

func TestZeltaBinaryExtracts(t *testing.T) {
	dir := extractZeltaToTemp(t)

	bin := filepath.Join(dir, "bin", "zelta")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("zelta binary not found at %s: %v", bin, err)
	}
}

func TestEnsureZeltaInstalledRefreshesChangedEmbeddedAssets(t *testing.T) {
	dir := extractZeltaToTemp(t)
	const asset = "zelta/share/zelta/zelta-common.awk"

	expected, err := zeltaFS.ReadFile(asset)
	if err != nil {
		t.Fatalf("failed to read embedded asset: %v", err)
	}

	installed := filepath.Join(dir, "share", "zelta", "zelta-common.awk")
	if err := os.WriteFile(installed, []byte("stale asset\n"), 0644); err != nil {
		t.Fatalf("failed to replace installed asset: %v", err)
	}
	if err := EnsureZeltaInstalled(); err != nil {
		t.Fatalf("failed to refresh zelta assets: %v", err)
	}

	actual, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("failed to read refreshed asset: %v", err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("installed asset was not refreshed from the embedded copy")
	}
}

func TestEmbeddedZeltaParsesBracketedIPv6Endpoints(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("awk unavailable")
	}
	dir := extractZeltaToTemp(t)
	// ipc-env prints the options zelta resolved from its arguments without running a verb.
	cmd := exec.Command(filepath.Join(dir, "bin", "zelta"), "ipc-env", "backup",
		"tank/source", "root@[2001:db8::1]:tank/backups@bk_test")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"),
		"ZELTA_SHARE="+filepath.Join(dir, "share", "zelta"),
		"ZELTA_ENV=/dev/null",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zelta ipc-env: %v: %s", err, output)
	}
	for _, want := range []string{
		"export ZELTA_TGT_REMOTE=root@2001:db8::1\n",
		"export ZELTA_TGT_HOST=2001:db8::1\n",
		"export ZELTA_TGT_DS=tank/backups\n",
		"export ZELTA_TGT_SNAP=@bk_test\n",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("missing %q in:\n%s", want, output)
		}
	}
}

func TestEmbeddedZeltaSSHSerializationPreservesPolicyAndStreamInput(t *testing.T) {
	resetZeltaTestGlobals(t)
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk is not installed")
	}
	dir := t.TempDir()
	SSHKeyDirectory = filepath.Join(dir, "host keys with spaces and 'quotes'")
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nfor arg do printf '<%s>\\n' \"$arg\"; done\nfor arg do if [ \"$arg\" = '-n' ]; then exit 0; fi; done\ncat\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "root@backup", SSHHostKey: testutil.SSHHostKey(t), SSHPort: 2222,
		SSHKeyPath: filepath.Join(dir, "login key with spaces and 'quotes'"), BackupRoot: "tank/backups"}
	if err := os.WriteFile(target.SSHKeyPath, []byte("login key"), 0600); err != nil {
		t.Fatal(err)
	}
	direct, err := service.buildSSHArgs(target)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := service.buildZeltaEnv(target)
	if err != nil {
		t.Fatal(err)
	}
	common, err := zeltaFS.ReadFile("zelta/share/zelta/zelta-common.awk")
	if err != nil {
		t.Fatal(err)
	}
	source := string(common)
	start := strings.Index(source, "function remote_str")
	end := strings.Index(source, "## Command builder")
	if start < 0 || end <= start {
		t.Fatal("bundled SSH command builder not found")
	}
	program := source[start:end] + `
BEGIN {
 Opt["DST_REMOTE"] = "root@backup"
 ep["REMOTE"] = "root@backup"
 Opt["REMOTE_DEFAULT"] = ENVIRON["ZELTA_REMOTE_DEFAULT"]
 Opt["REMOTE_SEND"] = ENVIRON["ZELTA_REMOTE_SEND"]
 Opt["REMOTE_RECV"] = ENVIRON["ZELTA_REMOTE_RECV"]
 print remote_str("DST", "DEFAULT") " 'zfs' 'version'"
 print remote_str("DST", "SEND") " 'zfs' 'version'"
 print remote_str("DST", "RECV") " 'zfs' 'version'"
 print get_remote_cmd(ep, "RECV") " 'zfs' 'version'"
}
`
	cmd := exec.Command(awk, "-f", "-")
	cmd.Env, cmd.Stdin = append(os.Environ(), environment...), strings.NewReader(program)
	serialized, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bundled builder: %v %s", err, serialized)
	}
	commands := strings.Split(strings.TrimSpace(string(serialized)), "\n")
	if len(commands) != 4 {
		t.Fatalf("commands=%q", serialized)
	}
	base := make([]string, 0, len(direct))
	for _, arg := range direct {
		if arg != "-n" {
			base = append(base, arg)
		}
	}
	for i, command := range commands {
		args := append([]string(nil), base...)
		if i < 2 {
			args = append(args, "-n")
		}
		args = append(args, "root@backup", "zfs", "version")
		var expected strings.Builder
		for _, arg := range args {
			expected.WriteString("<" + arg + ">\n")
		}
		if i >= 2 {
			expected.WriteString("stream input\n")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Stdin = strings.NewReader("stream input\n")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil || string(output) != expected.String() {
			t.Fatalf("command %d changed policy, paths, or input: %v\nwant=%q\ngot=%q", i, err, expected.String(), output)
		}
	}
}

func TestIntegrationRunBackupJobWithEmbeddedZelta(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	hostKey := requireLocalhostBackupSSH(t)

	poolName, gzfsClient, cleanup := zfstest.SharedPool(t)
	defer cleanup()
	_ = gzfsClient

	zfstest.EnsureDataset(t, gzfsClient, poolName+"/source/backup")
	zfstest.EnsureDataset(t, gzfsClient, poolName+"/target")

	ctx := context.Background()
	zfsBin, _ := exec.LookPath("zfs")
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/source/backup").CombinedOutput()
	exec.CommandContext(ctx, zfsBin, "set", "mountpoint=legacy", poolName+"/target").CombinedOutput()

	extractZeltaToTemp(t)

	db := newZeltaServiceTestDB(t, &clusterModels.BackupJob{}, &clusterModels.BackupTarget{}, &clusterModels.BackupEvent{})
	svc := &Service{
		DB:                db,
		Cluster:           newTestClusterService(db, ""),
		queuedJobs:        make(map[uint]struct{}),
		runningJobs:       make(map[uint]struct{}),
		runningWorkloadOp: make(map[string]string),
		GZFS:              gzfsClient,
	}

	target := clusterModels.BackupTarget{
		ID: 1, Name: "local-test", SSHHost: "root@localhost",
		BackupRoot: poolName + "/target", Enabled: true,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("failed to seed target: %v", err)
	}
	seedBackupTargetHostTrust(t, db, &target, hostKey)

	job := clusterModels.BackupJob{
		ID: 1, Name: "ephemeral-test", Mode: "dataset", TargetID: 1,
		SourceDataset: poolName + "/source/backup",
		CronExpr:      "0 0 * * *", Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to seed job: %v", err)
	}
	var loaded clusterModels.BackupJob
	db.Preload("Target").First(&loaded, 1)

	err := svc.runBackupJob(ctx, &loaded)
	if err != nil {
		t.Fatalf("runBackupJob failed after localhost SSH preflight: %v", err)
	}

	updated := fetchJob(t, db, 1)
	if updated.LastRunAt == nil || updated.LastStatus != "success" {
		t.Fatalf("backup did not succeed: status=%q error=%q", updated.LastStatus, updated.LastError)
	}
	if updated.LastRunAt != nil {
		t.Logf("backup ran at %v with status=%q error=%q",
			updated.LastRunAt, updated.LastStatus, updated.LastError)
	}

	var events []clusterModels.BackupEvent
	db.Find(&events)
	if len(events) != 1 || events[0].Status != "success" {
		t.Fatalf("expected one successful backup event, got %+v", events)
	}
	t.Logf("created %d backup events", len(events))
	for i, e := range events {
		t.Logf("  event[%d]: status=%q source=%q target=%q output=%d bytes",
			i, e.Status, e.SourceDataset, e.TargetEndpoint, len(e.Output))
		if e.Output != "" {
			t.Logf("  event[%d] output tail: %s", i, lastLines(e.Output, 3))
		}
	}

	snapshots := listZFSSnapshots(t, poolName+"/source/backup")
	if len(snapshots) == 0 {
		t.Fatal("backup created no source snapshots")
	}
	t.Logf("ZFS snapshots on source: %v", snapshots)
	snapshots = listZFSSnapshots(t, poolName+"/target")
	if len(snapshots) == 0 {
		t.Fatal("backup created no target snapshots")
	}
	t.Logf("ZFS snapshots on target: %v", snapshots)
}

func listZFSSnapshots(t *testing.T, dataset string) []string {
	t.Helper()
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "zfs", "list", "-H", "-o", "name", "-t", "snapshot", "-r", dataset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := make([]string, 0)
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
