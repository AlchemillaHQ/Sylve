// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
)

func TestLoadKernelModuleVerifiesSuccessfulLoad(t *testing.T) {
	original := startupRunCommand
	t.Cleanup(func() {
		startupRunCommand = original
	})

	loaded := false
	calls := []string{}
	startupRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		switch command {
		case "/sbin/kldstat":
			if loaded {
				return "pflog loaded", nil
			}
			return "", errors.New("module not found")
		case "/sbin/kldload":
			loaded = true
			return "", nil
		default:
			t.Fatalf("unexpected command call: %s %v", command, args)
			return "", nil
		}
	}

	if err := loadKernelModule("pflog"); err != nil {
		t.Fatalf("expected verified module load to succeed, got: %v", err)
	}

	joined := strings.Join(calls, "|")
	want := "/sbin/kldstat -m pflog|/sbin/kldload -n pflog|/sbin/kldstat -m pflog"
	if joined != want {
		t.Fatalf("unexpected module load calls: got %q want %q", joined, want)
	}
}

func TestCheckKernelModulesTreatsPflogAsOptional(t *testing.T) {
	original := startupRunCommand
	t.Cleanup(func() {
		startupRunCommand = original
	})

	calls := []string{}
	startupRunCommand = func(command string, args ...string) (string, error) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if len(args) == 2 && args[1] == "pflog" {
			return "", errors.New("pflog unavailable")
		}
		if command == "/sbin/kldstat" {
			return "module loaded", nil
		}
		t.Fatalf("unexpected command call: %s %v", command, args)
		return "", nil
	}

	err := (&Service{}).CheckKernelModules(models.BasicSettings{
		Services: []models.AvailableService{models.Firewall},
	})
	if err != nil {
		t.Fatalf("pflog failure must not fail startup: %v", err)
	}

	joined := strings.Join(calls, "|")
	if !strings.Contains(joined, "/sbin/kldload -n pflog") {
		t.Fatalf("expected explicit pflog load attempt, got: %v", calls)
	}
	if strings.Contains(joined, "if_pflog") {
		t.Fatalf("startup attempted the wrong if_pflog module: %v", calls)
	}
}

func TestWriteJailLogRotationConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "newsyslog.conf.d", "sylve.conf")
	jailsPath := "/var/db/sylve/jails"

	if err := writeJailLogRotationConfig(configPath, jailsPath); err != nil {
		t.Fatalf("writeJailLogRotationConfig: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	want := "# Managed by Sylve; changes will be overwritten.\n" +
		"/var/db/sylve/jails/*/*.log\t0644\t5\t1M\t*\tBEGNZ\n"
	if string(content) != want {
		t.Fatalf("generated config = %q, want %q", content, want)
	}

	if err := writeJailLogRotationConfig(configPath, jailsPath); err != nil {
		t.Fatalf("idempotent writeJailLogRotationConfig: %v", err)
	}
}

func TestWriteJailLogRotationConfigRejectsUnsafePath(t *testing.T) {
	err := writeJailLogRotationConfig(
		filepath.Join(t.TempDir(), "sylve.conf"),
		"/var/db/sylve jails",
	)
	if err == nil {
		t.Fatal("expected an unsafe jails path to be rejected")
	}
}

func TestSyncSambaAuditSyslogConfigCreatesScopedDropIn(t *testing.T) {
	root := t.TempDir()
	dropIn := filepath.Join(root, "etc", "syslog.d", "sylve-samba-audit.conf")
	auditLog := filepath.Join(root, "var", "log", "samba4", "audit.log")

	changed, err := syncSambaAuditSyslogConfig(dropIn, auditLog)
	if err != nil {
		t.Fatalf("sync Samba audit syslog config: %v", err)
	}
	if !changed {
		t.Fatal("first sync did not report a change")
	}

	content, err := os.ReadFile(dropIn)
	if err != nil {
		t.Fatalf("read generated drop-in: %v", err)
	}
	want := "# Sylve-managed Samba full_audit log\n!smbd_audit\nlocal5.notice\t" + auditLog + "\n!*\n"
	if string(content) != want {
		t.Fatalf("drop-in = %q, want %q", content, want)
	}
	info, err := os.Stat(auditLog)
	if err != nil {
		t.Fatalf("stat audit log: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("audit log mode = %o, want 600", info.Mode().Perm())
	}

	changed, err = syncSambaAuditSyslogConfig(dropIn, auditLog)
	if err != nil {
		t.Fatalf("idempotent sync: %v", err)
	}
	if changed {
		t.Fatal("idempotent sync reported a change")
	}
}

func TestSyncSambaAuditRotationConfigCreatesOwnedDropIn(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "newsyslog.conf.d", "sylve-samba-audit.conf")
	auditLog := filepath.Join(root, "var", "log", "samba4", "audit.log")
	if err := syncSambaAuditRotationConfig(configPath, auditLog); err != nil {
		t.Fatalf("sync Samba audit rotation config: %v", err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Samba rotation drop-in: %v", err)
	}
	want := "# Managed by Sylve; changes will be overwritten.\n" + auditLog + "\t0600\t7\t100M\t*\tJCE\n"
	if string(content) != want {
		t.Fatalf("rotation drop-in = %q, want %q", content, want)
	}
}

func TestCheckSambaSyslogConfigReloadsOnlyWhenSylveDropInChanges(t *testing.T) {
	root := t.TempDir()
	previousDropIn := sambaSyslogDropInPath
	previousAuditLog := sambaAuditLogPath
	previousRotationConfig := sambaAuditRotationConfigPath
	previousRunCommand := startupRunCommand
	sambaSyslogDropInPath = filepath.Join(root, "etc", "syslog.d", "sylve-samba-audit.conf")
	sambaAuditLogPath = filepath.Join(root, "var", "log", "samba4", "audit.log")
	sambaAuditRotationConfigPath = filepath.Join(root, "newsyslog.conf.d", "sylve-samba-audit.conf")
	reloadCalls := 0
	startupRunCommand = func(command string, args ...string) (string, error) {
		reloadCalls++
		if command != "/usr/sbin/service" || strings.Join(args, " ") != "syslogd reload" {
			t.Fatalf("unexpected command: %s %v", command, args)
		}
		return "", nil
	}
	t.Cleanup(func() {
		sambaSyslogDropInPath = previousDropIn
		sambaAuditLogPath = previousAuditLog
		sambaAuditRotationConfigPath = previousRotationConfig
		startupRunCommand = previousRunCommand
	})

	settings := models.BasicSettings{Services: []models.AvailableService{models.SambaServer}}
	service := &Service{}
	if err := service.CheckSambaSyslogConfig(settings); err != nil {
		t.Fatalf("first Samba syslog sync: %v", err)
	}
	if err := service.CheckSambaSyslogConfig(settings); err != nil {
		t.Fatalf("idempotent Samba syslog sync: %v", err)
	}
	if reloadCalls != 1 {
		t.Fatalf("syslog reload calls = %d, want 1", reloadCalls)
	}
}

func TestSysctlSyncRaisesNetFIBsWhenBelowMinimum(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	getValues := map[string]int64{
		"net.fibs": 1,
	}
	setValues := map[string]int32{}

	startupGetSysctlInt64 = func(name string) (int64, error) {
		if value, ok := getValues[name]; ok {
			return value, nil
		}
		return 0, nil
	}

	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("expected sysctl sync to succeed, got: %v", err)
	}

	if value, ok := setValues["net.fibs"]; !ok {
		t.Fatal("expected net.fibs to be set when current value is below minimum")
	} else if value != 8 {
		t.Fatalf("expected net.fibs to be set to 8, got %d", value)
	}
}

func TestSysctlSyncKeepsNetFIBsWhenAlreadyHighEnough(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	getValues := map[string]int64{
		"net.fibs": 12,
	}
	setValues := map[string]int32{}

	startupGetSysctlInt64 = func(name string) (int64, error) {
		if value, ok := getValues[name]; ok {
			return value, nil
		}
		return 0, nil
	}

	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("expected sysctl sync to succeed, got: %v", err)
	}

	if _, ok := setValues["net.fibs"]; ok {
		t.Fatal("expected net.fibs not to be changed when current value is already >= 8")
	}
}

func TestSysctlSyncSetsJailEnforceStatfs(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	setValues := map[string]int32{}

	startupGetSysctlInt64 = func(name string) (int64, error) {
		if name == "net.fibs" {
			return 8, nil
		}
		return 0, nil
	}

	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("expected sysctl sync to succeed, got: %v", err)
	}

	if value, ok := setValues["security.jail.enforce_statfs"]; !ok {
		t.Fatal("expected security.jail.enforce_statfs to be set")
	} else if value != 1 {
		t.Fatalf("expected security.jail.enforce_statfs to be set to 1, got %d", value)
	}
}

func TestSysctlSyncDisablesBridgeMACInheritance(t *testing.T) {
	previousGet := startupGetSysctlInt64
	previousSet := startupSetSysctlInt32
	t.Cleanup(func() {
		startupGetSysctlInt64 = previousGet
		startupSetSysctlInt32 = previousSet
	})

	setValues := map[string]int32{}
	startupGetSysctlInt64 = func(name string) (int64, error) {
		if name == "net.fibs" {
			return 8, nil
		}
		return 0, nil
	}
	startupSetSysctlInt32 = func(name string, value int32) error {
		setValues[name] = value
		return nil
	}

	svc := &Service{}
	if err := svc.SysctlSync(); err != nil {
		t.Fatalf("sysctl sync: %v", err)
	}
	if value, ok := setValues["net.link.bridge.inherit_mac"]; !ok || value != 0 {
		t.Fatalf("net.link.bridge.inherit_mac=(%d,%v), want (0,true)", value, ok)
	}
}
