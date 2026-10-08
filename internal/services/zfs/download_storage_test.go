// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestDownloadStorageMutationGuardProtectsReferencesAndReceivingUploads(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	resolver := downloadstorage.New(database, nil)
	service := &Service{DB: database, DownloadStorage: resolver}
	release := resolver.ReadLock("tank")
	if unlock, err := service.guardDownloadStorageMutation(t.Context(), "tank"); !errors.Is(err, ErrConflict) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("receiving upload was not fenced: %v", err)
	}
	release()
	if err := database.Create(&utilitiesModels.Downloads{DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: "tank", StorageDatasetGUID: "guid"}, UUID: "test", Path: "payload", URL: "source"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"tank", "tank/sylve", "tank/sylve/downloads", "tank/sylve/downloads@old", "tank/sylve/downloads/child"} {
		if unlock, err := service.guardDownloadStorageMutation(t.Context(), target); !errors.Is(err, ErrConflict) {
			if unlock != nil {
				unlock()
			}
			t.Fatalf("referenced storage mutation %s: %v", target, err)
		}
	}
	if !service.IsDatasetInUse("guid", true) {
		t.Fatal("download dataset not marked in use")
	}
	unlock, err := service.guardDownloadStorageMutation(t.Context(), "tank/unrelated")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

type poolCreationGuardRunner struct{}

func (poolCreationGuardRunner) Run(_ context.Context, _ io.Reader, stdout, _ io.Writer, name string, args ...string) error {
	if name == "zpool" && len(args) > 0 && args[0] == "list" {
		return json.NewEncoder(stdout).Encode(map[string]any{"pools": map[string]any{}})
	}
	return fmt.Errorf("unexpected mutation: %s %v", name, args)
}

func TestCreatePoolRefusesReferencedReplacementAndReceivingUpload(t *testing.T) {
	for _, receiving := range []bool{false, true} {
		t.Run(fmt.Sprintf("receiving=%t", receiving), func(t *testing.T) {
			database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
			client := gzfs.NewClient(gzfs.Options{Runner: poolCreationGuardRunner{}})
			service := NewZfsService(database, nil, nil, client).(*Service)
			if receiving {
				defer service.DownloadStorage.ReadLock("tank")()
			} else if err := database.Create(&utilitiesModels.Downloads{DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: "tank"}, UUID: "download", URL: "source", Path: "payload"}).Error; err != nil {
				t.Fatal(err)
			}
			request := zfsServiceInterfaces.CreateZPoolRequest{Name: "tank", Vdevs: []zfsServiceInterfaces.Vdev{{VdevDevices: []string{"/dev/test-device"}}}}
			if err := service.CreatePool(t.Context(), request); !errors.Is(err, downloadstorage.ErrInUse) {
				t.Fatalf("pool creation bypassed storage admission: %v", err)
			}
		})
	}
}

func TestIntegrationDownloadStorageDestructiveEntryPointsRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	zfstest.EnsureDataset(t, client, pool+"/sylve/downloads")
	dataset, err := client.ZFS.Get(t.Context(), pool+"/sylve/downloads", false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := dataset.Snapshot(t.Context(), "protected", false)
	if err != nil {
		t.Fatal(err)
	}
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	if err := database.Create(&utilitiesModels.Downloads{DownloadStorage: utilitiesModels.DownloadStorage{StoragePool: pool, StorageDatasetGUID: dataset.GUID}, UUID: "test", Path: "payload", URL: "source"}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewZfsService(database, nil, nil, client).(*Service)
	rootPool, err := client.Zpool.Get(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"filesystem delete", func() error { return service.DeleteFilesystem(t.Context(), dataset.GUID) }},
		{"bulk delete", func() error {
			return service.BulkDeleteDataset(t.Context(), []zfsServiceInterfaces.DatasetDeletionTarget{{Name: dataset.Name, GUID: dataset.GUID}})
		}},
		{"pool destroy", func() error { return service.DeletePool(t.Context(), rootPool.PoolGUID) }},
		{"readonly edit", func() error {
			return service.EditFilesystem(t.Context(), dataset.GUID, map[string]string{"readonly": "on"})
		}},
		{"snapshot rollback", func() error { return service.RollbackSnapshot(t.Context(), snapshot.GUID, false) }},
		{"named rollback", func() error { return service.RollbackSnapshotByName(t.Context(), snapshot.Name, false) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrConflict) {
				t.Fatalf("destructive entry point not protected: %v", err)
			}
		})
	}
	if after, err := client.ZFS.Get(t.Context(), dataset.Name, false); err != nil || after == nil || after.GUID != dataset.GUID || after.Properties["readonly"].Value == "on" {
		t.Fatalf("protected dataset mutated: %+v %v", after, err)
	}
}

func TestIntegrationDownloadStorageFlashVolumeRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if _, err := exec.LookPath("/usr/sbin/camdd"); err != nil {
		t.Skip("requires camdd")
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("requires qemu-img")
	}
	sourcePool, client := zfstest.DedicatedPool(t)
	destinationPool, _ := zfstest.DedicatedPool(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{},
		&utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{}, &utilitiesModels.Upload{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{})
	if err := database.Create(&models.BasicSettings{Pools: []string{sourcePool, destinationPool}}).Error; err != nil {
		t.Fatal(err)
	}
	resolver := downloadstorage.New(database, client)
	storage, layout, err := resolver.Reserve(t.Context(), sourcePool)
	if err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte("disk payload\n"), 512)
	path := filepath.Join(layout.Dir("path"), "image.img")
	if err := os.WriteFile(path, contents, 0644); err != nil {
		t.Fatal(err)
	}
	download := utilitiesModels.Downloads{DownloadStorage: storage, UUID: "flash-image", URL: "/source/image.img", Path: path,
		Type: utilitiesModels.DownloadTypePath, Name: "image.img", Status: utilitiesModels.DownloadStatusDone, Progress: 100}
	if err := database.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	volume, err := client.ZFS.CreateVolume(t.Context(), destinationPool+"/volume", 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	media := &libvirtService.Service{DB: database, DownloadStorage: resolver}
	service := NewZfsService(database, nil, media, client).(*Service)
	service.DownloadStorage = resolver
	if err := service.FlashVolume(t.Context(), volume.GUID, download.UUID); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open("/dev/zvol/" + volume.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	actual := make([]byte, len(contents))
	if _, err := io.ReadFull(file, actual); err != nil || !bytes.Equal(contents, actual) {
		t.Fatalf("cross-pool flash payload: %v", err)
	}
	dataset, err := client.ZFS.Get(t.Context(), sourcePool+"/sylve/downloads", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataset.Unmount(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := service.FlashVolume(t.Context(), volume.GUID, download.UUID); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("unavailable source was accepted: %v", err)
	}
}
