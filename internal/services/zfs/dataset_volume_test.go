// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestIntegrationEditVolumeGuardsSharedVolumesRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	name := pool + "/shared-volume"
	const mib = uint64(1 << 20)
	volume, err := client.ZFS.CreateVolume(ctx, name, 64*mib, map[string]string{"sparse": "on", "volblocksize": "16384"})
	if err != nil || volume == nil {
		t.Fatalf("create volume: %v", err)
	}
	property := func(key string) string {
		t.Helper()
		current, err := client.ZFS.Get(ctx, name, false)
		if err != nil || current == nil {
			t.Fatalf("get volume: %v", err)
		}
		return current.Properties[key].Value
	}

	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{},
		&iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetLUN{}, &iscsiModels.ISCSITargetPortal{},
	)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-11.test:shared"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	lun := iscsiModels.ISCSITargetLUN{TargetID: target.ID, LUNNumber: 0, ZVol: name}
	if err := database.Create(&lun).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}

	for label, props := range map[string]map[string]string{
		"shrink":      {"volsize": "33554432B"},
		"volume mode": {"volmode": "geom"},
	} {
		err := service.EditVolume(ctx, volume.GUID, props, nil)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "iscsi") {
			t.Fatalf("%s of a shared volume returned %v, want an iSCSI conflict", label, err)
		}
	}
	if property("volsize") != "67108864" || property("volmode") == "geom" {
		t.Fatalf("a refused edit changed the volume: volsize=%s volmode=%s", property("volsize"), property("volmode"))
	}

	unchangedMode := map[string]string{"volsize": "67108864B", "volmode": property("volmode"), "compression": "lz4"}
	if err := service.EditVolume(ctx, volume.GUID, unchangedMode, nil); err != nil {
		t.Fatalf("an edit that keeps the size and volume mode was refused: %v", err)
	}
	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"volsize": "134217728B", "compression": "zle"}, nil); err != nil {
		t.Fatalf("growing a shared volume failed: %v", err)
	}
	if property("volsize") != "134217728" || property("compression") != "zle" {
		t.Fatalf("grow did not apply: volsize=%s compression=%s", property("volsize"), property("compression"))
	}
	if err := service.EditVolume(ctx, volume.GUID, nil, []string{"compression"}); err != nil {
		t.Fatalf("resetting compression failed: %v", err)
	}
	if property("compression") == "zle" {
		t.Fatal("compression was not reset to inherited")
	}

	if err := database.Delete(&lun).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"volsize": "33554432B"}, nil); err != nil {
		t.Fatalf("shrinking an unshared volume failed: %v", err)
	}
	if property("volsize") != "33554432" {
		t.Fatalf("shrink did not apply: volsize=%s", property("volsize"))
	}

	if err := service.EditVolume(ctx, volume.GUID, nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("an empty edit returned %v, want ErrInvalidRequest", err)
	}
	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"compression": "lz4"}, []string{"compression"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("setting and inheriting the same property returned %v, want ErrInvalidRequest", err)
	}
}

func TestIntegrationCreateAndReprovisionVolumeRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	database := testutil.NewSQLiteTestDB(t, &models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{})
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}

	err := service.CreateVolume(ctx, "thick", pool, map[string]string{
		"size":         "67108864B",
		"sparse":       "off",
		"volblocksize": "16384",
		"volmode":      "dev",
		"compression":  "lz4",
	})
	if err != nil {
		t.Fatalf("create thick volume: %v", err)
	}
	volume, err := client.ZFS.Get(ctx, pool+"/thick", false)
	if err != nil || volume == nil {
		t.Fatalf("get volume: %v", err)
	}
	properties := func() map[string]string {
		t.Helper()
		current, err := client.ZFS.Get(ctx, volume.Name, false)
		if err != nil || current == nil {
			t.Fatalf("get volume: %v", err)
		}
		values := map[string]string{}
		for key, property := range current.Properties {
			values[key] = property.Value
		}
		return values
	}
	created := properties()
	if created["volsize"] != "67108864" || created["volblocksize"] != "16384" || created["volmode"] != "dev" ||
		created["compression"] != "lz4" || created["refreservation"] == "0" {
		t.Fatalf("created volume has unexpected properties: %v", created)
	}

	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"refreservation": "none"}, nil); err != nil {
		t.Fatalf("switch to sparse: %v", err)
	}
	if got := properties()["refreservation"]; got != "0" {
		t.Fatalf("sparse volume still reserves %s", got)
	}
	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"refreservation": "auto"}, nil); err != nil {
		t.Fatalf("switch to thick: %v", err)
	}
	if got := properties()["refreservation"]; got == "0" {
		t.Fatal("thick volume reserves nothing")
	}
}

func TestIntegrationDeleteRefusesSharedVolumesRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	pool, client, cleanup := zfstest.SharedPool(t)
	defer cleanup()

	ctx := context.Background()
	const mib = uint64(1 << 20)
	parentName := pool + "/lun-parent"
	zfstest.EnsureDataset(t, client, parentName)
	create := func(name string) *gzfs.Dataset {
		t.Helper()
		volume, err := client.ZFS.CreateVolume(ctx, name, 16*mib, map[string]string{"sparse": "on", "volblocksize": "16384"})
		if err != nil || volume == nil {
			t.Fatalf("create volume %s: %v", name, err)
		}
		return volume
	}
	shared := create(parentName + "/shared")
	unshared := create(parentName + "/unshared")
	parent, err := client.ZFS.Get(ctx, parentName, false)
	if err != nil || parent == nil {
		t.Fatalf("get parent: %v", err)
	}
	exists := func(name string) bool {
		dataset, err := client.ZFS.Get(ctx, name, false)
		return err == nil && dataset != nil
	}

	database := testutil.NewSQLiteTestDB(t,
		&models.ZFSCacheInvalidation{}, &utilitiesModels.Downloads{}, &utilitiesModels.Upload{},
		&vmModels.VMStorageDataset{}, &vmModels.Storage{},
		&iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetLUN{}, &iscsiModels.ISCSITargetPortal{},
	)
	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-11.test:delete"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	lun := iscsiModels.ISCSITargetLUN{TargetID: target.ID, LUNNumber: 0, ZVol: shared.Name}
	if err := database.Create(&lun).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: database, GZFS: client, syncMutex: &sync.Mutex{}}
	exact := func(datasets ...*gzfs.Dataset) []zfsServiceInterfaces.DatasetDeletionTarget {
		targets := make([]zfsServiceInterfaces.DatasetDeletionTarget, 0, len(datasets))
		for _, dataset := range datasets {
			targets = append(targets, zfsServiceInterfaces.DatasetDeletionTarget{Name: dataset.Name, GUID: dataset.GUID})
		}
		return targets
	}

	for label, remove := range map[string]func() error{
		"volume delete":         func() error { return service.DeleteVolume(ctx, shared.GUID) },
		"bulk delete":           func() error { return service.BulkDeleteDataset(ctx, exact(unshared, shared)) },
		"bulk delete of parent": func() error { return service.BulkDeleteDataset(ctx, exact(parent)) },
		"file system delete":    func() error { return service.DeleteFilesystem(ctx, parent.GUID) },
	} {
		err := remove()
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "shared_over_iscsi_by_"+target.TargetName) {
			t.Fatalf("%s of a shared volume returned %v, want an iSCSI conflict naming the target", label, err)
		}
		if !exists(shared.Name) || !exists(unshared.Name) {
			t.Fatalf("%s removed a volume despite the refusal", label)
		}
	}

	if err := service.BulkDeleteDataset(ctx, exact(unshared)); err != nil {
		t.Fatalf("deleting an unshared sibling failed: %v", err)
	}
	if exists(unshared.Name) {
		t.Fatal("the unshared volume still exists")
	}

	if err := database.Delete(&lun).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteVolume(ctx, shared.GUID); err != nil {
		t.Fatalf("deleting a volume after unsharing it failed: %v", err)
	}
	if exists(shared.Name) {
		t.Fatal("the unshared volume still exists after deletion")
	}
}

func TestIntegrationFlashVolumeConvertsImagesAndGuardsRealZFS(t *testing.T) {
	zfstest.SkipIfUnavailable(t)
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("requires qemu-img")
	}
	pool, client := zfstest.DedicatedPool(t)
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	ctx := t.Context()
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.ZFSCacheInvalidation{},
		&utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{}, &utilitiesModels.Upload{},
		&vmModels.VM{}, &vmModels.Storage{}, &vmModels.VMStorageDataset{},
		&iscsiModels.ISCSITarget{}, &iscsiModels.ISCSITargetLUN{}, &iscsiModels.ISCSITargetPortal{})
	if err := database.Create(&models.BasicSettings{Pools: []string{pool}}).Error; err != nil {
		t.Fatal(err)
	}
	resolver := downloadstorage.New(database, client)
	storage, layout, err := resolver.Reserve(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}

	dir := layout.Dir("path")
	contents := bytes.Repeat([]byte("converted disk\n\x00"), 1<<15)
	raw := filepath.Join(dir, "disk.raw")
	if err := os.WriteFile(raw, contents, 0644); err != nil {
		t.Fatal(err)
	}
	qemu := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("qemu-img", args...).CombinedOutput(); err != nil {
			t.Fatalf("qemu-img %v: %v: %s", args, err, output)
		}
	}
	images := map[string]string{
		"qcow2":   filepath.Join(dir, "disk.qcow2"),
		"large":   filepath.Join(dir, "large.qcow2"),
		"overlay": filepath.Join(dir, "overlay.qcow2"),
	}
	qemu("convert", "-f", "raw", "-O", "qcow2", raw, images["qcow2"])
	qemu("create", "-f", "qcow2", images["large"], "8M")
	qemu("create", "-f", "qcow2", "-F", "qcow2", "-b", images["qcow2"], images["overlay"])
	for uuid, path := range images {
		download := utilitiesModels.Downloads{DownloadStorage: storage, UUID: uuid, URL: "/source/" + filepath.Base(path), Path: path,
			Type: utilitiesModels.DownloadTypePath, Name: filepath.Base(path), Status: utilitiesModels.DownloadStatusDone, Progress: 100}
		if err := database.Create(&download).Error; err != nil {
			t.Fatal(err)
		}
	}

	volume, err := client.ZFS.CreateVolume(ctx, pool+"/target", 4<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	media := &libvirtService.Service{DB: database, DownloadStorage: resolver}
	service := NewZfsService(database, nil, media, client).(*Service)
	service.DownloadStorage = resolver
	head := func() []byte {
		t.Helper()
		device, err := os.Open("/dev/zvol/" + volume.Name)
		if err != nil {
			t.Fatal(err)
		}
		defer device.Close()
		data := make([]byte, len(contents))
		if _, err := io.ReadFull(device, data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	untouched := func(label string) {
		t.Helper()
		if !bytes.Equal(head(), make([]byte, len(contents))) {
			t.Fatalf("%s wrote to the volume", label)
		}
	}

	target := iscsiModels.ISCSITarget{TargetName: "iqn.2026-11.test:flash"}
	if err := database.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	lun := iscsiModels.ISCSITargetLUN{TargetID: target.ID, ZVol: volume.Name}
	if err := database.Create(&lun).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.FlashVolume(ctx, volume.GUID, "qcow2"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "shared_over_iscsi_by_"+target.TargetName) {
		t.Fatalf("flashing a shared volume returned %v, want an iSCSI conflict", err)
	}
	untouched("a flash refused for iSCSI")
	if err := database.Delete(&lun).Error; err != nil {
		t.Fatal(err)
	}

	if err := service.EditVolume(ctx, volume.GUID, map[string]string{"readonly": "on"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.FlashVolume(ctx, volume.GUID, "qcow2"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "is_read_only") {
		t.Fatalf("flashing a read-only volume returned %v, want a read-only conflict", err)
	}
	if err := service.EditVolume(ctx, volume.GUID, nil, []string{"readonly"}); err != nil {
		t.Fatal(err)
	}

	if err := service.FlashVolume(ctx, volume.GUID, "large"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "source_size_exceeds_volume_size") {
		t.Fatalf("flashing an image with a larger virtual size returned %v, want a size conflict", err)
	}
	if err := service.FlashVolume(ctx, volume.GUID, "overlay"); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "image_has_backing_file") {
		t.Fatalf("flashing an image with a backing file returned %v, want a refusal", err)
	}
	untouched("a refused flash")

	if err := service.FlashVolume(ctx, volume.GUID, "qcow2"); err != nil {
		t.Fatalf("flashing a qcow2 image failed: %v", err)
	}
	if !bytes.Equal(head(), contents) {
		t.Fatal("the qcow2 image was not converted to raw on the volume")
	}
}
