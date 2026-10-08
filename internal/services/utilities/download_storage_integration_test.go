// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
	"github.com/alchemillahq/sylve/internal/zfsutil"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/anacrolix/torrent"
)

func newPoolDownloadTestService(t *testing.T, pools []string, client *gzfs.Client) *Service {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	database := testutil.NewSQLiteTestDB(t, &models.BasicSettings{}, &models.SystemSecrets{},
		&utilitiesModels.Upload{}, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{}, &vmModels.VM{}, &vmModels.Storage{})
	if err := database.Create(&models.BasicSettings{Pools: pools}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewUtilitiesService(database, nil, nil, nil).(*Service)
	service.DownloadStorage = downloadstorage.New(database, client)
	service.uploadHostnameFn = func() (string, error) { return "storage-node", nil }
	service.enqueueDownloadStartFn = func(context.Context, utilitiesServiceInterfaces.DownloadStartPayload) error { return nil }
	return service
}

func TestIntegrationDownloadStorageLifecycleRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	other, _ := zfstest.DedicatedPool(t)
	service := newPoolDownloadTestService(t, []string{pool, other}, client)
	ctx := t.Context()
	poolRoot, err := client.ZFS.Get(ctx, pool, false)
	if err != nil {
		t.Fatal(err)
	}
	customRoot := filepath.Join(poolRoot.Properties["mountpoint"].Value, "custom-downloads")
	zfstest.EnsureDataset(t, client, pool+"/sylve")
	dataset, err := client.ZFS.CreateFilesystem(ctx, pool+"/sylve/downloads", map[string]string{"mountpoint": customRoot})
	if err != nil {
		t.Fatal(err)
	}

	choices := service.DownloadStorage.Choices(ctx)
	if len(choices.Choices) != 3 || !choices.Choices[1].Available || !choices.Choices[2].Available {
		t.Fatalf("choices: %+v", choices)
	}
	if before, err := client.ZFS.Get(ctx, other+"/sylve/downloads", false); before != nil || err != nil && !zfsutil.DatasetDoesNotExist(err) {
		t.Fatalf("GET provisioned storage: %+v %v", before, err)
	}

	var ids []int
	var poolDownload *utilitiesModels.Downloads
	for _, selectedPool := range []string{"", pool, other} {
		source := filepath.Join(t.TempDir(), "installer.iso")
		if err := os.WriteFile(source, []byte("local iso payload"), 0600); err != nil {
			t.Fatal(err)
		}
		id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: source, DownloadType: utilitiesModels.DownloadUTypeOther, StoragePool: selectedPool})
		if err != nil {
			t.Fatal(err)
		}
		// A fresh service/worker has no in-memory destination selection.
		restarted := NewUtilitiesService(service.DB, nil, nil, nil).(*Service)
		restarted.DownloadStorage = service.DownloadStorage
		if err := restarted.StartDownload(&id); err != nil {
			t.Fatal(err)
		}
		download, err := service.GetDownloadByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if download.StoragePool != selectedPool || download.Status != utilitiesModels.DownloadStatusDone {
			t.Fatalf("stored binding/state: %+v", download)
		}
		if selectedPool == pool {
			poolDownload = download
			if download.StorageRoot != customRoot || download.StorageDatasetGUID != dataset.GUID {
				t.Fatalf("custom mountpoint ignored: %+v", download)
			}
		}
		media := &libvirtService.Service{DB: service.DB, DownloadStorage: service.DownloadStorage}
		if path, err := media.FindISOByUUID(download.UUID, false); err != nil || path != download.Path {
			t.Fatalf("VM media lookup: %q %v", path, err)
		}
		if target, err := service.ResolveSignedDownloadTargetByID(download.UUID, int(id)); err != nil || target.Path != download.Path {
			t.Fatalf("signed resolution: %+v %v", target, err)
		}
		if selectedPool != "" {
			_, release, err := service.AcquireSignedDownloadTargetByID(download.UUID, int(id))
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := service.DownloadStorage.TryMutation(selectedPool)
			release()
			if unlock != nil {
				unlock()
			}
			if !errors.Is(err, downloadstorage.ErrInUse) {
				t.Fatalf("signed response did not fence mutation: %v", err)
			}
		}
		if _, err := service.CreateSignedDownloadURL(download.UUID, download.Name); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("original source removed: %v", err)
		}
		if _, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: source, DownloadType: utilitiesModels.DownloadUTypeOther, StoragePool: other}); !errors.Is(err, ErrDownloadConflict) {
			t.Fatalf("cross-pool source duplicated: %v", err)
		}
		ids = append(ids, int(id))
	}
	recoverySource := filepath.Join(t.TempDir(), "recovery.iso")
	if err := os.WriteFile(recoverySource, []byte("recover"), 0600); err != nil {
		t.Fatal(err)
	}
	recoveryID, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: recoverySource, DownloadType: utilitiesModels.DownloadUTypeOther, StoragePool: pool})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := service.GetDownloadByID(recoveryID)
	if err != nil {
		t.Fatal(err)
	}

	if err := dataset.SetProperties(ctx, "readonly", "on"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveSignedDownloadTargetByID(poolDownload.UUID, int(poolDownload.ID)); err != nil {
		t.Fatalf("read-only storage unreadable: %v", err)
	}
	if err := service.DeleteDownload(int(poolDownload.ID)); !errors.Is(err, downloadstorage.ErrReadOnly) {
		t.Fatalf("deleted read-only storage: %v", err)
	}
	if err := dataset.SetProperties(ctx, "readonly", "off"); err != nil {
		t.Fatal(err)
	}

	if err := dataset.Unmount(ctx, false); err != nil {
		t.Fatal(err)
	}
	// Underlying placeholders must not be trusted, written into, or deleted.
	if err := os.MkdirAll(filepath.Dir(poolDownload.Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poolDownload.Path, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveSignedDownloadTargetByID(poolDownload.UUID, int(poolDownload.ID)); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("served unmounted placeholder: %v", err)
	}
	if result, err := service.DeleteDownloads(ids); !errors.Is(err, downloadstorage.ErrUnavailable) || len(result.Deleted) != 0 {
		t.Fatalf("unsafe bulk cleanup: %+v %v", result, err)
	}
	if _, _, err := service.DownloadStorage.Reserve(ctx, pool); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("reserved unmounted target: %v", err)
	}
	if err := service.StartDownload(&recoveryID); !errors.Is(err, downloadstorage.ErrUnavailable) {
		t.Fatalf("queued start used placeholder: %v", err)
	}
	if _, err := os.Stat(recovery.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queued start wrote under unmounted storage: %v", err)
	}
	if data, err := os.ReadFile(poolDownload.Path); err != nil || string(data) != "placeholder" {
		t.Fatalf("placeholder mutated: %q %v", data, err)
	}
	if err := os.Remove(poolDownload.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(poolDownload.Path)); err != nil {
		t.Fatal(err)
	}
	if err := dataset.Mount(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := service.StartDownload(&recoveryID); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.GetDownloadByID(recoveryID)
	if err != nil || recovered.DownloadStorage != recovery.DownloadStorage || recovered.Status != utilitiesModels.DownloadStatusDone || recovered.Error != "" {
		t.Fatalf("recovery rebound or retained stale error: %+v %v", recovered, err)
	}
	ids = append(ids, int(recoveryID))
	if result, err := service.DeleteDownloads(ids); err != nil || len(result.Deleted) != 4 {
		t.Fatalf("mixed-root deletion: %+v %v", result, err)
	}
	if _, err := client.ZFS.Get(ctx, pool+"/sylve/downloads", false); err != nil {
		t.Fatalf("cleanup removed storage dataset: %v", err)
	}
}

func TestIntegrationPoolUploadsAndPostProcessingRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	service := newPoolDownloadTestService(t, []string{pool}, client)
	ctx := t.Context()
	storage, staging, release, err := service.PrepareDownloaderUpload(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	// Receiving uploads have no durable identity yet, but already fence storage.
	if unlock, err := service.DownloadStorage.TryMutation(pool); !errors.Is(err, downloadstorage.ErrInUse) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("receive was not fenced: %v", err)
	}
	uploadID := utils.GenerateRandomUUID()
	path := filepath.Join(staging, DownloaderUploadFinalName(uploadID))
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "etc/version", Mode: 0644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("base")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	upload, err := service.RegisterDownloaderUpload(ctx, uploadID, path, "base.tar", info.Size(), 7, info, storage)
	release()
	if err != nil {
		t.Fatal(err)
	}
	completion, err := service.CompleteDownloaderUpload(ctx, uploadID, 7, utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{DownloadType: utilitiesModels.DownloadUTypeBase})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := service.CompleteDownloaderUpload(ctx, uploadID, 7, utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{DownloadType: utilitiesModels.DownloadUTypeOther}); err != nil || again.DownloadID != completion.DownloadID {
		t.Fatalf("completion retry: %+v %v", again, err)
	}
	if err := service.StartDownload(&completion.DownloadID); err != nil {
		t.Fatal(err)
	}
	download, err := service.GetDownloadByID(completion.DownloadID)
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.Stat(download.Path)
	if err != nil || !os.SameFile(info, published) || download.DownloadStorage != storage {
		t.Fatalf("upload was copied or rebound: %+v %v", download, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging source retained: %v", err)
	}
	if _, err := os.Stat(config.GetDownloadsPath("uploads")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pool upload consumed Default staging: %v", err)
	}
	if err := service.DB.Model(download).Updates(map[string]any{"status": utilitiesModels.DownloadStatusProcessing, "automatic_extraction": true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.StartPostProcess(&download.ID); err != nil {
		t.Fatal(err)
	}
	base := &jailService.Service{DB: service.DB, DownloadStorage: service.DownloadStorage}
	root, err := base.FindBaseByUUID(download.UUID)
	if err != nil || root != filepath.Join(storage.StorageRoot, "extracted", download.UUID) {
		t.Fatalf("jail base resolution: %q %v", root, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "etc/version")); err != nil || string(data) != "base" {
		t.Fatalf("extracted contents: %q %v", data, err)
	}
	if err := service.DeleteDownload(int(download.ID)); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&upload, "id = ?", uploadID).Error; err == nil {
		t.Fatal("completed upload identity was not removed with download")
	}

	// Idle pool-local interrupted receives still have an orphan cleanup path.
	orphanID := utils.GenerateRandomUUID()
	orphan := filepath.Join(staging, DownloaderUploadPartialName(orphanID))
	if err := os.WriteFile(orphan, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * DownloaderUploadTTL)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupExpiredUploads(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pool orphan not collected: %v", err)
	}
}

func TestIntegrationPoolTransfersRealZFS(t *testing.T) {
	pool, client := zfstest.DedicatedPool(t)
	service := newPoolDownloadTestService(t, []string{pool}, client)
	contents := bytes.Repeat([]byte("pool payload\n"), 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "http.iso", time.Time{}, bytes.NewReader(contents))
	}))
	defer server.Close()
	id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: server.URL + "/http.iso", DownloadType: utilitiesModels.DownloadUTypeOther, StoragePool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartDownload(&id); err != nil {
		t.Fatal(err)
	}
	download, err := service.GetDownloadByID(id)
	if err != nil {
		t.Fatal(err)
	}
	response := service.httpResponses[download.UUID]
	select {
	case <-response.Done:
	case <-time.After(15 * time.Second):
		t.Fatal("HTTP transfer timed out")
	}
	if err := response.Err(); err != nil {
		t.Fatal(err)
	}
	// A pending transfer with only a partial payload must resume in that root.
	if err := os.WriteFile(download.Path, contents[:16384], 0600); err != nil {
		t.Fatal(err)
	}
	restarted := NewUtilitiesService(service.DB, nil, nil, nil).(*Service)
	restarted.DownloadStorage = service.DownloadStorage
	if err := restarted.StartDownload(&id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted.httpResponses[download.UUID].Done:
	case <-time.After(15 * time.Second):
		t.Fatal("resumed HTTP transfer timed out")
	}
	if err := restarted.httpResponses[download.UUID].Err(); err != nil {
		t.Fatal(err)
	}
	// Recreate the worker after transfer, retaining only database state.
	if err := service.DB.Model(download).Updates(map[string]any{"status": utilitiesModels.DownloadStatusProcessing, "progress": 99}).Error; err != nil {
		t.Fatal(err)
	}
	restarted = NewUtilitiesService(service.DB, nil, nil, nil).(*Service)
	restarted.DownloadStorage = service.DownloadStorage
	if err := restarted.StartDownload(&id); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(download.Path); err != nil || !bytes.Equal(data, contents) {
		t.Fatalf("HTTP payload: %v", err)
	}

	mi, magnet, seedRoot := newTorrentFixture(t, "torrent.iso", contents)
	magnet += "&tr=" + url.QueryEscape(server.URL+"/announce")
	peer := startTestTorrentSeeder(t, seedRoot, mi)
	runtime := newTestTorrentRuntime(t, config.GetDownloadsPath("torrents"))
	service.torrentClient = runtime
	torrentID, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: magnet, DownloadType: utilitiesModels.DownloadUTypeOther, StoragePool: pool})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.GetDownloadByID(torrentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pending.Path, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending.Path, "torrent.iso"), contents[:16384], 0600); err != nil {
		t.Fatal(err)
	}
	cacheTestTorrentMetadata(t, runtime, pending.UUID, mi)
	if err := service.StartDownload(&torrentID); err != nil {
		t.Fatal(err)
	}
	record, err := service.GetDownloadByID(torrentID)
	if err != nil {
		t.Fatal(err)
	}
	transfer := runtime.GetTorrent(record.UUID)
	duplicate, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{URL: magnet + "&dn=duplicate.iso", DownloadType: utilitiesModels.DownloadUTypeOther})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartDownload(&duplicate); !errors.Is(err, ErrDownloadConflict) {
		t.Fatalf("active infohash duplicated onto Default: %v", err)
	}
	transfer.(*anacrolixTorrentDownload).torrent.AddPeers([]torrent.PeerInfo{{Addr: peer}})
	waitForTorrentProgress(t, transfer, func(progress torrentProgress) bool { return progress.Complete })
	if err := service.SyncDownloadProgress(); err != nil {
		t.Fatal(err)
	}
	record, err = service.GetDownloadByID(torrentID)
	if err != nil || len(record.Files) != 1 || record.Status != utilitiesModels.DownloadStatusDone {
		t.Fatalf("pool catalog: %+v %v", record, err)
	}
	if target, err := service.ResolveSignedDownloadTargetByID(record.UUID, record.Files[0].ID); err != nil || target.Path != filepath.Join(record.Path, "torrent.iso") {
		t.Fatalf("pool torrent sharing: %+v %v", target, err)
	}
}

func TestIntegrationPoolRawConversionRealZFS(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("requires qemu-img")
	}
	pool, client := zfstest.DedicatedPool(t)
	service := newPoolDownloadTestService(t, []string{pool}, client)
	storage, layout, err := service.DownloadStorage.Reserve(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	contents := bytes.Repeat([]byte("disk contents\n"), 4096)
	rawSource := filepath.Join(t.TempDir(), "source.raw")
	image := filepath.Join(t.TempDir(), "source.qcow2")
	if err := os.WriteFile(rawSource, contents, 0644); err != nil {
		t.Fatal(err)
	}
	if err := qemuimg.Convert(rawSource, image, qemuimg.FormatQCOW2); err != nil {
		t.Fatal(err)
	}
	imageContents, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	for _, extract := range []bool{false, true} {
		name := "plain.img"
		if extract {
			name = "compressed.img.gz"
		}
		path := filepath.Join(layout.Dir("path"), name)
		payload := imageContents
		if extract {
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			if _, err := writer.Write(imageContents); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			payload = compressed.Bytes()
		}
		if err := os.WriteFile(path, payload, 0644); err != nil {
			t.Fatal(err)
		}
		download := utilitiesModels.Downloads{DownloadStorage: storage, UUID: utils.GenerateRandomUUID(), Type: utilitiesModels.DownloadTypePath,
			Path: path, URL: "/source/" + name, Name: name, AutomaticRawConversion: true, AutomaticExtraction: extract, Progress: 99, Status: utilitiesModels.DownloadStatusProcessing}
		if err := service.DB.Create(&download).Error; err != nil {
			t.Fatal(err)
		}
		if err := service.StartPostProcess(&download.ID); err != nil {
			t.Fatal(err)
		}
		converted, err := service.GetDownloadByID(download.ID)
		if err != nil || converted.DownloadStorage != storage || converted.Status != utilitiesModels.DownloadStatusDone || filepath.Ext(converted.Path) != ".raw" {
			t.Fatalf("RAW conversion changed storage: %+v %v", converted, err)
		}
		if data, err := os.ReadFile(converted.Path); err != nil || !bytes.Equal(data, contents) {
			t.Fatalf("RAW payload: %v", err)
		}
		if info, err := os.Stat(converted.Path); err != nil || info.Mode().Perm() != 0644 {
			t.Fatalf("RAW conversion changed consumer permissions: %v", err)
		}
		if err := service.DeleteDownload(int(download.ID)); err != nil {
			t.Fatal(err)
		}
	}
}
