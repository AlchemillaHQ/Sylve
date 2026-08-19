// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func setInitiatorConfigPathForTest(t *testing.T, path string) {
	t.Helper()
	previous := configPath
	configPath = path
	t.Cleanup(func() { configPath = previous })
}

func setTargetConfigPathForTest(t *testing.T, path string) {
	t.Helper()
	previous := targetConfigPath
	targetConfigPath = path
	t.Cleanup(func() { targetConfigPath = previous })
}

func TestWriteConfigReplacesExistingFileWithMode0600(t *testing.T) {
	svc := newInitiatorTestService(t)
	path := filepath.Join(t.TempDir(), "iscsi.conf")
	setInitiatorConfigPathForTest(t, path)

	if err := os.WriteFile(path, []byte("old config"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := svc.WriteConfig(false); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteTargetConfigValidationFailurePreservesActiveFile(t *testing.T) {
	svc := newTargetTestService(t)
	path := filepath.Join(t.TempDir(), "ctl.conf")
	setTargetConfigPathForTest(t, path)
	const existing = "existing config\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/false")
	})
	t.Cleanup(restoreCommand)

	err := svc.WriteTargetConfig(false)
	if !errors.Is(err, ErrApplyFailed) || err.Error() != "failed_to_validate_target_config" {
		t.Fatalf("unexpected validation error: %v", err)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read active config: %v", readErr)
	}
	if string(content) != existing {
		t.Fatalf("active config changed after failed validation: %q", content)
	}
}

func TestWriteTargetConfigValidatesThenReloads(t *testing.T) {
	svc := newTargetTestService(t)
	path := filepath.Join(t.TempDir(), "ctl.conf")
	setTargetConfigPathForTest(t, path)

	var commands []string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		commands = append(commands, command+" "+strings.Join(args, " "))
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatalf("WriteTargetConfig: %v", err)
	}

	want := []string{
		"/usr/sbin/ctld -t -f /dev/stdin",
		"/usr/sbin/service ctld onestatus",
		"/usr/sbin/service ctld onereload",
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat target config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("target config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteTargetConfigReturnsStableReloadError(t *testing.T) {
	svc := newTargetTestService(t)
	setTargetConfigPathForTest(t, filepath.Join(t.TempDir(), "ctl.conf"))

	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			return exec.Command("/bin/sh", "-c", "printf 'sensitive daemon output'; exit 1")
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	err := svc.WriteTargetConfig(true)
	if !errors.Is(err, ErrApplyFailed) || err.Error() != "failed_to_reload_target_config" {
		t.Fatalf("unexpected reload error: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("command output leaked through application error: %v", err)
	}
}

func TestWriteConfigLoadsConfiguredSessionsWithoutRemovingExistingSessions(t *testing.T) {
	svc := newInitiatorTestService(t)
	setInitiatorConfigPathForTest(t, filepath.Join(t.TempDir(), "iscsi.conf"))

	var calls [][]string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{command}, args...))
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.WriteConfig(true); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"/usr/bin/iscsictl", "-Aa"}) {
		t.Fatalf("commands = %#v", calls)
	}
}

func TestWriteTargetConfigStartsStoppedService(t *testing.T) {
	svc := newTargetTestService(t)
	setTargetConfigPathForTest(t, filepath.Join(t.TempDir(), "ctl.conf"))

	var commands []string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		commands = append(commands, command+" "+strings.Join(args, " "))
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onestatus"}) {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatalf("WriteTargetConfig: %v", err)
	}
	want := []string{
		"/usr/sbin/ctld -t -f /dev/stdin",
		"/usr/sbin/service ctld onestatus",
		"/usr/sbin/service ctld onestart",
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestSetEnabledWritesConfigsStartsServicesAndLoadsInitiators(t *testing.T) {
	svc := newTargetTestService(t)
	setInitiatorConfigPathForTest(t, filepath.Join(t.TempDir(), "iscsi.conf"))
	setTargetConfigPathForTest(t, filepath.Join(t.TempDir(), "ctl.conf"))

	var commands []string
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		commands = append(commands, command+" "+strings.Join(args, " "))
		if command == "/usr/sbin/service" && len(args) == 2 && args[1] == "onestatus" {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	want := []string{
		"/usr/sbin/ctld -t -f /dev/stdin",
		"/usr/sbin/service iscsid onestatus",
		"/usr/sbin/service iscsid onestart",
		"/usr/sbin/service ctld onestatus",
		"/usr/sbin/service ctld onestart",
		"/usr/bin/iscsictl -Aa",
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestConnectInitiatorTreatsExitOneAsFailure(t *testing.T) {
	svc := newInitiatorTestService(t)
	initiator := iscsiModels.ISCSIInitiator{
		Nickname:      "fblock0",
		TargetAddress: "192.0.2.10",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	}
	if err := svc.DB.Create(&initiator).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/false")
	})
	t.Cleanup(restoreCommand)

	err := svc.ConnectInitiator(initiator.ID)
	if !errors.Is(err, ErrApplyFailed) || err.Error() != "failed_to_connect_initiator" {
		t.Fatalf("unexpected connect error: %v", err)
	}
}

func TestGetStatusTreatsExitOneAsFailure(t *testing.T) {
	svc := newInitiatorTestService(t)
	restoreCommand := utils.SetCommandForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/false")
	})
	t.Cleanup(restoreCommand)

	_, err := svc.GetStatus()
	if !errors.Is(err, ErrApplyFailed) || err.Error() != "failed_to_get_status" {
		t.Fatalf("unexpected status error: %v", err)
	}
}
