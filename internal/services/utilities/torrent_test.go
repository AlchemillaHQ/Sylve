// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func newTestTorrentRuntime(t *testing.T, root string) *anacrolixTorrentRuntime {
	t.Helper()
	client, err := newAnacrolixTorrentRuntime(torrentRuntimeOptions{DataDir: root})
	if err != nil {
		t.Fatalf("start torrent runtime: %v", err)
	}
	runtime := client.(*anacrolixTorrentRuntime)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close torrent runtime: %v", err)
		}
	})
	return runtime
}

func newTorrentFixture(t *testing.T, name string, contents []byte) (*metainfo.MetaInfo, string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	mi, magnet := torrentFixtureFromPath(t, path)
	return mi, magnet, root
}

func torrentFixtureFromPath(t *testing.T, path string) (*metainfo.MetaInfo, string) {
	t.Helper()
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: encoded}
	magnet, err := mi.MagnetV2()
	if err != nil {
		t.Fatal(err)
	}
	return mi, magnet.String()
}

func cacheTestTorrentMetadata(t *testing.T, runtime *anacrolixTorrentRuntime, id string, mi *metainfo.MetaInfo) {
	t.Helper()
	file, err := os.Create(filepath.Join(runtime.metadataDir, id+".torrent"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := mi.Write(file); err != nil {
		t.Fatal(err)
	}
}

func waitForTorrentProgress(t *testing.T, download torrentDownload, ready func(torrentProgress) bool) torrentProgress {
	t.Helper()
	if download == nil {
		t.Fatal("torrent download is unavailable")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		progress, err := download.Stats()
		if err != nil {
			t.Fatalf("read torrent progress: %v", err)
		}
		if ready(progress) {
			return progress
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for torrent progress: %+v", progress)
		case <-ticker.C:
		}
	}
}

func startTestTorrentSeeder(t *testing.T, root string, mi *metainfo.MetaInfo) net.Addr {
	t.Helper()
	fileOptions := storage.NewFileClientOpts{
		ClientBaseDir:   root,
		PieceCompletion: storage.NewMapPieceCompletion(),
	}
	fileOptions.UsePartFiles.Set(false)
	fileStorage := storage.NewFileOpts(fileOptions)
	cfg := torrent.NewDefaultClientConfig()
	cfg.SetListenAddr("127.0.0.1:0")
	cfg.DisableIPv6 = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	cfg.DisableWebtorrent = true
	cfg.Seed = true
	cfg.DefaultStorage = fileStorage
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		_ = fileStorage.Close()
	})
	seed, err := client.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-seed.Complete().On():
	case <-time.After(10 * time.Second):
		t.Fatal("seeder did not verify its local files")
	}
	for _, addr := range client.ListenAddrs() {
		if addr.Network() == "tcp" {
			return addr
		}
	}
	t.Fatal("seeder has no TCP listener")
	return nil
}

func TestTorrentRuntimeDownloadsMagnetFromLocalPeer(t *testing.T) {
	contents := bytes.Repeat([]byte("torrent payload\n"), 4096)
	mi, magnet, seedRoot := newTorrentFixture(t, "installer.iso", contents)
	peer := startTestTorrentSeeder(t, seedRoot, mi)
	runtime := newTestTorrentRuntime(t, t.TempDir())
	id := utils.GenerateDeterministicUUID(magnet)
	root := filepath.Join(t.TempDir(), id)
	download, err := runtime.AddURI(magnet, id, root)
	if err != nil {
		t.Fatal(err)
	}
	download.(*anacrolixTorrentDownload).torrent.AddPeers([]torrent.PeerInfo{{Addr: peer}})
	progress := waitForTorrentProgress(t, download, func(progress torrentProgress) bool { return progress.Complete })
	if !progress.MetadataReady || progress.Size != int64(len(contents)) || progress.Name != "installer.iso" {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if runtime.GetTorrent(id) != download {
		t.Fatal("torrent did not retain its Sylve identity")
	}
	files, err := download.Files()
	if err != nil || len(files) != 1 || files[0].Path != "installer.iso" {
		t.Fatalf("files=%+v error=%v", files, err)
	}
	stored, err := os.ReadFile(filepath.Join(root, files[0].Path))
	if err != nil || !bytes.Equal(stored, contents) {
		t.Fatalf("downloaded contents differ: %v", err)
	}
	if _, err := metainfo.LoadFromFile(filepath.Join(runtime.metadataDir, id+".torrent")); err != nil {
		t.Fatalf("magnet metadata was not cached: %v", err)
	}
}

func TestTorrentRuntimeRestartsAndVerifiesPartialFiles(t *testing.T) {
	contents := bytes.Repeat([]byte("partial payload\n"), 8192)
	mi, magnet, seedRoot := newTorrentFixture(t, "partial.iso", contents)
	dataDir := t.TempDir()
	id := utils.GenerateDeterministicUUID(magnet)
	root := filepath.Join(dataDir, id)
	if err := os.MkdirAll(root, 0750); err != nil {
		t.Fatal(err)
	}
	partialSize := 32 << 10
	if err := os.WriteFile(filepath.Join(root, "partial.iso"), contents[:partialSize], 0600); err != nil {
		t.Fatal(err)
	}
	runtime := newTestTorrentRuntime(t, dataDir)
	cacheTestTorrentMetadata(t, runtime, id, mi)
	download, err := runtime.AddURI(magnet, id, root)
	if err != nil {
		t.Fatal(err)
	}
	partialReady := func(progress torrentProgress) bool {
		return progress.MetadataReady && progress.BytesCompleted == int64(partialSize) && !progress.Complete
	}
	waitForTorrentProgress(t, download, partialReady)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime = newTestTorrentRuntime(t, dataDir)
	download, err = runtime.AddURI(magnet, id, root)
	if err != nil {
		t.Fatal(err)
	}
	waitForTorrentProgress(t, download, partialReady)
	peer := startTestTorrentSeeder(t, seedRoot, mi)
	download.(*anacrolixTorrentDownload).torrent.AddPeers([]torrent.PeerInfo{{Addr: peer}})
	waitForTorrentProgress(t, download, func(progress torrentProgress) bool { return progress.Complete })
	stored, err := os.ReadFile(filepath.Join(root, "partial.iso"))
	if err != nil || !bytes.Equal(stored, contents) {
		t.Fatalf("resumed contents differ: %v", err)
	}
}

func TestTorrentRuntimeRejectsDuplicateInfohash(t *testing.T) {
	mi, magnet, _ := newTorrentFixture(t, "disk.img", []byte("disk contents"))
	runtime := newTestTorrentRuntime(t, t.TempDir())
	firstID, secondID := utils.GenerateRandomUUID(), utils.GenerateRandomUUID()
	firstRoot, secondRoot := filepath.Join(t.TempDir(), firstID), filepath.Join(t.TempDir(), secondID)
	cacheTestTorrentMetadata(t, runtime, firstID, mi)
	cacheTestTorrentMetadata(t, runtime, secondID, mi)
	first, err := runtime.AddURI(magnet, firstID, firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := runtime.AddURI(magnet, firstID, firstRoot)
	if err != nil || repeated != first {
		t.Fatalf("repeated add was not idempotent: %v", err)
	}
	if _, err := runtime.AddURI(magnet, firstID, secondRoot); !errors.Is(err, ErrDownloadConflict) {
		t.Fatalf("changed destination error=%v, want conflict", err)
	}
	if _, err := runtime.AddURI(magnet, secondID, secondRoot); !errors.Is(err, ErrDownloadConflict) {
		t.Fatalf("duplicate infohash error=%v, want conflict", err)
	}
	if runtime.GetTorrent(secondID) != nil || runtime.GetTorrent(firstID) != first {
		t.Fatal("duplicate infohash shared or replaced the original torrent")
	}
}

func TestTorrentRuntimeVerifiesMultifileDownloads(t *testing.T) {
	contents := map[string][]byte{
		"release/installer.iso": []byte("installer"),
		"release/SHA256":        []byte("checksums"),
	}
	seedRoot, root := t.TempDir(), t.TempDir()
	for path, data := range contents {
		for _, directory := range []string{seedRoot, root} {
			file := filepath.Join(directory, path)
			if err := os.MkdirAll(filepath.Dir(file), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	mi, magnet := torrentFixtureFromPath(t, filepath.Join(seedRoot, "release"))
	runtime := newTestTorrentRuntime(t, t.TempDir())
	id := utils.GenerateRandomUUID()
	cacheTestTorrentMetadata(t, runtime, id, mi)
	download, err := runtime.AddURI(magnet, id, root)
	if err != nil {
		t.Fatal(err)
	}
	waitForTorrentProgress(t, download, func(progress torrentProgress) bool { return progress.Complete })
	files, err := download.Files()
	if err != nil || len(files) != len(contents) {
		t.Fatalf("completed files=%+v, error=%v", files, err)
	}
	for _, file := range files {
		data, ok := contents[file.Path]
		if !ok || file.Size != int64(len(data)) {
			t.Fatalf("unexpected catalog entry: %+v", file)
		}
		stored, err := os.ReadFile(filepath.Join(root, file.Path))
		if err != nil || !bytes.Equal(stored, data) {
			t.Fatalf("file %s changed: %v", file.Path, err)
		}
	}
}

func TestTorrentFileStorageRejectsUnsafeOrCollidingPathsBeforeCreatingFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []metainfo.FileInfo
	}{
		{name: "traversal", files: []metainfo.FileInfo{{Path: []string{"..", "outside.iso"}}}},
		{name: "escaped path", files: []metainfo.FileInfo{
			{Path: []string{"valid.iso"}}, {Path: []string{"folder/../../../outside.iso"}},
		}},
		{name: "normalized collision", files: []metainfo.FileInfo{
			{Path: []string{"folder", "disk.iso"}}, {Path: []string{"folder/disk.iso"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			fileStorage := torrentFileStorage{storage.NewFileOpts(storage.NewFileClientOpts{
				ClientBaseDir: root, PieceCompletion: storage.NewMapPieceCompletion(),
			})}
			defer fileStorage.Close()
			info := &metainfo.Info{Name: "release", Files: tc.files}
			if _, err := fileStorage.OpenTorrent(context.Background(), info, metainfo.Hash{}); err == nil {
				t.Fatal("unsafe torrent file paths were accepted")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("files created before validation: %v, %v", entries, err)
			}
		})
	}
}

func TestTorrentRuntimeRemovalRetainsPayload(t *testing.T) {
	contents := []byte("retained media")
	mi, magnet, _ := newTorrentFixture(t, "media.iso", contents)
	runtime := newTestTorrentRuntime(t, t.TempDir())
	id := utils.GenerateRandomUUID()
	root := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(root, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "media.iso")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	cacheTestTorrentMetadata(t, runtime, id, mi)
	download, err := runtime.AddURI(magnet, id, root)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := runtime.RemoveTorrent(id); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GetTorrent(id) != nil {
		t.Fatal("removed torrent is still active")
	}
	if _, err := os.Stat(filepath.Join(runtime.metadataDir, id+".torrent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed torrent metadata still exists: %v", err)
	}
	if _, err := download.Stats(); !errors.Is(err, ErrUtilitiesNotReady) {
		t.Fatalf("detached torrent progress error=%v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, contents) {
		t.Fatalf("runtime removal changed payload: %v", err)
	}
}

func TestTorrentRuntimeRejectsInvalidIdentityAndSymlinkDirectory(t *testing.T) {
	_, magnet, _ := newTorrentFixture(t, "media.iso", []byte("media"))
	runtime := newTestTorrentRuntime(t, t.TempDir())
	if _, err := runtime.AddURI(magnet, "../../outside", t.TempDir()); !errors.Is(err, ErrDownloadInvalid) {
		t.Fatalf("unsafe ID error=%v", err)
	}
	if _, err := runtime.AddURI("magnet:?xt=urn:btih:"+strings.Repeat("0", 40), utils.GenerateRandomUUID(), t.TempDir()); !errors.Is(err, ErrDownloadInvalid) {
		t.Fatalf("zero infohash error=%v", err)
	}
	root := filepath.Join(t.TempDir(), "symlink")
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AddURI(magnet, utils.GenerateRandomUUID(), root); err == nil {
		t.Fatal("symlink torrent directory was accepted")
	}
}

func TestTorrentRuntimeRejectsMismatchedCachedMetadata(t *testing.T) {
	mi, _, _ := newTorrentFixture(t, "first.iso", []byte("first"))
	_, magnet, _ := newTorrentFixture(t, "second.iso", []byte("second"))
	runtime := newTestTorrentRuntime(t, t.TempDir())
	id := utils.GenerateRandomUUID()
	cacheTestTorrentMetadata(t, runtime, id, mi)
	if _, err := runtime.AddURI(magnet, id, filepath.Join(t.TempDir(), id)); err == nil {
		t.Fatal("cached metadata from a different torrent was accepted")
	}
}

func TestStartTorrentPublishesCatalog(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	contents := []byte("installer contents")
	mi, magnet, _ := newTorrentFixture(t, "installer.iso", contents)
	magnet += "&tr=http%3A%2F%2F127.0.0.1%3A1%2Fannounce"
	id := utils.GenerateDeterministicUUID(magnet)
	root := filepath.Join(config.GetDownloadsPath("torrents"), id)
	if err := os.MkdirAll(root, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "installer.iso"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	runtime := newTestTorrentRuntime(t, config.GetDownloadsPath("torrents"))
	cacheTestTorrentMetadata(t, runtime, id, mi)
	service := &Service{
		DB:            testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{}, &utilitiesModels.DownloadedFile{}),
		torrentClient: runtime,
	}
	download := utilitiesModels.Downloads{
		UUID: id, URL: magnet, Type: utilitiesModels.DownloadTypeTorrent,
		Path: root, Status: utilitiesModels.DownloadStatusPending,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.StartDownload(&download.ID); err != nil {
		t.Fatal(err)
	}
	if runtime.GetTorrent(id) == nil {
		t.Fatalf("torrent was not started: %+v", storedDownload(t, service, download.ID))
	}
	waitForTorrentProgress(t, runtime.GetTorrent(id), func(progress torrentProgress) bool { return progress.Complete })
	if err := service.SyncDownloadProgress(); err != nil {
		t.Fatal(err)
	}
	stored := storedDownload(t, service, download.ID)
	if stored.UUID != id || stored.Path != root || stored.Status != utilitiesModels.DownloadStatusDone || stored.Progress != 100 {
		t.Fatalf("completed record=%+v", stored)
	}
	if err := service.DB.Preload("Files").First(&stored, download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.Files) != 1 || stored.Files[0].Name != "installer.iso" {
		t.Fatalf("completed catalog=%+v", stored.Files)
	}
	if runtime.GetTorrent(id) != nil {
		t.Fatal("completed torrent was not detached")
	}
}

type stubTorrentDownload struct {
	progress torrentProgress
}

func (*stubTorrentDownload) Name() string                           { return "" }
func (d *stubTorrentDownload) Stats() (torrentProgress, error)      { return d.progress, nil }
func (*stubTorrentDownload) Files() ([]completedTorrentFile, error) { return nil, nil }

func TestSyncTorrentDoesNotPublishUnverifiedBytesAsDone(t *testing.T) {
	service := newDownloadFileTestService(t, nil)
	runtime := &fakeTorrentRuntime{download: &stubTorrentDownload{progress: torrentProgress{
		Name: "disk.img", Size: 100, BytesCompleted: 100, MetadataReady: true,
	}}}
	download := utilitiesModels.Downloads{
		UUID: utils.GenerateRandomUUID(), Path: "/unused", URL: "magnet:?xt=urn:btih:unverified",
		Type: utilitiesModels.DownloadTypeTorrent, Status: utilitiesModels.DownloadStatusPending,
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service.syncTorrent(runtime, &download)
	stored := storedDownload(t, service, download.ID)
	if stored.Status != utilitiesModels.DownloadStatusProcessing || stored.Progress != 99 || len(runtime.removed) != 0 {
		t.Fatalf("unverified torrent was published: %+v", stored)
	}
}

func TestSyncTorrentMetadataTimeoutUsesRuntimeStartTime(t *testing.T) {
	service := newDownloadFileTestService(t, nil)
	stub := &stubTorrentDownload{progress: torrentProgress{StartedAt: time.Now()}}
	runtime := &fakeTorrentRuntime{download: stub}
	download := utilitiesModels.Downloads{
		UUID: utils.GenerateRandomUUID(), Path: "/unused", URL: "magnet:?xt=urn:btih:timeout",
		Type: utilitiesModels.DownloadTypeTorrent, Status: utilitiesModels.DownloadStatusProcessing,
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service.syncTorrent(runtime, &download)
	if stored := storedDownload(t, service, download.ID); stored.Status == utilitiesModels.DownloadStatusFailed {
		t.Fatal("restarted torrent immediately timed out because of its old database timestamp")
	}
	stub.progress.StartedAt = time.Now().Add(-16 * time.Minute)
	service.syncTorrent(runtime, &download)
	stored := storedDownload(t, service, download.ID)
	if stored.Status != utilitiesModels.DownloadStatusFailed || stored.Error != "magnet_metadata_timeout" || len(runtime.removed) != 1 {
		t.Fatalf("metadata timeout was not recorded: %+v", stored)
	}
}

func TestSyncTorrentQueuesRecoveryFromSylveRecords(t *testing.T) {
	service := newDownloadFileTestService(t, nil)
	var queued uint
	service.enqueueDownloadStartFn = func(_ context.Context, payload utilitiesServiceInterfaces.DownloadStartPayload) error {
		queued = payload.ID
		return nil
	}
	download := utilitiesModels.Downloads{
		UUID: utils.GenerateRandomUUID(), Path: "/unused", URL: "magnet:?xt=urn:btih:restart",
		Type: utilitiesModels.DownloadTypeTorrent, Status: utilitiesModels.DownloadStatusProcessing,
		UpdatedAt: time.Now().Add(-time.Minute),
	}
	if err := service.DB.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	service.syncTorrent(&fakeTorrentRuntime{}, &download)
	if queued != download.ID {
		t.Fatalf("recovery queued ID=%d, want %d", queued, download.ID)
	}
}

func TestTorrentRuntimeConfigHonorsDHTSettings(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	previous := config.ParsedConfig
	t.Cleanup(func() { config.ParsedConfig = previous })
	config.ParsedConfig = &internal.SylveConfig{BTT: internal.BTT{DHT: internal.DHTConfig{Enabled: true, Port: 7247}}}
	cfg := torrentRuntimeConfig()
	if !cfg.DHTEnabled || cfg.DHTPort != 7247 || cfg.DataDir != config.GetDownloadsPath("torrents") {
		t.Fatalf("torrent runtime config=%+v", cfg)
	}
	config.ParsedConfig.BTT.DHT.Enabled = false
	if torrentRuntimeConfig().DHTEnabled {
		t.Fatal("DHT remained enabled")
	}
}
