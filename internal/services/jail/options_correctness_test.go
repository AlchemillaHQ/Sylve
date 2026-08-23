// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type fakeJailOptionOps struct {
	path   string
	calls  int
	failOn int
}

func (f *fakeJailOptionOps) DevFSRulesPath() string {
	return f.path
}

func (f *fakeJailOptionOps) ReloadDevFS() error {
	f.calls++
	if f.failOn == f.calls {
		return errors.New("forced_devfs_reload_failure")
	}
	return nil
}

func newJailOptionsTestService(
	t *testing.T,
	ctID uint,
	mutate func(*jailModels.Jail),
) (*Service, *jailModels.Jail, string, string) {
	t.Helper()
	requireSystemUUIDOrSkip(t)
	dataPath := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", dataPath)

	db := testutil.NewSQLiteTestDB(
		t,
		&jailModels.Jail{},
		&jailModels.Storage{},
		&jailModels.JailHooks{},
		&jailModels.JailSnapshot{},
		&jailModels.Network{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	startAtBoot := true
	record := &jailModels.Jail{
		CTID:        ctID,
		Name:        fmt.Sprintf("jail-options-%d", ctID),
		Type:        jailModels.JailTypeFreeBSD,
		StartAtBoot: &startAtBoot,
		StartOrder:  5,
	}
	if mutate != nil {
		mutate(record)
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	jailDir := filepath.Join(dataPath, "jails", strconv.FormatUint(uint64(ctID), 10))
	mountPoint := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(filepath.Join(jailDir, "scripts"), 0o755); err != nil {
		t.Fatalf("create jail directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(mountPoint, "etc"), 0o755); err != nil {
		t.Fatalf("create jail mount point: %v", err)
	}

	blocks := make([]string, 0, 2)
	if block := jailMetadataConfigBlock(record.MetadataMeta, record.MetadataEnv); block != "" {
		blocks = append(blocks, block)
	}
	if block := jailAdditionalOptionsConfigBlock(record.AdditionalOptions); block != "" {
		blocks = append(blocks, block)
	}
	extra := ""
	if len(blocks) > 0 {
		extra = strings.Join(blocks, "\n\n") + "\n"
	}
	configContent := fmt.Sprintf(
		"%s%s {\n\tpath = %s;\n%s\tpersist;\n}\n",
		JAIL_CONF_PREAMBLE,
		record.Name,
		strconv.Quote(mountPoint),
		extra,
	)
	configPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID))
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write jail config: %v", err)
	}

	service := &Service{DB: db, ctidHashByCTID: make(map[uint]string)}
	attachJailRootTestFixture(t, service, db, record.ID, ctID, mountPoint)
	return service, record, configPath, mountPoint
}

func loadJailOptionRecord(t *testing.T, db *gorm.DB, ctID uint) jailModels.Jail {
	t.Helper()
	var record jailModels.Jail
	if err := db.Preload("JailHooks").Where("ct_id = ?", ctID).First(&record).Error; err != nil {
		t.Fatalf("reload jail: %v", err)
	}
	return record
}

func TestManualFstabEditPersistsContent(t *testing.T) {
	service, _, configPath, _ := newJailOptionsTestService(t, 806, func(jail *jailModels.Jail) {
		jail.Fstab = "generated"
	})

	const manual = "tmpfs\t/custom/tmp\ttmpfs\trw\t0\t0\n"
	if err := service.ModifyFstab(806, manual); err != nil {
		t.Fatalf("ModifyFstab failed: %v", err)
	}
	refreshed := loadJailOptionRecord(t, service.DB, 806)
	if refreshed.Fstab != manual {
		t.Fatalf("manual fstab = %q", refreshed.Fstab)
	}
	fstabData, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), "fstab"))
	if err != nil {
		t.Fatalf("read manual fstab: %v", err)
	}
	if string(fstabData) != manual {
		t.Fatalf("fstab content = %q, want %q", fstabData, manual)
	}
}

func forceJailOptionUpdateFailure(t *testing.T, db *gorm.DB) {
	t.Helper()
	const callbackName = "tests:fail_jail_option_update"
	err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "jails" {
			tx.AddError(errors.New("forced_jail_option_database_failure"))
		}
	})
	if err != nil {
		t.Fatalf("register update callback: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove(callbackName)
	})
}

func TestJailBootOrderPersistsExplicitFalseAndZero(t *testing.T) {
	service, _, _, _ := newJailOptionsTestService(t, 901, nil)
	if err := service.ModifyBootOrder(901, false, 0); err != nil {
		t.Fatalf("ModifyBootOrder failed: %v", err)
	}
	record := loadJailOptionRecord(t, service.DB, 901)
	if record.StartAtBoot == nil || *record.StartAtBoot || record.StartOrder != 0 {
		t.Fatalf("explicit false/zero was not persisted: %+v", record)
	}
}

func TestJailExecutionTimeoutPersistsManagedConfig(t *testing.T) {
	service, _, configPath, mountPoint := newJailOptionsTestService(t, 910, nil)

	if err := service.ModifyExecutionTimeout(910, 300); err != nil {
		t.Fatalf("ModifyExecutionTimeout failed: %v", err)
	}
	record := loadJailOptionRecord(t, service.DB, 910)
	if record.ExecTimeout != 300 {
		t.Fatalf("execution timeout = %d, want 300", record.ExecTimeout)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(configData), "exec.timeout = 300;"); got != 1 {
		t.Fatalf("managed exec.timeout count = %d\n%s", got, configData)
	}
	metadata, err := os.ReadFile(filepath.Join(mountPoint, ".sylve", "jail.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metadata), `"execTimeout": 300`) {
		t.Fatalf("jail metadata missing execution timeout: %s", metadata)
	}
}

func TestJailExecutionTimeoutValidation(t *testing.T) {
	service, _, _, _ := newJailOptionsTestService(t, 911, nil)
	for _, value := range []int{-1, 0, jailModels.MaximumExecTimeoutSeconds + 1} {
		err := service.ModifyExecutionTimeout(911, value)
		if err == nil || !strings.Contains(err.Error(), "exec_timeout_out_of_range") {
			t.Fatalf("timeout %d: expected range error, got %v", value, err)
		}
	}
}

func TestSyncJailExecutionTimeoutAddsDefaultToExistingConfig(t *testing.T) {
	service, jail, configPath, _ := newJailOptionsTestService(t, 912, nil)
	jail.ExecTimeout = jailModels.DefaultExecTimeoutSeconds

	if err := service.syncJailExecTimeoutConfig(jail); err != nil {
		t.Fatalf("syncJailExecTimeoutConfig failed: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(configData), "exec.timeout = 120;"); got != 1 {
		t.Fatalf("managed default timeout count = %d\n%s", got, configData)
	}
}

func TestReconcileJailExecutionTimeoutAcceptsTrailingComments(t *testing.T) {
	comments := []string{
		"# legacy setting",
		"// legacy setting",
		"/* legacy setting */",
		"/* first comment */ # second comment",
	}
	for _, comment := range comments {
		t.Run(comment, func(t *testing.T) {
			config := fmt.Sprintf(
				"jail {\n\texec.timeout = 30; %s\n\tpersist;\n}\n",
				comment,
			)
			next, err := reconcileJailExecTimeoutConfig(config, jailModels.DefaultExecTimeoutSeconds)
			if err != nil {
				t.Fatalf("reconcileJailExecTimeoutConfig failed: %v", err)
			}
			if got := strings.Count(next, "exec.timeout = 120;"); got != 1 {
				t.Fatalf("managed timeout count = %d\n%s", got, next)
			}
			if strings.Contains(next, comment) {
				t.Fatalf("legacy commented timeout was not replaced:\n%s", next)
			}
		})
	}
}

func TestJailOptionDatabaseFailureIsReturnedAndFilesAreRestored(t *testing.T) {
	service, _, configPath, _ := newJailOptionsTestService(t, 902, func(record *jailModels.Jail) {
		record.MetadataMeta = "before"
		record.MetadataEnv = "before-env"
	})
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	forceJailOptionUpdateFailure(t, service.DB)

	err = service.ModifyMetadata(902, "after", "after-env")
	if err == nil || !strings.Contains(err.Error(), "forced_jail_option_database_failure") {
		t.Fatalf("expected database failure, got %v", err)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("config was not restored after database failure\nbefore=%q\nafter=%q", before, after)
	}
	record := loadJailOptionRecord(t, service.DB, 902)
	if record.MetadataMeta != "before" || record.MetadataEnv != "before-env" {
		t.Fatalf("database changed after failed update: %+v", record)
	}
}

func TestJailResolvConfEmptyValueRemovesManagedFile(t *testing.T) {
	service, _, _, mountPoint := newJailOptionsTestService(t, 903, func(record *jailModels.Jail) {
		record.ResolvConf = "nameserver 192.0.2.1\n"
	})
	resolvPath := filepath.Join(mountPoint, "etc", "resolv.conf")
	if err := os.WriteFile(resolvPath, []byte("nameserver 192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := service.ModifyResolvConf(903, ""); err != nil {
		t.Fatalf("ModifyResolvConf failed: %v", err)
	}
	if _, err := os.Stat(resolvPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolv.conf still exists or stat failed: %v", err)
	}
	if record := loadJailOptionRecord(t, service.DB, 903); record.ResolvConf != "" {
		t.Fatalf("resolv.conf database value was not cleared: %q", record.ResolvConf)
	}
}

func TestJailMetadataIsEscapedAndAdditionalOptionsArePreserved(t *testing.T) {
	const additional = "\texec.clean;\n\tallow.raw_sockets;"
	service, _, configPath, _ := newJailOptionsTestService(t, 904, func(record *jailModels.Jail) {
		record.AdditionalOptions = additional
	})
	meta := "value\";\nexec.stop = \"injected"
	env := "PATH=C:\\private"
	if err := service.ModifyMetadata(904, meta, env); err != nil {
		t.Fatalf("ModifyMetadata failed: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		"\tmeta = " + strconv.Quote(meta) + ";",
		"\tenv = " + strconv.Quote(env) + ";",
		jailAdditionalOptionsConfigBlock(additional),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("config is missing %q:\n%s", expected, text)
		}
	}
	record := loadJailOptionRecord(t, service.DB, 904)
	if record.MetadataMeta != meta || record.MetadataEnv != env || record.AdditionalOptions != additional {
		t.Fatalf("metadata/additional options were not persisted exactly: %+v", record)
	}
}

func TestJailAllowedOptionsAreNormalizedAndSerialized(t *testing.T) {
	service, _, configPath, _ := newJailOptionsTestService(t, 905, nil)
	want := []string{"allow.mount", "allow.raw_sockets"}
	if err := service.ModifyAllowedOptions(
		905,
		[]string{" allow.mount ", "allow.mount", "allow.raw_sockets"},
	); err != nil {
		t.Fatalf("ModifyAllowedOptions failed: %v", err)
	}
	record := loadJailOptionRecord(t, service.DB, 905)
	if !reflect.DeepEqual(record.AllowedOptions, want) {
		t.Fatalf("allowed options=%#v want=%#v", record.AllowedOptions, want)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "allow.mount;") != 1 ||
		strings.Count(string(content), "allow.raw_sockets;") != 1 {
		t.Fatalf("normalized options were not reconciled once:\n%s", content)
	}
}

func disabledJailLifecycleHooks() jailServiceInterfaces.Hooks {
	return jailServiceInterfaces.Hooks{}
}

func TestJailLifecycleHooksPreserveOtherManagedSections(t *testing.T) {
	service, record, configPath, mountPoint := newJailOptionsTestService(t, 906, nil)
	postStartPath := filepath.Join(filepath.Dir(configPath), "scripts", "post-start.sh")
	existing := `#!/bin/sh

### Start Sylve-Managed Hardware ###
cpuset -l 0 -j jail
### End Sylve-Managed Hardware ###

### Start Sylve-Managed Network ###
ifconfig epair0a up
### End Sylve-Managed Network ###

### Start User-Managed Hook ###
echo old-user-hook
### End User-Managed Hook ###
`
	if err := os.WriteFile(postStartPath, []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}

	hooks := disabledJailLifecycleHooks()
	hooks.Poststart = jailServiceInterfaces.HookPhase{Enabled: true, Script: "echo new-user-hook"}
	if err := service.ModifyLifecycleHooks(906, hooks); err != nil {
		t.Fatalf("ModifyLifecycleHooks failed: %v", err)
	}
	updated, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"cpuset -l 0 -j jail",
		"ifconfig epair0a up",
		"echo new-user-hook",
	} {
		if !strings.Contains(string(updated), expected) {
			t.Fatalf("post-start hook lost %q:\n%s", expected, updated)
		}
	}
	if strings.Contains(string(updated), "echo old-user-hook") {
		t.Fatalf("old user hook was not replaced:\n%s", updated)
	}
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configContent), "exec.poststart") {
		t.Fatalf("post-start hook was not wired into jail.conf:\n%s", configContent)
	}
	for _, inJailScript := range []string{"start.sh", "stop.sh"} {
		path := filepath.Join(mountPoint, "usr", "local", "sylve", "scripts", inJailScript)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing in-jail script copy %s: %v", path, err)
		}
	}

	persisted := loadJailOptionRecord(t, service.DB, 906)
	if len(persisted.JailHooks) != 6 {
		t.Fatalf("hook row count=%d want=6", len(persisted.JailHooks))
	}
	if persisted.ID != record.ID {
		t.Fatalf("jail identity changed: got=%d want=%d", persisted.ID, record.ID)
	}

	if err := service.ModifyLifecycleHooks(906, disabledJailLifecycleHooks()); err != nil {
		t.Fatalf("disable lifecycle hooks: %v", err)
	}
	disabled, err := os.ReadFile(postStartPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disabled), jailUserHookStart) ||
		!strings.Contains(string(disabled), "cpuset -l 0 -j jail") ||
		!strings.Contains(string(disabled), "ifconfig epair0a up") {
		t.Fatalf("disabling user hook changed other managed sections:\n%s", disabled)
	}
}

func TestJailDevFSRuleRemovalReloadsAndPreservesUnrelatedRules(t *testing.T) {
	if config.IsDevFSDisabled() {
		t.Skip("DevFS management is disabled in this environment")
	}
	service, _, configPath, _ := newJailOptionsTestService(t, 907, func(record *jailModels.Jail) {
		record.AllowedOptions = []string{"allow.mount.devfs"}
		record.DevFSRuleset = "add path 'bpf*' unhide"
	})
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	withRuleset, err := reconcileJailAllowedOptionsConfig(string(configContent), 907, "custom", []string{"allow.mount.devfs"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(withRuleset), 0o644); err != nil {
		t.Fatal(err)
	}
	devFSPath := filepath.Join(t.TempDir(), "devfs.rules")
	devFSBefore := `[devfsrules_jails=4]
add include $devfsrules_hide_all


[devfsrules_jails_sylve_907=907]
add include $devfsrules_jails
add path 'bpf*' unhide

[unrelated=42]
add path 'null' unhide
`
	if err := os.WriteFile(devFSPath, []byte(devFSBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	ops := &fakeJailOptionOps{path: devFSPath}
	service.optionOps = ops

	if err := service.ModifyDevfsRuleset(907, ""); err != nil {
		t.Fatalf("ModifyDevfsRuleset failed: %v", err)
	}
	if ops.calls != 1 {
		t.Fatalf("DevFS reload calls=%d want=1", ops.calls)
	}
	updated, err := os.ReadFile(devFSPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), "devfsrules_jails_sylve_907") ||
		!strings.Contains(string(updated), "[unrelated=42]") ||
		!strings.Contains(string(updated), "\n\n[devfsrules_jails_sylve") &&
			!strings.Contains(string(updated), "\n\n[unrelated=42]") {
		t.Fatalf("managed DevFS block removal damaged unrelated rules:\n%s", updated)
	}
	record := loadJailOptionRecord(t, service.DB, 907)
	if record.DevFSRuleset != "" {
		t.Fatalf("DevFS ruleset database value was not cleared: %q", record.DevFSRuleset)
	}
	updatedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedConfig), "devfs_ruleset=61181;") {
		t.Fatalf("default DevFS ruleset was not restored:\n%s", updatedConfig)
	}
}

func TestJailDevFSReloadFailureCompensatesDatabaseAndFiles(t *testing.T) {
	if config.IsDevFSDisabled() {
		t.Skip("DevFS management is disabled in this environment")
	}
	service, _, configPath, _ := newJailOptionsTestService(t, 908, func(record *jailModels.Jail) {
		record.AllowedOptions = []string{"allow.mount.devfs"}
		record.DevFSRuleset = "old-rule"
	})
	devFSPath := filepath.Join(t.TempDir(), "devfs.rules")
	devFSBefore := "[base=1]\nadd path 'null' unhide\n\n\n[unrelated=2]\nadd path 'zero' unhide\n"
	if err := os.WriteFile(devFSPath, []byte(devFSBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeJailOptionOps{path: devFSPath, failOn: 1}
	service.optionOps = ops

	err = service.ModifyDevfsRuleset(908, "new-rule")
	if err == nil || !strings.Contains(err.Error(), "forced_devfs_reload_failure") {
		t.Fatalf("expected DevFS reload failure, got %v", err)
	}
	if ops.calls != 2 {
		t.Fatalf("expected failed reload plus compensation reload, calls=%d", ops.calls)
	}
	configAfter, _ := os.ReadFile(configPath)
	devFSAfter, _ := os.ReadFile(devFSPath)
	if string(configAfter) != string(configBefore) || string(devFSAfter) != devFSBefore {
		t.Fatalf("DevFS compensation did not restore files\nconfig=%q\ndevfs=%q", configAfter, devFSAfter)
	}
	if record := loadJailOptionRecord(t, service.DB, 908); record.DevFSRuleset != "old-rule" {
		t.Fatalf("DevFS database value was not restored: %q", record.DevFSRuleset)
	}
}

func TestJailOptionMutationHonorsRestoreFence(t *testing.T) {
	service, _, _, _ := newJailOptionsTestService(t, 909, nil)
	operation := clusterModels.ReplicationGuestOperation{
		GuestType:    string(clusterModels.ReplicationGuestTypeJail),
		GuestID:      909,
		Operation:    clusterModels.ReplicationGuestOperationRestore,
		State:        "running",
		Token:        "jail-options-restore-fence",
		OwnerNodeID:  "node-a",
		TargetNodeID: "node-a",
		TaskID:       1,
		AcquiredAt:   time.Now().UTC(),
	}
	if err := service.DB.Create(&operation).Error; err != nil {
		t.Fatalf("seed restore operation: %v", err)
	}

	err := service.ModifyWakeOnLan(909, true)
	if err == nil || !strings.Contains(err.Error(), "restore_in_progress") {
		t.Fatalf("expected restore_in_progress, got %v", err)
	}
	if record := loadJailOptionRecord(t, service.DB, 909); record.WoL {
		t.Fatal("restore-fenced option mutation changed the jail")
	}
}
