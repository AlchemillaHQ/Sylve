// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package downloadstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
)

type storageRunner struct {
	pool     *gzfs.ZPool
	datasets map[string]*gzfs.Dataset
	creates  []string
	err      error
}

func (r *storageRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, command string, args ...string) error {
	if r.err != nil {
		return r.err
	}
	if command == "zpool" && args[0] == "list" {
		pools := map[string]*gzfs.ZPool{}
		if r.pool != nil {
			pools[r.pool.Name] = r.pool
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"pools": pools})
	}
	if command != "zfs" {
		return fmt.Errorf("unexpected command %s %v", command, args)
	}
	switch args[0] {
	case "list":
		name := args[len(args)-2]
		datasets := map[string]*gzfs.Dataset{}
		if dataset := r.datasets[name]; dataset != nil {
			datasets[name] = dataset
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"datasets": datasets})
	case "create":
		name := args[len(args)-1]
		parent := r.datasets[filepath.Dir(name)]
		root := filepath.Join(parent.Properties["mountpoint"].Value, filepath.Base(name))
		if err := os.MkdirAll(root, 0755); err != nil {
			return err
		}
		r.datasets[name] = storageDataset(name, root)
		r.creates = append(r.creates, name)
		return nil
	default:
		return fmt.Errorf("unexpected zfs command %v", args)
	}
}

func storageDataset(name, root string) *gzfs.Dataset {
	return &gzfs.Dataset{Name: name, Pool: "tank", Type: gzfs.DatasetTypeFilesystem,
		Properties: map[string]gzfs.ZFSProperty{
			"guid": {Value: name + "-guid"}, "mountpoint": {Value: root},
			"mounted": {Value: "yes"}, "readonly": {Value: "off"}, "keystatus": {Value: "available"},
		}}
}

func newStorageTestResolver(t *testing.T) (*Resolver, *storageRunner, utilitiesModels.DownloadStorage) {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	if err := database.Create(&models.BasicSettings{Pools: []string{"tank"}}).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runner := &storageRunner{pool: &gzfs.ZPool{Name: "tank", PoolGUID: "pool-guid"}, datasets: map[string]*gzfs.Dataset{
		"tank": storageDataset("tank", root), "tank/sylve/downloads": storageDataset("tank/sylve/downloads", filepath.Join(root, "custom-downloads")),
	}}
	if err := os.MkdirAll(filepath.Join(root, "custom-downloads"), 0755); err != nil {
		t.Fatal(err)
	}
	resolver := New(database, gzfs.NewClient(gzfs.Options{Runner: runner}))
	resolver.mount = func(string, string) error { return nil }
	ref := utilitiesModels.DownloadStorage{StoragePool: "tank", StorageRoot: filepath.Join(root, "custom-downloads"),
		StoragePoolGUID: "pool-guid", StorageDatasetGUID: "tank/sylve/downloads-guid"}
	return resolver, runner, ref
}

func TestStorageDefaultDoesNotRequireZFSAndCapturesRoot(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	var resolver *Resolver
	ref, layout, err := resolver.Reserve(t.Context(), "")
	if err != nil || ref.StoragePool != "" || ref.StorageRoot != layout.Root {
		t.Fatalf("Default reservation = %+v, %v", ref, err)
	}
	for _, kind := range []string{"http", "path", "torrents", "extracted", "uploads"} {
		if info, err := os.Stat(layout.Dir(kind)); err != nil || !info.IsDir() {
			t.Fatalf("Default directory %s: %v", kind, err)
		}
	}
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	if _, err := resolver.Resolve(t.Context(), ref, false); !errors.Is(err, ErrMismatch) {
		t.Fatalf("changed dataPath adopted: %v", err)
	}
	if _, err := resolver.Resolve(t.Context(), utilitiesModels.DownloadStorage{}, false); err != nil {
		t.Fatalf("legacy Default failed: %v", err)
	}
}

func TestStorageResolveNeverCreatesAndRejectsChangedTargets(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Resolver, *storageRunner, *utilitiesModels.DownloadStorage)
		want   error
	}{
		{"custom mountpoint", func(*Resolver, *storageRunner, *utilitiesModels.DownloadStorage) {}, nil},
		{"missing pool", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) { r.pool = nil }, ErrUnavailable},
		{"missing dataset", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			delete(r.datasets, "tank/sylve/downloads")
		}, ErrUnavailable},
		{"unmounted placeholder", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.datasets["tank/sylve/downloads"].Properties["mounted"] = gzfs.ZFSProperty{Value: "no"}
		}, ErrUnavailable},
		{"wrong backing mount", func(r *Resolver, _ *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.mount = func(string, string) error { return errors.New("underlying filesystem") }
		}, ErrUnavailable},
		{"locked", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.datasets["tank/sylve/downloads"].Properties["keystatus"] = gzfs.ZFSProperty{Value: "unavailable"}
		}, ErrUnavailable},
		{"read only", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.datasets["tank/sylve/downloads"].Properties["readonly"] = gzfs.ZFSProperty{Value: "on"}
		}, ErrReadOnly},
		{"read-only pool import", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.pool.Properties = map[string]gzfs.ZFSProperty{"readonly": {Value: "on"}}
		}, ErrReadOnly},
		{"replacement pool", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) { r.pool.PoolGUID = "new-pool" }, ErrMismatch},
		{"replacement dataset", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.datasets["tank/sylve/downloads"].Properties["guid"] = gzfs.ZFSProperty{Value: "new-dataset"}
		}, ErrMismatch},
		{"root change", func(_ *Resolver, _ *storageRunner, ref *utilitiesModels.DownloadStorage) { ref.StorageRoot += "-old" }, ErrMismatch},
		{"incomplete identity", func(_ *Resolver, _ *storageRunner, ref *utilitiesModels.DownloadStorage) { ref.StorageDatasetGUID = "" }, ErrMismatch},
		{"not filesystem", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.datasets["tank/sylve/downloads"].Type = gzfs.DatasetTypeVolume
		}, ErrMismatch},
		{"unmanaged", func(_ *Resolver, _ *storageRunner, ref *utilitiesModels.DownloadStorage) { ref.StoragePool = "other" }, ErrInvalid},
		{"altroot", func(_ *Resolver, r *storageRunner, _ *utilitiesModels.DownloadStorage) {
			r.pool.Properties = map[string]gzfs.ZFSProperty{"altroot": {Value: "/import"}}
		}, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver, runner, ref := newStorageTestResolver(t)
			test.change(resolver, runner, &ref)
			layout, err := resolver.Resolve(t.Context(), ref, true)
			if !errors.Is(err, test.want) || test.want == nil && layout.Root != ref.StorageRoot {
				t.Fatalf("resolve = %+v, %v; want %v", layout, err, test.want)
			}
			if len(runner.creates) != 0 {
				t.Fatalf("existing resolution provisioned %v", runner.creates)
			}
			if test.want == ErrReadOnly {
				if _, err := resolver.Resolve(t.Context(), ref, false); err != nil {
					t.Fatalf("read-only storage could not be read: %v", err)
				}
			}
		})
	}
}

func TestStorageChoicesAreReadOnlyAndReservationIsExplicit(t *testing.T) {
	resolver, runner, _ := newStorageTestResolver(t)
	delete(runner.datasets, "tank/sylve/downloads")
	choices := resolver.Choices(t.Context())
	if len(choices.Choices) != 2 || !choices.Choices[1].Available || len(runner.creates) != 0 {
		t.Fatalf("discovery = %+v, created=%v", choices, runner.creates)
	}
	ref, layout, err := resolver.Reserve(t.Context(), "tank")
	if err != nil || ref.StorageRoot != layout.Root || len(runner.creates) != 2 {
		t.Fatalf("explicit reservation = %+v, %v, creates=%v", ref, err, runner.creates)
	}
	if strings.HasSuffix(layout.Root, "custom-downloads") {
		t.Fatal("reservation guessed an old mountpoint")
	}
	if _, _, err := resolver.Reserve(t.Context(), "tank/sylve/downloads"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dataset accepted as pool: %v", err)
	}
	runner.err = errors.New("zfs unavailable")
	choices = resolver.Choices(t.Context())
	if !choices.Choices[0].Available || choices.Choices[1].Available || choices.Choices[1].Reason != ErrUnavailable.Error() {
		t.Fatalf("failed discovery hid Default or advertised unavailable pool: %+v", choices)
	}
}

func TestStorageReservationRefusesReferencedReplacements(t *testing.T) {
	resolver, runner, ref := newStorageTestResolver(t)
	if err := resolver.DB.Create(&utilitiesModels.Downloads{DownloadStorage: ref, UUID: "test", Path: "old", URL: "old"}).Error; err != nil {
		t.Fatal(err)
	}
	runner.datasets["tank/sylve/downloads"].Properties["guid"] = gzfs.ZFSProperty{Value: "replacement"}
	if _, _, err := resolver.Reserve(t.Context(), "tank"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("new reservation adopted replaced dataset: %v", err)
	}
	delete(runner.datasets, "tank/sylve/downloads")
	if _, _, err := resolver.Reserve(t.Context(), "tank"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("referenced missing storage recreated: %v", err)
	}
	if len(runner.creates) != 0 {
		t.Fatalf("created referenced replacement: %v", runner.creates)
	}
}

func TestConcurrentStorageReservationProvisionsNamespaceOnce(t *testing.T) {
	resolver, runner, _ := newStorageTestResolver(t)
	delete(runner.datasets, "tank/sylve/downloads")
	results := make(chan error, 2)
	for range 2 {
		go func() {
			defer resolver.ReadLock("tank")()
			_, _, err := resolver.Reserve(t.Context(), "tank")
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.creates) != 2 {
		t.Fatalf("namespace provisioned more than once: %v", runner.creates)
	}
}

func TestStorageManagedPathsRejectTraversalSymlinksAndChildMounts(t *testing.T) {
	resolver, _, ref := newStorageTestResolver(t)
	layout, err := resolver.Resolve(t.Context(), ref, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(layout.Root, "torrents", "torrent"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		layout.Dir("torrents"),
		filepath.Join(layout.Dir("torrents"), "other"),
		filepath.Join(layout.Dir("path"), "torrent"),
	} {
		download := utilitiesModels.Downloads{UUID: "torrent", Type: utilitiesModels.DownloadTypeTorrent, Path: path}
		if err := layout.ValidateDownload(download); !errors.Is(err, ErrMismatch) {
			t.Fatalf("torrent destination outside its UUID root %q: %v", path, err)
		}
	}
	for _, relative := range []string{"../outside.iso", "/outside.iso", "."} {
		if _, err := layout.TorrentFile("torrent", relative); !errors.Is(err, ErrMismatch) {
			t.Fatalf("unsafe torrent file %q: %v", relative, err)
		}
	}
	link := filepath.Join(layout.Root, "path")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := layout.ValidatePath(filepath.Join(link, "missing.iso")); !errors.Is(err, ErrMismatch) {
		t.Fatalf("symlink accepted: %v", err)
	}
	child := filepath.Join(layout.Root, "torrents", "torrent", "child")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	layout.mount = func(path, _ string) error {
		if path == child {
			return errors.New("child mount")
		}
		return nil
	}
	if err := layout.ValidateTree(filepath.Dir(child)); !errors.Is(err, ErrMismatch) {
		t.Fatalf("recursive cleanup crossed a child mount: %v", err)
	}
}

func TestStorageAdmissionFencesReceivingUploadsByPool(t *testing.T) {
	resolver := New(nil, nil)
	endReceive := resolver.ReadLock("tank")
	for _, target := range []string{"tank", "tank/sylve", "tank/sylve/downloads", "tank/sylve/downloads@old"} {
		if release, err := resolver.TryMutation(target); !errors.Is(err, ErrInUse) {
			if release != nil {
				release()
			}
			t.Fatalf("active receive did not fence %s: %v", target, err)
		}
	}
	for _, target := range []string{"other", "tank/sylve/jails/100"} {
		release, err := resolver.TryMutation(target)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	endReceive()
	release, err := resolver.TryMutation("tank")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestStorageUsageIncludesDownloadsAndOnlyDownloaderUploads(t *testing.T) {
	resolver, _, ref := newStorageTestResolver(t)
	if err := resolver.DB.Create(&utilitiesModels.Upload{DownloadStorage: ref, ID: "upload", Scope: utilitiesModels.UploadScopeFileExplorer}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireDatasetUnused(t.Context(), resolver.DB, "tank"); err != nil {
		t.Fatalf("Explorer upload reinterpreted: %v", err)
	}
	if err := resolver.DB.Model(&utilitiesModels.Upload{}).Where("id = ?", "upload").Update("scope", utilitiesModels.UploadScopeDownloader).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tank", "tank/sylve", "tank/sylve/downloads", "tank/sylve/downloads/child", "tank/sylve/downloads@old"} {
		if err := RequireDatasetUnused(t.Context(), resolver.DB, name); !errors.Is(err, ErrInUse) {
			t.Fatalf("upload did not protect %s: %v", name, err)
		}
	}
	if err := RequireDatasetUnused(t.Context(), resolver.DB, "tank/other", "tank/sylve/downloads-other"); err != nil {
		t.Fatal(err)
	}
	connection, err := resolver.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RequireDatasetUnused(t.Context(), resolver.DB, "tank"); err == nil {
		t.Fatal("database error failed open")
	}
}

func TestStorageSchemaMigrationKeepsLegacyPayloadsAndIdentities(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	download := utilitiesModels.Downloads{UUID: "legacy", Path: "/data/downloads/path/legacy.iso", URL: "legacy-source", Name: "legacy.iso", Type: utilitiesModels.DownloadTypePath, Status: utilitiesModels.DownloadStatusDone, Progress: 100}
	upload := utilitiesModels.Upload{ID: "legacy-upload", Scope: utilitiesModels.UploadScopeDownloader, Path: "/data/downloads/uploads/legacy.upload"}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}} {
		for _, column := range []string{"storage_pool", "storage_root", "storage_pool_guid", "storage_dataset_guid"} {
			if err := database.Migrator().DropColumn(model, column); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := database.AutoMigrate(&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}); err != nil {
		t.Fatal(err)
	}
	var stored utilitiesModels.Downloads
	if err := database.First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DownloadStorage != (utilitiesModels.DownloadStorage{}) || stored.UUID != download.UUID || stored.Path != download.Path || stored.Status != download.Status {
		t.Fatalf("legacy download mutated: %+v", stored)
	}
	var staged utilitiesModels.Upload
	if err := database.First(&staged, "id = ?", upload.ID).Error; err != nil {
		t.Fatal(err)
	}
	if staged.DownloadStorage != (utilitiesModels.DownloadStorage{}) || staged.Path != upload.Path {
		t.Fatalf("legacy upload moved: %+v", staged)
	}
	stored.DownloadStorage = utilitiesModels.DownloadStorage{StoragePool: "tank", StorageRoot: "/private/root", StoragePoolGUID: "pool-guid", StorageDatasetGUID: "dataset-guid"}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"storagePool":"tank"`) || strings.Contains(string(encoded), "/private/root") || strings.Contains(string(encoded), "pool-guid") || strings.Contains(string(encoded), "dataset-guid") {
		t.Fatalf("storage API leaked server binding: %s", encoded)
	}
}

func TestStorageUsageFailsClosedForMissingInventory(t *testing.T) {
	for _, missing := range []any{&utilitiesModels.Downloads{}, &utilitiesModels.Upload{}} {
		t.Run(fmt.Sprintf("%T", missing), func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
			if err := database.Migrator().DropTable(missing); err != nil {
				t.Fatal(err)
			}
			if err := RequireDatasetUnused(t.Context(), database, "tank"); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("missing inventory was treated as unused: %v", err)
			}
			if err := RequireDatasetGUIDUnused(t.Context(), database, "guid"); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("missing GUID inventory was treated as unused: %v", err)
			}
		})
	}
}
