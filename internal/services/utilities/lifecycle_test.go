// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/cenkalti/rain/v2/torrent"
)

type fakeTorrentRuntime struct {
	closeCalls int
}

func (*fakeTorrentRuntime) AddURI(string, *torrent.AddTorrentOptions) (*torrent.Torrent, error) {
	return nil, nil
}

func (*fakeTorrentRuntime) GetTorrent(string) *torrent.Torrent { return nil }

func (*fakeTorrentRuntime) RemoveTorrent(string, bool) error { return nil }

func (f *fakeTorrentRuntime) Close() error {
	f.closeCalls++
	return nil
}

func TestNewUtilitiesServiceDoesNotStartTorrentRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)

	service := NewUtilitiesService(nil, nil, nil, nil).(*Service)
	if _, err := service.activeTorrentClient(); !errors.Is(err, ErrUtilitiesNotReady) {
		t.Fatalf("active torrent client error = %v, want utilities not ready", err)
	}
	if _, err := os.Stat(filepath.Join(root, "downloads", "torrents", "torrent.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("torrent database exists before operational startup: %v", err)
	}
}

func TestUtilitiesOperationalLifecycleIsExplicitAndIdempotent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	previousConfig := config.ParsedConfig
	config.ParsedConfig = nil
	t.Cleanup(func() { config.ParsedConfig = previousConfig })

	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{})
	service := NewUtilitiesService(database, nil, nil, nil).(*Service)
	fake := &fakeTorrentRuntime{}
	factoryCalls := 0
	enqueueCalls := 0
	service.newTorrentClient = func(torrent.Config) (torrentRuntime, error) {
		factoryCalls++
		return fake, nil
	}
	service.enqueueNoPayloadFn = func(context.Context, string) error {
		enqueueCalls++
		return nil
	}

	if err := service.StartOperational(); err != nil {
		t.Fatalf("start operational runtime: %v", err)
	}
	if err := service.StartOperational(); err != nil {
		t.Fatalf("repeat operational start: %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("torrent factory calls = %d, want 1", factoryCalls)
	}
	if enqueueCalls != 1 {
		t.Fatalf("recovery enqueue calls = %d, want 1", enqueueCalls)
	}

	if err := service.Close(); err != nil {
		t.Fatalf("close operational runtime: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("torrent close calls = %d, want 1", fake.closeCalls)
	}
}

func TestMagnetDownloadRequiresOperationalUtilities(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &utilitiesModels.Downloads{})
	service := &Service{DB: database}

	_, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test.img&tr=https%3A%2F%2Ftracker.example.test%2Fannounce",
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if !errors.Is(err, ErrUtilitiesNotReady) {
		t.Fatalf("download error = %v, want utilities not ready", err)
	}
}
