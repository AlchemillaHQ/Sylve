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
	"reflect"
	"slices"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestValidateTargetCandidateCleansStagedKeyOnFailure(t *testing.T) {
	resetZeltaTestGlobals(t)
	SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		t.Fatalf("create key dir: %v", err)
	}
	target := &clusterModels.BackupTarget{ID: 99, SSHKey: "candidate-key", SSHHost: "root@backup"}
	err := (&Service{}).ValidateTargetCandidate(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "backup_root_required") {
		t.Fatalf("validation error=%v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(SSHKeyDirectory, ".target-validation-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("staged key leak matches=%v err=%v", matches, globErr)
	}
}

func TestValidateTargetWithFakeSSH(t *testing.T) {
	for _, test := range []struct {
		name      string
		target    clusterModels.BackupTarget
		wantError string
	}{
		{
			name:      "backup root required",
			target:    clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 22, BackupRoot: "   "},
			wantError: "backup_root_required",
		},
		{
			name:      "unsafe destination",
			target:    clusterModels.BackupTarget{SSHHost: "-oProxyCommand=touch", SSHPort: 22, BackupRoot: "tank/backups"},
			wantError: "invalid_ssh_host",
		},
		{
			name:      "unsafe dataset",
			target:    clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 22, BackupRoot: "tank/backups;touch"},
			wantError: "invalid_backup_root",
		},
		{
			name:      "invalid port",
			target:    clusterModels.BackupTarget{SSHHost: "user@target", SSHPort: 65536, BackupRoot: "tank/backups"},
			wantError: "invalid_ssh_port",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newFakeSSHHarness(t)
			err := (&Service{}).ValidateTarget(context.Background(), &test.target)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validation error = %v, want %q", err, test.wantError)
			}
			if calls := h.Calls(); len(calls) != 0 {
				t.Fatalf("invalid boundary invoked ssh: %#v", calls)
			}
		})
	}

	t.Run("dataset already exists", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{Stdout: "tank/backups\n", ExitCode: 0},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target",
			SSHPort:    22,
			BackupRoot: "tank/backups",
		}

		if err := s.ValidateTarget(context.Background(), target); err != nil {
			t.Fatalf("ValidateTarget failed: %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("dataset already exists with create flag enabled", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{Stdout: "tank/backups\n", ExitCode: 0},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			SSHPort:          22,
			BackupRoot:       "tank/backups",
			CreateBackupRoot: true,
		}

		if err := s.ValidateTarget(context.Background(), target); err != nil {
			t.Fatalf("ValidateTarget failed: %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("dataset missing without create flag is rejected without mutation", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
			},
		}})
		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			BackupRoot:       "tank/backups",
			CreateBackupRoot: false,
		}
		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_not_found") {
			t.Fatalf("validation error = %v", err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("managed candidate honors disabled create flag", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
			},
		}})
		SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
			t.Fatalf("create key dir: %v", err)
		}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			ID:         44, SSHHost: "user@target", SSHKey: "candidate-key",
			BackupRoot: "tank/backups", CreateBackupRoot: false,
		}
		err := (&Service{}).ValidateTargetCandidate(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_not_found") {
			t.Fatalf("candidate validation error = %v", err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
		matches, globErr := filepath.Glob(filepath.Join(SSHKeyDirectory, ".target-validation-*"))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("staged key leak matches=%v err=%v", matches, globErr)
		}
	})

	t.Run("managed candidate plans missing root without creating", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
			},
			"zpool list -H -o name tank": {{Stdout: "tank\n", ExitCode: 0}},
		}})

		SSHKeyDirectory = filepath.Join(t.TempDir(), "ssh")
		if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
			t.Fatalf("create key dir: %v", err)
		}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			ID:         45, SSHHost: "user@target", SSHKey: "candidate-key",
			BackupRoot: "tank/backups", CreateBackupRoot: true,
		}
		inspection, err := (&Service{}).InspectTargetCandidate(context.Background(), target)
		if err != nil || !inspection.RootProvisioningRequired || inspection.RootExists {
			t.Fatalf("inspection=%+v err=%v", inspection, err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
		})
		matches, globErr := filepath.Glob(filepath.Join(SSHKeyDirectory, ".target-validation-*"))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("staged key leak matches=%v err=%v", matches, globErr)
		}
	})

	t.Run("concurrent creation is accepted only after exact verification", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
				{Stdout: "tank/backups\n", ExitCode: 0},
			},
			"zpool list -H -o name tank": {{Stdout: "tank\n", ExitCode: 0}},
			"zfs create -p tank/backups": {
				{Stderr: "cannot create 'tank/backups': dataset already exists\n", ExitCode: 1},
			},
		}})
		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target", BackupRoot: "tank/backups", CreateBackupRoot: true,
		}
		if err := s.ProvisionBackupTargetRoot(context.Background(), target); err != nil {
			t.Fatalf("concurrent creation provisioning failed: %v", err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("retry observes the previously created root without creating again", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}, {ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
				{Stdout: "tank/backups\n", ExitCode: 0},
				{Stdout: "tank/backups\n", ExitCode: 0},
			},
			"zpool list -H -o name tank": {{Stdout: "tank\n", ExitCode: 0}},
			"zfs create -p tank/backups": {{ExitCode: 0}},
		}})
		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target", BackupRoot: "tank/backups", CreateBackupRoot: true,
		}
		if err := s.ProvisionBackupTargetRoot(context.Background(), target); err != nil {
			t.Fatalf("initial provisioning failed: %v", err)
		}
		if err := s.ProvisionBackupTargetRoot(context.Background(), target); err != nil {
			t.Fatalf("retry provisioning failed: %v", err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("readiness validation never creates a missing root", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
			"zfs version": {{ExitCode: 0}},
			"zfs list -H -o name -t filesystem -d 0 tank/backups": {
				{Stderr: "cannot open 'tank/backups': dataset does not exist\n", ExitCode: 1},
			},
		}})
		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target", BackupRoot: "tank/backups", CreateBackupRoot: true,
		}
		err := s.ValidateTargetReadiness(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_not_found") {
			t.Fatalf("readiness error = %v", err)
		}
		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("dataset lookup transport failure fails closed", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{Stderr: "ssh: connect to host target port 22: Connection refused\n", ExitCode: 255},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_check_failed") {
			t.Fatalf("expected backup_root_check_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("pool missing returns backup_pool_not_found", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{ExitCode: 0},
				},
				"zpool list -H -o name tank": {
					{Stderr: "no such pool", ExitCode: 1},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			BackupRoot:       "tank/backups",
			CreateBackupRoot: true,
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_pool_not_found") {
			t.Fatalf("expected backup_pool_not_found error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
		})
	})

	t.Run("pool check failure returns backup_pool_check_failed", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{ExitCode: 0},
				},
				"zpool list -H -o name tank": {
					{Stderr: "permission denied", ExitCode: 1},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			BackupRoot:       "tank/backups",
			CreateBackupRoot: true,
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_pool_check_failed") {
			t.Fatalf("expected backup_pool_check_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
		})
	})

	t.Run("create failure returns backup_root_create_failed", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{ExitCode: 0},
					{ExitCode: 0},
				},
				"zpool list -H -o name tank": {
					{Stdout: "tank\n", ExitCode: 0},
				},
				"zfs create -p tank/backups": {
					{Stderr: "permission denied", ExitCode: 1},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			BackupRoot:       "tank/backups",
			CreateBackupRoot: true,
		}

		err := s.ProvisionBackupTargetRoot(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_create_failed") ||
			BackupTargetProvisionFailureIsAmbiguous(err) {
			t.Fatalf("expected definite backup_root_create_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("post create verify failure returns backup_root_create_verify_failed", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{ExitCode: 0},
					{ExitCode: 0},
				},
				"zpool list -H -o name tank": {
					{Stdout: "tank\n", ExitCode: 0},
				},
				"zfs create -p tank/backups": {
					{ExitCode: 0},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey:       fakeSSHHostKey(),
			SSHHost:          "user@target",
			BackupRoot:       "tank/backups",
			CreateBackupRoot: true,
		}

		err := s.ProvisionBackupTargetRoot(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_create_verify_failed") ||
			!BackupTargetProvisionFailureIsAmbiguous(err) {
			t.Fatalf("expected ambiguous backup_root_create_verify_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
		})
	})

	t.Run("ssh connectivity failure", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{Stderr: "connection refused", ExitCode: 255},
				},
			},
		})

		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHostKey: fakeSSHHostKey(),
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "ssh_connection_failed") {
			t.Fatalf("expected ssh_connection_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
		})
	})
}

func assertFakeSSHCallSequence(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected fake ssh call sequence\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestBackupTargetHostKeyEnrollmentCommitsOnlyAfterSuccessfulCheck(t *testing.T) {
	for _, failure := range []string{"", "Permission denied (publickey).", "Connection refused", "zfs: command not found", "malformed key"} {
		t.Run(failure, func(t *testing.T) {
			harness := newFakeSSHHarness(t)
			response := fakeSSHResponse{}
			if failure != "" && failure != "malformed key" {
				response.Stderr, response.ExitCode = failure, 255
			}
			if failure == "malformed key" {
				t.Setenv("ZELTA_TEST_SSH_HOST_KEY", "invalid key")
			}
			harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{"zfs version": {response}}})
			database := newZeltaServiceTestDB(t)
			service := newTestZeltaService(database)
			service.TelemetryDB = testutil.NewSQLiteTestDB(t, &infoModels.AuditRecord{})
			target := clusterModels.BackupTarget{ID: 42, Name: "backup", SSHHost: "root@backup", SSHKey: "login key", BackupRoot: "tank/backups", Enabled: true}
			if err := database.Create(&target).Error; err != nil {
				t.Fatal(err)
			}
			seedBackupTargetHostTrust(t, database, &target, "")
			err := service.EnsureBackupTargetHostKey(t.Context(), &target)
			trust, loadErr := clusterModels.GetBackupTargetSSHHostTrust(database, &target)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if failure == "" {
				if err != nil || trust.Revision != 2 || trust.PublicKey != fakeSSHHostKey() || target.SSHHostKey != trust.PublicKey {
					t.Fatalf("enrollment=%+v err=%v", trust, err)
				}
				args, err := service.buildSSHArgs(&target)
				if err != nil || !slices.Contains(args, "StrictHostKeyChecking=yes") {
					t.Fatalf("normal policy=%v err=%v", args, err)
				}
				var audit infoModels.AuditRecord
				if err := service.TelemetryDB.First(&audit).Error; err != nil {
					t.Fatal(err)
				}
				fingerprint, _ := remoteexec.SSHHostKeyFingerprint(trust.PublicKey)
				if audit.Status != "success" || !strings.Contains(audit.Action, fingerprint) || !strings.Contains(audit.Action, `"revision":2`) || strings.Contains(audit.Action, target.SSHKey) {
					t.Fatalf("install audit=%+v", audit)
				}
			} else {
				if err == nil || trust.Revision != 1 || trust.PublicKey != "" || target.SSHHostKey != "" {
					t.Fatalf("failed enrollment stored trust=%+v err=%v", trust, err)
				}
				var count int64
				if err := service.TelemetryDB.Model(&infoModels.AuditRecord{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("failure audit count=%d err=%v", count, err)
				}
			}
			assertFakeSSHCallSequence(t, harness.Calls(), []string{"zfs version"})
			args := harness.Arguments()[0]
			if !slices.Contains(args, "StrictHostKeyChecking=accept-new") || !slices.Contains(args, "ControlPath=none") {
				t.Fatalf("enrollment did not use fresh first-use trust: %v", args)
			}
			for _, pattern := range []string{".host-key-enrollment-*", ".target-validation-*"} {
				files, err := filepath.Glob(filepath.Join(SSHKeyDirectory, pattern))
				if err != nil || len(files) != 0 {
					t.Fatalf("temporary files=%v err=%v", files, err)
				}
			}
		})
	}
}

func TestBackupTargetMissingTrustAndObservationalValidationCannotEnroll(t *testing.T) {
	for _, state := range []string{"missing", "empty"} {
		t.Run(state, func(t *testing.T) {
			harness := newFakeSSHHarness(t)
			database := newZeltaServiceTestDB(t)
			service := newTestZeltaService(database)
			target := clusterModels.BackupTarget{ID: 42, Name: "backup", SSHHost: "root@backup", BackupRoot: "tank/backups", Enabled: true}
			if err := database.Create(&target).Error; err != nil {
				t.Fatal(err)
			}
			if state == "empty" {
				seedBackupTargetHostTrust(t, database, &target, "")
			} else {
				if err := service.EnsureBackupTargetHostKey(t.Context(), &target); err == nil || !strings.Contains(err.Error(), "state_unavailable") {
					t.Fatalf("missing enrollment=%v", err)
				}
			}
			err := service.ValidateTargetReadiness(t.Context(), &target)
			if err == nil || (state == "empty" && !strings.Contains(err.Error(), "unlearned")) {
				t.Fatalf("observational check=%v", err)
			}
			if calls := harness.Calls(); len(calls) != 0 {
				t.Fatalf("unapproved state contacted SSH: %v", calls)
			}
		})
	}
}

func TestCandidateEnrollmentCarriesExactKeyIntoStrictInspection(t *testing.T) {
	harness := newFakeSSHHarness(t)
	harness.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{
		"zfs version": {{}, {}}, "zfs list -H -o name -t filesystem -d 0 tank/backups": {{Stdout: "tank/backups\n"}},
	}})
	service := newTestZeltaService(newZeltaServiceTestDB(t))
	target := clusterModels.BackupTarget{SSHHost: "root@backup", SSHKey: "login key", BackupRoot: "tank/backups"}
	result, err := service.InspectTargetCandidate(t.Context(), &target)
	if err != nil || result.HostKey != fakeSSHHostKey() || !result.RootExists {
		t.Fatalf("candidate result=%+v err=%v", result, err)
	}
	calls := harness.Arguments()
	if len(calls) != 3 || !slices.Contains(calls[0], "StrictHostKeyChecking=accept-new") {
		t.Fatalf("enrollment calls=%v", calls)
	}
	for _, args := range calls[1:] {
		if !slices.Contains(args, "StrictHostKeyChecking=yes") || !slices.Contains(args, "ControlPath=none") {
			t.Fatalf("inspection uses learning or old socket: %v", args)
		}
	}
	var count int64
	if err := service.DB.Model(&clusterModels.BackupTargetSSHHostTrust{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("candidate left committed trust=%d err=%v", count, err)
	}
}
