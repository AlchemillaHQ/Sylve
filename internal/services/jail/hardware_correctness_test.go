// SPDX-License-Identifier: BSD-2-Clause

package jail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

type fakeJailHardwareOps struct {
	running      bool
	memoryTotal  uint64
	logicalCores int
	calls        []string
	failCPUOnce  bool
}

func (f *fakeJailHardwareOps) IsJailRunning(uint) (bool, error) {
	return f.running, nil
}

func (f *fakeJailHardwareOps) HostMemoryTotal() (uint64, error) {
	return f.memoryTotal, nil
}

func (f *fakeJailHardwareOps) HostLogicalCores() int {
	return f.logicalCores
}

func (f *fakeJailHardwareOps) ApplyMemory(_ string, memoryMiB int64) error {
	f.calls = append(f.calls, fmt.Sprintf("memory:%d", memoryMiB))
	return nil
}

func (f *fakeJailHardwareOps) RemoveMemory(string) error {
	f.calls = append(f.calls, "memory:remove")
	return nil
}

func (f *fakeJailHardwareOps) ApplyCPU(_ string, cpuList string) error {
	f.calls = append(f.calls, "cpu:"+cpuList)
	if f.failCPUOnce {
		f.failCPUOnce = false
		return errors.New("cpu_apply_failed")
	}
	return nil
}

func newJailHardwareTestService(
	t *testing.T,
	ctID uint,
	state jailHardwareState,
	hook string,
	ops *fakeJailHardwareOps,
) (*Service, string, string) {
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
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.ObjectResolution{},
		&clusterModels.ReplicationPolicy{},
		&clusterModels.ReplicationLease{},
		&clusterModels.ReplicationGuestOperation{},
	)
	enabled := state.resourceLimits
	jail := jailModels.Jail{
		CTID:           ctID,
		Name:           fmt.Sprintf("jail-%d", ctID),
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &enabled,
		Memory:         int(state.memory),
		Cores:          state.cores,
		CPUSet:         append([]int(nil), state.cpuSet...),
	}
	if err := db.Create(&jail).Error; err != nil {
		t.Fatalf("seed jail: %v", err)
	}

	jailDir := filepath.Join(dataPath, "jails", fmt.Sprintf("%d", ctID))
	scriptsDir := filepath.Join(jailDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("create jail scripts directory: %v", err)
	}
	configPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctID))
	postStartPath := filepath.Join(scriptsDir, "post-start.sh")
	mountPoint := t.TempDir()
	configContent := fmt.Sprintf(
		"%s%s {\n\tpath = %q;\n}\n",
		JAIL_CONF_PREAMBLE,
		fmt.Sprintf("jail-%d", ctID),
		mountPoint,
	)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write jail config: %v", err)
	}
	if err := os.WriteFile(postStartPath, []byte(hook), 0o755); err != nil {
		t.Fatalf("write post-start hook: %v", err)
	}

	service := &Service{
		DB:             db,
		ctidHashByCTID: make(map[uint]string),
		hardwareOps:    ops,
	}
	attachJailRootTestFixture(t, service, db, jail.ID, ctID, mountPoint)
	return service, configPath, postStartPath
}

func loadJailHardwareStateForTest(t *testing.T, db *gorm.DB, ctID uint) jailHardwareState {
	t.Helper()
	var jail jailModels.Jail
	if err := db.Where("ct_id = ?", ctID).First(&jail).Error; err != nil {
		t.Fatalf("reload jail: %v", err)
	}
	return jailHardwareStateFromModel(&jail)
}

func TestStoppedJailMemoryUpdatePersistsManagedHookWithoutRuntimeCommands(t *testing.T) {
	ops := &fakeJailHardwareOps{memoryTotal: 8 * 1024 * 1024 * 1024, logicalCores: 4}
	service, configPath, hookPath := newJailHardwareTestService(t, 801, jailHardwareState{
		resourceLimits: true,
		memory:         1024 * 1024 * 1024,
		cores:          1,
		cpuSet:         []int{0},
	}, "#!/bin/sh\n", ops)

	requested := int64(1536*1024*1024 + 1)
	result, err := service.UpdateMemory(801, requested)
	if err != nil {
		t.Fatalf("UpdateMemory failed: %v", err)
	}
	wantMemory := int64(1537 * 1024 * 1024)
	if result.Memory != wantMemory || len(ops.calls) != 0 {
		t.Fatalf("unexpected result/runtime calls: result=%+v calls=%v", result, ops.calls)
	}
	state := loadJailHardwareStateForTest(t, service.DB, 801)
	if state.memory != wantMemory {
		t.Fatalf("persisted memory=%d, want %d", state.memory, wantMemory)
	}
	hook, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if !strings.Contains(string(hook), jailHardwareManagedStart) ||
		!strings.Contains(string(hook), "memoryuse:deny=1537M") ||
		!strings.Contains(string(hook), "cpuset -l 0") {
		t.Fatalf("managed hardware hook not reconciled:\n%s", hook)
	}
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(configContent), "exec.poststart") {
		t.Fatalf("post-start hook was not wired into jail config:\n%s", configContent)
	}
}

func TestDisablingJailLimitsPreservesUserHookContent(t *testing.T) {
	ops := &fakeJailHardwareOps{memoryTotal: 8 * 1024 * 1024 * 1024, logicalCores: 4}
	userHook := `#!/bin/sh
### Start Sylve-Managed Hardware ###
rctl -a jail:legacy:memoryuse:deny=1024M
cpuset -l 0 -j legacy
### End Sylve-Managed Hardware ###
### Start User-Managed Hook ###
echo user-content
rctl -a jail:someone-else:memoryuse:deny=512M
cpuset -l 2 -j someone-else
### End User-Managed Hook ###
`
	service, configPath, hookPath := newJailHardwareTestService(t, 802, jailHardwareState{
		resourceLimits: true,
		memory:         1024 * 1024 * 1024,
		cores:          1,
		cpuSet:         []int{0},
	}, userHook, ops)

	// Use the actual hash in the managed block so it is owned by this jail.
	hash := service.GetCTIDHash(802)
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	content = []byte(strings.ReplaceAll(string(content), "legacy", hash))
	if err := os.WriteFile(hookPath, content, 0o755); err != nil {
		t.Fatalf("update hook fixture: %v", err)
	}

	if _, err := service.UpdateResourceLimits(802, false); err != nil {
		t.Fatalf("UpdateResourceLimits failed: %v", err)
	}
	updatedHook, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read updated hook: %v", err)
	}
	text := string(updatedHook)
	if strings.Contains(text, jailHardwareManagedStart) || !strings.Contains(text, "echo user-content") ||
		!strings.Contains(text, "jail:someone-else:memoryuse") || !strings.Contains(text, "-j someone-else") {
		t.Fatalf("user hook content was not preserved:\n%s", text)
	}
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(configContent), "exec.poststart") {
		t.Fatalf("config should retain post-start wiring for the user hook:\n%s", configContent)
	}
}

func TestRunningJailRuntimeFailureCompensatesDatabaseAndFiles(t *testing.T) {
	ops := &fakeJailHardwareOps{
		running:      true,
		memoryTotal:  8 * 1024 * 1024 * 1024,
		logicalCores: 4,
		failCPUOnce:  true,
	}
	prior := jailHardwareState{
		resourceLimits: true,
		memory:         1024 * 1024 * 1024,
		cores:          1,
		cpuSet:         []int{0},
	}
	service, configPath, hookPath := newJailHardwareTestService(
		t,
		803,
		prior,
		"#!/bin/sh\necho before\n",
		ops,
	)
	configBefore, _ := os.ReadFile(configPath)
	hookBefore, _ := os.ReadFile(hookPath)

	if _, err := service.UpdateResourceLimits(803, false); err == nil {
		t.Fatal("expected runtime CPU failure")
	}
	state := loadJailHardwareStateForTest(t, service.DB, 803)
	if !state.resourceLimits || state.memory != prior.memory || state.cores != prior.cores ||
		jailHardwareCPUList(state.cpuSet) != "0" {
		t.Fatalf("database state was not restored: %+v", state)
	}
	configAfter, _ := os.ReadFile(configPath)
	hookAfter, _ := os.ReadFile(hookPath)
	if string(configAfter) != string(configBefore) || string(hookAfter) != string(hookBefore) {
		t.Fatalf("configuration files were not restored\nconfig=%q\nhook=%q", configAfter, hookAfter)
	}
	joinedCalls := strings.Join(ops.calls, ",")
	if !strings.Contains(joinedCalls, "memory:remove") ||
		!strings.Contains(joinedCalls, "cpu:0-3") ||
		!strings.Contains(joinedCalls, "cpu:0") ||
		!strings.Contains(joinedCalls, "memory:1024") {
		t.Fatalf("runtime compensation was incomplete: %v", ops.calls)
	}
}

func TestJailHardwareRejectsInvalidValuesBeforeMutation(t *testing.T) {
	ops := &fakeJailHardwareOps{memoryTotal: 8 * 1024 * 1024 * 1024, logicalCores: 4}
	service, configPath, hookPath := newJailHardwareTestService(t, 804, jailHardwareState{
		resourceLimits: true,
		memory:         1024 * 1024 * 1024,
		cores:          1,
		cpuSet:         []int{0},
	}, "#!/bin/sh\necho unchanged\n", ops)
	configBefore, _ := os.ReadFile(configPath)
	hookBefore, _ := os.ReadFile(hookPath)

	if _, err := service.UpdateMemory(804, 512*1024); err == nil || !strings.Contains(err.Error(), "memory_limit_too_low") {
		t.Fatalf("expected memory_limit_too_low, got %v", err)
	}
	if _, err := service.UpdateCPU(804, 5); err == nil || !strings.Contains(err.Error(), "invalid_cores") {
		t.Fatalf("expected invalid_cores, got %v", err)
	}
	configAfter, _ := os.ReadFile(configPath)
	hookAfter, _ := os.ReadFile(hookPath)
	if len(ops.calls) != 0 || string(configAfter) != string(configBefore) || string(hookAfter) != string(hookBefore) {
		t.Fatalf("invalid requests changed state: calls=%v config=%q hook=%q", ops.calls, configAfter, hookAfter)
	}
}

func TestJailHardwareDeniedWhenReplicationLeaseNotOwned(t *testing.T) {
	ops := &fakeJailHardwareOps{memoryTotal: 8 * 1024 * 1024 * 1024, logicalCores: 4}
	service, configPath, hookPath := newJailHardwareTestService(t, 805, jailHardwareState{
		resourceLimits: true,
		memory:         1024 * 1024 * 1024,
		cores:          1,
		cpuSet:         []int{0},
	}, "#!/bin/sh\necho unchanged\n", ops)
	policy := clusterModels.ReplicationPolicy{
		Name:            "hardware-policy",
		GuestType:       clusterModels.ReplicationGuestTypeJail,
		GuestID:         805,
		SourceNodeID:    "node-a",
		ActiveNodeID:    "node-a",
		OwnerEpoch:      1,
		SourceMode:      clusterModels.ReplicationSourceModeFollowActive,
		FailbackMode:    clusterModels.ReplicationFailbackManual,
		FailoverMode:    clusterModels.ReplicationFailoverManual,
		CronExpr:        "* * * * *",
		Enabled:         true,
		TransitionState: clusterModels.ReplicationTransitionStateNone,
	}
	if err := service.DB.Create(&policy).Error; err != nil {
		t.Fatalf("seed replication policy: %v", err)
	}
	lease := clusterModels.ReplicationLease{
		PolicyID:    policy.ID,
		GuestType:   clusterModels.ReplicationGuestTypeJail,
		GuestID:     805,
		OwnerNodeID: "other-node",
		OwnerEpoch:  policy.OwnerEpoch,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Version:     1,
	}
	if err := service.DB.Create(&lease).Error; err != nil {
		t.Fatalf("seed replication lease: %v", err)
	}
	configBefore, _ := os.ReadFile(configPath)
	hookBefore, _ := os.ReadFile(hookPath)

	if _, err := service.UpdateMemory(805, 2*1024*1024*1024); err == nil ||
		!strings.Contains(err.Error(), "replication_lease_not_owned") {
		t.Fatalf("expected replication_lease_not_owned, got %v", err)
	}
	configAfter, _ := os.ReadFile(configPath)
	hookAfter, _ := os.ReadFile(hookPath)
	if len(ops.calls) != 0 || string(configAfter) != string(configBefore) || string(hookAfter) != string(hookBefore) {
		t.Fatalf("denied request changed state: calls=%v config=%q hook=%q", ops.calls, configAfter, hookAfter)
	}
}

func TestCreateHardwareConfigUsesPersistedCPUSet(t *testing.T) {
	service := &Service{ctidHashByCTID: make(map[uint]string)}
	cpuConfig, memoryConfig, err := service.CreateHardwareConfig(jailModels.Jail{
		CTID:   806,
		Cores:  2,
		CPUSet: []int{1, 3},
		Memory: 1024*1024*1024 + 1,
	})
	if err != nil {
		t.Fatalf("CreateHardwareConfig failed: %v", err)
	}
	if !strings.Contains(cpuConfig, "cpuset -l 1,3") {
		t.Fatalf("CPU config did not use persisted CPU set: %q", cpuConfig)
	}
	if !strings.Contains(memoryConfig, "memoryuse:deny=1025M") {
		t.Fatalf("memory config was not rounded up consistently: %q", memoryConfig)
	}
}

func TestNormalizeRestoredJailHardwareForDestination(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	enabled := true
	if err := db.Create(&jailModels.Jail{
		CTID:           22,
		Name:           "jail-22",
		Type:           jailModels.JailTypeFreeBSD,
		ResourceLimits: &enabled,
		Cores:          1,
		CPUSet:         []int{0},
	}).Error; err != nil {
		t.Fatalf("seed existing jail: %v", err)
	}

	service := &Service{
		DB:             db,
		ctidHashByCTID: make(map[uint]string),
		hardwareOps:    &fakeJailHardwareOps{logicalCores: 2},
	}
	restored := jailModels.Jail{
		CTID:           23,
		ResourceLimits: &enabled,
		Cores:          1,
		Memory:         1024 * 1024 * 1024,
	}
	if err := service.NormalizeRestoredJailHardware(&restored); err != nil {
		t.Fatalf("NormalizeRestoredJailHardware failed: %v", err)
	}
	if len(restored.CPUSet) != 1 || restored.CPUSet[0] != 1 {
		t.Fatalf("destination CPU set = %v, want [1]", restored.CPUSet)
	}
	if _, _, err := service.CreateHardwareConfig(restored); err != nil {
		t.Fatalf("normalized hardware configuration was rejected: %v", err)
	}
}

func TestNormalizeRestoredJailHardwareCanonicalizesDisabledLimits(t *testing.T) {
	disabled := false
	service := &Service{}
	restored := jailModels.Jail{
		CTID:           23,
		ResourceLimits: &disabled,
		Cores:          2,
		CPUSet:         []int{0, 1},
		Memory:         1024 * 1024 * 1024,
	}
	if err := service.NormalizeRestoredJailHardware(&restored); err != nil {
		t.Fatalf("NormalizeRestoredJailHardware failed: %v", err)
	}
	if restored.Cores != 0 || len(restored.CPUSet) != 0 || restored.Memory != 0 {
		t.Fatalf("disabled hardware limits were not canonicalized: %+v", restored)
	}
}

func TestNormalizeRestoredJailHardwareRejectsInsufficientDestinationCPU(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &jailModels.Jail{})
	enabled := true
	service := &Service{DB: db, hardwareOps: &fakeJailHardwareOps{logicalCores: 2}}
	restored := jailModels.Jail{CTID: 23, ResourceLimits: &enabled, Cores: 3}
	err := service.NormalizeRestoredJailHardware(&restored)
	if err == nil || !strings.Contains(err.Error(), "restored_jail_cpu_capacity_insufficient") {
		t.Fatalf("insufficient CPU error = %v", err)
	}
}
