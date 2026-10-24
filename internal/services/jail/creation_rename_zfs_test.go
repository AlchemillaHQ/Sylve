// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"gorm.io/gorm"
)

func newRenameSourceFixture(t *testing.T) (*Service, []*gzfs.Dataset, jailServiceInterfaces.CreateJailRequest) {
	t.Helper()
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	poolRoot, err := client.ZFS.Get(t.Context(), pool, false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", map[string]string{
		"mountpoint": filepath.Join(poolRoot.Mountpoint, "custom-source"), "canmount": "noauto", creationOwnerProperty: "previous-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Mount(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	child, err := client.ZFS.CreateFilesystem(t.Context(), source.Name+"/dbdir", map[string]string{"mountpoint": filepath.Join(source.Mountpoint, "var", "db")})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := client.ZFS.CreateFilesystem(t.Context(), child.Name+"/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ds := range []*gzfs.Dataset{source, child, grandchild} {
		if err := os.WriteFile(filepath.Join(ds.Mountpoint, "data"), []byte(ds.GUID), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(source.Mountpoint, ".sylve", "host-config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Mountpoint, ".sylve", "host-config", "old"), []byte("original configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Snapshot(t.Context(), "user-history", true); err != nil {
		t.Fatal(err)
	}
	req := creationSourceRequest(source, pool, 880)
	req.ZFSSource.Mode = "rename"
	return svc, []*gzfs.Dataset{source, child, grandchild}, req
}

func requireRenameDataset(t *testing.T, svc *Service, name, guid, mountpoint string) {
	t.Helper()
	ds, err := svc.GZFS.ZFS.Get(t.Context(), name, false)
	if err != nil || ds == nil || ds.GUID != guid || ds.Mountpoint != mountpoint {
		t.Fatalf("dataset %s: %+v, %v; want GUID=%s mountpoint=%s", name, ds, err, guid, mountpoint)
	}
	requireCreationFile(t, filepath.Join(mountpoint, "data"), guid)
}

func TestIntegrationZFSCreationRenamesNestedTreeRealZFS(t *testing.T) {
	svc, source, req := newRenameSourceFixture(t)
	snapshots := make(map[string]string)
	for _, ds := range source {
		snapshot, err := svc.GZFS.ZFS.Get(t.Context(), ds.Name+"@user-history", false)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[ds.Name] = snapshot.GUID
	}
	if err := svc.CreateJail(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	rootName := fmt.Sprintf("%s/sylve/jails/%d", req.Pool, *req.CTID)
	root, err := svc.GZFS.ZFS.Get(t.Context(), rootName, false)
	if err != nil || root == nil {
		t.Fatalf("renamed root: %+v, %v", root, err)
	}
	for _, ds := range source {
		name := rootName + strings.TrimPrefix(ds.Name, source[0].Name)
		rel, err := filepath.Rel(source[0].Mountpoint, ds.Mountpoint)
		if err != nil {
			t.Fatal(err)
		}
		requireRenameDataset(t, svc, name, ds.GUID, filepath.Join(root.Mountpoint, rel))
		if old, err := svc.GZFS.ZFS.Get(t.Context(), ds.Name, false); old != nil || !isZFSDatasetMissingError(err) {
			t.Fatalf("old dataset name retained: %+v, %v", old, err)
		}
		snapshot, err := svc.GZFS.ZFS.Get(t.Context(), name+"@user-history", false)
		if err != nil || snapshot.GUID != snapshots[ds.Name] {
			t.Fatalf("snapshot not preserved: %+v, %v", snapshot, err)
		}
		canmount, err := svc.GZFS.ZFS.GetProperty(t.Context(), name, "canmount")
		if err != nil || canmount.Value != "on" {
			t.Fatalf("normal mount behavior not restored: %+v, %v", canmount, err)
		}
	}
	remaining, err := svc.GZFS.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, rootName)
	if err != nil || len(remaining) != len(source) {
		t.Fatalf("temporary snapshots stranded: %+v, %v", remaining, err)
	}
	if _, err := os.Stat(filepath.Join(root.Mountpoint, ".sylve", "host-config", "old")); !os.IsNotExist(err) {
		t.Fatalf("stale host configuration retained: %v", err)
	}
	if _, err := svc.CreateJailSnapshot(t.Context(), *req.CTID, "managed-snapshot", "renamed jail"); err != nil {
		t.Fatalf("normal jail snapshot failed: %v", err)
	}
}

func TestIntegrationZFSCreationCopiesNestedTreeRealZFS(t *testing.T) {
	svc, source, req := newRenameSourceFixture(t)
	req.ZFSSource.Mode = "copy"
	if err := svc.CreateJail(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	root, err := svc.GZFS.ZFS.Get(t.Context(), fmt.Sprintf("%s/sylve/jails/%d", req.Pool, *req.CTID), false)
	if err != nil || root == nil {
		t.Fatalf("copied root: %+v, %v", root, err)
	}
	for _, ds := range source {
		requireRenameDataset(t, svc, ds.Name, ds.GUID, ds.Mountpoint)
		name := root.Name + strings.TrimPrefix(ds.Name, source[0].Name)
		copy, err := svc.GZFS.ZFS.Get(t.Context(), name, false)
		if err != nil || copy == nil || copy.GUID == ds.GUID || copy.Properties["origin"].Value != "-" {
			t.Fatalf("child not copied independently: %+v, %v", copy, err)
		}
		rel, err := filepath.Rel(source[0].Mountpoint, ds.Mountpoint)
		if err != nil || copy.Mountpoint != filepath.Join(root.Mountpoint, rel) {
			t.Fatalf("child mountpoint not relocated: %s, %v", copy.Mountpoint, err)
		}
		requireCreationFile(t, filepath.Join(copy.Mountpoint, "data"), ds.GUID)
	}
	if err := os.WriteFile(filepath.Join(root.Mountpoint, "var", "db", "logs", "data"), []byte("copy only"), 0600); err != nil {
		t.Fatal(err)
	}
	requireCreationFile(t, filepath.Join(source[2].Mountpoint, "data"), source[2].GUID)
}

func TestIntegrationZFSCreationRenameRollbackRestoresNestedTreeRealZFS(t *testing.T) {
	for _, recovery := range []string{"failure", "restart"} {
		t.Run(recovery, func(t *testing.T) {
			svc, source, req := newRenameSourceFixture(t)
			properties := make(map[string]map[string]gzfs.ZFSProperty)
			for _, ds := range source {
				properties[ds.Name] = make(map[string]gzfs.ZFSProperty)
				for _, key := range renameSourceProperties {
					property, err := svc.GZFS.ZFS.GetProperty(t.Context(), ds.Name, key)
					if err != nil {
						t.Fatal(err)
					}
					properties[ds.Name][key] = property
				}
			}
			op, state, err := svc.prepareCreation(t.Context(), nil, req)
			if err != nil {
				t.Fatal(err)
			}
			root, mountpoint, err := svc.provisionJailRoot(t.Context(), req, op, state)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"data", "var/db/data", "var/db/logs/data", "etc/rc.conf"} {
				if err := os.WriteFile(filepath.Join(mountpoint, path), []byte("creation changed this"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if recovery == "failure" {
				if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("injected_configuration_failure")); err != nil {
					t.Fatal(err)
				}
				if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("repeated_cleanup")); err != nil {
					t.Fatalf("repeated cleanup: %v", err)
				}
			} else {
				restarted := &Service{DB: svc.DB, GZFS: svc.GZFS}
				restarted.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
				if err := restarted.RecoverCreations(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			for _, ds := range source {
				requireRenameDataset(t, svc, ds.Name, ds.GUID, ds.Mountpoint)
				for _, key := range renameSourceProperties {
					property, err := svc.GZFS.ZFS.GetProperty(t.Context(), ds.Name, key)
					if err != nil || property != properties[ds.Name][key] {
						t.Fatalf("%s %s: %+v, %v; want %+v", ds.Name, key, property, err, properties[ds.Name][key])
					}
				}
			}
			requireCreationFile(t, filepath.Join(source[0].Mountpoint, ".sylve", "host-config", "old"), "original configuration")
			if _, err := os.Stat(filepath.Join(source[0].Mountpoint, "etc", "rc.conf")); !os.IsNotExist(err) {
				t.Fatalf("new configuration not rolled back: %v", err)
			}
			if ds, err := svc.GZFS.ZFS.Get(t.Context(), root.Name, false); ds != nil || !isZFSDatasetMissingError(err) {
				t.Fatalf("destination retained: %+v, %v", ds, err)
			}
			if err := svc.DB.First(op, "id = ?", op.ID).Error; err != nil || op.ActiveCTID != nil {
				t.Fatalf("cleanup not complete: %+v, %v", op, err)
			}
			remaining, err := svc.GZFS.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source[0].Name)
			if err != nil || len(remaining) != len(source) {
				t.Fatalf("snapshot history changed: %+v, %v", remaining, err)
			}
		})
	}
}

func TestIntegrationZFSCreationRenameInterruptedBeforeConfigurationRealZFS(t *testing.T) {
	for _, command := range []string{"snapshot", "rename"} {
		t.Run(command, func(t *testing.T) {
			svc, source, req := newRenameSourceFixture(t)
			svc.creationCommand = &gzfs.Cmd{Bin: "zfs", Runner: interruptCreationRunner{command: command}}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("creation wasn't interrupted")
					}
				}()
				if err := svc.CreateJail(t.Context(), req); err != nil {
					t.Fatalf("creation failed before interruption: %v", err)
				}
			}()
			restarted := &Service{DB: svc.DB, GZFS: svc.GZFS}
			restarted.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
			if err := restarted.RecoverCreations(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, ds := range source {
				requireRenameDataset(t, svc, ds.Name, ds.GUID, ds.Mountpoint)
			}
			var op jailModels.JailCreation
			if err := svc.DB.First(&op).Error; err != nil || op.ActiveCTID != nil {
				t.Fatalf("cleanup not complete: %+v, %v", op, err)
			}
		})
	}
}

func TestIntegrationZFSCreationRenameAmbiguousRecoveryRetainsTreeRealZFS(t *testing.T) {
	for _, scenario := range []string{"source-collision", "unknown-child", "newer-snapshot"} {
		t.Run(scenario, func(t *testing.T) {
			svc, source, req := newRenameSourceFixture(t)
			op, state, err := svc.prepareCreation(t.Context(), nil, req)
			if err != nil {
				t.Fatal(err)
			}
			root, mountpoint, err := svc.provisionJailRoot(t.Context(), req, op, state)
			if err != nil {
				t.Fatal(err)
			}
			var conflicting *gzfs.Dataset
			switch scenario {
			case "source-collision":
				conflicting, err = svc.GZFS.ZFS.CreateFilesystem(t.Context(), source[0].Name, nil)
			case "unknown-child":
				conflicting, err = svc.GZFS.ZFS.CreateFilesystem(t.Context(), root.Name+"/unexpected", nil)
			case "newer-snapshot":
				conflicting, err = root.Snapshot(t.Context(), "new-user-snapshot", false)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.failCreation(t.Context(), op, state, fmt.Errorf("injected_failure")); err == nil || !strings.Contains(err.Error(), "jail_creation_cleanup_blocked") {
				t.Fatalf("ambiguous cleanup succeeded: %v", err)
			}
			ds, err := svc.GZFS.ZFS.Get(t.Context(), root.Name, false)
			if err != nil || ds == nil || ds.GUID != source[0].GUID {
				t.Fatalf("adopted tree lost: %+v, %v", ds, err)
			}
			if ds, err := svc.GZFS.ZFS.Get(t.Context(), conflicting.Name, false); err != nil || ds == nil || ds.GUID != conflicting.GUID {
				t.Fatalf("conflicting resource lost: %+v, %v", ds, err)
			}
			if err := svc.DB.First(op, "id = ?", op.ID).Error; err != nil || op.ActiveCTID == nil {
				t.Fatalf("destination reservation lost: %+v, %v", op, err)
			}
			if scenario != "newer-snapshot" {
				requireCreationFile(t, filepath.Join(mountpoint, "var", "db", "data"), source[1].GUID)
			}
		})
	}
}

func TestIntegrationZFSCreationRenamePreservesCloneOriginRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	origin, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/origin", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, origin.Mountpoint)
	snapshot, err := origin.Snapshot(t.Context(), "origin", false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := client.ZFS.Clone(t.Context(), snapshot.Name, pool+"/foreign-clone", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := creationSourceRequest(source, pool, 881)
	req.ZFSSource.Mode = "rename"
	if err := svc.CreateJail(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	renamed, err := client.ZFS.Get(t.Context(), pool+"/sylve/jails/881", false)
	if err != nil || renamed == nil || renamed.GUID != source.GUID || renamed.Properties["origin"].Value != snapshot.Name {
		t.Fatalf("clone relationship changed: %+v, %v", renamed, err)
	}
}

func TestIntegrationZFSCreationRenameConfigurationFailureRestoresOriginalRealZFS(t *testing.T) {
	svc, source, req := newRenameSourceFixture(t)
	req.ResolvConf = "nameserver 192.0.2.1\n"
	const callback = "test:fail_rename_configuration"
	if err := svc.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if updates, ok := tx.Statement.Dest.(map[string]any); ok && tx.Statement.Table == "jail_creations" && updates["phase"] == "ready" {
			tx.AddError(fmt.Errorf("injected_configuration_failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := svc.CreateJail(t.Context(), req)
	if err := svc.DB.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if err == nil || !strings.Contains(err.Error(), "injected_configuration_failure") {
		t.Fatalf("creation = %v", err)
	}
	for _, ds := range source {
		requireRenameDataset(t, svc, ds.Name, ds.GUID, ds.Mountpoint)
	}
	requireCreationFile(t, filepath.Join(source[0].Mountpoint, ".sylve", "host-config", "old"), "original configuration")
	if _, err := os.Stat(filepath.Join(source[0].Mountpoint, "etc", "resolv.conf")); !os.IsNotExist(err) {
		t.Fatalf("created resolver configuration retained: %v", err)
	}
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 0, "")
	assertModelCount(t, svc.DB, &jailModels.Storage{}, 0, "")
	var op jailModels.JailCreation
	if err := svc.DB.First(&op).Error; err != nil || op.ActiveCTID != nil {
		t.Fatalf("cleanup not complete: %+v, %v", op, err)
	}
}

func TestIntegrationZFSCreationRenameReadyRecoveryFinishesWithoutRollbackRealZFS(t *testing.T) {
	svc, source, req := newRenameSourceFixture(t)
	const callback = "test:fail_rename_commit"
	if err := svc.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if updates, ok := tx.Statement.Dest.(map[string]any); ok && tx.Statement.Table == "jail_creations" && updates["phase"] == "committed" {
			tx.AddError(fmt.Errorf("injected_commit_failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := svc.CreateJail(t.Context(), req)
	if err := svc.DB.Callback().Update().Remove(callback); err != nil {
		t.Fatal(err)
	}
	if err == nil || !strings.Contains(err.Error(), "jail_creation_completion_pending") {
		t.Fatalf("creation = %v", err)
	}
	rootName := fmt.Sprintf("%s/sylve/jails/%d", req.Pool, *req.CTID)
	snapshots, err := svc.GZFS.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, rootName)
	if err != nil || len(snapshots) != len(source) {
		t.Fatalf("rollback snapshots were not cleaned before commit: %+v, %v", snapshots, err)
	}
	restarted := &Service{DB: svc.DB, GZFS: svc.GZFS}
	restarted.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
	if err := restarted.RecoverCreations(t.Context()); err != nil {
		t.Fatal(err)
	}
	root, err := svc.GZFS.ZFS.Get(t.Context(), rootName, false)
	if err != nil || root == nil || root.GUID != source[0].GUID {
		t.Fatalf("ready tree was not retained: %+v, %v", root, err)
	}
	requireCreationFile(t, filepath.Join(root.Mountpoint, "var", "db", "logs", "data"), source[2].GUID)
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 1, "creation_pending = ?", false)
	var op jailModels.JailCreation
	if err := svc.DB.First(&op).Error; err != nil || op.ActiveCTID != nil || op.Phase != "committed" {
		t.Fatalf("commit not complete: %+v, %v", op, err)
	}
}
