// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

const (
	testSylveKeyUUID  = "0b5e2f2e-3c4d-5e6f-8a9b-0c1d2e3f4a5b"
	testSylveKeyUUID2 = "1c6f3a3f-4d5e-4f70-9bac-1d2e3f4a5b6c"
)

func setEncryptionKeyDir(t *testing.T, dir string) {
	orig := EncryptionKeyDirectory
	EncryptionKeyDirectory = dir
	t.Cleanup(func() { EncryptionKeyDirectory = orig })
}

func newEncryptionTestService(t *testing.T, client *gzfs.Client) (*Service, *cluster.Service) {
	t.Helper()
	db := newZeltaServiceTestDB(t, &clusterModels.EncryptionKey{}, &clusterModels.EncryptionKeyTombstone{})
	clusterSvc := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	return &Service{DB: db, GZFS: client, Cluster: clusterSvc}, clusterSvc
}

func TestSylveKeyName(t *testing.T) {
	for name, want := range map[string]bool{
		testSylveKeyUUID:                       true,
		"restore-0123456789abcdef01234567":     true,
		"zroot_sylve":                          false,
		"restore-0123":                         false,
		"0B5E2F2E-3C4D-5E6F-8A9B-0C1D2E3F4A5B": false,
		"":                                     false,
	} {
		if got := sylveKeyName(name); got != want {
			t.Errorf("sylveKeyName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestReconcileEncryptionKeys(t *testing.T) {
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	s, _ := newEncryptionTestService(t, nil)

	if err := s.ReconcileEncryptionKeys(); err != nil {
		t.Fatalf("reconcile an empty store: %v", err)
	}

	for _, key := range []clusterModels.EncryptionKey{
		{UUID: testSylveKeyUUID, KeyData: "sylve-key", KeyFormat: "passphrase"},
		{UUID: testSylveKeyUUID2, KeyData: "cluster-key", KeyFormat: "passphrase"},
		{UUID: "zroot_sylve", KeyData: "copied-user-key", KeyFormat: "passphrase"},
	} {
		if err := s.DB.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
	}
	differing := filepath.Join(keyDir, testSylveKeyUUID2)
	if err := os.WriteFile(differing, []byte("this-node's-own-key"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := s.ReconcileEncryptionKeys(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(keyDir, testSylveKeyUUID)); err != nil || string(data) != "sylve-key" {
		t.Fatalf("missing Sylve key file = %q, %v", data, err)
	}
	if data, _ := os.ReadFile(differing); string(data) != "this-node's-own-key" {
		t.Fatalf("a differing key file was replaced: %q", data)
	}
	if _, err := os.Stat(filepath.Join(keyDir, "zroot_sylve")); !os.IsNotExist(err) {
		t.Fatalf("a copied user key was written to the node: %v", err)
	}
}

func TestEnsureEncryptionKeyFile(t *testing.T) {
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	s, _ := newEncryptionTestService(t, nil)
	if err := s.DB.Create(&clusterModels.EncryptionKey{
		UUID: testSylveKeyUUID, KeyData: "sylve-key", KeyFormat: "passphrase",
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"  ", "zroot_sylve"} {
		if err := s.EnsureEncryptionKeyFile(name); err == nil {
			t.Fatalf("EnsureEncryptionKeyFile(%q) wrote a file Sylve doesn't own", name)
		}
	}
	if err := s.EnsureEncryptionKeyFile(testSylveKeyUUID2); err == nil ||
		!strings.Contains(err.Error(), "encryption_key_not_found_in_cluster_store") {
		t.Fatalf("missing key error = %v", err)
	}
	if err := s.EnsureEncryptionKeyFile(testSylveKeyUUID); err != nil {
		t.Fatalf("write the key file: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(keyDir, testSylveKeyUUID)); err != nil || string(data) != "sylve-key" {
		t.Fatalf("key file = %q, %v", data, err)
	}
	if err := s.EnsureEncryptionKeyFile(testSylveKeyUUID); err != nil {
		t.Fatalf("an existing key file: %v", err)
	}
}

func TestTargetRestorePassphraseStaysOutOfOperationRecord(t *testing.T) {
	restoreNetwork := true
	request, normalized, err := marshalBackupTargetRestoreOperationRequest(restoreFromTargetPayload{
		TargetID:           1,
		RemoteDataset:      "tank/backups/data",
		Snapshot:           "@bk_j1_c1_test",
		DestinationDataset: "zroot/restored",
		RestoreNetwork:     &restoreNetwork,
		EncryptionKey:      "one-time-passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request, "one-time-passphrase") {
		t.Fatalf("the replicated operation record carries the passphrase: %s", request)
	}
	queued, err := json.Marshal(normalized)
	if err != nil || !strings.Contains(string(queued), "one-time-passphrase") {
		t.Fatalf("queued payload = %s, %v", queued, err)
	}
}

func createEncryptedDataset(t *testing.T, name, keylocation, passphrase string) {
	t.Helper()
	cmd := exec.Command("zfs", "create",
		"-o", "encryption=on",
		"-o", "keyformat=passphrase",
		"-o", "keylocation="+keylocation,
		name,
	)
	cmd.Stdin = strings.NewReader(passphrase + "\n" + passphrase + "\n")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create encrypted %s: %v\n%s", name, err, output)
	}
}

func writeKeyFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func rawSendRecv(t *testing.T, snapshot, destination string) {
	t.Helper()
	if output, err := exec.Command("sh", "-c", "zfs send -w "+snapshot+" | zfs recv -u "+destination).CombinedOutput(); err != nil {
		t.Fatalf("raw send %s to %s: %v\n%s", snapshot, destination, err, output)
	}
}

func zfsProperty(t *testing.T, dataset, property string) string {
	t.Helper()
	output, err := exec.Command("zfs", "get", "-H", "-o", "value", property, dataset).CombinedOutput()
	if err != nil {
		t.Fatalf("get %s of %s: %v\n%s", property, dataset, err, output)
	}
	return strings.TrimSpace(string(output))
}

func requireNoKeyFiles(t *testing.T, keyDir string) {
	t.Helper()
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Fatalf("key files written to %s: %v", keyDir, entries)
	}
}

func TestIntegrationEnsureEncryptionKeyForLoadedPromptDataset(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	root := pool + "/encrypted"
	passphrase := "sylve-encryption-test-passphrase"
	createEncryptedDataset(t, root, "prompt", passphrase)
	if output, err := exec.Command("zfs", "create", root+"/child").CombinedOutput(); err != nil {
		t.Fatalf("create inherited encrypted child: %v\noutput: %s", err, output)
	}

	s, _ := newEncryptionTestService(t, client)
	for _, name := range []string{root, root + "/child"} {
		ds, err := client.ZFS.Get(ctx, name, false)
		if err != nil {
			t.Fatalf("get dataset %s: %v", name, err)
		}
		available, err := s.ensureEncryptionKeyForDataset(ctx, ds)
		if err != nil {
			t.Fatalf("ensure loaded key for %s: %v", name, err)
		}
		if !available {
			t.Fatalf("expected loaded key to be available for %s", name)
		}
	}

	for _, name := range []string{root + "/child", root} {
		if output, err := exec.Command("zfs", "unmount", "-f", name).CombinedOutput(); err != nil {
			t.Fatalf("unmount encrypted dataset %s: %v\noutput: %s", name, err, output)
		}
	}
	if output, err := exec.Command("zfs", "unload-key", root).CombinedOutput(); err != nil {
		t.Fatalf("unload encrypted dataset key: %v\noutput: %s", err, output)
	}

	err := s.fixRestoredProperties(ctx, root, "", "")
	if err == nil || !strings.Contains(err.Error(), "restore_encryption_key_required_for_") {
		t.Fatalf("expected locked restore activation to require a key, got: %v", err)
	}

	setEncryptionKeyDir(t, t.TempDir())
	if err := s.RegisterRestoreEncryptionKey(passphrase, "passphrase"); err != nil {
		t.Fatalf("register external restore passphrase: %v", err)
	}
	if err := s.fixRestoredProperties(ctx, root, "", ""); err != nil {
		t.Fatalf("activate restored dataset with registered passphrase: %v", err)
	}
}

func TestIntegrationAutoDiscoverDropsCopiedUserKeys(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	userKey := filepath.Join(t.TempDir(), "zroot_sylve")
	writeKeyFile(t, userKey, "user-passphrase-kept-elsewhere")
	writeKeyFile(t, filepath.Join(keyDir, "zroot_sylve"), "user-passphrase-kept-elsewhere")
	writeKeyFile(t, filepath.Join(keyDir, testSylveKeyUUID), "sylve-created-passphrase")
	createEncryptedDataset(t, pool+"/userenc", "file://"+userKey, "")
	createEncryptedDataset(t, pool+"/sylveenc", "file://"+filepath.Join(keyDir, testSylveKeyUUID), "")

	s, clusterSvc := newEncryptionTestService(t, client)
	if err := s.DB.Create(&clusterModels.EncryptionKey{
		UUID: "zroot_sylve", KeyData: "user-passphrase-kept-elsewhere", KeyFormat: "passphrase",
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.AutoDiscoverAndRegisterKeys(ctx)
	if key, err := clusterSvc.GetEncryptionKeyByUUID(testSylveKeyUUID); err != nil || key.KeyData != "sylve-created-passphrase" {
		t.Fatalf("Sylve's key wasn't shared: %+v, %v", key, err)
	}
	if _, err := clusterSvc.GetEncryptionKeyByUUID("zroot_sylve"); err == nil {
		t.Fatal("the copied user key is still in the store")
	}

	s.RemoveCopiedKeyFiles(ctx)
	if _, err := os.Stat(filepath.Join(keyDir, "zroot_sylve")); !os.IsNotExist(err) {
		t.Fatalf("the copied key file is still on the node: %v", err)
	}
	if data, err := os.ReadFile(userKey); err != nil || string(data) != "user-passphrase-kept-elsewhere" {
		t.Fatalf("the user's own key file changed: %q, %v", data, err)
	}
	var tombstones int64
	s.DB.Model(&clusterModels.EncryptionKeyTombstone{}).Count(&tombstones)
	if tombstones != 0 {
		t.Fatalf("%d tombstones left after the copy was removed", tombstones)
	}
}

func TestIntegrationRestoredTreeInheritsParentKey(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	userKey := filepath.Join(t.TempDir(), "parent-key")
	writeKeyFile(t, userKey, "parent-passphrase-outside-sylve")

	parent := pool + "/encparent"
	createEncryptedDataset(t, parent, "file://"+userKey, "")
	zfstest.EnsureDataset(t, client, parent+"/child/sub")
	if output, err := exec.Command("zfs", "snapshot", "-r", parent+"/child@a").CombinedOutput(); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, output)
	}
	zfstest.EnsureDataset(t, client, pool+"/enctarget")
	rawSendRecv(t, parent+"/child@a", pool+"/enctarget/child")
	rawSendRecv(t, parent+"/child/sub@a", pool+"/enctarget/child/sub")
	restored := parent + "/restored"
	rawSendRecv(t, pool+"/enctarget/child@a", restored)
	rawSendRecv(t, pool+"/enctarget/child/sub@a", restored+"/sub")
	if got := zfsProperty(t, restored+"/sub", "encryptionroot"); got != restored+"/sub" {
		t.Fatalf("a raw-received child should arrive as its own encryption root, got %s", got)
	}

	s, _ := newEncryptionTestService(t, client)
	if err := s.fixRestoredProperties(ctx, restored, "", ""); err != nil {
		t.Fatalf("fix restored properties: %v", err)
	}
	for _, name := range []string{restored, restored + "/sub"} {
		if got := zfsProperty(t, name, "encryptionroot"); got != parent {
			t.Fatalf("%s encryption root = %s, want %s", name, got, parent)
		}
		if got := zfsProperty(t, name, "mounted"); got != "yes" {
			t.Fatalf("%s mounted = %s", name, got)
		}
	}
	requireNoKeyFiles(t, keyDir)
}

func TestIntegrationInPlaceRestoreKeepsOwnKey(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	userKey := filepath.Join(t.TempDir(), "parent-key")
	writeKeyFile(t, userKey, "parent-passphrase-outside-sylve")
	ownKey := "file://" + filepath.Join(keyDir, testSylveKeyUUID)
	writeKeyFile(t, filepath.Join(keyDir, testSylveKeyUUID), "a-separate-passphrase")

	parent := pool + "/encparent2"
	createEncryptedDataset(t, parent, "file://"+userKey, "")
	own := parent + "/own"
	createEncryptedDataset(t, own, ownKey, "")
	if output, err := exec.Command("zfs", "snapshot", own+"@a").CombinedOutput(); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, output)
	}
	zfstest.EnsureDataset(t, client, pool+"/enctarget2")
	rawSendRecv(t, own+"@a", pool+"/enctarget2/own")
	archive := parent + "/own_restore-backup"
	if output, err := exec.Command("zfs", "rename", own, archive).CombinedOutput(); err != nil {
		t.Fatalf("archive: %v\n%s", err, output)
	}
	rawSendRecv(t, pool+"/enctarget2/own@a", own)

	s, _ := newEncryptionTestService(t, client)
	if err := s.fixRestoredProperties(ctx, own, archive, ""); err != nil {
		t.Fatalf("fix restored properties: %v", err)
	}
	if got := zfsProperty(t, own, "encryptionroot"); got != own {
		t.Fatalf("encryption root = %s, want the dataset's own", got)
	}
	if got := zfsProperty(t, own, "keylocation"); got != ownKey {
		t.Fatalf("keylocation = %s, want %s", got, ownKey)
	}
}

func TestIntegrationRestoreWithOneTimePassphrase(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	passphrase := "typed-into-the-restore-dialog"
	source := pool + "/onetime"
	createEncryptedDataset(t, source, "prompt", passphrase)
	if output, err := exec.Command("zfs", "snapshot", source+"@a").CombinedOutput(); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, output)
	}
	zfstest.EnsureDataset(t, client, pool+"/plain")
	restored := pool + "/plain/onetime"
	rawSendRecv(t, source+"@a", restored)

	s, _ := newEncryptionTestService(t, client)
	if err := s.fixRestoredProperties(ctx, restored, "", ""); err == nil ||
		!strings.Contains(err.Error(), "restore_encryption_key_required_for_") {
		t.Fatalf("restore without a key = %v, want key required", err)
	}
	if err := s.fixRestoredProperties(ctx, restored, "", passphrase); err != nil {
		t.Fatalf("restore with the passphrase: %v", err)
	}
	if got := zfsProperty(t, restored, "keystatus"); got != "available" {
		t.Fatalf("keystatus = %s", got)
	}
	if got := zfsProperty(t, restored, "keylocation"); got != "prompt" {
		t.Fatalf("keylocation = %s, want prompt", got)
	}
	requireNoKeyFiles(t, keyDir)
	var keys int64
	s.DB.Model(&clusterModels.EncryptionKey{}).Count(&keys)
	if keys != 0 {
		t.Fatalf("the passphrase landed in the cluster store (%d keys)", keys)
	}
}

func TestIntegrationRemoveCopiedKeyFilesMovesUsersToParentKey(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	userKey := filepath.Join(t.TempDir(), "zroot_sylve")
	writeKeyFile(t, userKey, "user-passphrase-kept-elsewhere")
	copied := filepath.Join(keyDir, "zroot_sylve")
	writeKeyFile(t, copied, "user-passphrase-kept-elsewhere")
	other := filepath.Join(keyDir, "other")
	writeKeyFile(t, other, "not-the-removed-key")

	parent := pool + "/encparent3"
	createEncryptedDataset(t, parent, "file://"+userKey, "")
	restored := parent + "/restored"
	createEncryptedDataset(t, restored, "file://"+copied, "")

	s, _ := newEncryptionTestService(t, client)
	for _, tombstone := range []clusterModels.EncryptionKeyTombstone{
		{UUID: "zroot_sylve", KeyHash: clusterModels.EncryptionKeyHash("user-passphrase-kept-elsewhere")},
		{UUID: "other", KeyHash: clusterModels.EncryptionKeyHash("the-removed-key")},
	} {
		if err := s.DB.Create(&tombstone).Error; err != nil {
			t.Fatal(err)
		}
	}

	s.RemoveCopiedKeyFiles(ctx)
	if got := zfsProperty(t, restored, "encryptionroot"); got != parent {
		t.Fatalf("encryption root = %s, want %s", got, parent)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("the copy is still there: %v", err)
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "not-the-removed-key" {
		t.Fatalf("a file with other content was touched: %q, %v", data, err)
	}
	var tombstones int64
	s.DB.Model(&clusterModels.EncryptionKeyTombstone{}).Count(&tombstones)
	if tombstones != 0 {
		t.Fatalf("%d tombstones left", tombstones)
	}
}

func TestIntegrationRestoreUnderParentWithOtherKeyKeepsOwnRoot(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	sourceKey := filepath.Join(t.TempDir(), "source-key")
	writeKeyFile(t, sourceKey, "the-source-parent-key")
	destinationKey := filepath.Join(t.TempDir(), "destination-key")
	writeKeyFile(t, destinationKey, "another-parent-key")

	source := pool + "/encsource"
	createEncryptedDataset(t, source, "file://"+sourceKey, "")
	zfstest.EnsureDataset(t, client, source+"/data")
	if output, err := exec.Command("zfs", "snapshot", source+"/data@a").CombinedOutput(); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, output)
	}
	destination := pool + "/encdestination"
	createEncryptedDataset(t, destination, "file://"+destinationKey, "")
	restored := destination + "/data"
	rawSendRecv(t, source+"/data@a", restored)

	s, _ := newEncryptionTestService(t, client)
	if err := s.fixRestoredProperties(ctx, restored, "", "the-source-parent-key"); err != nil {
		t.Fatalf("fix restored properties: %v", err)
	}
	if got := zfsProperty(t, restored, "encryptionroot"); got != restored {
		t.Fatalf("encryption root = %s, want the dataset's own", got)
	}
	if got := zfsProperty(t, restored, "keystatus"); got != "available" {
		t.Fatalf("keystatus = %s", got)
	}
}

func TestIntegrationRemoveCopiedKeyFilesKeepsCopyUnderOtherKey(t *testing.T) {
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	keyDir := t.TempDir()
	setEncryptionKeyDir(t, keyDir)
	parentKey := filepath.Join(t.TempDir(), "parent-key")
	writeKeyFile(t, parentKey, "the-parent-key")
	copied := filepath.Join(keyDir, "zroot_sylve")
	writeKeyFile(t, copied, "a-different-user-key")

	parent := pool + "/encparent4"
	createEncryptedDataset(t, parent, "file://"+parentKey, "")
	restored := parent + "/restored"
	createEncryptedDataset(t, restored, "file://"+copied, "")

	s, _ := newEncryptionTestService(t, client)
	if err := s.DB.Create(&clusterModels.EncryptionKeyTombstone{
		UUID: "zroot_sylve", KeyHash: clusterModels.EncryptionKeyHash("a-different-user-key"),
	}).Error; err != nil {
		t.Fatal(err)
	}

	s.RemoveCopiedKeyFiles(ctx)
	if got := zfsProperty(t, restored, "encryptionroot"); got != restored {
		t.Fatalf("encryption root = %s, want the dataset's own", got)
	}
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("a copy still in use was removed: %v", err)
	}
	var tombstones int64
	s.DB.Model(&clusterModels.EncryptionKeyTombstone{}).Count(&tombstones)
	if tombstones != 1 {
		t.Fatalf("tombstones = %d, want it kept for a later attempt", tombstones)
	}
}

func TestRegisterRestoreEncryptionKey(t *testing.T) {
	db := newZeltaServiceTestDB(t, &clusterModels.EncryptionKey{})
	clusterSvc := cluster.NewClusterService(db, nil, nil).(*cluster.Service)
	s := &Service{Cluster: clusterSvc}

	if err := s.RegisterRestoreEncryptionKey("restore-passphrase", "passphrase"); err != nil {
		t.Fatalf("register restore key: %v", err)
	}
	if err := s.RegisterRestoreEncryptionKey("restore-passphrase", "passphrase"); err != nil {
		t.Fatalf("register duplicate restore key: %v", err)
	}

	var keys []clusterModels.EncryptionKey
	if err := db.Find(&keys).Error; err != nil {
		t.Fatalf("list restore keys: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyData != "restore-passphrase" {
		t.Fatalf("unexpected registered restore keys: %+v", keys)
	}
}
