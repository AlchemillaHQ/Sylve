// SPDX-License-Identifier: BSD-2-Clause

package iscsi

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"log"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/rs/zerolog"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestExtraConfigSaveRetryClearAndRestartStatus(t *testing.T) {
	svc := newTargetTestService(t)
	extra := "# exact editor text\nportal-group user { listen 127.0.0.1:49222 }\ntarget iqn.extra { portal-group user lun 0 { backend ramdisk size 64K serial extra-serial } }"
	result, err := svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
		t.Fatalf("save: result=%v error=%v", result, err)
	}
	content, err := os.ReadFile(svc.targetPath())
	if err != nil || !strings.HasPrefix(string(content), configMarker+"\n") || !strings.Contains(string(content), `listen "127.0.0.1:49222"`) {
		t.Fatal("normalized owned file was not written")
	}
	restarted := &Service{DB: svc.DB, runtime: svc.runtime, backingStat: svc.backingStat, ipv6Only: svc.ipv6Only}
	result, err = restarted.GetConfig()
	if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
		t.Fatalf("restart: result=%v error=%v", result, err)
	}
	run := svc.runtime.run
	reloads := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			reloads++
			return "credential-near-token", errors.New("credential-near-token")
		}
		return run(ctx, input, command, args...)
	}
	result, err = svc.SetExtraTargetConfig(&extra)
	if !errors.Is(err, ErrApplyFailed) || result.ApplyStatus != "pending" || strings.Contains(err.Error(), "credential") || reloads != 1 {
		t.Fatalf("same-value retry: %v", err)
	}
	if _, err := svc.SetExtraTargetConfig(nil); err != nil || reloads != 1 {
		t.Fatal("omitted field attempted runtime apply")
	}
	svc.runtime.run = run
	extra = ""
	result, err = svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(svc.targetPath())
	if strings.Contains(string(content), extraConfigBanner) || strings.Contains(string(content), "iqn.extra") {
		t.Fatal("clear retained user stanzas")
	}
}

func TestExtraConfigByteLimitAppliesToSavedText(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix string
		suffix string
	}{
		{name: "comment"},
		{name: "final newline", suffix: "\n"},
		{name: "listener expansion", prefix: "portal-group user { listen 127.0.0.1 }\n"},
		{name: "UTF-8 comments", prefix: strings.Repeat("# é\n", 1000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newTargetTestService(t)
			extra := test.prefix + "#" + strings.Repeat("x", MaxExtraConfigBytes-len(test.prefix)-len(test.suffix)-1) + test.suffix
			result, err := svc.SetExtraTargetConfig(&extra)
			if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
				t.Fatalf("at-limit save failed: %v", err)
			}
			if err := svc.CreateTarget("iqn.managed", "", "None", "", "", "", ""); err != nil {
				t.Fatalf("managed preflight rejected at-limit saved text: %v", err)
			}
			result, err = svc.GetConfig()
			if err != nil || result.ApplyStatus != "checked" || result.ExtraTargetConfig != extra {
				t.Fatalf("at-limit status failed: %v", err)
			}
			before, err := os.ReadFile(svc.targetPath())
			if err != nil {
				t.Fatal(err)
			}
			oversized := extra + "x"
			if _, err := svc.SetExtraTargetConfig(&oversized); !errors.Is(err, ErrExtraConfigTooLarge) {
				t.Fatalf("oversized text returned wrong error: %v", err)
			}
			settings, err := svc.targetSettings()
			if err != nil || settings.ExtraTargetConfig != extra {
				t.Fatal("oversized text changed saved configuration")
			}
			after, err := os.ReadFile(svc.targetPath())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("oversized text changed the target file")
			}
		})
	}
}

func TestExtraConfigInvalidCandidateNeverRunsCTLDOrSaves(t *testing.T) {
	svc := newTargetTestService(t)
	before := []byte("previous file")
	os.WriteFile(svc.targetPath(), before, 0600)
	ctldCalls := 0
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" {
			ctldCalls++
		}
		return run(ctx, input, command, args...)
	}
	extra := `auth-group user { chap "x\" listen 127.0.0.1:3260 #" }`
	if _, err := svc.SetExtraTargetConfig(&extra); !errors.Is(err, ErrInvalidExtraConfig) || ctldCalls != 0 {
		t.Fatal("quote injection reached native validator")
	}
	settings, err := svc.targetSettings()
	if err != nil || settings.ExtraTargetConfig != "" {
		t.Fatal("invalid candidate was saved")
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(after) != string(before) {
		t.Fatal("invalid candidate replaced the file")
	}
}

func TestExtraConfigValidationAndDBFailuresPreserveFile(t *testing.T) {
	for _, failure := range []string{"managed", "merged", "database"} {
		t.Run(failure, func(t *testing.T) {
			svc := newTargetTestService(t)
			if err := svc.WriteTargetConfig(true); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(svc.targetPath())
			run := svc.runtime.run
			validations := 0
			svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
				if command == "/usr/sbin/ctld" {
					validations++
					if failure == "managed" && validations == 1 || failure == "merged" && validations == 2 {
						return "near 'secret'", errors.New("near 'secret'")
					}
				}
				return run(ctx, input, command, args...)
			}
			if failure == "database" {
				if err := svc.DB.Exec("CREATE TRIGGER reject_extra_config BEFORE UPDATE ON iscsi_settings BEGIN SELECT RAISE(ABORT, 'save failed'); END").Error; err != nil {
					t.Fatal(err)
				}
			}
			extra := "# saved only after validation"
			_, err := svc.SetExtraTargetConfig(&extra)
			if err == nil || strings.Contains(err.Error(), "secret") || errors.Is(err, ErrApplyFailed) {
				t.Fatalf("precommit failure: %v", err)
			}
			if failure == "managed" && !errors.Is(err, ErrConflict) || failure == "merged" && !errors.Is(err, ErrInvalidExtraConfig) {
				t.Fatal("wrong precommit failure class")
			}
			settings, _ := svc.targetSettings()
			after, _ := os.ReadFile(svc.targetPath())
			if settings.ExtraTargetConfig != "" || string(after) != string(before) {
				t.Fatal("precommit failure changed desired or file state")
			}
		})
	}
}

func TestExtraConfigDisabledSaveAndManagedMutationDoNotStartDaemon(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && len(args) > 1 && (args[1] == "onestart" || args[1] == "onereload") {
			t.Fatal("disabled target mutation started or reloaded a service")
		}
		return run(ctx, input, command, args...)
	}
	extra := `portal-group user { listen 127.0.0.1:49222 } target iqn.extra { portal-group user }`
	result, err := svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "disabled" {
		t.Fatalf("disabled save: %v", err)
	}
	if err := svc.CreateTarget("iqn.managed", "", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	result, err = svc.GetConfig()
	if err != nil || result.ApplyStatus != "disabled" || result.ExtraTargetConfig != extra {
		t.Fatal("disabled status did not match saved state")
	}
}

func TestExtraConfigFailedStopPreservesFileAndDesiredText(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.WriteTargetConfig(true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onestop"}) {
			return "", errors.New("stop failed")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.SetEnabled(false); !errors.Is(err, ErrApplyFailed) {
		t.Fatal("failed stop was not pending")
	}
	extra := "# desired while stop is pending"
	if _, err := svc.SetExtraTargetConfig(&extra); !errors.Is(err, ErrApplyFailed) {
		t.Fatal("save did not retain failed-stop state")
	}
	after, _ := os.ReadFile(svc.targetPath())
	settings, _ := svc.targetSettings()
	if string(after) != string(before) || settings.ExtraTargetConfig != extra {
		t.Fatal("failed stop changed file or lost desired text")
	}
	result, err := svc.GetConfig()
	if err != nil || result.ApplyStatus != "pending" {
		t.Fatal("running daemon was reported as disabled")
	}
	svc.runtime.run = run
	result, err = svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "disabled" {
		t.Fatal("same-value disabled retry failed")
	}
}

func TestExtraConfigLaterManagedCandidatesCannotCollide(t *testing.T) {
	svc := newTargetTestService(t)
	extra := `portal-group reserved { listen 127.0.0.1:49222 } target iqn.user { portal-group reserved }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateTarget("IQN.USER", "", "None", "", "", "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("managed create collided with a user target")
	}
	if err := svc.CreateTarget("iqn.managed", "", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var target iscsiModels.ISCSITarget
	svc.DB.First(&target)
	before, _ := os.ReadFile(svc.targetPath())
	if err := svc.AddPortal(target.ID, "127.0.0.1", 49222); !errors.Is(err, ErrConflict) {
		t.Fatal("managed portal collided with a user reservation")
	}
	if err := svc.UpdateTarget(target.ID, "iqn.user", "", "None", "", "", "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("managed rename collided with a user target")
	}
	after, _ := os.ReadFile(svc.targetPath())
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITargetPortal{}).Count(&count)
	if count != 0 || string(after) != string(before) {
		t.Fatal("rejected candidate changed DB or file")
	}
}

func TestExtraConfigStoredInvalidStatusAndStartupFallback(t *testing.T) {
	svc := newTargetTestService(t)
	valid := `portal-group user {} target iqn.user { portal-group user }`
	if _, err := svc.SetExtraTargetConfig(&valid); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	if err := svc.DB.Model(&iscsiModels.ISCSISettings{}).Where("id = ?", 1).Update("extra_target_config", "listen secret").Error; err != nil {
		t.Fatal(err)
	}
	result, err := svc.GetConfig()
	if err != nil || result.ApplyStatus != "invalid" || result.ExtraTargetConfig != "listen secret" {
		t.Fatal("invalid text was not available for repair")
	}
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_using_owned_file" {
		t.Fatalf("fallback: %v", err)
	}
	after, _ := os.ReadFile(svc.targetPath())
	if string(after) != string(before) {
		t.Fatal("invalid desired config replaced the last valid owned file")
	}
	if err := os.Remove(svc.targetPath()); err != nil {
		t.Fatal(err)
	}
	if err := svc.StartTargets(); !errors.Is(err, ErrApplyFailed) || err.Error() != "target_startup_no_valid_owned_file" {
		t.Fatal("startup used an absent file")
	}
}

func TestExtraConfigRuntimeVerifiesNamedLUNsNamespacesAndProperties(t *testing.T) {
	svc := newTargetTestService(t)
	svc.backingStat = os.Stat
	backing, other := t.TempDir()+"/backing", t.TempDir()+"/other"
	for _, path := range []string{backing, other} {
		if err := os.WriteFile(path, make([]byte, 65536), 0600); err != nil {
			t.Fatal(err)
		}
	}
	extra := `lun shared { backend ramdisk size 64K serial fixture device-id device-id-value option vendor SYLVE }
lun backing { path ` + nativeQuote(backing) + ` }
portal-group empty {} target iqn.unbound { portal-group empty lun 0 shared }
transport-group nvme { listen tcp 127.0.0.1:49222 option note native } controller nqn.test { transport-group nvme namespace 1 shared namespace 2 backing }`
	result, err := svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" {
		t.Fatal(err)
	}
	text, _ := svc.GenerateTargetConfig()
	state, err := svc.prepareTargetConfig(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	fixture := fakeTargetRuntime{service: svc, live: state}
	for _, test := range []struct {
		name   string
		mutate func(*ctlPorts, *ctlLUNs)
	}{
		{"size", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocks-- }},
		{"backend", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Backend = "block" }},
		{"blocksize", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Blocksize = 4096 }},
		{"serial", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Serial = "old" }},
		{"device ID", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].DeviceID = "old" }},
		{"device type", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].DeviceType++ }},
		{"LUN option", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[0].Options[0].Value = "old" }},
		{"backing path", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[1].File = other }},
		{"backing device", func(p *ctlPorts, l *ctlLUNs) { l.LUNs[1].File, l.LUNs[1].Device = "", other }},
		{"namespace number", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].LUNs[0].Number = 0 }},
		{"namespace mapping", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].LUNs[0].ID++ }},
		{"missing namespace", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].LUNs = p.Ports[0].LUNs[:1] }},
		{"controller name", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].NQN = "old" }},
		{"transport group", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].TransportGroup = "old" }},
		{"port ID", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].PortID = 0 }},
		{"offline port", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].Online = "NO" }},
		{"port option", func(p *ctlPorts, l *ctlLUNs) { p.Ports[0].Options[0].Value = "old" }},
		{"missing LUN", func(p *ctlPorts, l *ctlLUNs) { l.LUNs = l.LUNs[:1] }},
		{"missing port", func(p *ctlPorts, l *ctlLUNs) { p.Ports = nil }},
		{"obsolete LUN", func(p *ctlPorts, l *ctlLUNs) { l.LUNs = append(l.LUNs, ctlLUN{ID: 91, Name: "obsolete"}) }},
		{"obsolete port", func(p *ctlPorts, l *ctlLUNs) {
			p.Ports = append(p.Ports, ctlPort{ID: 91, TransportGroup: "old", NQN: "nqn.old"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports, luns := fixture.inventory()
			if err := svc.matchCTLState(state, ports, luns); err != nil {
				t.Fatalf("fixture did not match before mutation: %v", err)
			}
			test.mutate(ports, luns)
			if err := svc.matchCTLState(state, ports, luns); err == nil {
				t.Fatal("partial extra runtime passed")
			}
		})
	}
	for _, test := range []struct {
		name   string
		nqn    string
		subnqn string
		valid  bool
	}{
		{name: "nqn", nqn: "nqn.test", valid: true},
		{name: "subnqn", subnqn: "nqn.test", valid: true},
		{name: "matching identities", nqn: "nqn.test", subnqn: "nqn.test", valid: true},
		{name: "conflicting subnqn", nqn: "nqn.test", subnqn: "nqn.other"},
		{name: "conflicting nqn", nqn: "nqn.other", subnqn: "nqn.test"},
		{name: "missing identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports, luns := fixture.inventory()
			ports.Ports[0].NQN, ports.Ports[0].SubNQN = test.nqn, test.subnqn
			data, err := xml.Marshal(ports)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ctlPorts
			if err := xml.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if err := svc.matchCTLState(state, &decoded, luns); (err == nil) != test.valid {
				t.Fatalf("controller XML identity check failed: %v", err)
			}
		})
	}
	ports, luns := fixture.inventory()
	cleared := &targetConfiguration{groups: make(map[string]targetGroup)}
	if svc.matchCTLState(cleared, &ctlPorts{}, luns) == nil || svc.matchCTLState(cleared, ports, &ctlLUNs{}) == nil {
		t.Fatal("removed extra objects still passed")
	}
	if err := svc.matchCTLState(cleared, &ctlPorts{}, &ctlLUNs{}); err != nil {
		t.Fatal("empty inventory failed after clear")
	}
	if err := os.Rename(other, backing); err != nil {
		t.Fatal(err)
	}
	if svc.matchCTLState(state, ports, luns) == nil {
		t.Fatal("replaced extra backing passed with unchanged path text")
	}
}

func TestExtraConfigFailedStartRequiresOperatorReview(t *testing.T) {
	for _, test := range []struct {
		name  string
		state *targetConfiguration
	}{
		{name: "named LUN", state: &targetConfiguration{luns: []targetLUN{{name: "user", line: 1}}}},
		{name: "target", state: &targetConfiguration{targets: []targetDefinition{{name: "iqn.user", line: 1}}}},
		{name: "controller", state: &targetConfiguration{targets: []targetDefinition{{name: "nqn.user", controller: true, line: 1}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newTargetTestService(t)
			svc.runtime.run = func(context.Context, string, string, ...string) (string, error) {
				t.Fatal("user-resource recovery inspected or changed native state")
				return "", nil
			}
			err := svc.rememberFailedTargetStart(t.Context(), test.state, nil)
			if err == nil || err.Error() != "target_start_extra_config_requires_operator_review" {
				t.Fatalf("failed start did not require operator review: %v", err)
			}
			if _, err := os.Stat(svc.targetRecoveryPath()); !os.IsNotExist(err) {
				t.Fatal("user-resource recovery created an automatic cleanup record")
			}
		})
	}
}

func TestExtraConfigRegularFileBackingAndSingleton(t *testing.T) {
	svc := newTargetTestService(t)
	svc.backingStat = os.Stat
	path := t.TempDir() + "/backing"
	if err := os.WriteFile(path, make([]byte, 65536), 0600); err != nil {
		t.Fatal(err)
	}
	extra := `lun standalone { path "` + path + `" }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&iscsiModels.ISCSISettings{ID: 2}).Error; err == nil {
		t.Fatal("second settings row was allowed")
	}
	for range 3 {
		if _, err := svc.GetConfig(); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSISettings{}).Count(&count)
	if count != 1 {
		t.Fatal("defensive reads created multiple rows")
	}
}

func TestExtraConfigTimeoutDoesNotSave(t *testing.T) {
	svc := newTargetTestService(t)
	svc.runtime.deadline = time.Millisecond
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" {
			<-ctx.Done()
			return "secret", ctx.Err()
		}
		return run(ctx, input, command, args...)
	}
	extra := "# candidate"
	if _, err := svc.SetExtraTargetConfig(&extra); !errors.Is(err, ErrConflict) {
		t.Fatal("validation timeout saved candidate")
	}
	var settings iscsiModels.ISCSISettings
	if err := svc.DB.First(&settings).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("timeout committed settings")
	}
	var basic models.BasicSettings
	if err := svc.DB.First(&basic).Error; err != nil || len(basic.Services) != 1 {
		t.Fatal("timeout changed desired service state")
	}
}

func TestExtraConfigStoredNativeFailureBlocksManagedPrecommit(t *testing.T) {
	svc := newTargetTestService(t)
	extra := `portal-group user {} target iqn.user { portal-group user auth-type invalid-native-value }`
	if err := svc.DB.Create(&iscsiModels.ISCSISettings{ID: 1, ExtraTargetConfig: extra}).Error; err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" && strings.Contains(input, "invalid-native-value") {
			return "credential-near-token", errors.New("credential-near-token")
		}
		return run(ctx, input, command, args...)
	}
	if err := svc.CreateTarget("iqn.managed", "", "None", "", "", "", ""); !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), "credential") {
		t.Fatal("native-invalid stored text bypassed managed precommit")
	}
	var count int64
	svc.DB.Model(&iscsiModels.ISCSITarget{}).Count(&count)
	if count != 0 {
		t.Fatal("managed target was committed")
	}
}

func TestExtraConfigManagedPrecommitValidatesTheActualCandidate(t *testing.T) {
	for _, operation := range []string{"create", "rename", "portal"} {
		t.Run(operation, func(t *testing.T) {
			svc := newTargetTestService(t)
			if err := svc.CreateTarget("iqn.existing", "", "None", "", "", "", ""); err != nil {
				t.Fatal(err)
			}
			extra := "# retained extra text"
			if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(svc.targetPath())
			targets, err := svc.GetTargets()
			if err != nil || len(targets) != 1 {
				t.Fatal("cannot inspect test target")
			}
			needle := "iqn.candidate"
			if operation == "portal" {
				needle = "127.0.0.1:49222"
			}
			run := svc.runtime.run
			gates := 0
			svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
				if command == "/usr/sbin/ctld" && strings.Contains(input, needle) {
					if !strings.Contains(input, extraConfigBanner) {
						t.Fatal("native candidate gate omitted stored extra text")
					}
					gates++
					return "credential-near-token", errors.New("credential-near-token")
				}
				return run(ctx, input, command, args...)
			}
			switch operation {
			case "create":
				err = svc.CreateTarget("iqn.candidate", "", "None", "", "", "", "")
			case "rename":
				err = svc.UpdateTarget(targets[0].ID, "iqn.candidate", "", "None", "", "", "", "")
			case "portal":
				err = svc.AddPortal(targets[0].ID, "127.0.0.1", 49222)
			}
			if !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), "credential") || gates != 1 {
				t.Fatal("candidate was not safely rejected before commit")
			}
			afterTargets, _ := svc.GetTargets()
			after, _ := os.ReadFile(svc.targetPath())
			if !reflect.DeepEqual(targets, afterTargets) || string(before) != string(after) {
				t.Fatal("rejected native candidate changed DB or file")
			}
		})
	}
}

func TestExtraConfigManagedRenameCannotClaimANamedLUN(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.CreateTarget("iqn.existing", "", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var target iscsiModels.ISCSITarget
	svc.DB.First(&target)
	if err := svc.AddLUN(target.ID, 0, "tank/test"); err != nil {
		t.Fatal(err)
	}
	extra := `lun "iqn.candidate,lun,0" { backend ramdisk size 64K }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	if err := svc.UpdateTarget(target.ID, "iqn.candidate", "", "None", "", "", "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("rename claimed a user LUN identity")
	}
	svc.DB.First(&target)
	after, _ := os.ReadFile(svc.targetPath())
	if target.TargetName != "iqn.existing" || string(after) != string(before) {
		t.Fatal("conflicting rename changed DB or file")
	}
}

func TestExtraConfigOtherManagedMutationKeepsDesiredStateAfterInvalidStoredText(t *testing.T) {
	svc := newTargetTestService(t)
	if err := svc.CreateTarget("iqn.existing", "", "None", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var target iscsiModels.ISCSITarget
	svc.DB.First(&target)
	settings, _ := svc.targetSettings()
	invalid := "pidfile credential-near-token"
	if err := svc.DB.Model(settings).Update("extra_target_config", invalid).Error; err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(svc.targetPath())
	if err := svc.UpdateTarget(target.ID, target.TargetName, "saved alias", "None", "", "", "", ""); !errors.Is(err, ErrApplyFailed) || strings.Contains(err.Error(), "credential") {
		t.Fatal("saved edit did not return safe apply-pending")
	}
	svc.DB.First(&target)
	after, _ := os.ReadFile(svc.targetPath())
	result, err := svc.GetConfig()
	if err != nil || result.ApplyStatus != "invalid" || result.ExtraTargetConfig != invalid || target.Alias != "saved alias" || string(before) != string(after) {
		t.Fatal("invalid stored block lost desired edit, replaced file, or hid saved text")
	}
}

func TestExtraConfigCredentialsStayOutOfApplicationAndDatabaseLogs(t *testing.T) {
	svc := newTargetTestService(t)
	var logs bytes.Buffer
	previous := logger.L
	logger.L = zerolog.New(&logs)
	t.Cleanup(func() { logger.L = previous })
	svc.DB = svc.DB.Session(&gorm.Session{Logger: gormlogger.New(log.New(&logs, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})})
	extra := `auth-group user { chap credential-user credential-near-token }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetConfig(); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"credential-user", "credential-near-token", "auth-group user"} {
		if strings.Contains(logs.String(), marker) {
			t.Fatal("saved extra text entered an application log")
		}
	}
	if err := svc.DB.Exec("CREATE TRIGGER reject_extra_config BEFORE UPDATE ON iscsi_settings BEGIN SELECT RAISE(ABORT, 'native-near-token'); END").Error; err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	extra += "\n# candidate"
	if _, err := svc.SetExtraTargetConfig(&extra); err == nil || strings.Contains(err.Error(), "native-near-token") {
		t.Fatal("database failure was not safely returned")
	}
	run := svc.runtime.run
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/ctld" {
			return "native-near-token", errors.New("native-near-token")
		}
		return run(ctx, input, command, args...)
	}
	if _, err := svc.SetExtraTargetConfig(&extra); err == nil || strings.Contains(err.Error(), "native-near-token") {
		t.Fatal("native failure was not safely returned")
	}
	for _, marker := range []string{"credential-user", "credential-near-token", "native-near-token", "auth-group user"} {
		if strings.Contains(logs.String(), marker) {
			t.Fatal("extra text or native output entered an application log")
		}
	}
}

func TestExtraConfigObservableOptionsNeedUnambiguousInventory(t *testing.T) {
	wanted := map[string]string{"vendor": "SYLVE"}
	property := ctlProperty{XMLName: xml.Name{Local: "vendor"}, Value: "SYLVE"}
	if !matchCTLOptions(wanted, []ctlProperty{property}) || matchCTLOptions(wanted, nil) || matchCTLOptions(wanted, []ctlProperty{property, property}) {
		t.Fatal("missing or duplicate observable property was accepted")
	}
	property.XMLName.Space = "foreign"
	if matchCTLOptions(wanted, []ctlProperty{property}) {
		t.Fatal("namespaced property was accepted as native inventory")
	}
}

func TestExtraConfigDelayedCredentialOnlyReloadIsOnlyChecked(t *testing.T) {
	svc := newTargetTestService(t)
	extra := `auth-group user { chap user secretpassw0rd } portal-group empty {} target iqn.user { portal-group empty user }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	reloads := 0
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			reloads++
			return "", nil
		}
		return run(ctx, input, command, args...)
	}
	extra = strings.Replace(extra, "secretpassw0rd", "newsecretpass", 1)
	result, err := svc.SetExtraTargetConfig(&extra)
	if err != nil || result.ApplyStatus != "checked" || reloads != 1 {
		t.Fatal("unchanged observable topology was described as a reload acknowledgement")
	}
}

func TestExtraConfigFailedResizeRemainsSavedAndPending(t *testing.T) {
	svc := newTargetTestService(t)
	extra := `portal-group user { listen 127.0.0.1:49222 } target iqn.user { portal-group user lun 0 { backend ramdisk size 64K } }`
	if _, err := svc.SetExtraTargetConfig(&extra); err != nil {
		t.Fatal(err)
	}
	run := svc.runtime.run
	svc.runtime.deadline = 300 * time.Millisecond
	svc.runtime.run = func(ctx context.Context, input, command string, args ...string) (string, error) {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			return "", nil
		}
		return run(ctx, input, command, args...)
	}
	extra = strings.Replace(extra, "size 64K", "size 128K", 1)
	result, err := svc.SetExtraTargetConfig(&extra)
	if !errors.Is(err, ErrApplyFailed) || result.ApplyStatus != "pending" {
		t.Fatal("failed resize passed identity-only checks")
	}
	settings, _ := svc.targetSettings()
	if settings.ExtraTargetConfig != extra {
		t.Fatal("failed resize lost the saved desired size")
	}
	result, err = svc.GetConfig()
	if err != nil || result.ApplyStatus != "pending" {
		t.Fatal("runtime mismatch was lost on status recomputation")
	}
}
