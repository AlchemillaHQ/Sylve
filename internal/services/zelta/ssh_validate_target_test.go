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
	"reflect"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

func TestValidateTargetWithFakeSSH(t *testing.T) {
	t.Run("backup_root_required", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		s := &Service{}
		target := &clusterModels.BackupTarget{
			SSHHost:    "user@target",
			SSHPort:    22,
			BackupRoot: "   ",
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_required") {
			t.Fatalf("expected backup_root_required error, got %v", err)
		}

		if got := h.Calls(); len(got) != 0 {
			t.Fatalf("expected no ssh calls for backup_root_required, got %#v", got)
		}
	})

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

	t.Run("dataset missing pool exists and create succeeds", func(t *testing.T) {
		h := newFakeSSHHarness(t)
		h.SetScenario(fakeSSHScenario{
			Responses: map[string][]fakeSSHResponse{
				"zfs version": {
					{ExitCode: 0},
				},
				"zfs list -H -o name -t filesystem -d 0 tank/backups": {
					{ExitCode: 0},
					{Stdout: "tank/backups\n", ExitCode: 0},
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
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
		}

		if err := s.ValidateTarget(context.Background(), target); err != nil {
			t.Fatalf("ValidateTarget failed: %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
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
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
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
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
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
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_create_failed") {
			t.Fatalf("expected backup_root_create_failed error, got %v", err)
		}

		assertFakeSSHCallSequence(t, h.Calls(), []string{
			"zfs version",
			"zfs list -H -o name -t filesystem -d 0 tank/backups",
			"zpool list -H -o name tank",
			"zfs create -p tank/backups",
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
			SSHHost:    "user@target",
			BackupRoot: "tank/backups",
		}

		err := s.ValidateTarget(context.Background(), target)
		if err == nil || !strings.Contains(err.Error(), "backup_root_create_verify_failed") {
			t.Fatalf("expected backup_root_create_verify_failed error, got %v", err)
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
