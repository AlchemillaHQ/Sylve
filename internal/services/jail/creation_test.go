// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newCreationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{}, &jailModels.Jail{},
		&clusterModels.ReplicationPolicy{}, &clusterModels.ReplicationGuestOperation{}, &clusterModels.ReplicationRunOperation{},
		&jailModels.JailCreation{}, &jailModels.Storage{}, &jailModels.Network{}, &jailModels.JailHooks{},
		&jailModels.JailStats{}, &jailModels.JailSnapshot{}, &vmModels.VM{}, &networkModels.Object{},
		&networkModels.ObjectEntry{}, &networkModels.ObjectResolution{}, &utilitiesModels.Downloads{}, &taskModels.GuestLifecycleTask{})
}

func newCreationTestService(t *testing.T) (*Service, *jailCreateTestZFSRunner, jailServiceInterfaces.CreateJailRequest, uint) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newCreationTestDB(t)
	runner := newJailCreateTestZFSRunner(t, nil)
	svc := newJailCreateTestService(db, runner, "testpool")
	svc.SetGuestIdentityCoordinator(&clusterService.Service{DB: db, NodeID: "creation-node"})
	base := t.TempDir()
	seedCreationRoot(t, base)
	seedBaseDownload(t, db, "creation-base", base)
	req := jailCreateRequest(830, "testpool", "creation-base")
	task := taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: *req.CTID, Action: "create", Status: "queued"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return svc, runner, req, task.ID
}

func TestQueuedJailCreationSurvivesRequestCancellationAndDuplicateExecution(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	requestCtx, cancel := context.WithCancel(t.Context())
	if err := svc.PrepareCreateJail(requestCtx, taskID, req); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := svc.ValidateCreate(t.Context(), req); err == nil || !strings.Contains(err.Error(), "jail_creation_in_progress") {
		t.Fatalf("duplicate admission = %v", err)
	}
	identity := svc.guestIdentityCoordinator
	if _, err := identity.ReserveGuestIdentities(t.Context(), "vm", []uint{*req.CTID}); err == nil {
		t.Fatal("VM acquired a queued creation's CTID")
	}
	for i := 0; i < 2; i++ {
		if err := svc.ExecuteCreateJail(t.Context(), taskID); err != nil {
			t.Fatal(err)
		}
	}
	if runner.createCalls != 1 {
		t.Fatalf("provisioned %d times", runner.createCalls)
	}
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 1, "ct_id = ? AND creation_pending = ?", *req.CTID, false)
	var task taskModels.GuestLifecycleTask
	if err := svc.DB.First(&task, taskID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "success" {
		t.Fatalf("task = %+v", task)
	}
}

func TestJailCreationAdmissionDoesNotAddNameReservations(t *testing.T) {
	svc, _, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	ctID := *req.CTID + 1
	req.CTID = &ctID
	if err := svc.ValidateCreate(t.Context(), req); err != nil {
		t.Fatalf("same name, different ID rejected: %v", err)
	}
}

func TestJailCreationSourceExclusivity(t *testing.T) {
	svc, _, req, _ := newCreationTestService(t)
	req.ZFSSource = &jailServiceInterfaces.ZFSSource{Dataset: "pool/root", GUID: "1"}
	if err := svc.ValidateCreate(t.Context(), req); err == nil || !strings.Contains(err.Error(), "jail_source_mutually_exclusive") {
		t.Fatalf("validation = %v", err)
	}
}

func TestRecoverQueuedCreationReleasesExactReservation(t *testing.T) {
	svc, _, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	svc.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
	if err := svc.RecoverCreations(t.Context()); err != nil {
		t.Fatal(err)
	}
	var operation jailModels.JailCreation
	if err := svc.DB.Where("task_id = ?", taskID).First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if operation.Phase != "failed" || operation.ActiveCTID != nil {
		t.Fatalf("operation = %+v", operation)
	}
	if _, err := svc.guestIdentityCoordinator.ReserveGuestIdentities(t.Context(), "vm", []uint{*req.CTID}); err != nil {
		t.Fatalf("reservation stranded: %v", err)
	}
}

func TestRecoverReadyCreationAfterIdentityFinalizeAndTaskCommitFailure(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	const callback = "test:fail_creation_task_commit"
	if err := svc.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if updates, ok := tx.Statement.Dest.(map[string]any); ok && tx.Statement.Table == "guest_lifecycle_tasks" && updates["status"] == "success" {
			tx.AddError(fmt.Errorf("injected_task_commit_failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := svc.ExecuteCreateJail(t.Context(), taskID)
	var pending *creationCompletionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("execution = %v", err)
	}
	if err := svc.DB.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 1, "creation_pending = ?", true)
	svc.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
	if err := svc.RecoverCreations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.destroyCalls != 0 {
		t.Fatal("completed filesystem was destroyed")
	}
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 1, "creation_pending = ?", false)
	var task taskModels.GuestLifecycleTask
	if err := svc.DB.First(&task, taskID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "success" {
		t.Fatalf("task status = %s", task.Status)
	}
	if err := svc.ExecuteCreateJail(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if runner.createCalls != 1 {
		t.Fatal("recovery provisioned a second filesystem")
	}
}

func TestCreationRollbackRetainsUnownedDatasetAndConfiguration(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	op, state, _, err := svc.loadCreation(taskID)
	if err != nil {
		t.Fatal(err)
	}
	name := "testpool/sylve/jails/830"
	runner.datasets[name] = jailCreateTestZFSDataset{guid: "foreign-guid", owner: "another-operation", mountpoint: t.TempDir()}
	state.Datasets = []creationDataset{{Name: name, GUID: "foreign-guid"}}
	state.ConfigDir = t.TempDir()
	marker := filepath.Join(state.ConfigDir, ".creation-operation")
	if err := os.WriteFile(marker, []byte("another-operation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("injected_failure")); err == nil {
		t.Fatal("ambiguous cleanup succeeded")
	}
	if runner.destroyCalls != 0 {
		t.Fatal("destroyed an unowned dataset")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("removed foreign configuration")
	}
	if err := svc.DB.First(op, "id = ?", op.ID).Error; err != nil {
		t.Fatal(err)
	}
	if op.ActiveCTID == nil || !strings.Contains(op.Error, "Cleanup is blocked") {
		t.Fatalf("operation = %+v", op)
	}
}

func TestCreationOwnershipJournalDoesNotExposeReservationInTaskPayload(t *testing.T) {
	svc, _, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	op, state, _, err := svc.loadCreation(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LocalToken == "" || !strings.Contains(op.State, state.LocalToken) {
		t.Fatal("reservation was not durably recorded")
	}
	var task taskModels.GuestLifecycleTask
	if err := svc.DB.First(&task, taskID).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), state.LocalToken) {
		t.Fatal("private reservation exposed in public task")
	}
}

func TestValidateCopiedRootRejectsConfigurationSymlinks(t *testing.T) {
	for _, target := range []string{"etc/rc.conf", "etc/resolv.conf", "usr/local/sylve/scripts/start.sh", "usr/local/sylve/scripts/stop.sh", ".sylve/jail.json"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			seedCreationRoot(t, root)
			path := filepath.Join(root, target)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "host-file"), path); err != nil {
				t.Fatal(err)
			}
			if err := validateCopiedRoot(root, "freebsd"); err == nil || !strings.Contains(err.Error(), "unsafe_configuration_path") {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func seedCreationRoot(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"bin", "etc", "usr", "var"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"bin/sh", "etc/rc"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("fixture; never executed\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJailCreationRejectsIncompleteAndProtectedSources(t *testing.T) {
	svc, _, _, _ := newCreationTestService(t)
	ctID := uint(860)
	op := jailModels.JailCreation{ID: "pending-source", CTID: ctID, ActiveCTID: &ctID, Request: `{"pool":"testpool"}`, State: "{}", Phase: "configuring"}
	if err := svc.DB.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	source := jailServiceInterfaces.ZFSSource{Dataset: "testpool/sylve/jails/860", GUID: "1"}
	if err := svc.requireSourceOperationAllowed(t.Context(), source); err == nil || !strings.Contains(err.Error(), "jail_creation_in_progress") {
		t.Fatalf("unfinished source = %v", err)
	}
	if err := svc.DB.Model(&op).Update("active_ct_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&clusterModels.ReplicationPolicy{GuestType: "jail", GuestID: ctID, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.requireSourceOperationAllowed(t.Context(), source); err == nil || !strings.Contains(err.Error(), "zfs_source_protected") {
		t.Fatalf("protected source = %v", err)
	}
}

func TestCreationStagingCleanupRetainsUnownedDirectories(t *testing.T) {
	for _, foreignMarker := range []bool{false, true} {
		dir := t.TempDir()
		if foreignMarker {
			if err := os.WriteFile(filepath.Join(dir, ".creation-operation"), []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := removeCreationStagingDir("ours", dir); err == nil {
			t.Fatal("unowned staging directory removed")
		}
		if _, err := os.Stat(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCurrentZFSSourceRejectsExternalUserlandMounts(t *testing.T) {
	root := t.TempDir()
	seedCreationRoot(t, root)
	if err := os.Mkdir(filepath.Join(root, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	tree := sourceTree{Members: []sourceMember{{Dataset: "pool/root", GUID: "1"}}}
	mounts := fmt.Sprintf("pool/root %s zfs rw 0 0\n", root)
	if err := validateSourceRootMounts(root, tree, mounts); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bin", "lib"} {
		external := mounts + fmt.Sprintf("/external/release %s nullfs ro 0 0\n", filepath.Join(root, path))
		if err := validateSourceRootMounts(root, tree, external); err == nil {
			t.Fatalf("external userland mount accepted: %s", path)
		}
	}
}

func TestZFSSourceRejectsSubdirectoryRootAndRunningForeignJail(t *testing.T) {
	root := t.TempDir()
	seedCreationRoot(t, filepath.Join(root, "root"))
	if err := validateCopiedRoot(root, "freebsd"); err == nil {
		t.Fatal("manager container with subdirectory root accepted")
	}
	svc := &Service{creationRunningPaths: func(context.Context) ([]string, error) { return []string{root + "/root"}, nil }}
	if err := svc.requireSourceStopped(t.Context(), root); err == nil || !strings.Contains(err.Error(), "zfs_source_jail_running") {
		t.Fatalf("foreign running root accepted: %v", err)
	}
}

func TestCreationRollbackRetainsUnknownChildrenAndOtherPools(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	op, state, _, err := svc.loadCreation(taskID)
	if err != nil {
		t.Fatal(err)
	}
	root := "testpool/sylve/jails/830"
	runner.datasets[root] = jailCreateTestZFSDataset{guid: "ours", owner: op.ID, mountpoint: t.TempDir()}
	runner.datasets[root+"/foreign"] = jailCreateTestZFSDataset{guid: "unknown", mountpoint: t.TempDir()}
	runner.datasets["anotherpool/sylve/jails/830"] = jailCreateTestZFSDataset{guid: "other-pool", mountpoint: t.TempDir()}
	state.Datasets = []creationDataset{{Name: root, GUID: "ours"}}
	if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("injected_failure")); err == nil {
		t.Fatal("unknown descendant cleanup succeeded")
	}
	if runner.destroyCalls != 0 {
		t.Fatal("rollback recursively destroyed unknown descendants or another pool")
	}
}

func TestCreationRollbackRechecksDurableCompletionBeforeDestroying(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	if err := svc.ExecuteCreateJail(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	op, state, _, err := svc.loadCreation(taskID)
	if err != nil {
		t.Fatal(err)
	}
	op.Phase = "configuring"
	var pending *creationCompletionPendingError
	if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("stale_in_memory_phase")); !errors.As(err, &pending) {
		t.Fatalf("stale cleanup = %v", err)
	}
	if runner.destroyCalls != 0 {
		t.Fatal("durably completed filesystem destroyed")
	}
}

func TestZFSSourceRejectsRetiredSnapshotFields(t *testing.T) {
	for _, field := range []string{`"snapshot":"selected"`, `"snapshot":""`, `"snapshot":null`, `"Snapshot":"selected"`, `"snapshotGuid":"123"`, `"snapshotGuid":""`, `"snapshotGuid":null`} {
		var request jailServiceInterfaces.CreateJailRequest
		payload := `{"zfsSource":{"dataset":"pool/root","guid":"1",` + field + `}}`
		if err := json.Unmarshal([]byte(payload), &request); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("snapshot field silently accepted: %s: %v", field, err)
		}
	}
	if err := validateZFSSourceIdentity(jailServiceInterfaces.ZFSSource{Dataset: "pool/root@selected", GUID: "1"}); err == nil {
		t.Fatal("snapshot identity accepted as a dataset")
	}
}

func TestFilterZFSSourcesKeepsExternalAndCanonicalJailRootCandidates(t *testing.T) {
	var datasets []*gzfs.Dataset
	var mounts string
	add := func(name string, root bool) *gzfs.Dataset {
		mountpoint := t.TempDir()
		if root {
			seedCreationRoot(t, mountpoint)
		}
		dataset := &gzfs.Dataset{Name: name, GUID: "fixture-guid", Type: gzfs.DatasetTypeFilesystem, Mountpoint: mountpoint,
			Properties: map[string]gzfs.ZFSProperty{"encryption": {Value: "off"}, "readonly": {Value: "off"}}}
		datasets = append(datasets, dataset)
		mounts += fmt.Sprintf("%s %s zfs rw 0 0\n", name, mountpoint)
		return dataset
	}
	add("tank/iocage/jails/legacy/root", true)
	add("otherpool/releases/14.3", true)
	add("tank/sylve-external/root", true)
	add("tank/sylve/jails/42", true)
	for _, name := range []string{"tank", "tank/sylve", "tank/sylve/jails", "tank/sylve/jails/0", "tank/sylve/jails/042", "tank/sylve/jails/10000", "tank/sylve/jails/43/root", "tank/sylve/bootstraps/freebsd-base", "tank/sylve/jails/create-operation/root", "tank/sylve/virtual-machines/100"} {
		add(name, true)
	}
	add("tank/container", false)
	add("tank/jail-data", false)
	container := add("tank/subdirectory-root", false)
	seedCreationRoot(t, filepath.Join(container.Mountpoint, "root"))
	add("tank/encrypted", true).Properties["encryption"] = gzfs.ZFSProperty{Value: "on"}
	add("tank/readonly", true).Properties["readonly"] = gzfs.ZFSProperty{Value: "on"}
	add("tank/unknown-encryption", true).Properties = nil
	add("tank/volume", true).Type = gzfs.DatasetTypeVolume
	add("tank/snapshot@selected", true).Type = gzfs.DatasetTypeSnapshot
	add("tank/missing-guid", true).GUID = ""
	add("tank/system-root", true).Mountpoint = "/"
	for _, mountpoint := range []string{"none", "legacy", "relative/path"} {
		add("tank/invalid-"+strings.ReplaceAll(mountpoint, "/", "-"), true).Mountpoint = mountpoint
	}
	unmounted := add("tank/unmounted", true)
	mounts = strings.ReplaceAll(mounts, fmt.Sprintf("%s %s zfs rw 0 0\n", unmounted.Name, unmounted.Mountpoint), "")
	unsafe := add("tank/unsafe-config", true)
	if err := os.Symlink(filepath.Join(t.TempDir(), "host-config"), filepath.Join(unsafe.Mountpoint, "etc", "rc.conf")); err != nil {
		t.Fatal(err)
	}
	sources := filterZFSSources(datasets, mounts)
	want := []string{"otherpool/releases/14.3", "tank/iocage/jails/legacy/root", "tank/sylve-external/root", "tank/sylve/jails/42"}
	if len(sources) != len(want) {
		t.Fatalf("sources = %+v, want %v", sources, want)
	}
	for i, source := range sources {
		if source.Name != want[i] {
			t.Fatalf("source[%d] = %s, want %s", i, source.Name, want[i])
		}
	}
}

func newOrphanZFSSourceFixture(t *testing.T) (*Service, *jailCreateTestZFSRunner, []*gzfs.Dataset, string) {
	t.Helper()
	svc, runner, _, _ := newCreationTestService(t)
	svc.creationRunningPaths = func(context.Context) ([]string, error) { return nil, nil }
	runner.datasets["testpool/sylve/jails"] = jailCreateTestZFSDataset{guid: "destination-parent", mountpoint: t.TempDir()}
	root := t.TempDir()
	seedCreationRoot(t, root)
	childMount := filepath.Join(root, "var", "app")
	if err := os.MkdirAll(childMount, 0755); err != nil {
		t.Fatal(err)
	}
	datasets := []*gzfs.Dataset{
		{Name: "testpool/sylve/jails/123", GUID: "orphan-root", Type: gzfs.DatasetTypeFilesystem, Mountpoint: root,
			Properties: map[string]gzfs.ZFSProperty{"encryption": {Value: "off"}}},
		{Name: "testpool/sylve/jails/123/app", GUID: "orphan-child", Type: gzfs.DatasetTypeFilesystem, Mountpoint: childMount,
			Properties: map[string]gzfs.ZFSProperty{"encryption": {Value: "off"}}},
	}
	var mounts string
	for _, dataset := range datasets {
		runner.datasets[dataset.Name] = jailCreateTestZFSDataset{guid: dataset.GUID, mountpoint: dataset.Mountpoint}
		mounts += fmt.Sprintf("%s %s zfs rw 0 0\n", dataset.Name, dataset.Mountpoint)
	}
	return svc, runner, datasets, mounts
}

func TestOrphanZFSSourceOwnershipAndOperations(t *testing.T) {
	for _, scenario := range []string{
		"unused", "committed-creation", "released-failed-creation", "registered-jail", "pending-jail",
		"registered-root-storage", "registered-child-storage", "pending-creation", "pending-creation-other-pool", "unrelated-creation", "retained-cleanup",
		"queued-lifecycle", "running-lifecycle", "completed-lifecycle", "enabled-policy", "disabled-policy",
		"disabled-policy-queued-run", "disabled-policy-running-run", "policy-transition", "unrelated-policy-transition", "migration", "restore",
		"root-replication-property", "child-replication-property", "running-foreign-jail",
		"missing-jail-table", "missing-storage-table", "malformed-journal", "property-inspection-failure", "runtime-inspection-failure",
	} {
		t.Run(scenario, func(t *testing.T) {
			svc, runner, datasets, mounts := newOrphanZFSSourceFixture(t)
			ctID := uint(123)
			allowed, wantError := false, false
			switch scenario {
			case "unused":
				allowed = true
			case "committed-creation", "released-failed-creation", "pending-creation", "pending-creation-other-pool", "unrelated-creation", "retained-cleanup", "malformed-journal":
				op := jailModels.JailCreation{ID: "previous-operation", CTID: ctID, Request: `{"pool":"testpool"}`, State: "{}", Phase: "committed"}
				if scenario == "released-failed-creation" {
					op.Phase = "failed"
				} else if scenario != "committed-creation" {
					op.ActiveCTID, op.Phase = &ctID, "configuring"
					if scenario == "retained-cleanup" {
						op.Phase = "failed"
					} else if scenario == "malformed-journal" {
						op.Request, wantError = "{", true
					} else if scenario == "pending-creation-other-pool" {
						op.Request = `{"pool":"otherpool"}`
					} else if scenario == "unrelated-creation" {
						otherCTID := uint(321)
						op.CTID, op.ActiveCTID = otherCTID, &otherCTID
					}
				}
				if err := svc.DB.Create(&op).Error; err != nil {
					t.Fatal(err)
				}
				if scenario != "unrelated-creation" {
					dataset := runner.datasets[datasets[0].Name]
					dataset.owner = op.ID
					runner.datasets[datasets[0].Name] = dataset
				}
				allowed = scenario == "committed-creation" || scenario == "released-failed-creation" || scenario == "unrelated-creation"
			case "registered-jail", "pending-jail":
				if err := svc.DB.Create(&jailModels.Jail{CTID: ctID, Name: "registered", Type: jailModels.JailTypeFreeBSD, CreationPending: scenario == "pending-jail"}).Error; err != nil {
					t.Fatal(err)
				}
			case "registered-root-storage", "registered-child-storage":
				jail := jailModels.Jail{CTID: 321, Name: "storage-owner", Type: jailModels.JailTypeFreeBSD}
				if err := svc.DB.Create(&jail).Error; err != nil {
					t.Fatal(err)
				}
				guid := datasets[0].GUID
				if scenario == "registered-child-storage" {
					guid = datasets[1].GUID
				}
				if err := svc.DB.Create(&jailModels.Storage{JailID: jail.ID, Pool: "testpool", GUID: guid, Name: "Attached filesystem"}).Error; err != nil {
					t.Fatal(err)
				}
			case "queued-lifecycle", "running-lifecycle", "completed-lifecycle":
				status := taskModels.LifecycleTaskStatusQueued
				if scenario == "running-lifecycle" {
					status = taskModels.LifecycleTaskStatusRunning
				} else if scenario == "completed-lifecycle" {
					status, allowed = taskModels.LifecycleTaskStatusSuccess, true
				}
				if err := svc.DB.Create(&taskModels.GuestLifecycleTask{GuestType: "jail", GuestID: ctID, Action: "stop", Status: status}).Error; err != nil {
					t.Fatal(err)
				}
			case "enabled-policy", "disabled-policy", "disabled-policy-queued-run", "disabled-policy-running-run", "policy-transition", "unrelated-policy-transition":
				policy := clusterModels.ReplicationPolicy{GuestType: "jail", GuestID: ctID, Enabled: scenario == "enabled-policy"}
				if scenario == "policy-transition" || scenario == "unrelated-policy-transition" {
					policy.TransitionState = clusterModels.ReplicationTransitionStateCatchup
					if scenario == "unrelated-policy-transition" {
						policy.GuestID = 321
					}
				}
				if err := svc.DB.Create(&policy).Error; err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(scenario, "disabled-policy-") {
					state := clusterModels.ReplicationRunOperationQueued
					if scenario == "disabled-policy-running-run" {
						state = clusterModels.ReplicationRunOperationRunning
					}
					if err := svc.DB.Create(&clusterModels.ReplicationRunOperation{PolicyID: policy.ID, Token: "active-run", State: state}).Error; err != nil {
						t.Fatal(err)
					}
				}
				allowed = scenario == "disabled-policy" || scenario == "unrelated-policy-transition"
			case "migration", "restore":
				if err := svc.DB.Create(&clusterModels.ReplicationGuestOperation{GuestType: "jail", GuestID: ctID, Operation: scenario, State: clusterModels.ReplicationGuestOperationPreCutover, Token: "active-operation"}).Error; err != nil {
					t.Fatal(err)
				}
			case "root-replication-property", "child-replication-property":
				name := datasets[0].Name
				if scenario == "child-replication-property" {
					name = datasets[1].Name
				}
				dataset := runner.datasets[name]
				dataset.policyID = "protected"
				runner.datasets[name] = dataset
			case "running-foreign-jail":
				svc.creationRunningPaths = func(context.Context) ([]string, error) { return []string{datasets[0].Mountpoint}, nil }
			case "missing-jail-table":
				if err := svc.DB.Migrator().DropTable(&jailModels.Jail{}); err != nil {
					t.Fatal(err)
				}
				wantError = true
			case "missing-storage-table":
				if err := svc.DB.Migrator().DropTable(&jailModels.Storage{}); err != nil {
					t.Fatal(err)
				}
				wantError = true
			case "property-inspection-failure":
				delete(runner.datasets, datasets[1].Name)
				wantError = true
			case "runtime-inspection-failure":
				svc.creationRunningPaths = func(context.Context) ([]string, error) { return nil, fmt.Errorf("injected_runtime_failure") }
				wantError = true
			}
			candidates := filterZFSSources(datasets, mounts)
			if len(candidates) != 1 || candidates[0].Name != datasets[0].Name {
				t.Fatalf("structural candidates = %+v", candidates)
			}
			sources, err := svc.filterAvailableZFSSources(t.Context(), candidates, datasets)
			if wantError {
				if err == nil || len(sources) != 0 {
					t.Fatalf("inspection failed open: sources=%+v error=%v", sources, err)
				}
			} else if err != nil || allowed && len(sources) != 1 || !allowed && len(sources) != 0 {
				t.Fatalf("sources=%+v error=%v; allowed=%v", sources, err, allowed)
			}
			if runner.createCalls != 0 || runner.destroyCalls != 0 {
				t.Fatal("source discovery changed datasets")
			}
		})
	}
}

func TestZFSSourceAuthoritativeInspectionRejectsOtherInternalDatasets(t *testing.T) {
	svc, runner, _, _ := newCreationTestService(t)
	for _, name := range []string{
		"testpool/sylve", "testpool/sylve/jails", "testpool/sylve/jails/123/root", "testpool/sylve/jails/0123",
		"testpool/sylve/jails/create-operation/root", "testpool/sylve/bootstraps/freebsd-base", "testpool/sylve/virtual-machines/123",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.inspectZFSSource(t.Context(), jailServiceInterfaces.ZFSSource{Dataset: name, GUID: "fixture-guid"}, "testpool")
			if err == nil || !strings.Contains(err.Error(), "zfs_source_protected: internal_dataset") {
				t.Fatalf("internal source passed inspection: %v", err)
			}
		})
	}
	if runner.createCalls != 0 || runner.destroyCalls != 0 {
		t.Fatal("internal source validation changed datasets")
	}
}

func TestOrphanZFSSourceRechecksRegistrationAfterDiscovery(t *testing.T) {
	for _, registration := range []string{"jail", "root-storage", "child-storage"} {
		t.Run(registration, func(t *testing.T) {
			svc, runner, datasets, mounts := newOrphanZFSSourceFixture(t)
			sources, err := svc.filterAvailableZFSSources(t.Context(), filterZFSSources(datasets, mounts), datasets)
			if err != nil || len(sources) != 1 {
				t.Fatalf("unused source not discoverable: %+v, %v", sources, err)
			}
			ctID := uint(321)
			if registration == "jail" {
				ctID = 123
			}
			jail := jailModels.Jail{CTID: ctID, Name: "new-owner", Type: jailModels.JailTypeFreeBSD}
			if err := svc.DB.Create(&jail).Error; err != nil {
				t.Fatal(err)
			}
			if registration != "jail" {
				guid := datasets[0].GUID
				if registration == "child-storage" {
					guid = datasets[1].GUID
				}
				if err := svc.DB.Create(&jailModels.Storage{JailID: jail.ID, Pool: "testpool", GUID: guid}).Error; err != nil {
					t.Fatal(err)
				}
			}
			_, err = svc.inspectZFSSource(t.Context(), jailServiceInterfaces.ZFSSource{Dataset: datasets[0].Name, GUID: datasets[0].GUID}, "testpool")
			if err == nil || !strings.Contains(err.Error(), "zfs_source_protected: registered_jail") {
				t.Fatalf("registered source passed authoritative inspection: %v", err)
			}
			if runner.createCalls != 0 || runner.destroyCalls != 0 {
				t.Fatal("registered source was mutated")
			}
		})
	}
}

func TestFilterZFSSourcesRequiresSelfContainedChildTrees(t *testing.T) {
	for _, scenario := range []string{"valid", "encrypted-child", "readonly-child", "volume-child", "unmounted-child", "outside-child", "external-bin", "external-lib"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			seedCreationRoot(t, root)
			if err := os.Mkdir(filepath.Join(root, "lib"), 0755); err != nil {
				t.Fatal(err)
			}
			source := &gzfs.Dataset{Name: "pool/root", GUID: "1", Type: gzfs.DatasetTypeFilesystem, Mountpoint: root,
				Properties: map[string]gzfs.ZFSProperty{"encryption": {Value: "off"}}}
			child := &gzfs.Dataset{Name: "pool/root/app", GUID: "2", Type: gzfs.DatasetTypeFilesystem, Mountpoint: filepath.Join(root, "var", "app"),
				Properties: map[string]gzfs.ZFSProperty{"encryption": {Value: "off"}}}
			switch scenario {
			case "encrypted-child":
				child.Properties["encryption"] = gzfs.ZFSProperty{Value: "on"}
			case "readonly-child":
				child.Properties["readonly"] = gzfs.ZFSProperty{Value: "on"}
			case "volume-child":
				child.Type = gzfs.DatasetTypeVolume
			case "outside-child":
				child.Mountpoint = t.TempDir()
			}
			if err := os.MkdirAll(child.Mountpoint, 0755); err != nil {
				t.Fatal(err)
			}
			mounts := fmt.Sprintf("%s %s zfs rw 0 0\n", source.Name, root)
			if scenario != "unmounted-child" {
				mounts += fmt.Sprintf("%s %s zfs rw 0 0\n", child.Name, child.Mountpoint)
			}
			if strings.HasPrefix(scenario, "external-") {
				mounts += fmt.Sprintf("/release %s nullfs ro 0 0\n", filepath.Join(root, strings.TrimPrefix(scenario, "external-")))
			}
			datasets := []*gzfs.Dataset{child, source,
				{Name: source.Name + "@existing", Type: gzfs.DatasetTypeSnapshot},
				{Name: child.Name + "@existing", Type: gzfs.DatasetTypeSnapshot},
			}
			sources := filterZFSSources(datasets, mounts)
			if scenario == "valid" {
				if len(sources) != 1 || sources[0].Name != source.Name {
					t.Fatalf("self-contained root excluded: %+v", sources)
				}
				tree, err := describeSourceTree(source, datasets)
				if err != nil || len(tree.Members) != 2 {
					t.Fatalf("ordinary snapshots counted as source members: %+v, %v", tree, err)
				}
			} else if len(sources) != 0 {
				t.Fatalf("unsupported tree accepted: %+v", sources)
			}
		})
	}
}
