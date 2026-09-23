// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

const (
	testControlSocketWarning = "ControlSocket /tmp/sylve-ssh-ae77692e.sock already exists, disabling multiplexing\r\n"
	testPoolGUIDPrimary      = uint64(1234567890123456789)
	testPoolGUIDSecondary    = uint64(16320762043821906000)
)

func writeFakeLocalZpool(t *testing.T, guid uint64) {
	t.Helper()

	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' " + strconv.FormatUint(guid, 10) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "zpool"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake zpool: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunTargetSSHSuccessDropsStderrChatter(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Default: &fakeSSHResponse{
		Stdout:   strconv.FormatUint(testPoolGUIDSecondary, 10) + "\n",
		Stderr:   testControlSocketWarning,
		ExitCode: 0,
	}})

	service := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 22, BackupRoot: "tank/backups"}

	output, err := service.runTargetSSH(
		context.Background(),
		target,
		"zpool", "get", "-H", "-p", "-o", "value", "guid", "tank",
	)
	if err != nil {
		t.Fatalf("run target ssh: %v", err)
	}
	if strings.TrimSpace(output) != strconv.FormatUint(testPoolGUIDSecondary, 10) {
		t.Fatalf("stdout payload = %q", output)
	}
	if strings.Contains(output, "ControlSocket") {
		t.Fatalf("ssh chatter leaked into stdout: %q", output)
	}
}

func TestRunTargetSSHFailureKeepsStderrDiagnostics(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Default: &fakeSSHResponse{
		Stdout:   "cannot open 'tank': permission denied\n",
		Stderr:   "Permission denied (publickey).\n",
		ExitCode: 255,
	}})

	service := &Service{}
	target := &clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 22, BackupRoot: "tank/backups"}

	output, err := service.runTargetSSH(
		context.Background(),
		target,
		"zpool", "list", "-H", "-o", "name", "tank",
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"cannot open 'tank'", "Permission denied (publickey)"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v missing %q", err, want)
		}
	}
}

func TestValidateBackupScopesDoNotOverlapTargetIgnoresStderrChatter(t *testing.T) {
	h := newFakeSSHHarness(t)
	h.SetScenario(fakeSSHScenario{Default: &fakeSSHResponse{
		Stdout:   strconv.FormatUint(testPoolGUIDSecondary, 10) + "\n",
		Stderr:   testControlSocketWarning,
		ExitCode: 0,
	}})
	writeFakeLocalZpool(t, testPoolGUIDPrimary)

	service := &Service{}
	job := &clusterModels.BackupJob{
		Target: clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 22, BackupRoot: "tank/backups"},
	}

	err := service.validateBackupScopesDoNotOverlapTarget(context.Background(), job, []backupScope{
		{sourceDataset: "zroot/data", destSuffix: "zroot/data"},
	})
	if err != nil {
		t.Fatalf("validation failed with ssh chatter present: %v", err)
	}
}

func TestValidateBackupScopesDoNotOverlapTargetStillRejectsOverlap(t *testing.T) {
	for _, test := range []struct {
		name       string
		localGUID  uint64
		remoteGUID uint64
		backupRoot string
		destSuffix string
		wantErr    string
	}{
		{
			name:       "same pool with overlapping dataset trees",
			localGUID:  testPoolGUIDSecondary,
			remoteGUID: testPoolGUIDSecondary,
			backupRoot: "tank",
			destSuffix: "data",
			wantErr:    "backup_source_target_overlap",
		},
		{
			name:       "same pool with disjoint dataset trees",
			localGUID:  testPoolGUIDSecondary,
			remoteGUID: testPoolGUIDSecondary,
			backupRoot: "tank",
			destSuffix: "other",
		},
		{
			name:       "different pools with matching dataset names",
			localGUID:  testPoolGUIDPrimary,
			remoteGUID: testPoolGUIDSecondary,
			backupRoot: "tank",
			destSuffix: "data",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newFakeSSHHarness(t)
			h.SetScenario(fakeSSHScenario{Default: &fakeSSHResponse{
				Stdout:   strconv.FormatUint(test.remoteGUID, 10) + "\n",
				Stderr:   testControlSocketWarning,
				ExitCode: 0,
			}})
			writeFakeLocalZpool(t, test.localGUID)

			service := &Service{}
			job := &clusterModels.BackupJob{
				Target: clusterModels.BackupTarget{
					SSHHost:    "user@target",
					SSHPort:    22,
					BackupRoot: test.backupRoot,
				},
			}

			err := service.validateBackupScopesDoNotOverlapTarget(context.Background(), job, []backupScope{
				{sourceDataset: "zroot/data", destSuffix: test.destSuffix},
			})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validation error = %v, want %q", err, test.wantErr)
			}
		})
	}
}
