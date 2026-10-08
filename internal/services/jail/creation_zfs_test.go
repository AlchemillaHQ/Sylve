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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func newRealZFSCreationService(t *testing.T, pool string, client *gzfs.Client) *Service {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	zfstest.EnsureDataset(t, client, pool+"/sylve/jails")
	db := newCreationTestDB(t)
	svc := &Service{DB: db, GZFS: client, System: jailCreateTestSystemService{pools: []*gzfs.ZPool{{Name: pool}}}, ctidHashByCTID: make(map[uint]string)}
	svc.SetGuestIdentityCoordinator(&clusterService.Service{DB: db, NodeID: "creation-node"})
	return svc
}

func creationSourceRequest(ds *gzfs.Dataset, pool string, ctID uint) jailServiceInterfaces.CreateJailRequest {
	req := jailCreateRequest(ctID, pool, "")
	req.ZFSSource = &jailServiceInterfaces.ZFSSource{Dataset: ds.Name, GUID: ds.GUID}
	return req
}

func requireCreationFile(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != want {
		t.Fatalf("%s: content=%q error=%v; want %q", path, content, err, want)
	}
}

func TestIntegrationZFSCreationCurrentTreeCustomMountpointsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	if err := os.WriteFile(filepath.Join(source.Mountpoint, "root-data"), []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	child, err := client.ZFS.CreateFilesystem(t.Context(), source.Name+"/app", map[string]string{"mountpoint": filepath.Join(source.Mountpoint, "var", "app-data")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.Mountpoint, "data"), []byte("child-original"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("root-data", filepath.Join(source.Mountpoint, "data-link")); err != nil {
		t.Fatal(err)
	}
	if err := source.SetProperties(t.Context(), "sylve:replication-run-id", "foreign-run"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateJail(t.Context(), creationSourceRequest(source, pool, 850)); err != nil {
		t.Fatal(err)
	}
	root, err := client.ZFS.Get(t.Context(), pool+"/sylve/jails/850", false)
	if err != nil {
		t.Fatal(err)
	}
	requireCreationFile(t, filepath.Join(root.Mountpoint, "root-data"), "original")
	requireCreationFile(t, filepath.Join(root.Mountpoint, "var", "app-data", "data"), "child-original")
	if root.Properties["origin"].Value != "-" {
		t.Fatalf("dependent copy: %s", root.Properties["origin"].Value)
	}
	provenance, err := client.ZFS.GetProperty(t.Context(), root.Name, "sylve:replication-run-id")
	if err != nil || provenance.Value != "-" {
		t.Fatalf("foreign provenance copied: %+v, %v", provenance, err)
	}
	childCopy, err := client.ZFS.Get(t.Context(), root.Name+"/app", false)
	if err != nil || childCopy.Mountpoint != filepath.Join(root.Mountpoint, "var", "app-data") {
		t.Fatalf("child mapping = %+v, %v", childCopy, err)
	}
	for _, ds := range []*gzfs.Dataset{root, childCopy} {
		canmount, err := client.ZFS.GetProperty(t.Context(), ds.Name, "canmount")
		if err != nil || canmount.Value != "on" {
			t.Fatalf("normal mount behavior not restored: %+v, %v", canmount, err)
		}
	}
	link, err := os.Readlink(filepath.Join(root.Mountpoint, "data-link"))
	if err != nil || link != "root-data" {
		t.Fatalf("symlink = %q, %v", link, err)
	}
	info, err := os.Stat(filepath.Join(root.Mountpoint, "root-data"))
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("file mode not preserved: %v, %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(root.Mountpoint, "root-data"), []byte("copied-only"), 0640); err != nil {
		t.Fatal(err)
	}
	requireCreationFile(t, filepath.Join(source.Mountpoint, "root-data"), "original")
	requireCreationFile(t, filepath.Join(child.Mountpoint, "data"), "child-original")
	snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("temporary snapshots stranded: %+v, %v", snapshots, err)
	}
	var created jailModels.Jail
	if err := svc.DB.Where("ct_id = ?", 850).First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.CreationPending || created.StartAtBoot != nil && *created.StartAtBoot {
		t.Fatalf("unexpected automatic start configuration: %+v", created)
	}
	if _, err := svc.CreateJailSnapshot(t.Context(), 850, "normal-snapshot", "copied jail"); err != nil {
		t.Fatalf("normal jail snapshot failed: %v", err)
	}
}

func TestIntegrationZFSCreationCopiesCurrentContentsAndPreservesExistingSnapshotsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	zpool, err := client.Zpool.Get(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := zpool.SetProperty(t.Context(), "listsnapshots", "on"); err != nil {
		t.Fatal(err)
	}
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	if err := os.WriteFile(filepath.Join(source.Mountpoint, "version"), []byte("historical"), 0600); err != nil {
		t.Fatal(err)
	}
	child, err := client.ZFS.CreateFilesystem(t.Context(), source.Name+"/app", map[string]string{"mountpoint": filepath.Join(source.Mountpoint, "var", "app")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.Mountpoint, "version"), []byte("historical-child"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(t.Context(), "existing", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Mountpoint, "version"), []byte("live"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.Mountpoint, "version"), []byte("live-child"), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := svc.ListZFSSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range sources {
		if candidate.Name == source.Name && candidate.GUID == source.GUID {
			found = true
		}
	}
	if !found {
		t.Fatal("ordinary snapshots hid a valid source root")
	}
	if err := svc.CreateJail(t.Context(), creationSourceRequest(source, pool, 851)); err != nil {
		t.Fatal(err)
	}
	copy, err := client.ZFS.Get(t.Context(), pool+"/sylve/jails/851", false)
	if err != nil {
		t.Fatal(err)
	}
	requireCreationFile(t, filepath.Join(copy.Mountpoint, "version"), "live")
	requireCreationFile(t, filepath.Join(copy.Mountpoint, "var", "app", "version"), "live-child")
	requireCreationFile(t, filepath.Join(source.Mountpoint, "version"), "live")
	if _, err := os.Stat(filepath.Join(copy.Mountpoint, "etc", "rc")); err != nil {
		t.Fatal("current root wasn't copied")
	}
	after, err := client.ZFS.Get(t.Context(), snapshot.Name, false)
	if err != nil || after.GUID != snapshot.GUID {
		t.Fatalf("user snapshot changed: %+v, %v", after, err)
	}
	holds, _, err := svc.creationZFS().RunBytes(t.Context(), nil, "holds", "-H", snapshot.Name)
	if err != nil || len(holds) != 0 {
		t.Fatalf("operation hold leaked: %s, %v", holds, err)
	}
	snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("existing snapshots removed or temporary snapshots stranded: %+v, %v", snapshots, err)
	}
	copiedSnapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, copy.Name)
	if err != nil || len(copiedSnapshots) != 0 {
		t.Fatalf("snapshot history imported: %+v, %v", copiedSnapshots, err)
	}
}

func TestIntegrationZFSCreationSnapshotCleanupRequiresOwnershipRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	op := &jailModels.JailCreation{ID: "snapshot-cleanup"}
	for _, scenario := range []string{"owned", "foreign-owner", "inherited-owner", "changed-guid"} {
		t.Run(scenario, func(t *testing.T) {
			source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/"+scenario, map[string]string{creationOwnerProperty: op.ID})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := source.Snapshot(t.Context(), "sylve_create_cleanup", false)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "inherited-owner" {
				owner := op.ID
				if scenario == "foreign-owner" {
					owner = "another-operation"
				}
				if err := snapshot.SetProperties(t.Context(), creationOwnerProperty, owner); err != nil {
					t.Fatal(err)
				}
			}
			var state creationState
			journal := fmt.Sprintf(`{"snapshots":[{"name":%q,"guid":%q,"created":true}]}`, snapshot.Name, snapshot.GUID)
			if err := json.Unmarshal([]byte(journal), &state); err != nil {
				t.Fatal(err)
			}
			if scenario == "changed-guid" {
				state.Snapshots[0].GUID = "another-snapshot"
			}
			hold := "sylve_create_" + op.ID
			if _, _, err := svc.creationZFS().RunBytes(t.Context(), nil, "hold", hold, snapshot.Name); err != nil {
				t.Fatal(err)
			}
			err = svc.cleanupCreationSnapshots(t.Context(), op, &state)
			if scenario == "owned" {
				if err != nil {
					t.Fatal(err)
				}
				if actual, err := client.ZFS.Get(t.Context(), snapshot.Name, false); actual != nil || !isZFSDatasetMissingError(err) {
					t.Fatalf("owned snapshot retained: %+v, %v", actual, err)
				}
				if err := svc.cleanupCreationSnapshots(t.Context(), op, &state); err != nil {
					t.Fatalf("repeated cleanup: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unverified snapshot cleanup succeeded")
			}
			if actual, err := client.ZFS.Get(t.Context(), snapshot.Name, false); err != nil || actual.GUID != snapshot.GUID {
				t.Fatalf("unverified snapshot changed: %+v, %v", actual, err)
			}
			holds, _, err := svc.creationZFS().RunBytes(t.Context(), nil, "holds", "-H", snapshot.Name)
			if err != nil || !strings.Contains(string(holds), hold) {
				t.Fatalf("unverified snapshot hold changed: %s, %v", holds, err)
			}
			if _, _, err := svc.creationZFS().RunBytes(t.Context(), nil, "release", hold, snapshot.Name); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIntegrationZFSCreationCloneSourceAcrossPoolsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	sourcePool, client := zfstest.DedicatedPool(t)
	destPool, _ := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, destPool, client)
	origin, err := client.ZFS.CreateFilesystem(t.Context(), sourcePool+"/origin", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, origin.Mountpoint)
	snapshot, err := origin.Snapshot(t.Context(), "origin", false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := client.ZFS.Clone(t.Context(), snapshot.Name, sourcePool+"/foreign-clone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateJail(t.Context(), creationSourceRequest(source, destPool, 852)); err != nil {
		t.Fatal(err)
	}
	copy, err := client.ZFS.Get(t.Context(), destPool+"/sylve/jails/852", false)
	if err != nil || copy.Properties["origin"].Value != "-" {
		t.Fatalf("copy not independent: %+v, %v", copy, err)
	}
	after, err := client.ZFS.Get(t.Context(), source.Name, false)
	if err != nil || after.GUID != source.GUID || after.Properties["origin"].Value != snapshot.Name {
		t.Fatalf("clone source changed: %+v, %v", after, err)
	}
}

type failCreationReceiveRunner struct {
	calls  int
	failAt int
}

func (r *failCreationReceiveRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	err := (gzfs.LocalRunner{}).Run(ctx, stdin, stdout, stderr, name, args...)
	if len(args) > 0 && args[0] == "receive" {
		r.calls++
		if err == nil && r.calls == r.failAt {
			return fmt.Errorf("injected_receive_failure")
		}
	}
	return err
}

func TestIntegrationZFSCreationReceiveFailurePreservesSourceRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	if _, err := client.ZFS.CreateFilesystem(t.Context(), source.Name+"/app", map[string]string{"mountpoint": filepath.Join(source.Mountpoint, "var", "app")}); err != nil {
		t.Fatal(err)
	}
	svc.creationCommand = &gzfs.Cmd{Bin: "zfs", Runner: &failCreationReceiveRunner{failAt: 2}}
	err = svc.CreateJail(t.Context(), creationSourceRequest(source, pool, 853))
	if err == nil || !strings.Contains(err.Error(), "injected_receive_failure") {
		t.Fatalf("copy failure = %v", err)
	}
	var op jailModels.JailCreation
	if err := svc.DB.First(&op).Error; err != nil {
		t.Fatal(err)
	}
	if op.ActiveCTID != nil {
		t.Fatalf("cleanup incomplete: %s", op.Error)
	}
	destinations, err := client.ZFS.List(t.Context(), true, pool+"/sylve/jails")
	if err != nil || len(destinations) != 1 {
		t.Fatalf("partial destination stranded: %+v, %v", destinations, err)
	}
	snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("temporary source snapshots stranded: %+v, %v", snapshots, err)
	}
	after, err := client.ZFS.Get(t.Context(), source.Name, false)
	if err != nil || after.GUID != source.GUID {
		t.Fatalf("source changed: %+v, %v", after, err)
	}
	assertModelCount(t, svc.DB, &jailModels.Jail{}, 0, "")
}

func TestIntegrationZFSCreationCuratedSourcesRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	child, err := client.ZFS.CreateFilesystem(t.Context(), source.Name+"/app", map[string]string{"mountpoint": filepath.Join(source.Mountpoint, "var", "app")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/empty", nil); err != nil {
		t.Fatal(err)
	}
	managed, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/sylve/jails/854", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, managed.Mountpoint)
	if err := svc.DB.Create(&jailModels.Jail{CTID: 854, Name: "registered-root", Type: jailModels.JailTypeFreeBSD}).Error; err != nil {
		t.Fatal(err)
	}
	orphan, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/sylve/jails/855", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, orphan.Mountpoint)
	sources, err := svc.ListZFSSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var local []*gzfs.Dataset
	for _, candidate := range sources {
		if strings.HasPrefix(candidate.Name, pool+"/") {
			local = append(local, candidate)
		}
	}
	if len(local) != 2 || local[0].Name != source.Name || local[0].GUID != source.GUID || local[1].Name != orphan.Name || local[1].GUID != orphan.GUID {
		t.Fatalf("curated pool sources = %+v; child=%s managed=%s", local, child.Name, managed.Name)
	}
	snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("discovery modified source snapshots: %+v, %v", snapshots, err)
	}
}

func TestIntegrationZFSCreationCopiesOrphanedCanonicalRootRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/sylve/jails/858", map[string]string{creationOwnerProperty: "committed-operation"})
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, source.Mountpoint)
	if err := os.WriteFile(filepath.Join(source.Mountpoint, "source-data"), []byte("preserved orphan"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := svc.DB.Create(&jailModels.JailCreation{ID: "committed-operation", CTID: 858, Request: `{"pool":"` + pool + `"}`, State: "{}", Phase: "committed"}).Error; err != nil {
		t.Fatal(err)
	}
	sources, err := svc.ListZFSSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range sources {
		if candidate.Name == source.Name && candidate.GUID == source.GUID {
			found = true
		}
	}
	if !found {
		t.Fatal("committed orphan root was not discoverable")
	}
	if err := svc.CreateJail(t.Context(), creationSourceRequest(source, pool, 859)); err != nil {
		t.Fatal(err)
	}
	copy, err := client.ZFS.Get(t.Context(), pool+"/sylve/jails/859", false)
	if err != nil || copy == nil {
		t.Fatalf("copy = %+v, %v", copy, err)
	}
	if copy.GUID == source.GUID || copy.Properties["origin"].Value != "-" {
		t.Fatalf("orphan was adopted or cloned instead of copied: %+v", copy)
	}
	requireCreationFile(t, filepath.Join(copy.Mountpoint, "source-data"), "preserved orphan")
	if err := os.WriteFile(filepath.Join(copy.Mountpoint, "source-data"), []byte("copy only"), 0640); err != nil {
		t.Fatal(err)
	}
	requireCreationFile(t, filepath.Join(source.Mountpoint, "source-data"), "preserved orphan")
	owner, err := client.ZFS.GetProperty(t.Context(), source.Name, creationOwnerProperty)
	if err != nil || owner.Value != "committed-operation" {
		t.Fatalf("source ownership changed: %+v, %v", owner, err)
	}
	snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("temporary source snapshots stranded: %+v, %v", snapshots, err)
	}
}

func TestIntegrationZFSCreationRejectsEncryptedRootAndDescendantsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client := zfstest.DedicatedPool(t)
	svc := newRealZFSCreationService(t, pool, client)
	key := filepath.Join(t.TempDir(), "fixture-key")
	if err := os.WriteFile(key, []byte("test-only-jail-copy-passphrase\n"), 0600); err != nil {
		t.Fatal(err)
	}
	properties := map[string]string{"encryption": "on", "keyformat": "passphrase", "keylocation": "file://" + key}
	source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/encrypted", properties)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ValidateCreate(t.Context(), creationSourceRequest(source, pool, 856)); err == nil || !strings.Contains(err.Error(), "encryption_unsupported") {
		t.Fatalf("unlocked encrypted root accepted: %v", err)
	}
	plain, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	seedCreationRoot(t, plain.Mountpoint)
	properties["mountpoint"] = filepath.Join(plain.Mountpoint, "var", "encrypted")
	if _, err := client.ZFS.CreateFilesystem(t.Context(), plain.Name+"/encrypted", properties); err != nil {
		t.Fatal(err)
	}
	if err := svc.ValidateCreate(t.Context(), creationSourceRequest(plain, pool, 857)); err == nil || !strings.Contains(err.Error(), "encryption_unsupported") {
		t.Fatalf("unlocked encrypted child accepted: %v", err)
	}
}

type interruptCreationRunner struct{ command string }

func (r interruptCreationRunner) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	err := (gzfs.LocalRunner{}).Run(ctx, stdin, stdout, stderr, name, args...)
	if err == nil && len(args) > 0 && args[0] == r.command {
		panic("simulated_creation_process_exit")
	}
	return err
}

func TestIntegrationZFSCreationRestartReconcilesOwnedPartialOutputRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	for _, command := range []string{"receive", "rename"} {
		t.Run(command, func(t *testing.T) {
			pool, client := zfstest.DedicatedPool(t)
			svc := newRealZFSCreationService(t, pool, client)
			source, err := client.ZFS.CreateFilesystem(t.Context(), pool+"/foreign", nil)
			if err != nil {
				t.Fatal(err)
			}
			seedCreationRoot(t, source.Mountpoint)
			svc.creationCommand = &gzfs.Cmd{Bin: "zfs", Runner: interruptCreationRunner{command: command}}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("creation wasn't interrupted")
					}
				}()
				if err := svc.CreateJail(t.Context(), creationSourceRequest(source, pool, 858)); err != nil {
					t.Fatalf("creation failed before interruption: %v", err)
				}
			}()
			restarted := &Service{DB: svc.DB, GZFS: client}
			restarted.SetGuestIdentityCoordinator(&clusterService.Service{DB: svc.DB, NodeID: "creation-node"})
			if err := restarted.RecoverCreations(t.Context()); err != nil {
				t.Fatal(err)
			}
			var op jailModels.JailCreation
			if err := svc.DB.First(&op).Error; err != nil {
				t.Fatal(err)
			}
			if op.ActiveCTID != nil || op.Phase != "failed" {
				t.Fatalf("operation = %+v", op)
			}
			destinations, err := client.ZFS.List(t.Context(), true, pool+"/sylve/jails")
			if err != nil || len(destinations) != 1 {
				t.Fatalf("destination cleanup: %+v %v", destinations, err)
			}
			snapshots, err := client.ZFS.ListByType(t.Context(), gzfs.DatasetTypeSnapshot, true, source.Name)
			if err != nil || len(snapshots) != 0 {
				t.Fatalf("source snapshot cleanup: %+v %v", snapshots, err)
			}
			after, err := client.ZFS.Get(t.Context(), source.Name, false)
			if err != nil || after.GUID != source.GUID {
				t.Fatalf("source identity changed: %+v %v", after, err)
			}
		})
	}
}
