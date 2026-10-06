// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
)

func TestIntegrationISCSIExtraConfigTargetIOAndLifecycle(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	managedEndpoint, endpoint := f.endpoint(t), f.endpoint(t)
	managed := f.target(t, "managed", managedEndpoint, "None", "", "", "", "")
	user := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":extra"}
	f.manifest.Targets = append(f.manifest.Targets, user.TargetName)
	backing := f.fileBacking(t, "extra")
	extra := "# preserved native text\nportal-group user { discovery-auth-group no-authentication listen " + endpoint + " }\ntarget " + nativeQuote(user.TargetName) + " { portal-group user auth-group no-authentication lun 0 { path " + nativeQuote(backing) + " serial extra-owned } }"
	result, err := f.service.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
		t.Fatalf("save extra target: %v", err)
	}
	f.record(t)
	f.discover(t, endpoint, user)
	f.initiator(t, user, endpoint, "extra", "None", "", "", "", "")
	f.initiator(t, managed, managedEndpoint, "managed", "None", "", "", "", "")
	f.extraIO(t, user, endpoint, backing, 'E')
	f.io(t, managed, managedEndpoint, 'M')
	if err := f.service.UpdateTarget(managed.ID, managed.TargetName, "managed alias", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	f.disconnect(t, user, endpoint)
	f.disconnect(t, managed, managedEndpoint)
	if err := f.service.WriteConfig(true); err != nil {
		t.Fatal(err)
	}
	f.extraIO(t, user, endpoint, backing, 'F')
	f.io(t, managed, managedEndpoint, 'N')
	before, _ := os.ReadFile(f.service.targetPath())
	invalid := extra + "\npidfile /tmp/unowned"
	if _, err := f.service.SetExtraTargetConfig(&invalid); !errors.Is(err, ErrInvalidExtraConfig) {
		t.Fatal("unsupported candidate was not rejected")
	}
	if err := f.service.AddPortal(managed.ID, "127.0.0.1", mustPort(t, endpoint)); !errors.Is(err, ErrConflict) {
		t.Fatal("managed portal bypassed extra listener reservation")
	}
	after, _ := os.ReadFile(f.service.targetPath())
	if string(after) != string(before) {
		t.Fatal("rejected candidate changed native file")
	}
	f.extraIO(t, user, endpoint, backing, 'G')
	f.disconnect(t, user, endpoint)
	clear := ""
	if _, err := f.service.SetExtraTargetConfig(&clear); err != nil {
		t.Fatal(err)
	}
	f.record(t)
	f.io(t, managed, managedEndpoint, 'P')
	ctx, cancel := f.service.targetContext()
	defer cancel()
	luns, err := f.service.readCTLLUNs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lun := range luns.LUNs {
		if strings.HasPrefix(lun.Name, user.TargetName+",") {
			t.Fatal("cleared user LUN remains in CTL")
		}
	}
}

func TestIntegrationISCSIExtraConfigByteLimit(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	f.target(t, "byte-limit", f.endpoint(t), "None", "", "", "", "")
	for _, test := range []struct {
		name   string
		prefix string
	}{
		{name: "comments"},
		{name: "listener expansion", prefix: "portal-group boundary-unused { listen 127.0.0.1 }\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			extra := test.prefix + "#" + strings.Repeat("x", MaxExtraConfigBytes-len(test.prefix)-1)
			result, err := f.service.SetExtraTargetConfig(&extra)
			if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
				t.Fatalf("at-limit native save failed: %v", err)
			}
			f.record(t)
			before, err := os.ReadFile(f.service.targetPath())
			if err != nil {
				t.Fatal(err)
			}
			oversized := extra + "x"
			if _, err := f.service.SetExtraTargetConfig(&oversized); !errors.Is(err, ErrExtraConfigTooLarge) {
				t.Fatalf("oversized text returned wrong error: %v", err)
			}
			result, err = f.service.GetConfig()
			if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
				t.Fatalf("at-limit native status failed: %v", err)
			}
			after, err := os.ReadFile(f.service.targetPath())
			if err != nil || string(after) != string(before) {
				t.Fatal("oversized text changed the native file")
			}
		})
	}
}

func TestIntegrationISCSIExtraConfigAuthentication(t *testing.T) {
	for _, mutual := range []bool{false, true} {
		label, auth := "CHAP", "CHAP"
		if mutual {
			label, auth = "MutualCHAP", "MutualCHAP"
		}
		t.Run(label, func(t *testing.T) {
			f := requireISCSIIntegrationFixture(t)
			endpoint := f.endpoint(t)
			user := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":extra-auth"}
			other := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":other-auth"}
			f.manifest.Targets = append(f.manifest.Targets, user.TargetName, other.TargetName)
			backing := f.fileBacking(t, "extra-auth")
			otherBacking := f.fileBacking(t, "other-auth")
			clause := `chap "user" "secretpassw0rd"`
			mutualUser, mutualSecret := "", ""
			if mutual {
				clause = `chap-mutual "user" "secretpassw0rd" "target" "targetpassw0rd"`
				mutualUser, mutualSecret = "target", "targetpassw0rd"
			}
			otherClause := strings.Replace(strings.Replace(clause, `"user"`, `"other-user"`, 1), "secretpassw0rd", "otherpassw0rd", 1)
			extra := "auth-group user-auth { " + clause + " } auth-group other-auth { " + otherClause + " } portal-group user { listen " + endpoint + " } target " + nativeQuote(user.TargetName) + " { portal-group user auth-group user-auth lun 0 { path " + nativeQuote(backing) + " } } target " + nativeQuote(other.TargetName) + " { portal-group user auth-group other-auth lun 0 { path " + nativeQuote(otherBacking) + " } }"
			if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
				t.Fatal(err)
			}
			initiator := f.initiator(t, user, endpoint, "extra-auth", auth, "user", "wrongpassw0rd", mutualUser, mutualSecret)
			f.session(t, user.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), auth, "user", "secretpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.session(t, user.TargetName, endpoint, true)
			f.extraIO(t, user, endpoint, backing, 'A')
			otherInitiator := f.initiator(t, other, endpoint, "other-auth", auth, "user", "secretpassw0rd", mutualUser, mutualSecret)
			f.session(t, other.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(otherInitiator.ID, otherInitiator.Nickname, endpoint, other.TargetName, f.initiatorName(), auth, "other-user", "otherpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.extraIO(t, other, endpoint, otherBacking, 'B')
			if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), auth, "other-user", "otherpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.session(t, user.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), auth, "user", "secretpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.extraIO(t, user, endpoint, backing, 'C')
			if mutual {
				if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), auth, "user", "secretpassw0rd", mutualUser, "wrongtargetpwd"); err != nil {
					t.Fatal(err)
				}
				f.session(t, user.TargetName, endpoint, false)
			}
		})
	}
}

func (f *iscsiIntegrationFixture) extraIO(t *testing.T, target iscsiModels.ISCSITarget, endpoint, backing string, pattern byte) {
	t.Helper()
	owned := false
	for _, file := range f.manifest.FileBackings {
		if file.Path == backing && f.ownsFileBacking(file) {
			owned = true
		}
	}
	if !owned {
		t.Fatal("file backing is not recorded and owned")
	}
	device := f.disk(t, target, endpoint)
	data := strings.Repeat(string([]byte{pattern}), 4096)
	path := filepath.Join(f.directory, "pattern")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationCommand(f.service, "/bin/dd", "if="+path, "of="+device, "bs=4096", "count=1", "seek=8"); err != nil {
		t.Fatal("owned extra disk write failed")
	}
	if _, err := integrationCommand(f.service, "/sbin/camcontrol", "cmd", strings.TrimPrefix(device, "/dev/"), "-c", "35 00 00 00 00 00 00 00 00 00"); err != nil {
		t.Fatal("owned extra disk flush failed")
	}
	for _, source := range []string{device, backing} {
		out, err := integrationCommand(f.service, "/bin/dd", "if="+source, "bs=4096", "count=1", "skip=8")
		if err != nil || out != data {
			t.Fatal("extra disk read does not match its owned backing")
		}
	}
}

func TestIntegrationISCSIExtraConfigDisabledAndStartup(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	user := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":extra-startup"}
	f.manifest.Targets = append(f.manifest.Targets, user.TargetName)
	backing := f.fileBacking(t, "extra-startup")
	extra := "portal-group user { listen " + endpoint + " } target " + nativeQuote(user.TargetName) + " { portal-group user auth-group no-authentication lun 0 { path " + nativeQuote(backing) + " } }"
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "disabled" {
		t.Fatal("disabled save did not remain disabled")
	}
	f.target(t, "unbound", "", "None", "", "", "", "")
	ctx, cancel := f.service.targetContext()
	if err := f.service.checkTargetStopped(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	setIntegrationDesiredEnabled(t, f)
	if err := f.service.StartTargets(); err != nil {
		t.Fatal(err)
	}
	f.initiator(t, user, endpoint, "extra-startup", "None", "", "", "", "")
	f.extraIO(t, user, endpoint, backing, 'S')
	before, _ := os.ReadFile(f.service.targetPath())
	if err := f.service.DB.Model(&iscsiModels.ISCSISettings{}).Where("id = ?", 1).Update("extra_target_config", "pidfile /tmp/unowned").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_using_owned_file" {
		t.Fatalf("invalid desired config did not use independently validated owned file: %v", err)
	}
	after, _ := os.ReadFile(f.service.targetPath())
	if string(after) != string(before) {
		t.Fatal("startup overwrote last valid owned file")
	}
	result, err = f.service.GetConfig()
	if err != nil || result.ApplyStatus != "invalid" {
		t.Fatal("invalid restored row was not reported")
	}
	f.extraIO(t, user, endpoint, backing, 'T')
	f.disconnect(t, user, endpoint)
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.service.targetPath()); err != nil {
		t.Fatal(err)
	}
	setIntegrationDesiredEnabled(t, f)
	if err := f.service.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_no_valid_owned_file" {
		t.Fatal("startup started without a valid owned file")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	if _, err := f.service.targetPID(ctx); !errors.Is(err, errTargetStopped) {
		t.Fatal("invalid startup started ctld")
	}
}

func TestIntegrationISCSIExtraConfigPinnedGrammar(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	for _, text := range loadNativeGrammarFixture(t).Accepted {
		state, rendered, err := f.service.parseExtraTargetConfig(t.Context(), text, nil)
		if err != nil || state == nil {
			t.Fatal("pinned fixture was rejected by the extra parser")
		}
		ctx, cancel := f.service.targetContext()
		_, err = f.service.runTargetCommand(ctx, rendered, "/usr/sbin/ctld", "-t", "-f", "/dev/stdin")
		cancel()
		if err != nil {
			t.Fatal("pinned extra grammar differs from native ctld")
		}
	}
}

func TestIntegrationISCSIExtraConfigUnusedGroupsNamedLUNsAndOverlap(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	backing := f.fileBacking(t, "named")
	name := iscsiTestIQNPrefix + f.manifest.RunID + ":named"
	target := iscsiTestIQNPrefix + f.manifest.RunID + ":unbound"
	f.manifest.NamedLUNs = append(f.manifest.NamedLUNs, name)
	f.manifest.Targets = append(f.manifest.Targets, target)
	extra := "portal-group unused { listen " + endpoint + " } portal-group empty {} lun " + nativeQuote(name) + " { path " + nativeQuote(backing) + " serial named-owned option vendor SYLVE } target " + nativeQuote(target) + " { portal-group empty lun 0 " + nativeQuote(name) + " }"
	if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	f.record(t)
	ctx, cancel := f.service.targetContext()
	pid, err := f.service.targetPID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listeners, err := f.service.targetListeners(ctx, pid)
	cancel()
	if err != nil || len(listeners) != 0 {
		t.Fatal("unused or empty group opened a listener")
	}
	before, _ := os.ReadFile(f.service.targetPath())
	for _, addition := range []string{
		"portal-group duplicate { listen " + endpoint + " }",
		"transport-group duplicate { listen tcp " + endpoint + " }",
		"portal-group wildcard { listen 0.0.0.0:" + strconv.Itoa(mustPort(t, endpoint)) + " }",
	} {
		invalid := extra + "\n" + addition
		if _, err := f.service.SetExtraTargetConfig(&invalid); !errors.Is(err, ErrInvalidExtraConfig) {
			t.Fatal("unused cross-protocol reservation allowed an overlap")
		}
	}
	after, _ := os.ReadFile(f.service.targetPath())
	if string(after) != string(before) {
		t.Fatal("rejected overlap changed the file")
	}
	clear := ""
	if _, err := f.service.SetExtraTargetConfig(&clear); err != nil {
		t.Fatal(err)
	}
	f.record(t)
}

func TestIntegrationISCSIExtraConfigCanonicalReload(t *testing.T) {
	for _, test := range []struct{ label, network, initial, explicit string }{
		{label: "DefaultPort", network: "tcp4", initial: "127.0.0.1", explicit: "127.0.0.1:3260"},
		{label: "IPv6", network: "tcp6", initial: "[0:0:0:0:0:0:0:1]:3261", explicit: "[::1]:3261"},
	} {
		t.Run(test.label, func(t *testing.T) {
			f := requireISCSIIntegrationFixture(t)
			probe, err := net.Listen(test.network, test.explicit)
			if err != nil {
				t.Fatal("canonical native test endpoint is in use; left untouched")
			}
			probe.Close()
			f.manifest.Endpoints = append(f.manifest.Endpoints, test.explicit)
			user := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":canonical"}
			f.manifest.Targets = append(f.manifest.Targets, user.TargetName)
			backing := f.fileBacking(t, "canonical")
			extra := "portal-group user { listen " + nativeQuote(test.initial) + " } target " + nativeQuote(user.TargetName) + " { portal-group user auth-group no-authentication lun 0 { path " + nativeQuote(backing) + " } }"
			if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(f.service.targetPath())
			f.initiator(t, user, test.explicit, "canonical", "None", "", "", "", "")
			f.extraIO(t, user, test.explicit, backing, 'C')
			extra = strings.Replace(extra, nativeQuote(test.initial), nativeQuote(test.explicit), 1)
			if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(f.service.targetPath())
			if string(after) != string(before) {
				t.Fatal("equivalent listener changed the rendered spelling")
			}
			f.extraIO(t, user, test.explicit, backing, 'D')
		})
	}
}

func TestIntegrationISCSIExtraConfigDelayedCHAPReload(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	user := iscsiModels.ISCSITarget{TargetName: iscsiTestIQNPrefix + f.manifest.RunID + ":delayed"}
	f.manifest.Targets = append(f.manifest.Targets, user.TargetName)
	backing := f.fileBacking(t, "delayed")
	extra := "auth-group user-auth { chap user secretpassw0rd } portal-group user { listen " + endpoint + " } target " + nativeQuote(user.TargetName) + " { portal-group user auth-group user-auth lun 0 { path " + nativeQuote(backing) + " } }"
	if _, err := f.service.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	initiator := f.initiator(t, user, endpoint, "delayed", "CHAP", "user", "secretpassw0rd", "", "")
	f.extraIO(t, user, endpoint, backing, 'H')
	ctx, cancel := f.service.targetContext()
	pid, err := f.service.targetPID(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	paused := true
	t.Cleanup(func() {
		if paused {
			if _, err := integrationCommand(f.service, "/bin/kill", "-CONT", strconv.Itoa(pid)); err != nil {
				t.Error("cannot resume owned delayed-reload daemon")
			}
		}
	})
	if _, err := integrationCommand(f.service, "/bin/kill", "-STOP", strconv.Itoa(pid)); err != nil {
		t.Fatal("cannot pause owned target daemon")
	}
	extra = strings.Replace(extra, "secretpassw0rd", "newsecretpass", 1)
	result, err := f.service.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" {
		t.Fatal("unchanged topology must only be reported as checked, not a reload acknowledgement")
	}
	if _, err := integrationCommand(f.service, "/bin/kill", "-CONT", strconv.Itoa(pid)); err != nil {
		t.Fatal("cannot resume owned target daemon")
	}
	paused = false
	if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), "CHAP", "user", "newsecretpass", "", ""); err != nil {
		t.Fatal(err)
	}
	f.extraIO(t, user, endpoint, backing, 'J')
	if err := f.service.UpdateInitiator(initiator.ID, initiator.Nickname, endpoint, user.TargetName, f.initiatorName(), "CHAP", "user", "secretpassw0rd", "", ""); err != nil {
		t.Fatal(err)
	}
	f.session(t, user.TargetName, endpoint, false)
}

func TestIntegrationISCSIExtraConfigNativeSyslogLimit(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	logFile, err := os.Open("/var/log/messages")
	if err != nil {
		t.Fatal("native syslog limit test requires /var/log/messages")
	}
	defer logFile.Close()
	info, err := logFile.Stat()
	if err != nil {
		t.Fatal("cannot inspect native log offset")
	}
	marker := "SYLVE_NONSECRET_EXTRA_LOG_" + f.manifest.RunID
	ctx, cancel := f.service.targetContext()
	_, err = f.service.runTargetCommand(ctx, "portal-group user { "+marker+" }", "/usr/sbin/ctld", "-t", "-f", "/dev/stdin")
	cancel()
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatal("native output entered the Sylve command error")
	}
	waitIntegration(t, "nonsecret native syslog marker", func(context.Context) bool {
		data := make([]byte, 1<<20)
		n, _ := logFile.ReadAt(data, info.Size())
		return strings.Contains(string(data[:n]), marker)
	})
}
