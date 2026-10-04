// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestIntegrationISCSISharedEndpointDiscoveryAndIO(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	first := f.target(t, "first", endpoint, "None", "", "", "", "")
	second := f.target(t, "second", endpoint, "None", "", "", "", "")
	f.discoveryInventory(t, endpoint, map[string][]string{first.TargetName: {endpoint}, second.TargetName: {endpoint}})
	f.discover(t, endpoint, first, second)
	f.initiator(t, first, endpoint, "first", "None", "", "", "", "")
	f.initiator(t, second, endpoint, "second", "None", "", "", "", "")
	f.io(t, first, endpoint, 'A')
	f.io(t, second, endpoint, 'B')
	var lun iscsiModels.ISCSITargetLUN
	f.service.DB.Where("target_id = ?", first.ID).First(&lun)
	out, err := integrationCommand(f.service, "/bin/dd", "if=/dev/zvol/"+lun.ZVol, "bs=4096", "count=1", "skip=8")
	if err != nil || out != strings.Repeat("A", 4096) {
		t.Fatal("second target write changed first backing")
	}
	before, _ := os.ReadFile(f.service.targetPath())
	if err := f.service.AddPortal(second.ID, "0.0.0.0", mustPort(t, endpoint)); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap error=%v", err)
	}
	after, _ := os.ReadFile(f.service.targetPath())
	if string(before) != string(after) {
		t.Fatal("rejected candidate changed live configuration")
	}
	if err := f.service.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	var initiator iscsiModels.ISCSIInitiator
	f.service.DB.Where("nickname = ?", "first").First(&initiator)
	if err := f.service.ConnectInitiator(initiator.ID); err != nil {
		t.Fatal(err)
	}
	f.io(t, first, endpoint, 'C')
}

func TestIntegrationISCSILegacyMigrationWithActiveSession(t *testing.T) {
	for _, normalize := range []bool{false, true} {
		label := "stable-listener"
		if normalize {
			label = "normalization-requires-stop"
		}
		t.Run(label, func(t *testing.T) {
			f := requireISCSIIntegrationFixture(t)
			endpoint := f.endpoint(t)
			first := f.target(t, "legacy-first", endpoint, "None", "", "", "", "")
			second := f.target(t, "legacy-second", "", "None", "", "", "", "")
			if err := f.service.SetEnabled(false); err != nil {
				t.Fatal(err)
			}
			text, err := os.ReadFile(f.service.targetPath())
			if err != nil {
				t.Fatal(err)
			}
			portal, err := f.service.endpoint("127.0.0.1", mustPort(t, endpoint))
			if err != nil {
				t.Fatal(err)
			}
			legacy := strings.ReplaceAll(string(text), f.service.groupName(portal), "pg-"+strconv.FormatUint(uint64(first.ID), 10))
			if normalize {
				legacy = strings.Replace(legacy, nativeQuote(endpoint), nativeQuote("127.0.0.1:0"+strconv.Itoa(portal.port)), 1)
			}
			if err := utils.AtomicWriteFile(f.service.targetPath(), []byte(legacy), 0600); err != nil {
				t.Fatal(err)
			}
			setIntegrationDesiredEnabled(t, f)
			ctx, cancel := f.service.targetContext()
			state, err := f.service.prepareTargetConfig(ctx, legacy)
			if err == nil {
				err = f.service.applyTargetRuntime(ctx, state, false)
			}
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			f.initiator(t, first, endpoint, "legacy-first", "None", "", "", "", "")
			f.io(t, first, endpoint, 'R')
			if normalize {
				err := f.service.AddPortal(second.ID, "127.0.0.1", portal.port)
				if !errors.Is(err, ErrConflict) || err.Error() != "iscsi_listener_normalization_requires_stop" {
					t.Fatalf("candidate result=%v", err)
				}
				var count int64
				if err := f.service.DB.Model(&iscsiModels.ISCSITargetPortal{}).Where("target_id = ?", second.ID).Count(&count).Error; err != nil || count != 0 {
					t.Fatal("unsafe candidate was committed")
				}
				if err := f.service.UpdateTarget(first.ID, first.TargetName, "normalization pending", "None", "", "", "", ""); !errors.Is(err, ErrApplyFailed) || err.Error() != "iscsi_listener_normalization_requires_stop" {
					t.Fatalf("saved mutation result=%v", err)
				}
				after, err := os.ReadFile(f.service.targetPath())
				if err != nil || string(after) != legacy {
					t.Fatal("unsafe listener normalization replaced the file")
				}
				if err := f.service.SetEnabled(false); err != nil {
					t.Fatal(err)
				}
				f.disconnect(t, first, endpoint)
				if err := f.service.SetEnabled(true); err != nil {
					t.Fatal(err)
				}
				f.io(t, first, endpoint, 'W')
			}
			if err := f.service.AddPortal(second.ID, "127.0.0.1", portal.port); err != nil {
				t.Fatal(err)
			}
			var initiator iscsiModels.ISCSIInitiator
			if err := f.service.DB.Where("nickname = ?", "legacy-first").First(&initiator).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.service.ConnectInitiator(initiator.ID); err != nil {
				t.Fatal(err)
			}
			f.initiator(t, second, endpoint, "legacy-second", "None", "", "", "", "")
			f.io(t, first, endpoint, 'X')
			f.io(t, second, endpoint, 'Y')
			f.record(t)
		})
	}
}

func mustPort(t *testing.T, endpoint string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		t.Fatal("invalid fixture endpoint port")
	}
	return number
}

func TestIntegrationISCSIAuthentication(t *testing.T) {
	for _, auth := range []string{"CHAP", "MutualCHAP"} {
		t.Run(auth, func(t *testing.T) {
			f := requireISCSIIntegrationFixture(t)
			endpoint := f.endpoint(t)
			mutualUser, mutualSecret := "", ""
			if auth == "MutualCHAP" {
				mutualUser, mutualSecret = "target-user", "targetpassw0rd"
			}
			target := f.target(t, "auth", endpoint, auth, "test-user", "secretpassw0rd", mutualUser, mutualSecret)
			other := f.target(t, "other-auth", endpoint, auth, "other-user", "otherpassw0rd", mutualUser, mutualSecret)
			initiator := f.initiator(t, target, endpoint, "auth", auth, "test-user", "wrongpassw0rd", mutualUser, mutualSecret)
			f.session(t, target.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "test-user", "secretpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.io(t, target, endpoint, 'S')
			otherInitiator := f.initiator(t, other, endpoint, "other-auth", auth, "test-user", "secretpassw0rd", mutualUser, mutualSecret)
			f.session(t, other.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(otherInitiator.ID, "other-auth", endpoint, other.TargetName, f.initiatorName(), auth, "other-user", "otherpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.io(t, other, endpoint, 'O')
			if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "other-user", "otherpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.session(t, target.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "test-user", "secretpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			if auth == "MutualCHAP" {
				if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "test-user", "secretpassw0rd", mutualUser, "wrongtargetpwd"); err != nil {
					t.Fatal(err)
				}
				f.session(t, target.TargetName, endpoint, false)
				if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "test-user", "secretpassw0rd", mutualUser, mutualSecret); err != nil {
					t.Fatal(err)
				}
				f.io(t, target, endpoint, 'T')
			}
			if err := f.service.UpdateTarget(target.ID, target.TargetName, "", auth, "test-user", "changedpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			if err := f.service.ConnectInitiator(initiator.ID); err != nil {
				t.Fatal(err)
			}
			f.session(t, target.TargetName, endpoint, false)
			if err := f.service.UpdateInitiator(initiator.ID, "auth", endpoint, target.TargetName, f.initiatorName(), auth, "test-user", "changedpassw0rd", mutualUser, mutualSecret); err != nil {
				t.Fatal(err)
			}
			f.io(t, target, endpoint, 'U')
			f.io(t, other, endpoint, 'V')
		})
	}
}

func TestIntegrationISCSITopologyEditsAndRestart(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	shared, extra := f.endpoint(t), f.endpoint(t)
	first := f.target(t, "topology-first", shared, "None", "", "", "", "")
	second := f.target(t, "topology-second", shared, "None", "", "", "", "")
	if err := f.service.AddPortal(first.ID, "127.0.0.1", mustPort(t, extra)); err != nil {
		t.Fatal(err)
	}
	f.discoveryInventory(t, shared, map[string][]string{first.TargetName: {shared, extra}, second.TargetName: {shared}})
	f.discover(t, shared, first, second)
	f.discover(t, extra, first)
	oldInitiator := f.initiator(t, first, shared, "first-shared", "None", "", "", "", "")
	extraInitiator := f.initiator(t, first, extra, "first-extra", "None", "", "", "", "")
	secondInitiator := f.initiator(t, second, shared, "second-shared", "None", "", "", "", "")
	f.io(t, first, shared, 'E')
	f.io(t, first, extra, 'F')
	f.io(t, second, shared, 'G')
	if err := f.service.DeleteInitiator(oldInitiator.ID); err != nil {
		t.Fatal(err)
	}
	var portal iscsiModels.ISCSITargetPortal
	if err := f.service.DB.Where("target_id = ? AND port = ?", first.ID, mustPort(t, shared)).First(&portal).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.RemovePortal(first.ID, portal.ID); err != nil {
		t.Fatal(err)
	}
	f.io(t, second, shared, 'H')
	f.io(t, first, extra, 'I')
	f.disconnect(t, first, extra)
	var lun iscsiModels.ISCSITargetLUN
	if err := f.service.DB.Where("target_id = ? AND lun_number = 0", first.ID).First(&lun).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.RemoveLUN(first.ID, lun.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.AddLUN(first.ID, 0, f.volume(t, "replacement")); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConnectInitiator(extraInitiator.ID); err != nil {
		t.Fatal(err)
	}
	f.io(t, first, extra, 'J')
	if err := f.service.DeleteInitiator(secondInitiator.ID); err != nil {
		t.Fatal(err)
	}
	portal = iscsiModels.ISCSITargetPortal{}
	if err := f.service.DB.Where("target_id = ?", second.ID).First(&portal).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.RemovePortal(second.ID, portal.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := f.service.targetContext()
	defer cancel()
	pid, err := f.service.targetPID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listeners, err := f.service.targetListeners(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, listener := range listeners {
		if listener.listen() == shared {
			t.Fatal("final shared attachment left a listener")
		}
	}
	if err := f.service.DeleteTarget(second.ID); err != nil {
		t.Fatal(err)
	}
	f.disconnect(t, first, extra)
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	f.io(t, first, extra, 'K')
	f.record(t)
}

func TestIntegrationISCSIQuotedIPv6ConfigValidation(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	target := f.target(t, "ipv6-validation", "", "None", "", "", "", "")
	port := mustPort(t, f.endpoint(t))
	for _, address := range []string{"::1", "fe80::1%lo0"} {
		if err := f.service.AddPortal(target.ID, address, port); err != nil {
			t.Fatal(err)
		}
	}
	text, err := f.service.GenerateTargetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `listen "[fe80::1%lo0]:`) {
		t.Fatal("scoped listener was not quoted")
	}
	if err := f.service.WriteTargetConfig(false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.service.targetPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("target config mode is not 0600")
	}
	if _, err := f.service.targetPID(context.Background()); !errors.Is(err, errTargetStopped) {
		t.Fatal("disabled config validation started ctld")
	}
}

func TestIntegrationISCSINativeQuotedCredentials(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	const user = `literal\user`
	const secret = `secret\passw0rd`
	target := f.target(t, "native-quotes", endpoint, "CHAP", user, secret, "", "")
	f.initiator(t, target, endpoint, "native-quotes", "CHAP", user, secret, "", "")
	f.io(t, target, endpoint, 'Q')
}

func TestIntegrationISCSIStartupFallbackAndRepair(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	endpoint := f.endpoint(t)
	target := f.target(t, "fallback", endpoint, "None", "", "", "", "")
	before, err := os.ReadFile(f.service.targetPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	setIntegrationDesiredEnabled(t, f)
	var portal iscsiModels.ISCSITargetPortal
	if err := f.service.DB.Where("target_id = ?", target.ID).First(&portal).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.DB.Model(&portal).Update("address", "invalid-stored-address").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_using_owned_file" {
		t.Fatalf("startup result=%v", err)
	}
	after, err := os.ReadFile(f.service.targetPath())
	if err != nil || string(after) != string(before) {
		t.Fatal("invalid stored rows replaced the fallback file")
	}
	initiator := f.initiator(t, target, endpoint, "fallback", "None", "", "", "", "")
	f.io(t, target, endpoint, 'L')
	f.disconnect(t, target, endpoint)
	if err := f.service.RemovePortal(target.ID, portal.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.AddPortal(target.ID, "127.0.0.1", mustPort(t, endpoint)); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConnectInitiator(initiator.ID); err != nil {
		t.Fatal(err)
	}
	f.io(t, target, endpoint, 'M')
}

func setIntegrationDesiredEnabled(t *testing.T, f *iscsiIntegrationFixture) {
	t.Helper()
	var settings models.BasicSettings
	if err := f.service.DB.First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.service.DB.Model(&settings).Select("Services").Updates(&models.BasicSettings{Services: []models.AvailableService{models.ISCSI}}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationISCSIColdStartFailureAndRetry(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	target := f.target(t, "bind-failure", "", "None", "", "", "", "")
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	endpoint := f.endpoint(t)
	blocker, err := net.Listen("tcp4", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { blocker.Close() })
	if err := f.service.AddPortal(target.ID, "127.0.0.1", mustPort(t, endpoint)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.service.targetPath())
	if err != nil {
		t.Fatal(err)
	}
	setIntegrationDesiredEnabled(t, f)
	if err := f.service.StartTargets(); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("bind failure result=%v", err)
	}
	after, err := os.ReadFile(f.service.targetPath())
	if err != nil || string(after) != string(before) {
		t.Fatal("partial runtime failure restored or changed the candidate file")
	}
	f.record(t)
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StartTargets(); err != nil {
		t.Fatal(err)
	}
	f.initiator(t, target, endpoint, "bind-retry", "None", "", "", "", "")
	f.io(t, target, endpoint, 'N')
}

func TestIntegrationISCSINativeValidationErrorIsSafe(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	const marker = "SYLVE_NONSECRET_NATIVE_LOG_MARKER"
	ctx, cancel := f.service.targetContext()
	defer cancel()
	_, err := f.service.runTargetCommand(ctx, `portal-group pg-test { listen "127.0.0.1:3260" `+marker+` }`, "/usr/sbin/ctld", "-t", "-f", "/dev/stdin")
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatal("native validation output entered the command error")
	}
	if _, err := f.service.targetPID(context.Background()); !errors.Is(err, errTargetStopped) {
		t.Fatal("native syntax validation started a target daemon")
	}
}

func TestIntegrationISCSINativeGoldenSyntax(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	text, err := os.ReadFile("testdata/shared-portals.conf")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := f.service.targetContext()
	defer cancel()
	if _, err := f.service.runTargetCommand(ctx, string(text), "/usr/sbin/ctld", "-t", "-f", "/dev/stdin"); err != nil {
		t.Fatal("native shared-portal golden config syntax was rejected")
	}
}

func TestIntegrationISCSIUnboundMissingBackingAndDisable(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	target := f.target(t, "unbound", "", "None", "", "", "", "")
	if err := f.service.AddLUN(target.ID, 1, f.manifest.Pool+"/missing"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing backing error=%v", err)
	}
	ctx, cancel := f.service.targetContext()
	ports, err := f.service.readCTLPorts(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range ports.Ports {
		if port.Target == target.TargetName {
			t.Fatal("portal-less target created a kernel port")
		}
	}
	if err := f.service.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	endpoint := f.endpoint(t)
	if err := f.service.AddPortal(target.ID, "127.0.0.1", mustPort(t, endpoint)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = f.service.targetContext()
	defer cancel()
	if _, err := f.service.targetPID(ctx); !errors.Is(err, errTargetStopped) {
		t.Fatal("disabled edit started ctld")
	}
	if err := f.service.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	f.initiator(t, target, endpoint, "enabled", "None", "", "", "", "")
	f.io(t, target, endpoint, 'D')
}

func TestIntegrationISCSIExistingBackingRepair(t *testing.T) {
	f := requireISCSIIntegrationFixture(t)
	target := f.target(t, "backing-repair", "", "None", "", "", "", "")
	var lun iscsiModels.ISCSITargetLUN
	if err := f.service.DB.Where("target_id = ?", target.ID).First(&lun).Error; err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.service.targetPath())
	if err != nil {
		t.Fatal(err)
	}
	renamed := lun.ZVol + "-moved"
	if _, err := integrationCommand(f.service, "/sbin/zfs", "rename", lun.ZVol, renamed); err != nil {
		t.Fatal("cannot rename owned test backing")
	}
	needsRepair := true
	t.Cleanup(func() {
		if needsRepair {
			if _, err := integrationCommand(f.service, "/sbin/zfs", "rename", renamed, lun.ZVol); err != nil {
				t.Error("cannot restore owned backing name")
			}
		}
	})
	if err := f.service.UpdateTarget(target.ID, target.TargetName, "saved while backing is missing", "None", "", "", "", ""); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("missing saved backing result=%v", err)
	}
	after, err := os.ReadFile(f.service.targetPath())
	if err != nil || string(after) != string(before) {
		t.Fatal("missing backing replaced the preceding file")
	}
	var saved iscsiModels.ISCSITarget
	if err := f.service.DB.First(&saved, target.ID).Error; err != nil || saved.Alias != "saved while backing is missing" {
		t.Fatal("missing backing rolled back stored target edit")
	}
	if _, err := integrationCommand(f.service, "/sbin/zfs", "rename", renamed, lun.ZVol); err != nil {
		t.Fatal("cannot repair owned test backing")
	}
	needsRepair = false
	if err := f.service.RemoveLUN(target.ID, lun.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.AddLUN(target.ID, 0, lun.ZVol); err != nil {
		t.Fatal(err)
	}
	endpoint := f.endpoint(t)
	if err := f.service.AddPortal(target.ID, "127.0.0.1", mustPort(t, endpoint)); err != nil {
		t.Fatal(err)
	}
	f.initiator(t, target, endpoint, "backing-repair", "None", "", "", "", "")
	f.io(t, target, endpoint, 'Z')
}
