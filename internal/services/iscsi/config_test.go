// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

func setInitiatorConfigPathForTest(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(SetConfigPath(path))
}

func setTargetConfigPathForTest(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(SetTargetConfigPath(path))
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
	path := svc.targetPath()
	const existing = "existing config\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" {
			return "sensitive validation output", errors.New("validation failed")
		}
		return run(ctx, input, command, args...)
	}

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
	path := svc.targetPath()
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}

	var commands []string
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		commands = append(commands, command+" "+strings.Join(args, " "))
		return run(ctx, input, command, args...)
	}

	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatalf("WriteTargetConfig: %v", err)
	}

	validation := slices.Index(commands, "/usr/sbin/ctld -t -f /dev/stdin")
	reload := slices.Index(commands, "/usr/sbin/service ctld onereload")
	if validation < 0 || reload <= validation || slices.Contains(commands, "/usr/sbin/service ctld onestart") {
		t.Fatalf("invalid reload order: %#v", commands)
	}
	if count := strings.Count(strings.Join(commands, "\n"), "ctladm portlist -x"); count != 2 {
		t.Fatalf("runtime samples=%d want 2", count)
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
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}

	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			return "sensitive daemon output", errors.New("reload failed")
		}
		return run(ctx, input, command, args...)
	}

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
	restoreCommand := utils.SetCommandWithContextForTest(func(command string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{command}, args...))
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	if err := svc.WriteConfig(true); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"/usr/bin/iscsictl", "-c", configPath, "-Aa"}) {
		t.Fatalf("commands = %#v", calls)
	}
}

func TestWriteTargetConfigStartsStoppedService(t *testing.T) {
	svc := newTargetTestService(t)

	var commands []string
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		commands = append(commands, command+" "+strings.Join(args, " "))
		return run(ctx, input, command, args...)
	}

	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatalf("WriteTargetConfig: %v", err)
	}
	want := []string{
		"/usr/sbin/ctld -t -f /dev/stdin",
		"/usr/sbin/service ctld onestatus",
		"/usr/sbin/service ctld onestart",
	}
	if len(commands) < 3 || !slices.Equal(commands[:3], want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestSetEnabledWritesConfigsStartsServicesAndLoadsInitiators(t *testing.T) {
	svc := newTargetTestService(t)

	var commands []string
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		commands = append(commands, command+" "+strings.Join(args, " "))
		return run(ctx, input, command, args...)
	}

	if err := svc.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	want := []string{
		"/usr/sbin/ctld -t -f /dev/stdin",
		"/usr/sbin/service iscsid onestatus",
		"/usr/sbin/service ctld onestatus",
		"/usr/sbin/service ctld onestart",
		"/usr/bin/iscsictl -c " + configPath + " -Aa",
	}
	for _, command := range want {
		if !slices.Contains(commands, command) {
			t.Fatalf("missing %s in %#v", command, commands)
		}
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

	restoreCommand := utils.SetCommandWithContextForTest(func(string, ...string) *exec.Cmd {
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
	restoreCommand := utils.SetCommandWithContextForTest(func(string, ...string) *exec.Cmd {
		return exec.Command("/usr/bin/false")
	})
	t.Cleanup(restoreCommand)

	_, err := svc.GetStatus()
	if !errors.Is(err, ErrApplyFailed) || err.Error() != "failed_to_get_status" {
		t.Fatalf("unexpected status error: %v", err)
	}
}

func TestSharedPortalConfigIsDeterministicAndKeepsAuthentication(t *testing.T) {
	svc := newTargetTestService(t)
	for i, name := range []string{"first", "second"} {
		target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:" + name, AuthMethod: "CHAP", CHAPName: name, CHAPSecret: "secretpassw0rd", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
		target.LUNs = []iscsiModels.ISCSITargetLUN{{LUNNumber: 0, ZVol: "tank/" + name}}
		if i == 1 {
			target.Portals = append(target.Portals, iscsiModels.ISCSITargetPortal{Address: "::1", Port: 3260})
		}
		if err := svc.DB.Create(&target).Error; err != nil {
			t.Fatal(err)
		}
	}
	unbound := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:unbound", AuthMethod: "None"}
	svc.DB.Create(&unbound)
	first, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.GenerateTargetConfig()
	if err != nil || first != second {
		t.Fatal("rendering changed")
	}
	golden, err := os.ReadFile("testdata/shared-portals.conf")
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSuffix(first, "\n") != string(golden) {
		index := 0
		for index < min(len(first), len(golden)) && first[index] == golden[index] {
			index++
		}
		t.Fatalf("shared-portals golden mismatch at byte %d (generated %d bytes, fixture %d bytes)", index, len(first), len(golden))
	}
	if strings.Count(first, `listen "127.0.0.1:3260"`) != 1 || strings.Count(first, `listen "[::1]:3260"`) != 1 {
		t.Fatal("listeners duplicated")
	}
	if strings.Count(first, "auth-group ag-") != 4 || !strings.Contains(first, "portal-group pg-unbound") {
		t.Fatal("auth or unbound group missing")
	}
	state, err := svc.inspectTargetConfig(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.groups) != 3 || len(state.targets[0].groups) != 1 || len(state.targets[1].groups) != 2 {
		t.Fatal("wrong attachment topology")
	}
}

func TestTargetRuntimeDetectsPartialApplyAndRemovals(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:runtime", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}, LUNs: []iscsiModels.ISCSITargetLUN{{ZVol: "tank/test", LUNNumber: 0}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	text, _ := svc.GenerateTargetConfig()
	state, err := svc.prepareTargetConfig(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &fakeTargetRuntime{service: svc, live: state}
	for _, test := range []struct {
		name   string
		change func(*ctlPorts, *ctlLUNs)
	}{
		{"missing port", func(p *ctlPorts, l *ctlLUNs) { p.Ports = nil }},
		{"missing LUN", func(p *ctlPorts, l *ctlLUNs) { l.LUNs = nil }},
		{"failed resize", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocks-- }},
		{"old mapping", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].LUNs[0].ID++ }},
		{"obsolete LUN", func(p *ctlPorts, l *ctlLUNs) { l.LUNs = append(l.LUNs, ctlLUN{ID: 91, Name: "obsolete"}) }},
		{"obsolete port", func(p *ctlPorts, l *ctlLUNs) { p.Ports = append(p.Ports, ctlPort{ID: 91, Group: "old", Target: "old"}) }},
		{"offline port", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].Online = "NO" }},
		{"wrong backend", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Backend = "ramdisk" }},
		{"wrong blocksize", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocksize = 4096 }},
		{"wrong device type", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].DeviceType = 1 }},
		{"duplicate LUN ID", func(p *ctlPorts, l *ctlLUNs) { l.LUNs = append(l.LUNs, ctlLUN{ID: l.LUNs[0].ID}) }},
		{"duplicate port ID", func(p *ctlPorts, l *ctlLUNs) { p.Ports = append(p.Ports, ctlPort{ID: p.Ports[0].ID}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, l := fixture.inventory()
			test.change(p, l)
			if svc.matchCTLState(state, p, l) == nil {
				t.Fatal("partial state passed")
			}
		})
	}
}

func TestTargetListenerInventoryUsesWideIPv6Output(t *testing.T) {
	svc := newTargetTestService(t)
	svc.interfaceLookup = func(string) (*net.Interface, error) {
		return &net.Interface{Name: "em0", Index: 2}, nil
	}
	svc.runtime.run = func(_ context.Context, _, command string, args ...string) (string, error) {
		if command != "/usr/bin/sockstat" || !slices.Contains(args, "-w") {
			t.Fatal("listener inventory did not request wide native output")
		}
		return "root ctld 23942 3 tcp6 fe80::1234:5678:abcd:ef01%em0:49200 *:*\nroot ctld 23943 3 tcp4 127.0.0.1:49201 *:*\n", nil
	}
	listeners, err := svc.targetListeners(t.Context(), 23942)
	if err != nil || len(listeners) != 1 || listeners[0].listen() != "[fe80::1234:5678:abcd:ef01%em0]:49200" || listeners[0].scope != 2 {
		t.Fatalf("incorrect scoped IPv6 listener inventory: %v", err)
	}
}

func TestTargetRuntimeNeedsTwoSamplesFromSameProcess(t *testing.T) {
	svc := newTargetTestService(t)
	text, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.prepareTargetConfig(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	birthReads, samples := 0, 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/bin/ps" {
			birthReads++
			if birthReads <= 2 {
				return "first birth", nil
			}
			return "second birth", nil
		}
		if command == "/usr/sbin/ctladm" && args[0] == "portlist" {
			samples++
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	if samples != 3 || birthReads != 6 {
		t.Fatalf("samples=%d birth reads=%d; want 3 complete samples across a process change", samples, birthReads)
	}

	if err := svc.matchCTLState(state, &ctlPorts{Ports: []ctlPort{{ID: 93}}}, &ctlLUNs{LUNs: []ctlLUN{{ID: 93}}}); err != nil {
		t.Fatal("unstamped CTL objects were classified as owned")
	}
}

func TestTargetEnableFailureKeepsDesiredStateAndRetries(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	starts := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[0] == "ctld" && args[1] == "onestart" {
			starts++
			if starts == 1 {
				return "nonsecret fixture output", errors.New("native start failure")
			}
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.SetEnabled(true); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("first enable error=%v", err)
	}
	if enabled, err := svc.desiredTargetEnabled(); err != nil || !enabled {
		t.Fatal("pending enable rolled back desired state")
	}
	if err := svc.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if starts != 2 {
		t.Fatalf("start attempts=%d want 2", starts)
	}
}

func TestTargetRuntimeDetectsBackingReplacement(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:backing", AuthMethod: "None", LUNs: []iscsiModels.ISCSITargetLUN{{ZVol: "tank/test", LUNNumber: 0}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	text, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.prepareTargetConfig(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &fakeTargetRuntime{service: svc, live: state}
	ports, luns := fixture.inventory()
	if err := svc.matchCTLState(state, ports, luns); err != nil {
		t.Fatal(err)
	}
	svc.backingStat = func(string) (os.FileInfo, error) { return os.Stat("/dev/zero") }
	if err := svc.matchCTLState(state, ports, luns); err == nil {
		t.Fatal("replaced backing passed with unchanged path text")
	}
}

func TestTargetVerificationCommandsShareApplyDeadline(t *testing.T) {
	for _, command := range []string{"/usr/sbin/service", "/usr/bin/sockstat", "/usr/sbin/ctladm", "/bin/ps"} {
		t.Run(command, func(t *testing.T) {
			svc := newTargetTestService(t)
			if err := svc.WriteTargetConfig(true); err != nil {
				t.Fatal(err)
			}
			run := svc.runtime.run
			signaled := false
			svc.runtime.run = func(ctx context.Context, input, name string, args ...string) (string, error) {
				if name == "/usr/sbin/service" && args[1] == "onereload" {
					signaled = true
				} else if signaled && name == command {
					<-ctx.Done()
					return "", ctx.Err()
				}
				return run(ctx, input, name, args...)
			}
			text, err := svc.GenerateTargetConfig()
			if err != nil {
				t.Fatal(err)
			}
			state, err := svc.prepareTargetConfig(t.Context(), text)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			if err := svc.applyTargetRuntime(ctx, state, true); !errors.Is(err, ErrApplyFailed) {
				t.Fatalf("error=%v", err)
			}
			if !signaled || time.Since(start) > time.Second {
				t.Fatal("verification commands did not share the apply deadline")
			}
		})
	}
}

func TestTargetSuccessfulStopCommandNeedsFollowupChecks(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	svc.runtime.deadline = 50 * time.Millisecond
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[1] == "onestop" {
			return "", nil
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.SetEnabled(false); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_stop_pending" {
		t.Fatalf("error=%v", err)
	}
	enabled, err := svc.desiredTargetEnabled()
	if err != nil || enabled {
		t.Fatal("pending stop rolled back desired state")
	}
}

func TestTargetDisablePersistsAndTargetEditsDoNotStart(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateTarget("iqn.2026-10.test:disabled", "", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	enabled, err := svc.desiredTargetEnabled()
	if err != nil || enabled {
		t.Fatalf("enabled=%v error=%v", enabled, err)
	}
	if _, err := os.Stat(svc.targetPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.targetPID(t.Context()); !errors.Is(err, errTargetStopped) {
		t.Fatal("disabled target service started")
	}
}

func TestTargetCommandDeadlineIsShared(t *testing.T) {
	svc := newTargetTestService(t)
	svc.runtime.deadline = 20 * time.Millisecond
	svc.runtime.run = func(ctx context.Context, _, _ string, _ ...string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	start := time.Now()
	if err := svc.WriteTargetConfig(true); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("command exceeded deadline")
	}
}

func TestTargetStopFailureKeepsDesiredStateAndFile(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[1] == "onestop" {
			return "secret", errors.New("stop failed")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.SetEnabled(false); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	if err := svc.CreateTarget("iqn.2026-10.test:pending", "", "None", "", "", "", ""); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(before) != string(after) {
		t.Fatal("failed stop replaced file")
	}
	enabled, _ := svc.desiredTargetEnabled()
	if enabled {
		t.Fatal("failed stop rolled back desired state")
	}
	svc.runtime.run = run
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	var settings models.BasicSettings
	svc.DB.First(&settings)
	if strings.Contains(fmt.Sprint(settings.Services), "iscsi") {
		t.Fatal("retry changed desired state")
	}
}

func TestOwnedTargetInspectorRejectsQuoteInjectionAndUnsupportedSyntax(t *testing.T) {
	svc := newTargetTestService(t)
	for _, input := range []string{`option note "x\" listen 127.0.0.1:3260 #"`, `target x { }`, `controller x { }`, "target x { alias \"line\nline\" }"} {
		if _, err := svc.inspectTargetConfig(configMarker + "\n" + input); err == nil {
			t.Fatalf("unsafe input accepted: %s", strconv.Quote(input))
		}
	}
}

func TestOwnedTargetInspectorPreservesQuotedDelimiters(t *testing.T) {
	svc := newTargetTestService(t)
	text := configMarker + "\n" + `portal-group pg-unbound {} auth-group ag-1 { chap "}" "secret{#12345" } target "iqn.2026-10.test:quoted" { alias "listen # {}" portal-group pg-unbound auth-group ag-1 }`
	if _, err := svc.inspectTargetConfig(text); err != nil {
		t.Fatal(err)
	}
}

func TestTargetStartupUsesIndependentlyCheckedOwnedFile(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:fallback", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
	svc.DB.Create(&target)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	svc.DB.Model(&iscsiModels.ISCSITargetPortal{}).Where("target_id = ?", target.ID).Update("address", "not-an-ip")
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_using_owned_file" {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(before) != string(after) {
		t.Fatal("invalid rows replaced fallback")
	}
}

func TestTargetStartupRefusesUninspectableFallback(t *testing.T) {
	svc := newTargetTestService(t)
	svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "invalid\nname", AuthMethod: "None"})
	os.WriteFile(svc.targetPath(), []byte(configMarker+"\ncontroller unsupported {}"), 0600)
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	if _, err := svc.targetPID(t.Context()); !errors.Is(err, errTargetStopped) {
		t.Fatal("unsafe fallback started")
	}
}

func TestTargetNormalizationTransitionRequiresStop(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:legacy", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
	svc.DB.Create(&target)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	legacy := strings.Replace(string(before), `"127.0.0.1:3260"`, `"127.0.0.1"`, 1)
	os.WriteFile(svc.targetPath(), []byte(legacy), 0600)
	if err := svc.WriteTargetConfig(true); !errors.Is(err, ErrApplyFailed) || err.Error() != "iscsi_listener_normalization_requires_stop" {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(after) != legacy {
		t.Fatal("unsafe reload replaced legacy file")
	}
}

func TestTargetStartupDoesNotRetryPartialRuntimeApply(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:obsolete", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(svc.targetPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Where("target_id = ?", target.ID).Delete(&iscsiModels.ISCSITargetPortal{}).Error; err != nil {
		t.Fatal(err)
	}
	svc.runtime.deadline = 30 * time.Millisecond
	run := svc.runtime.run
	reloads := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[1] == "onereload" {
			reloads++
			return "", nil
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	if reloads != 1 {
		t.Fatalf("reloads=%d want 1", reloads)
	}
	after, err := os.ReadFile(svc.targetPath())
	if err != nil || string(after) == string(before) || strings.Contains(string(after), "listen ") {
		t.Fatal("partial failure did not retain the candidate file")
	}
}

func TestTargetAsyncStartExitIsNotSuccess(t *testing.T) {
	svc := newTargetTestService(t)
	svc.runtime.deadline = 30 * time.Millisecond
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[1] == "onestart" {
			return "", nil
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.WriteTargetConfig(true); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(svc.targetPath()); err != nil {
		t.Fatal("failed start removed candidate")
	}
}

func TestFixtureTargetPIDFileNeedsVerifiedProcessInventory(t *testing.T) {
	exitOne := exec.Command("/usr/bin/false").Run()
	var exitErr *exec.ExitError
	if !errors.As(exitOne, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatal("cannot create exit-one fixture")
	}
	for _, test := range []struct {
		name      string
		contents  string
		missing   bool
		processes string
		inspect   error
		want      error
	}{
		{name: "missing", missing: true, inspect: exitOne, want: errTargetStopped},
		{name: "empty", inspect: exitOne, want: errTargetStopped},
		{name: "whitespace", contents: " \n", inspect: exitOne, want: errTargetStopped},
		{name: "inventory-failure", inspect: errors.New("inventory failed"), want: errors.New("failed_to_check_target_process")},
		{name: "empty-success", want: errors.New("failed_to_check_target_process")},
		{name: "conflicting-inventory", processes: "23942", inspect: exitOne, want: errors.New("failed_to_check_target_process")},
		{name: "malformed", contents: "not-a-pid", want: errors.New("invalid_target_pidfile")},
		{name: "pid-one", contents: "1", want: errors.New("invalid_target_pidfile")},
		{name: "multiple-pids", contents: "23942 23943", want: errors.New("invalid_target_pidfile")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ctld.pid")
			if !test.missing {
				if err := os.WriteFile(path, []byte(test.contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			svc := &Service{runtime: &targetRuntime{pidFile: path}}
			svc.runtime.run = func(_ context.Context, _, command string, args ...string) (string, error) {
				if command != "/bin/pgrep" || !slices.Equal(args, []string{"-x", "ctld"}) {
					t.Fatalf("unverified process command: %s %v", command, args)
				}
				return test.processes, test.inspect
			}
			for _, action := range []string{"onestatus", "onereload", "onestop"} {
				if _, err := svc.targetService(t.Context(), action); err == nil || err.Error() != test.want.Error() || errors.Is(err, errTargetStopped) != errors.Is(test.want, errTargetStopped) {
					t.Fatalf("%s error=%v want %v", action, err, test.want)
				}
			}
			data, err := os.ReadFile(path)
			if test.missing {
				if !os.IsNotExist(err) {
					t.Fatal("status inspection created a PID file")
				}
			} else if err != nil || string(data) != test.contents {
				t.Fatal("status inspection changed or removed the PID file")
			}
		})
	}
}

func TestFixtureTargetWaitsForPIDFileOrProcessExit(t *testing.T) {
	exitOne := exec.Command("/usr/bin/false").Run()
	var exitErr *exec.ExitError
	if !errors.As(exitOne, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatal("cannot create exit-one fixture")
	}
	for _, test := range []struct {
		name   string
		action string
	}{
		{name: "exits", action: "onestatus"},
		{name: "publishes-owned-pid", action: "onestatus"},
		{name: "foreign-stop", action: "onestop"},
		{name: "foreign-reload", action: "onereload"},
		{name: "pending", action: "onestop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "ctld.pid")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			svc := &Service{runtime: &targetRuntime{pidFile: path, configFile: filepath.Join(directory, "ctl.conf"), deadline: time.Second, interval: time.Microsecond}}
			if test.name == "pending" {
				svc.runtime.deadline = 20 * time.Millisecond
			}
			checks := 0
			svc.runtime.run = func(_ context.Context, _, command string, args ...string) (string, error) {
				switch command {
				case "/bin/pgrep":
					checks++
					if checks > 1 {
						if test.name == "exits" {
							return "", exitOne
						}
						if test.name != "pending" {
							if err := os.WriteFile(path, []byte("23942"), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					return "23942", nil
				case "/bin/ps":
					if slices.Equal(args, []string{"-p", "23942", "-o", "comm="}) {
						return "ctld", nil
					}
					if slices.Equal(args, []string{"-p", "23942", "-o", "args="}) {
						if strings.HasPrefix(test.name, "foreign-") {
							return "/usr/sbin/ctld -f /etc/ctl.conf", nil
						}
						return "/usr/sbin/ctld -f " + svc.targetPath(), nil
					}
				}
				t.Fatalf("unverified process command: %s %v", command, args)
				return "", nil
			}
			status, err := svc.targetService(t.Context(), test.action)
			switch test.name {
			case "exits":
				if !errors.Is(err, errTargetStopped) {
					t.Fatalf("error=%v", err)
				}
			case "publishes-owned-pid":
				if err != nil || status != "ctld is running as pid 23942." {
					t.Fatalf("status=%q error=%v", status, err)
				}
			case "pending":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unverified process was not left pending: %v", err)
				}
			default:
				if err == nil || err.Error() != "target_pidfile_process_not_owned" {
					t.Fatalf("foreign process error=%v", err)
				}
			}
			if checks < 2 {
				t.Fatal("PID file state was not checked again")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("PID file was removed during inspection")
			}
		})
	}
}

func TestFixtureTargetRetriesAndStopsAfterEmptyPIDFile(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:empty-pid", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}, LUNs: []iscsiModels.ISCSITargetLUN{{LUNNumber: 0, ZVol: "tank/test"}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	exitOne := exec.Command("/usr/bin/false").Run()
	var exitErr *exec.ExitError
	if !errors.As(exitOne, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatal("cannot create exit-one fixture")
	}
	svc.runtime.pidFile = filepath.Join(t.TempDir(), "ctld.pid")
	fixture := &fakeTargetRuntime{service: svc}
	starts := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		switch command {
		case "/usr/sbin/ctld":
			if slices.Equal(args, []string{"-f", svc.targetPath()}) {
				starts++
				if starts > 1 {
					ports, _ := fixture.inventory()
					if len(ports.Ports) != 0 {
						t.Fatal("stock ctld cannot adopt a retained kernel port")
					}
				}
				if _, err := fixture.run(ctx, input, "/usr/sbin/service", "ctld", "onestart"); err != nil {
					return "", err
				}
				if starts == 1 {
					fixture.running = false
					return "", os.WriteFile(svc.runtime.pidFile, nil, 0600)
				}
				return "", os.WriteFile(svc.runtime.pidFile, []byte("23942"), 0600)
			}
		case "/bin/pgrep":
			if !fixture.running {
				return "", exitOne
			}
		case "/bin/ps":
			if !fixture.running {
				return "", exitOne
			}
			if slices.Equal(args, []string{"-p", "23942", "-o", "comm="}) {
				return "ctld", nil
			}
			if slices.Equal(args, []string{"-p", "23942", "-o", "args="}) {
				return "/usr/sbin/ctld -f " + svc.targetPath(), nil
			}
		case "/bin/kill":
			if slices.Equal(args, []string{"-TERM", "23942"}) {
				return fixture.run(ctx, input, "/usr/sbin/service", "ctld", "onestop")
			}
			t.Fatalf("unexpected signal: %v", args)
		}
		return fixture.run(ctx, input, command, args...)
	}
	startErr := svc.StartTargets()
	if !errors.Is(startErr, ErrApplyFailed) {
		t.Fatalf("failed-start result=%v", startErr)
	}
	if _, err := svc.targetPID(t.Context()); !errors.Is(err, errTargetStopped) {
		t.Fatalf("failed-start status=%v", err)
	}
	if fixture.live == nil || len(fixture.live.targets) != 1 {
		t.Fatalf("fixture did not retain kernel state: starts=%d error=%v", starts, startErr)
	}
	if record, err := svc.readTargetRecovery(); err != nil || record == nil || len(record.Ports) != 1 {
		t.Fatalf("failed-start ownership was not saved: %v", err)
	}
	if err := svc.StartTargets(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if starts != 2 {
		t.Fatalf("start attempts=%d want 2", starts)
	}
	if err := svc.SetEnabled(false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if fixture.running || fixture.live != nil {
		t.Fatal("owned runtime state remains after stop")
	}
}

func newFailedTargetStartForTest(t *testing.T) (*Service, *fakeTargetRuntime, *targetConfiguration, *targetStartBaseline) {
	t.Helper()
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:recovery", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}, {Address: "127.0.0.2", Port: 3261}}, LUNs: []iscsiModels.ISCSITargetLUN{{LUNNumber: 0, ZVol: "tank/test"}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	exitOne := exec.Command("/usr/bin/false").Run()
	var exitErr *exec.ExitError
	if !errors.As(exitOne, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatal("cannot create exit-one fixture")
	}
	fixture := &fakeTargetRuntime{service: svc}
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/bin/pgrep" && !fixture.running {
			return "", exitOne
		}
		return fixture.run(ctx, input, command, args...)
	}
	text, err := svc.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.prepareTargetConfig(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.targetPath(), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := svc.targetStartInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixture.live = state
	return svc, fixture, state, before
}

func saveFailedTargetStartForTest(t *testing.T, svc *Service, state *targetConfiguration, before *targetStartBaseline) {
	t.Helper()
	if err := svc.rememberFailedTargetStart(t.Context(), state, before); err != nil {
		t.Fatal(err)
	}
}

func TestTargetRecoveryOnlyRemovesRecordedPorts(t *testing.T) {
	svc, fixture, state, before := newFailedTargetStartForTest(t)
	saveFailedTargetStartForTest(t, svc, state, before)
	record, err := svc.readTargetRecovery()
	if err != nil || record == nil || len(record.Ports) != 2 || len(record.LUNs) != 1 {
		t.Fatalf("saved ownership=%v error=%v", record, err)
	}
	info, err := os.Stat(svc.targetRecoveryPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("recovery record is not private")
	}
	data, err := os.ReadFile(svc.targetPath())
	if err != nil {
		t.Fatal(err)
	}
	_, luns := fixture.inventory()
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctladm" && args[0] == "portlist" {
			ports, _ := fixture.inventory()
			ports.Ports = append(ports.Ports, ctlPort{ID: 90, Frontend: "ioctl", Online: "YES"})
			out, err := xml.Marshal(ports)
			return string(out), err
		}
		return run(ctx, input, command, args...)
	}
	lock, err := svc.lockTargetRecovery()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := svc.recoverTargetPorts(t.Context()); err != nil {
		t.Fatal(err)
	}
	ports, err := svc.readCTLPorts(t.Context())
	if err != nil || len(ports.Ports) != 1 || ports.Ports[0].ID != 90 {
		t.Fatal("unowned physical port was not left intact")
	}
	_, afterLUNs := fixture.inventory()
	if !reflect.DeepEqual(luns, afterLUNs) {
		t.Fatal("port recovery changed LUN identities")
	}
	after, err := os.ReadFile(svc.targetPath())
	if err != nil || string(after) != string(data) {
		t.Fatal("port recovery changed saved configuration")
	}
	if enabled, err := svc.desiredTargetEnabled(); err != nil || !enabled {
		t.Fatal("port recovery changed desired service state")
	}
	if _, err := os.Stat(svc.targetRecoveryPath()); !os.IsNotExist(err) {
		t.Fatal("completed recovery retained its pending record")
	}
}

func TestTargetRecoveryRejectsUncertainState(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		output  string
		failure error
		change  func(*ctlPorts, *ctlLUNs)
		backing bool
		late    bool
	}{
		{name: "live-daemon", command: "/bin/pgrep", output: "23942"},
		{name: "failed-process-inventory", command: "/bin/pgrep", failure: errors.New("inventory failed")},
		{name: "empty-successful-process-inventory", command: "/bin/pgrep"},
		{name: "daemon-appears-between-checks", command: "/bin/pgrep", output: "23942", late: true},
		{name: "live-listener", command: "/usr/bin/sockstat", output: "root ctld 23942 3 tcp4 127.0.0.1:3260 *:*\n"},
		{name: "active-session", command: "/usr/sbin/ctladm islist", output: "<ctlislist><connection><target>iqn.2026-10.test:recovery</target></connection></ctlislist>"},
		{name: "unknown-session-state", command: "/usr/sbin/ctladm islist", failure: errors.New("inventory failed")},
		{name: "malformed-session-inventory", command: "/usr/sbin/ctladm islist", output: "<ctlislist>"},
		{name: "wrong-session-inventory-root", command: "/usr/sbin/ctladm islist", output: "<not-sessions/>"},
		{name: "malformed-port-inventory", command: "/usr/sbin/ctladm portlist", output: "<ctlportlist>"},
		{name: "malformed-lun-inventory", command: "/usr/sbin/ctladm devlist", output: "<ctllunlist>"},
		{name: "new-boot", command: "/sbin/sysctl", output: "different boot"},
		{name: "unknown-boot", command: "/sbin/sysctl", failure: errors.New("inventory failed")},
		{name: "reused-port-id", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Target = "iqn.2026-10.test:foreign" }},
		{name: "reused-physical-port-id", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0] = ctlPort{ID: p.Ports[0].ID, Frontend: "ioctl"} }},
		{name: "changed-tag", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Tag++ }},
		{name: "changed-group", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Group = "pg-foreign" }},
		{name: "changed-frontend", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Frontend = "ioctl" }},
		{name: "changed-online-state", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Online = "NO" }},
		{name: "changed-mapping", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].LUNs[0].Number++ }},
		{name: "duplicate-port-id", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports = append(p.Ports, p.Ports[0]) }},
		{name: "duplicate-selector", change: func(p *ctlPorts, _ *ctlLUNs) { port := p.Ports[0]; port.ID = 99; p.Ports = append(p.Ports, port) }},
		{name: "foreign-port", change: func(p *ctlPorts, _ *ctlLUNs) {
			p.Ports = append(p.Ports, ctlPort{ID: 99, Frontend: "iscsi", Target: "foreign", Group: "pg-foreign", Tag: 99})
		}},
		{name: "foreign-serial", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].Serial = "foreign" }},
		{name: "foreign-path", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].File = "/dev/zvol/non-test/volume" }},
		{name: "foreign-backend", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].Backend = "ramdisk" }},
		{name: "changed-capacity", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocks++ }},
		{name: "changed-blocksize", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocksize = 4096 }},
		{name: "changed-lun-id", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs[0].ID++ }},
		{name: "missing-lun", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs = nil }},
		{name: "duplicate-lun-id", change: func(_ *ctlPorts, l *ctlLUNs) { l.LUNs = append(l.LUNs, l.LUNs[0]) }},
		{name: "replaced-backing", backing: true},
		{name: "changed-between-checks", change: func(p *ctlPorts, _ *ctlLUNs) { p.Ports[0].Tag++ }, late: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, fixture, state, before := newFailedTargetStartForTest(t)
			saveFailedTargetStartForTest(t, svc, state, before)
			if test.backing {
				svc.backingStat = func(string) (os.FileInfo, error) { return os.Stat("/dev/zero") }
			}
			run := svc.runtime.run
			portReads, keyReads, removals := 0, 0, 0
			svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
				if command == "/usr/sbin/ctladm" && args[0] == "port" {
					removals++
					t.Fatal("unproven recovery attempted removal")
				}
				key := command
				if command == "/usr/sbin/ctladm" {
					key += " " + args[0]
				}
				if key == test.command {
					keyReads++
					if !test.late || keyReads > 2 {
						return test.output, test.failure
					}
				}
				if test.change != nil && command == "/usr/sbin/ctladm" && (args[0] == "portlist" || args[0] == "devlist") {
					ports, luns := fixture.inventory()
					if args[0] == "portlist" {
						portReads++
					}
					if !test.late || portReads > 1 {
						test.change(ports, luns)
					}
					var data []byte
					var err error
					if args[0] == "portlist" {
						data, err = xml.Marshal(ports)
					} else {
						data, err = xml.Marshal(luns)
					}
					return string(data), err
				}
				return run(ctx, input, command, args...)
			}
			if err := svc.recoverTargetPorts(t.Context()); err == nil || removals != 0 {
				t.Fatalf("unsafe recovery result=%v removals=%d", err, removals)
			}
			if _, err := os.Stat(svc.targetRecoveryPath()); err != nil {
				t.Fatal("uncertain recovery discarded ownership evidence")
			}
		})
	}
}

func TestTargetRecoveryRejectsUnsafeRecords(t *testing.T) {
	for _, test := range []string{"malformed", "version", "other-config", "duplicate-port", "invalid-selector", "missing-lun-record", "mode", "symlink", "oversized"} {
		t.Run(test, func(t *testing.T) {
			svc, _, state, before := newFailedTargetStartForTest(t)
			saveFailedTargetStartForTest(t, svc, state, before)
			record, err := svc.readTargetRecovery()
			if err != nil {
				t.Fatal(err)
			}
			switch test {
			case "version":
				record.Version++
			case "other-config":
				record.Config = "/etc/foreign.conf"
			case "duplicate-port":
				record.Ports = append(record.Ports, record.Ports[0])
			case "invalid-selector":
				record.Ports[0].Target = "target=foreign"
			case "missing-lun-record":
				record.LUNs = nil
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if test == "malformed" {
				data = []byte("{")
			} else if test == "oversized" {
				data = append(data, []byte(strings.Repeat(" ", 1<<20))...)
			}
			if err := os.WriteFile(svc.targetRecoveryPath(), data, 0600); err != nil {
				t.Fatal(err)
			}
			if test == "mode" {
				if err := os.Chmod(svc.targetRecoveryPath(), 0644); err != nil {
					t.Fatal(err)
				}
			} else if test == "symlink" {
				if err := os.Remove(svc.targetRecoveryPath()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(svc.targetPath(), svc.targetRecoveryPath()); err != nil {
					t.Fatal(err)
				}
			}
			run := svc.runtime.run
			svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
				if command == "/usr/sbin/ctladm" && args[0] == "port" {
					t.Fatal("unsafe record authorized removal")
				}
				return run(ctx, input, command, args...)
			}
			if err := svc.recoverTargetPorts(t.Context()); err == nil {
				t.Fatal("unsafe record accepted")
			}
			if _, err := os.Lstat(svc.targetRecoveryPath()); err != nil {
				t.Fatal("unsafe record was removed")
			}
		})
	}
}

func TestTargetRecoveryRetainsEvidenceAfterPartialRemoval(t *testing.T) {
	svc, fixture, state, before := newFailedTargetStartForTest(t)
	saveFailedTargetStartForTest(t, svc, state, before)
	_, luns := fixture.inventory()
	run := svc.runtime.run
	removals := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctladm" && args[0] == "port" {
			removals++
			if removals == 2 {
				return "", errors.New("remove failed")
			}
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.recoverTargetPorts(t.Context()); err == nil {
		t.Fatal("partial removal was accepted as complete")
	}
	ports, afterLUNs := fixture.inventory()
	if len(ports.Ports) != 1 || !reflect.DeepEqual(luns, afterLUNs) {
		t.Fatal("partial removal changed the wrong kernel objects")
	}
	if _, err := os.Stat(svc.targetRecoveryPath()); err != nil {
		t.Fatal("partial recovery discarded its ownership record")
	}
	if err := svc.recoverTargetPorts(t.Context()); err != nil {
		t.Fatalf("retry partial recovery: %v", err)
	}
	ports, afterLUNs = fixture.inventory()
	if len(ports.Ports) != 0 || !reflect.DeepEqual(luns, afterLUNs) || removals != 3 {
		t.Fatal("retry did not finish only the remaining port removal")
	}
}

func TestTargetRecoveryWaitsForRequestedOfflinePort(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupted), func(t *testing.T) {
			svc, fixture, state, before := newFailedTargetStartForTest(t)
			saveFailedTargetStartForTest(t, svc, state, before)
			run := svc.runtime.run
			pending, reads, removals := -1, 0, 0
			svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
				if command == "/usr/sbin/ctladm" && args[0] == "port" {
					removals++
					if pending >= 0 {
						t.Fatal("offline removal was issued twice")
					}
					id, err := strconv.Atoi(args[5])
					if err != nil {
						t.Fatal(err)
					}
					pending, reads = id, 0
					if interrupted && removals == 1 {
						return "", errors.New("interrupted removal")
					}
					return "", nil
				}
				if command == "/usr/sbin/ctladm" && args[0] == "portlist" && pending >= 0 {
					reads++
					if reads == 6 {
						if fixture.removedPorts == nil {
							fixture.removedPorts = make(map[int]bool)
						}
						fixture.removedPorts[pending] = true
						pending = -1
					}
					ports, _ := fixture.inventory()
					for i := range ports.Ports {
						if ports.Ports[i].ID == pending {
							ports.Ports[i].Online = "NO"
						}
					}
					data, err := xml.Marshal(ports)
					return string(data), err
				}
				return run(ctx, input, command, args...)
			}
			if interrupted {
				if err := svc.recoverTargetPorts(t.Context()); err == nil {
					t.Fatal("interrupted removal was accepted as complete")
				}
				saved, err := svc.readTargetRecovery()
				if err != nil || saved == nil || !slices.Contains(saved.Removing, pending) {
					t.Fatal("removal intent was not saved before the native command")
				}
			}
			if err := svc.recoverTargetPorts(t.Context()); err != nil {
				t.Fatal(err)
			}
			if pending >= 0 || removals != 2 {
				t.Fatalf("pending port=%d removal commands=%d", pending, removals)
			}
		})
	}
}

func TestTargetRecoveryRequiresNewOwnedPortsFromFailedStart(t *testing.T) {
	for _, test := range []string{"no-baseline", "preexisting-port-id", "preexisting-selector", "changed-config", "incomplete-apply", "no-serial", "unverified-baseline"} {
		t.Run(test, func(t *testing.T) {
			svc, fixture, state, before := newFailedTargetStartForTest(t)
			switch test {
			case "no-baseline":
				before = nil
			case "preexisting-port-id":
				before.ports.Ports = []ctlPort{{ID: 3}}
			case "preexisting-selector":
				ports, _ := fixture.inventory()
				port := ports.Ports[0]
				port.ID = 99
				before.ports.Ports = []ctlPort{port}
			case "changed-config":
				if err := os.WriteFile(svc.targetPath(), []byte("changed config"), 0600); err != nil {
					t.Fatal(err)
				}
			case "incomplete-apply":
				fixture.removedPorts = map[int]bool{3: true}
			case "unverified-baseline":
				if _, err := svc.targetStartInventory(t.Context()); err == nil {
					t.Fatal("existing ports were accepted as an empty baseline")
				}
				return
			case "no-serial":
				run := svc.runtime.run
				svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
					if command == "/usr/sbin/ctladm" && args[0] == "devlist" {
						_, luns := fixture.inventory()
						luns.LUNs[0].Serial = ""
						out, err := xml.Marshal(luns)
						return string(out), err
					}
					return run(ctx, input, command, args...)
				}
			}
			if err := svc.rememberFailedTargetStart(t.Context(), state, before); err == nil {
				t.Fatal("unproven start created an ownership claim")
			}
			if _, err := os.Stat(svc.targetRecoveryPath()); !os.IsNotExist(err) {
				t.Fatal("unproven start saved a recovery record")
			}
		})
	}
}

func TestTargetRecoveryWithoutRecordDoesNotInspectOrRemovePorts(t *testing.T) {
	svc := &Service{runtime: &targetRuntime{configFile: filepath.Join(t.TempDir(), "ctl.conf"), run: func(context.Context, string, string, ...string) (string, error) {
		t.Fatal("normal startup entered orphan cleanup without a failed-start record")
		return "", nil
	}}}
	if err := svc.recoverTargetPorts(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestTargetReloadDoesNotRecoverPortsFromLiveExports(t *testing.T) {
	svc, fixture, state, before := newFailedTargetStartForTest(t)
	saveFailedTargetStartForTest(t, svc, state, before)
	fixture.running = true
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/bin/pgrep" || command == "/sbin/sysctl" || command == "/usr/sbin/ctladm" && (args[0] == "port" || args[0] == "islist") {
			t.Fatal("normal reload entered stopped-port recovery")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	ports, _ := fixture.inventory()
	if !fixture.running || len(ports.Ports) != 2 {
		t.Fatal("normal reload removed a live export")
	}
}

func TestTargetRecoveryBlocksStartWhenIdentityChanges(t *testing.T) {
	svc, _, state, before := newFailedTargetStartForTest(t)
	saveFailedTargetStartForTest(t, svc, state, before)
	svc.backingStat = func(string) (os.FileInfo, error) { return os.Stat("/dev/zero") }
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[0] == "ctld" && args[1] == "onestart" || command == "/usr/sbin/ctladm" && args[0] == "port" {
			t.Fatal("uncertain ownership started or changed the target runtime")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("uncertain recovery result=%v", err)
	}
	if record, err := svc.readTargetRecovery(); err != nil || record == nil {
		t.Fatal("blocked start discarded its ownership evidence")
	}
}

func TestTargetRecoveryLockSerializesServiceInstances(t *testing.T) {
	svc := &Service{runtime: &targetRuntime{configFile: filepath.Join(t.TempDir(), "ctl.conf")}}
	lock, err := svc.lockTargetRecovery()
	if err != nil {
		t.Fatal(err)
	}
	other := &Service{runtime: &targetRuntime{configFile: svc.targetPath()}}
	if second, err := other.lockTargetRecovery(); err == nil {
		second.Close()
		t.Fatal("another service instance acquired the recovery lock")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := other.lockTargetRecovery()
	if err != nil {
		t.Fatal("recovery lock was not released")
	}
	second.Close()
}

func TestTargetStopRecoversOnlyFailedStartPorts(t *testing.T) {
	svc, fixture, state, before := newFailedTargetStartForTest(t)
	saveFailedTargetStartForTest(t, svc, state, before)
	_, luns := fixture.inventory()
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	ports, afterLUNs := fixture.inventory()
	if len(ports.Ports) != 0 || !reflect.DeepEqual(luns, afterLUNs) {
		t.Fatal("stopped orphan recovery changed LUNs")
	}
}

func TestTargetAuthOnlyChecksDoNotAcknowledgeReload(t *testing.T) {
	svc := newTargetTestService(t)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-10.test:auth-only", AuthMethod: "None", Portals: []iscsiModels.ISCSITargetPortal{{Address: "127.0.0.1", Port: 3260}}}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	samples := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && args[1] == "onereload" {
			return "", nil
		}
		if command == "/usr/sbin/ctladm" && args[0] == "portlist" {
			samples++
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.UpdateTarget(target.ID, target.TargetName, "", "CHAP", "user", "secretpassw0rd", "", ""); err != nil {
		t.Fatal(err)
	}
	if samples != 2 {
		t.Fatalf("samples=%d want 2", samples)
	}
}

func TestTargetDisableDoesNotValidateStoredConfig(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "invalid\nname", AuthMethod: "None"}).Error; err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" {
			t.Fatal("stop attempted configuration validation")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
}

func TestTargetDesiredStateSaveFailureDoesNotSignal(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.DB.Callback().Update().Before("gorm:update").Register("test:fail_state_save", func(tx *gorm.DB) { tx.AddError(errors.New("fixture save failure")) }); err != nil {
		t.Fatal(err)
	}
	svc.runtime.run = func(context.Context, string, string, ...string) (string, error) {
		t.Fatal("failed desired-state save signaled native state")
		return "", nil
	}
	if err := svc.SetEnabled(false); err == nil || errors.Is(err, ErrApplyFailed) {
		t.Fatalf("error=%v", err)
	}
	enabled, err := svc.desiredTargetEnabled()
	if err != nil || !enabled {
		t.Fatal("failed save changed desired state")
	}
}

func TestTargetFallbackRejectsDuplicateListenersAndMissingBacking(t *testing.T) {
	for _, test := range []string{"duplicate listeners", "missing backing"} {
		t.Run(test, func(t *testing.T) {
			svc := newTargetTestService(t)
			svc.DB.Create(&iscsiModels.ISCSITarget{TargetName: "invalid\nname", AuthMethod: "None"})
			text := configMarker + "\n" + `portal-group pg-1 { listen 127.0.0.1:3260 } target iqn.2026-10.test:fallback { portal-group pg-1 auth-group no-authentication lun 0 { path /dev/zvol/tank/test } }`
			if test == "duplicate listeners" {
				text = strings.Replace(text, "listen 127.0.0.1:3260", "listen 127.0.0.1:3260 listen 127.0.0.1:3260", 1)
			} else {
				svc.backingStat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			}
			if err := os.WriteFile(svc.targetPath(), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) {
				t.Fatalf("error=%v", err)
			}
			if _, err := svc.targetPID(t.Context()); !errors.Is(err, errTargetStopped) {
				t.Fatal("unsafe fallback started")
			}
		})
	}
}
