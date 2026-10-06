// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

type fakeFirewallApplyRuntime struct {
	mu            sync.Mutex
	enabled       bool
	tables        map[string]string
	commands      []string
	loads         []string
	validationErr error
	previousErr   error
	loadErr       error
	enableErr     error
	rollbackErr   error
	enablePartial bool
}

var testFirewallTableFilePattern = regexp.MustCompile(`(?m)^table <([^>]+)> persist file "([^"]+)"`)

func (runtime *fakeFirewallApplyRuntime) run(command string, args ...string) (string, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.commands = append(runtime.commands, command+" "+strings.Join(args, " "))
	if command == "/sbin/kldstat" && reflect.DeepEqual(args, []string{"-m", "pf"}) {
		return "pf loaded", nil
	}
	if command != "/sbin/pfctl" || len(args) == 0 {
		return "", fmt.Errorf("unexpected command: %s %v", command, args)
	}
	switch args[0] {
	case "-si":
		if runtime.enabled {
			return "Status: Enabled for 00:10:00", nil
		}
		return "Status: Disabled", nil
	case "-s":
		if !reflect.DeepEqual(args, []string{"-s", "Tables"}) {
			break
		}
		names := []string{}
		for name := range runtime.tables {
			names = append(names, name)
		}
		sort.Strings(names)
		return strings.Join(names, "\n"), nil
	case "-t":
		if len(args) == 4 && args[2] == "-T" && args[3] == "show" {
			return runtime.tables[args[1]], nil
		}
		if len(args) == 4 && args[2] == "-T" && args[3] == "kill" {
			delete(runtime.tables, args[1])
			return "", nil
		}
	case "-nf", "-f":
		if len(args) != 2 {
			break
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return "", err
		}
		text := string(data)
		isCandidate := strings.Contains(text, `anchor "traffic-rules" {`) && !strings.Contains(text, "# previous managed policy")
		if args[0] == "-nf" {
			if isCandidate && runtime.validationErr != nil {
				return "", runtime.validationErr
			}
			if !isCandidate && runtime.previousErr != nil {
				return "", runtime.previousErr
			}
			return "", nil
		}
		runtime.loads = append(runtime.loads, text)
		if !isCandidate && runtime.rollbackErr != nil {
			return "", runtime.rollbackErr
		}
		for _, match := range testFirewallTableFilePattern.FindAllStringSubmatch(text, -1) {
			values, err := os.ReadFile(match[2])
			if err != nil {
				return "", err
			}
			runtime.tables[match[1]] = string(values)
		}
		if isCandidate {
			return "", runtime.loadErr
		}
		return "", nil
	case "-e":
		if runtime.enableErr != nil {
			if runtime.enablePartial {
				runtime.enabled = true
			}
			return "", runtime.enableErr
		}
		runtime.enabled = true
		return "", nil
	case "-d":
		runtime.enabled = false
		return "", nil
	case "-a":
		if len(args) == 3 && (args[2] == "-vvsr" || args[2] == "-vvsn") {
			return "", nil
		}
	}
	return "", fmt.Errorf("unexpected PF command: %v", args)
}

func setupFirewallApplyTest(t *testing.T, enabled bool) (*Service, *fakeFirewallApplyRuntime, []firewallFileState) {
	t.Helper()
	previousPaths := []string{pfMainConfPath, pfBackupConfPath, pfSylveDir, pfObjectTablesPath, pfObjectTableEntriesDir, pfNatRulesPath, pfTrafficRulesPath, firewallRCConfPath}
	previousRun, previousWrite := firewallRunCommand, firewallWriteFile
	root := t.TempDir()
	pfMainConfPath = filepath.Join(root, "pf.conf")
	pfBackupConfPath = filepath.Join(root, "pf.conf.pre-sylve")
	pfSylveDir = filepath.Join(root, "pf.sylve")
	pfObjectTablesPath = filepath.Join(pfSylveDir, "object-tables.conf")
	pfObjectTableEntriesDir = filepath.Join(pfSylveDir, "entries")
	pfNatRulesPath = filepath.Join(pfSylveDir, "nat-rules.conf")
	pfTrafficRulesPath = filepath.Join(pfSylveDir, "traffic-rules.conf")
	firewallRCConfPath = filepath.Join(root, "rc.conf")
	t.Cleanup(func() {
		pfMainConfPath, pfBackupConfPath, pfSylveDir, pfObjectTablesPath = previousPaths[0], previousPaths[1], previousPaths[2], previousPaths[3]
		pfObjectTableEntriesDir, pfNatRulesPath, pfTrafficRulesPath, firewallRCConfPath = previousPaths[4], previousPaths[5], previousPaths[6], previousPaths[7]
		firewallRunCommand, firewallWriteFile = previousRun, previousWrite
	})
	runtime := &fakeFirewallApplyRuntime{enabled: enabled, tables: map[string]string{
		"sylve_obj_41_inet": "10.9.9.9\n", "sshguard": "8.8.8.8\n", "friends": "10.32.51.0/24\n",
	}}
	firewallRunCommand = runtime.run
	firewallWriteFile = atomicWriteFile
	oldFiles := map[string]string{
		pfMainConfPath:     "# independently loaded policy\ntable <sylve_obj_41_inet> persist\ntable <sshguard> persist\nblock in quick all\n",
		firewallRCConfPath: "hostname=\"edge1\"\npf_enable=\"YES\"\n",
		pfObjectTablesPath: "# old table declarations\n", pfNatRulesPath: "# old NAT\n", pfTrafficRulesPath: "# old traffic\n",
		filepath.Join(pfObjectTableEntriesDir, "sylve_obj_41_inet"): "10.0.0.7\n",
		filepath.Join(pfObjectTableEntriesDir, "operator-notes"):    "leave unmanaged files alone\n",
	}
	files := []firewallFileState{}
	for path, text := range oldFiles {
		if err := atomicWriteFile(path, []byte(text), 0640); err != nil {
			t.Fatal(err)
		}
		file, err := captureFirewallFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.FirewallAdvancedSettings{},
		&networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{}, &networkModels.Object{}, &networkModels.ObjectEntry{}, &networkModels.ObjectResolution{})
	if err := db.Create(&models.BasicSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&networkModels.FirewallAdvancedSettings{}).Error; err != nil {
		t.Fatal(err)
	}
	obj := networkModels.Object{ID: 41, Type: "Network", Entries: []networkModels.ObjectEntry{{Value: "10.32.50.0/24"}}}
	if err := db.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	rule := networkModels.FirewallTrafficRule{Name: "LAN", Visible: true, Enabled: true, Action: "pass", Direction: "in",
		Protocol: "any", Family: "inet", SourceObjID: &obj.ID}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	return svc, runtime, files
}

func assertFirewallApplyFilesRestored(t *testing.T, files []firewallFileState) {
	t.Helper()
	for _, previous := range files {
		current, err := captureFirewallFile(previous.path)
		if err != nil || current.exists != previous.exists || string(current.content) != string(previous.content) || current.mode != previous.mode {
			t.Errorf("file not restored: %s content=%q mode=%v err=%v", previous.path, current.content, current.mode, err)
		}
	}
}

func TestFirewallApplySamplesBeforeReloadAndRefreshesRuleNumbersAfterward(t *testing.T) {
	svc, runtime, _ := setupFirewallApplyTest(t, true)
	svc.SetFirewallServiceEnabledForTelemetry(true)
	var rule networkModels.FirewallTrafficRule
	if err := svc.DB.First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	firewallRunCommand = func(command string, args ...string) (string, error) {
		output, err := runtime.run(command, args...)
		if err != nil || command != "/sbin/pfctl" || len(args) != 3 || args[1] != "sylve/traffic-rules" {
			return output, err
		}
		runtime.mu.Lock()
		reloaded := len(runtime.loads) > 0
		runtime.mu.Unlock()
		if reloaded {
			return fmt.Sprintf("@0 pass in from any to any label \"sylve_trf_%d\"\n  [ Evaluations: 1 Packets: 2 Bytes: 20 States: 0 ]", rule.ID), nil
		}
		return fmt.Sprintf("@9 pass in from any to any label \"sylve_trf_%d\"\n  [ Evaluations: 1 Packets: 10 Bytes: 100 States: 0 ]", rule.ID), nil
	}

	if err := svc.ApplyFirewallConfig(); err != nil {
		t.Fatal(err)
	}
	rt := svc.getFirewallTelemetryRuntime()
	key := firewallCounterKey{RuleType: "traffic", RuleID: rule.ID}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if total := rt.totals[key]; total.Packets != 12 || total.Bytes != 120 {
		t.Fatalf("reload lost cumulative counters: %+v", total)
	}
	if rt.trafficRuleNumbers[0] != rule.ID || len(rt.trafficRuleNumbers) != 1 {
		t.Fatalf("rule number mapping was not refreshed after reload: %v", rt.trafficRuleNumbers)
	}
}

func TestFirewallApplySyntaxFailureLeavesRunningPFAndBootSettingsUntouched(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	runtime.validationErr = errors.New("candidate has a syntax error")
	var validationErr *pfValidationError
	if err := svc.ApplyFirewallConfig(); !errors.As(err, &validationErr) || !strings.Contains(err.Error(), runtime.validationErr.Error()) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if len(runtime.loads) != 0 || !runtime.enabled || len(runtime.commands) != 2 {
		t.Fatalf("PF touched on preflight failure: %+v", runtime)
	}
}

func TestFirewallApplyLoadFailureRestoresActualStateFilesAndLiveTables(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("initially-enabled=%t", enabled), func(t *testing.T) {
			svc, runtime, files := setupFirewallApplyTest(t, enabled)
			runtime.loadErr = errors.New("candidate load failed")
			err := svc.ApplyFirewallConfig()
			if !errors.Is(err, runtime.loadErr) || strings.Contains(err.Error(), "rollback failed") {
				t.Fatalf("unexpected apply failure: %v", err)
			}
			assertFirewallApplyFilesRestored(t, files)
			if runtime.enabled != enabled || len(runtime.loads) != 2 || runtime.tables["sylve_obj_41_inet"] != "10.9.9.9\n" {
				t.Fatalf("previous live state/tables not restored: %+v", runtime)
			}
			if runtime.tables["sshguard"] != "8.8.8.8\n" || runtime.tables["friends"] != "10.32.51.0/24\n" {
				t.Fatalf("external tables altered: %v", runtime.tables)
			}
			for _, command := range runtime.commands {
				if strings.Contains(command, "-T replace") || strings.Contains(command, "-T flush") || strings.Contains(command, "-F ") ||
					(enabled && strings.HasSuffix(command, " -d")) {
					t.Errorf("unsafe recovery command: %s", command)
				}
			}
		})
	}
}

func TestFirewallApplyFileFailureRestoresBootSettingsWithoutReload(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	failure := errors.New("cannot install traffic rules")
	firewallWriteFile = func(path string, content []byte, mode os.FileMode) error {
		if path == pfTrafficRulesPath {
			return failure
		}
		return atomicWriteFile(path, content, mode)
	}
	if err := svc.ApplyFirewallConfig(); !errors.Is(err, failure) {
		t.Fatalf("expected file failure, got %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if len(runtime.loads) != 0 || !runtime.enabled {
		t.Fatalf("untouched running policy should not be reloaded: %+v", runtime)
	}
}

func TestFirewallApplyEnableFailureRestoresStoppedPF(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, false)
	runtime.enableErr = errors.New("cannot enable PF")
	if err := svc.ApplyFirewallConfig(); !errors.Is(err, runtime.enableErr) {
		t.Fatalf("expected enable failure, got %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if runtime.enabled || len(runtime.loads) != 2 || runtime.tables["sylve_obj_41_inet"] != "10.9.9.9\n" {
		t.Fatalf("stopped PF not restored: %+v", runtime)
	}
}

func TestFirewallApplyReportsRestorationFailure(t *testing.T) {
	svc, runtime, _ := setupFirewallApplyTest(t, true)
	runtime.loadErr, runtime.rollbackErr = errors.New("candidate load failed"), errors.New("previous policy reload failed")
	err := svc.ApplyFirewallConfig()
	if !errors.Is(err, runtime.loadErr) || !errors.Is(err, runtime.rollbackErr) || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("must report both failures, got %v", err)
	}
}

func TestFirewallApplyFailedReloadRestoresPreviousManagedPolicy(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	previousMain := buildPFMainConfig("# previous managed policy\nset block-policy return\ntable <sshguard> persist", "", "", "", "", "",
		renderFirewallObjectTables(map[uint]firewallObjectTable{41: {ObjectID: 41, InetName: "sylve_obj_41_inet", InetValues: []string{"10.0.0.7"}}}, pfObjectTableEntriesDir),
		pfNatRulesPath, pfTrafficRulesPath)
	if err := atomicWriteFile(pfMainConfPath, []byte(previousMain), 0640); err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if files[i].path == pfMainConfPath {
			var err error
			files[i], err = captureFirewallFile(pfMainConfPath)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.loadErr = errors.New("reload rejected")
	if err := svc.ApplyFirewallConfig(); !errors.Is(err, runtime.loadErr) || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("failed reload did not restore: %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if len(runtime.loads) != 2 || !strings.Contains(runtime.loads[1], "# previous managed policy") || runtime.tables["sylve_obj_41_inet"] != "10.9.9.9\n" || !runtime.enabled {
		t.Fatalf("old policy/live table state not restored: %+v", runtime)
	}
}

func TestFirewallApplyRefusesUnrestorableRunningPolicyBeforeWriting(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	runtime.previousErr = errors.New("previous policy cannot be parsed")
	if err := svc.ApplyFirewallConfig(); !errors.Is(err, runtime.previousErr) {
		t.Fatalf("unrestorable old policy accepted: %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if len(runtime.loads) != 0 || !runtime.enabled {
		t.Fatalf("unrestorable running PF was changed: %+v", runtime)
	}
}

func TestFirewallApplyFailureRemovesNewFilesAndManagedTables(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, false)
	for i := range files {
		if files[i].path == firewallRCConfPath || filepath.Base(files[i].path) == "operator-notes" {
			continue
		}
		if err := os.Remove(files[i].path); err != nil {
			t.Fatal(err)
		}
		var err error
		files[i], err = captureFirewallFile(files[i].path)
		if err != nil {
			t.Fatal(err)
		}
	}
	obj := networkModels.Object{ID: 42, Name: "new-host", Type: "Host", Entries: []networkModels.ObjectEntry{{Value: "10.0.0.42"}}}
	if err := svc.DB.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	rule := networkModels.FirewallTrafficRule{Name: "new managed table", Enabled: true, Action: "pass", Direction: "in", Protocol: "any", Family: "inet", SourceObjID: &obj.ID}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	runtime.enableErr, runtime.enablePartial = errors.New("enable partially failed"), true
	if err := svc.ApplyFirewallConfig(); !errors.Is(err, runtime.enableErr) || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("enable failure did not restore: %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if runtime.enabled || len(runtime.loads) != 2 || runtime.tables["sylve_obj_41_inet"] != "10.9.9.9\n" || runtime.tables["sshguard"] != "8.8.8.8\n" {
		t.Fatalf("previously disabled live state not restored: %+v", runtime)
	}
	if _, exists := runtime.tables["sylve_obj_42_inet"]; exists {
		t.Fatalf("candidate's new persistent table survived rollback: %+v", runtime.tables)
	}
	if _, err := os.Stat(filepath.Join(pfObjectTableEntriesDir, "sylve_obj_42_inet")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate's new entry file survived rollback: %v", err)
	}
}

func TestFirewallFQDNRefreshRetriesApplyFailureWithUnchangedAnswers(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	var settings models.BasicSettings
	if err := svc.DB.First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	settings.Services = []models.AvailableService{models.Firewall}
	if err := svc.DB.Save(&settings).Error; err != nil {
		t.Fatal(err)
	}
	obj := networkModels.Object{ID: 42, Name: "service", Type: "FQDN", Entries: []networkModels.ObjectEntry{{Value: "service.example"}}}
	if err := svc.DB.Create(&obj).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(buildResolutionRows(obj.ID, []string{"10.0.0.10"})).Error; err != nil {
		t.Fatal(err)
	}
	rule := networkModels.FirewallNATRule{Name: "service redirect", Enabled: true, NATType: "dnat", Family: "inet", Protocol: "tcp", IngressInterfaces: []string{"em0"}, DNATTargetObjID: &obj.ID}
	if err := svc.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	previousResolver := refreshNetworkObjectFQDN
	t.Cleanup(func() { refreshNetworkObjectFQDN = previousResolver })
	refreshNetworkObjectFQDN = func(context.Context, string) ([]string, error) { return []string{"10.0.0.11"}, nil }
	runtime.loadErr = errors.New("temporary PF reload failure")
	if err := svc.RefreshObjectByID(obj.ID); !errors.Is(err, runtime.loadErr) {
		t.Fatalf("expected reported apply failure: %v", err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if err := svc.DB.First(&obj, obj.ID).Error; err != nil || !strings.HasPrefix(obj.LastRefreshError, "firewall_apply_failed:") {
		t.Fatalf("apply failure not visible on FQDN object: %+v err=%v", obj, err)
	}
	if len(runtime.loads) != 2 || !runtime.enabled {
		t.Fatalf("last-good live policy was not restored: %+v", runtime)
	}
	runtime.loadErr = nil
	if err := svc.RefreshObjectByID(obj.ID); err != nil {
		t.Fatalf("unchanged DNS answers must retry a failed firewall apply: %v", err)
	}
	if err := svc.DB.First(&obj, obj.ID).Error; err != nil || obj.LastRefreshError != "" || len(runtime.loads) != 3 {
		t.Fatalf("retry did not apply/clear error: object=%+v loads=%d err=%v", obj, len(runtime.loads), err)
	}
	text, err := os.ReadFile(pfNatRulesPath)
	if err != nil || !strings.Contains(string(text), "-> 10.0.0.11") {
		t.Fatalf("recovered NAT target was not applied: text=%s err=%v", text, err)
	}
}

func TestFirewallApplyLoadsManagedTableContentsWithCandidate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("initially-enabled=%t", enabled), func(t *testing.T) {
			svc, runtime, _ := setupFirewallApplyTest(t, enabled)
			if err := svc.ApplyFirewallConfig(); err != nil {
				t.Fatal(err)
			}
			if len(runtime.loads) != 1 || !runtime.enabled || runtime.tables["sylve_obj_41_inet"] != "10.32.50.0/24\n" {
				t.Fatalf("candidate missing live table contents: %+v", runtime)
			}
			if !strings.Contains(runtime.loads[0], fmt.Sprintf("table <sylve_obj_41_inet> persist file %q", filepath.Join(pfObjectTableEntriesDir, "sylve_obj_41_inet"))) {
				t.Fatalf("table must load in same transaction as rules: %s", runtime.loads[0])
			}
			boot, err := os.ReadFile(firewallRCConfPath)
			if err != nil || !strings.Contains(string(boot), `pf_enable="NO"`) {
				t.Fatalf("startup ownership changed: boot=%s err=%v", boot, err)
			}
		})
	}
}

func TestFirewallMutationValidationFailureDoesNotReapplyDatabasePolicy(t *testing.T) {
	svc, runtime, files := setupFirewallApplyTest(t, true)
	var settings models.BasicSettings
	if err := svc.DB.First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	settings.Services = []models.AvailableService{models.Firewall}
	if err := svc.DB.Save(&settings).Error; err != nil {
		t.Fatal(err)
	}
	runtime.validationErr = errors.New("invalid candidate")
	_, err := svc.CreateFirewallTrafficRule(&networkServiceInterfaces.UpsertFirewallTrafficRuleRequest{
		Name: "test", Kind: "advanced", RawPF: "block in from <sshguard>\n",
	})
	var validationErr *pfValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), runtime.validationErr.Error()) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	var rules []networkModels.FirewallTrafficRule
	if err := svc.DB.Find(&rules).Error; err != nil || len(rules) != 1 || rules[0].Name != "LAN" {
		t.Fatalf("database rows not restored: %+v err=%v", rules, err)
	}
	assertFirewallApplyFilesRestored(t, files)
	if len(runtime.loads) != 0 || len(runtime.commands) != 2 || !runtime.enabled {
		t.Fatalf("database rollback reapplied over independent policy: %+v", runtime)
	}
}
