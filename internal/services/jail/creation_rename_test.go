// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

func TestZFSSourceModeValidation(t *testing.T) {
	for _, mode := range []string{"", "copy", "rename", "move", "Rename"} {
		source := jailServiceInterfaces.ZFSSource{Dataset: "tank/root", GUID: "123", Mode: mode}
		err := validateZFSSourceIdentity(source)
		valid := mode == "" || mode == "copy" || mode == "rename"
		if valid && err != nil || !valid && (err == nil || err.Error() != "invalid_zfs_source_mode") {
			t.Fatalf("mode %q: %v", mode, err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		var decoded jailServiceInterfaces.ZFSSource
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != source {
			t.Fatalf("source mode lost: %+v, %v", decoded, err)
		}
	}
}

func TestRenameSourceRequiresSamePoolBeforeInspectingOrMutatingZFS(t *testing.T) {
	svc, runner, _, _ := newCreationTestService(t)
	source := jailServiceInterfaces.ZFSSource{Dataset: "otherpool/root", GUID: "123", Mode: "rename"}
	if _, err := svc.inspectZFSSource(t.Context(), source, "testpool", ""); err == nil || err.Error() != "zfs_source_rename_requires_same_pool" {
		t.Fatalf("cross-pool rename = %v", err)
	}
	if runner.createCalls != 0 || runner.destroyCalls != 0 {
		t.Fatal("cross-pool rename mutated ZFS")
	}
}

func TestZFSSourceOperationsReserveOverlappingSourceTrees(t *testing.T) {
	for _, dataset := range []string{"testpool/foreign", "testpool/foreign/dbdir", "testpool/foreign/dbdir/logs", "testpool/foreign-other"} {
		t.Run(dataset, func(t *testing.T) {
			svc, _, _, _ := newCreationTestService(t)
			ctID := uint(900)
			op := jailModels.JailCreation{
				ID: "rename-operation", CTID: ctID, ActiveCTID: &ctID, Phase: "queued", State: "{}",
				Request: `{"pool":"testpool","zfsSource":{"dataset":"testpool/foreign/dbdir","guid":"123","mode":"rename"}}`,
			}
			if err := svc.DB.Create(&op).Error; err != nil {
				t.Fatal(err)
			}
			source := jailServiceInterfaces.ZFSSource{Dataset: dataset, GUID: "123"}
			err := svc.requireSourceOperationAllowed(t.Context(), source, "")
			if dataset == "testpool/foreign-other" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "jail_creation_in_progress") {
				t.Fatalf("overlapping source not reserved: %v", err)
			}
			if err := svc.requireSourceOperationAllowed(t.Context(), source, op.ID); err != nil {
				t.Fatalf("operation blocked its own source: %v", err)
			}
		})
	}
}

func TestRenameSourceRejectsReferencedDescendants(t *testing.T) {
	svc, _, _, _ := newCreationTestService(t)
	if err := svc.DB.AutoMigrate(&vmModels.VMStorageDataset{}); err != nil {
		t.Fatal(err)
	}
	tree := sourceTree{Members: []sourceMember{
		{Dataset: "testpool/foreign", GUID: "root-guid"},
		{Dataset: "testpool/foreign/dbdir", GUID: "child-guid"},
	}}
	if err := svc.requireRenameSourceUnreferenced(t.Context(), tree); err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&vmModels.VMStorageDataset{Pool: "testpool", GUID: "child-guid", Name: "testpool/foreign/dbdir"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.requireRenameSourceUnreferenced(t.Context(), tree); err == nil || !strings.Contains(err.Error(), "zfs_source_protected") {
		t.Fatalf("referenced child accepted: %v", err)
	}
}

func TestRenameRollbackNeverDestroysAdoptedDatasets(t *testing.T) {
	svc, runner, req, taskID := newCreationTestService(t)
	if err := svc.PrepareCreateJail(t.Context(), taskID, req); err != nil {
		t.Fatal(err)
	}
	op, state, _, err := svc.loadCreation(taskID)
	if err != nil {
		t.Fatal(err)
	}
	state.Rename = &creationRename{}
	name := "testpool/sylve/jails/830"
	runner.datasets[name] = jailCreateTestZFSDataset{guid: "adopted-guid", owner: op.ID, mountpoint: t.TempDir()}
	state.Datasets = []creationDataset{{Name: name, GUID: "adopted-guid"}}
	if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("injected_failure")); err == nil {
		t.Fatal("incomplete rename journal did not block cleanup")
	}
	if runner.destroyCalls != 0 || !runner.hasDataset(name) {
		t.Fatal("adopted dataset was destroyed")
	}
	if err := svc.DB.First(op, "id = ?", op.ID).Error; err != nil || op.ActiveCTID == nil {
		t.Fatalf("destination reservation lost: %+v, %v", op, err)
	}
}

func TestCreationChildMountpointsAreCreatedWithoutFollowingSymlinks(t *testing.T) {
	for _, scenario := range []string{"missing", "existing", "symlink", "file"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "var")
			switch scenario {
			case "existing":
				if err := os.MkdirAll(filepath.Join(path, "db"), 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			confined, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer confined.Close()
			err = prepareCreationChildMountpoint(confined, "var/db/logs")
			if scenario == "missing" || scenario == "existing" {
				if err != nil {
					t.Fatal(err)
				}
				if info, err := os.Stat(filepath.Join(path, "db", "logs")); err != nil || !info.IsDir() {
					t.Fatalf("mountpoint missing: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "unsafe_child_mount_directory") {
				t.Fatalf("unsafe mountpoint accepted: %v", err)
			}
		})
	}
}
