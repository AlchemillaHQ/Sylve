// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/google/uuid"
)

type torrentProgress struct {
	Name           string
	Size           int64
	BytesCompleted int64
	MetadataReady  bool
	Complete       bool
	StartedAt      time.Time
}

type torrentDownload interface {
	Name() string
	Stats() (torrentProgress, error)
	Files() ([]completedTorrentFile, error)
}

type torrentRuntime interface {
	AddURI(uri, id, directory string) (torrentDownload, error)
	GetTorrent(id string) torrentDownload
	RemoveTorrent(id string) error
	Close() error
}

type torrentRuntimeOptions struct {
	DataDir    string
	DHTEnabled bool
	DHTPort    int
}

type torrentRuntimeFactory func(torrentRuntimeOptions) (torrentRuntime, error)

func torrentRuntimeConfig() torrentRuntimeOptions {
	cfg := torrentRuntimeOptions{
		DataDir:    config.GetDownloadsPath("torrents"),
		DHTEnabled: true,
		DHTPort:    7246,
	}
	if config.ParsedConfig != nil {
		cfg.DHTEnabled = config.ParsedConfig.BTT.DHT.Enabled
		cfg.DHTPort = config.ParsedConfig.BTT.DHT.Port
	}
	return cfg
}

type anacrolixTorrentRuntime struct {
	mu          sync.Mutex
	client      *torrent.Client
	metadataDir string
	torrents    map[string]*anacrolixTorrentDownload
	closed      bool
}

type anacrolixTorrentDownload struct {
	mu            sync.Mutex
	torrent       *torrent.Torrent
	storage       storage.ClientImplCloser
	uri           string
	directory     string
	metadataPath  string
	metadataSaved bool
	startedAt     time.Time
}

func newAnacrolixTorrentRuntime(options torrentRuntimeOptions) (torrentRuntime, error) {
	metadataDir := filepath.Join(options.DataDir, ".metadata")
	if err := os.MkdirAll(metadataDir, 0700); err != nil {
		return nil, fmt.Errorf("create torrent metadata directory: %w", err)
	}
	if options.DHTEnabled && (options.DHTPort < 0 || options.DHTPort > 65535) {
		return nil, errors.New("invalid torrent DHT port")
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = options.DataDir
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: options.DataDir})
	cfg.NoDHT = !options.DHTEnabled
	cfg.ListenPort = 0
	if options.DHTEnabled {
		cfg.ListenPort = options.DHTPort
	}
	cfg.NoDefaultPortForwarding = true
	cfg.DisableWebtorrent = true
	cfg.Seed = true
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &anacrolixTorrentRuntime{
		client:      client,
		metadataDir: metadataDir,
		torrents:    make(map[string]*anacrolixTorrentDownload),
	}, nil
}

func (r *anacrolixTorrentRuntime) AddURI(uri, id, directory string) (torrentDownload, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil || parsedID.String() != id || !filepath.IsAbs(directory) {
		return nil, ErrDownloadInvalid
	}
	spec, err := torrent.TorrentSpecFromMagnetUri(uri)
	if err != nil {
		return nil, fmt.Errorf("parse torrent magnet: %w", err)
	}
	if spec.InfoHash.IsZero() {
		return nil, fmt.Errorf("%w: missing or zero torrent infohash", ErrDownloadInvalid)
	}
	directory = filepath.Clean(directory)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrUtilitiesNotReady
	}
	if existing := r.torrents[id]; existing != nil {
		if existing.uri != uri || existing.directory != directory {
			return nil, ErrDownloadConflict
		}
		return existing, nil
	}

	metadataPath := filepath.Join(r.metadataDir, id+".torrent")
	if err := loadTorrentMetadata(spec, metadataPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0750); err != nil {
		return nil, fmt.Errorf("create torrent directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("torrent directory is not a regular directory")
	}

	fileOptions := storage.NewFileClientOpts{
		ClientBaseDir:   directory,
		PieceCompletion: storage.NewMapPieceCompletion(),
		FilePathMaker:   torrentFilePath,
	}
	fileOptions.UsePartFiles.Set(false)
	fileStorage := torrentFileStorage{storage.NewFileOpts(fileOptions)}
	spec.Storage = fileStorage
	t, added, err := r.client.AddTorrentSpec(spec)
	if err == nil && !added {
		// Anacrolix otherwise ignores the new storage and returns the existing
		// infohash. Never let two Sylve records share that torrent's files.
		err = ErrDownloadConflict
	}
	if err != nil {
		_ = fileStorage.Close()
		return nil, err
	}
	download := &anacrolixTorrentDownload{
		torrent:       t,
		storage:       fileStorage,
		uri:           uri,
		directory:     directory,
		metadataPath:  metadataPath,
		metadataSaved: len(spec.InfoBytes) != 0,
		startedAt:     time.Now(),
	}
	r.torrents[id] = download
	go func() {
		select {
		case <-t.GotInfo():
			download.mu.Lock()
			defer download.mu.Unlock()
			select {
			case <-t.Closed():
				return
			default:
				t.DownloadAll()
			}
		case <-t.Closed():
		}
	}()
	return download, nil
}

func loadTorrentMetadata(spec *torrent.TorrentSpec, path string) error {
	mi, err := metainfo.LoadFromFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read torrent metadata: %w", err)
	}
	cached, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return fmt.Errorf("parse torrent metadata: %w", err)
	}
	if spec.InfoHash != cached.InfoHash ||
		spec.InfoHashV2.Ok && (!cached.InfoHashV2.Ok || spec.InfoHashV2.Unwrap() != cached.InfoHashV2.Unwrap()) {
		return errors.New("torrent metadata infohash mismatch")
	}
	spec.InfoBytes = mi.InfoBytes
	spec.PieceLayers = mi.PieceLayers
	return nil
}

func (r *anacrolixTorrentRuntime) GetTorrent(id string) torrentDownload {
	r.mu.Lock()
	defer r.mu.Unlock()
	if download := r.torrents[id]; download != nil {
		return download
	}
	return nil
}

func (r *anacrolixTorrentRuntime) RemoveTorrent(id string) error {
	parsedID, err := uuid.Parse(id)
	if err != nil || parsedID.String() != id {
		return ErrDownloadInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if download := r.torrents[id]; download != nil {
		download.mu.Lock()
		download.torrent.Drop()
		err = download.storage.Close()
		download.mu.Unlock()
		delete(r.torrents, id)
	}
	if removeErr := os.Remove(filepath.Join(r.metadataDir, id+".torrent")); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		err = errors.Join(err, removeErr)
	}
	// Payload deletion remains owned by the service's managed-path preflight.
	return err
}

func (r *anacrolixTorrentRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var closeErr error
	for _, download := range r.torrents {
		download.mu.Lock()
		closeErr = errors.Join(closeErr, download.saveMetadata())
		download.mu.Unlock()
	}
	closeErr = errors.Join(closeErr, errors.Join(r.client.Close()...))
	for _, download := range r.torrents {
		closeErr = errors.Join(closeErr, download.storage.Close())
	}
	clear(r.torrents)
	return closeErr
}

func (d *anacrolixTorrentDownload) Name() string { return d.torrent.Name() }

func (d *anacrolixTorrentDownload) Stats() (torrentProgress, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	progress := torrentProgress{Name: d.Name(), StartedAt: d.startedAt}
	select {
	case <-d.torrent.Closed():
		return progress, ErrUtilitiesNotReady
	default:
	}
	if !d.metadataReady() {
		return progress, nil
	}
	progress.MetadataReady = true
	progress.Size = d.torrent.Length()
	progress.BytesCompleted = d.torrent.BytesCompleted()
	progress.Complete = d.torrent.Complete().Bool()
	return progress, d.saveMetadata()
}

func (d *anacrolixTorrentDownload) Files() ([]completedTorrentFile, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-d.torrent.Closed():
		return nil, ErrUtilitiesNotReady
	default:
	}
	if !d.metadataReady() {
		return nil, errors.New("torrent metadata is unavailable")
	}
	files := d.torrent.Files()
	info := d.torrent.Info()
	catalog := make([]completedTorrentFile, 0, len(files))
	for _, file := range files {
		fileInfo := file.FileInfo()
		path := fileInfo.BestPath()
		if len(info.Files) > 0 && (strings.ContainsRune(fileInfo.Attr, 'p') ||
			len(path) > 0 && strings.HasPrefix(path[len(path)-1], "_____padding_file")) {
			continue
		}
		catalog = append(catalog, completedTorrentFile{
			Path: torrentFilePath(storage.FilePathMakerOpts{Info: info, File: &fileInfo}),
			Size: file.Length(),
		})
	}
	return catalog, nil
}

func torrentFilePath(opts storage.FilePathMakerOpts) string {
	components := opts.File.BestPath()
	if name := opts.Info.BestName(); name != metainfo.NoName {
		components = append([]string{name}, components...)
	}
	return filepath.Join(components...)
}

type torrentFileStorage struct {
	storage.ClientImplCloser
}

func (s torrentFileStorage) OpenTorrent(ctx context.Context, info *metainfo.Info, hash metainfo.Hash) (storage.TorrentImpl, error) {
	paths := make(map[string]struct{})
	for _, file := range info.UpvertedFiles() {
		for _, component := range append([]string{info.BestName()}, file.BestPath()...) {
			if strings.TrimSpace(component) == ".." {
				return storage.TorrentImpl{}, errors.New("invalid torrent file path")
			}
		}
		path := torrentFilePath(storage.FilePathMakerOpts{Info: info, File: &file})
		if path == "." || path == ".." || filepath.IsAbs(path) || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
			return storage.TorrentImpl{}, errors.New("invalid torrent file path")
		}
		if _, exists := paths[path]; exists {
			return storage.TorrentImpl{}, fmt.Errorf("duplicate torrent file path: %q", path)
		}
		paths[path] = struct{}{}
	}
	return s.ClientImplCloser.OpenTorrent(ctx, info, hash)
}

func (d *anacrolixTorrentDownload) metadataReady() bool {
	select {
	case <-d.torrent.GotInfo():
		return true
	default:
		return false
	}
}

func (d *anacrolixTorrentDownload) saveMetadata() error {
	if d.metadataSaved || !d.metadataReady() {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(d.metadataPath), ".torrent-*")
	if err != nil {
		return fmt.Errorf("create torrent metadata: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	mi := d.torrent.Metainfo()
	if err := mi.Write(file); err != nil {
		return fmt.Errorf("write torrent metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), d.metadataPath); err != nil {
		return err
	}
	d.metadataSaved = true
	return nil
}
