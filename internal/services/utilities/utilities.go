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
	"crypto/tls"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/cavaliergopher/grab/v3"
	"github.com/cenkalti/rain/v2/torrent"
	"gorm.io/gorm"

	"sync"
)

var _ utilitiesServiceInterfaces.UtilitiesServiceInterface = (*Service)(nil)

type Service struct {
	DB           *gorm.DB
	BTTClient    *torrent.Session
	GrabClient   *grab.Client
	GrabInsecure *grab.Client

	httpRspMu     sync.Mutex
	httpResponses map[string]*grab.Response

	postq      chan uint
	workerOnce sync.Once

	inflightMu sync.Mutex
	inflight   map[uint]struct{}
}

func NewUtilitiesService(db *gorm.DB) utilitiesServiceInterfaces.UtilitiesServiceInterface {
	torrent.DisableLogging()
	cfg := torrent.DefaultConfig
	cfg.Database = config.GetDownloadsPath("torrent.db")
	cfg.DataDir = config.GetDownloadsPath("torrents")

	cfg.RPCEnabled = config.ParsedConfig.BTT.RPC.Enabled

	if config.ParsedConfig.BTT.RPC.Enabled {
		cfg.RPCHost = config.ParsedConfig.BTT.RPC.Address
		cfg.RPCPort = config.ParsedConfig.BTT.RPC.Port
	}

	cfg.DHTEnabled = config.ParsedConfig.BTT.DHT.Enabled

	if config.ParsedConfig.BTT.DHT.Enabled {
		cfg.DHTPort = uint16(config.ParsedConfig.BTT.DHT.Port)
	}

	session, err := torrent.NewSession(cfg)
	if err != nil {
		logger.L.Fatal().Msgf("Failed to create torrent downloader %v", err)
	}

	secureClient := grab.NewClient()
	insecureClient := &grab.Client{
		HTTPClient: &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}},
	}

	return &Service{
		DB:            db,
		BTTClient:     session,
		GrabClient:    secureClient,
		GrabInsecure:  insecureClient,
		httpResponses: make(map[string]*grab.Response),
		inflight:      make(map[uint]struct{}),
	}
}

func defaultOutName(src string, ext string) string {
	base := filepath.Base(src)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	if ext == "" {
		return base
	}

	return base
}

func (s *Service) RegisterJobs() {
	db.QueueRegisterJSON("utils-download-start", func(ctx context.Context, payload utilitiesServiceInterfaces.DownloadStartPayload) error {
		if err := s.StartDownload(&payload.ID); err != nil {
			logger.L.Error().Uint("download_id", payload.ID).Err(err).Msg("StartDownload failed")
		}

		return nil
	})

	db.QueueRegisterNoPayload("utils-download-sync", func(ctx context.Context) error {
		if err := s.SyncDownloadProgress(); err != nil {
			logger.L.Error().Err(err).Msg("SyncDownloadProgress failed")
		}

		return nil
	})

	db.QueueRegisterJSON("utils-download-postproc", func(ctx context.Context, payload utilitiesServiceInterfaces.DownloadPostProcPayload) error {
		if err := s.StartPostProcess(&payload.ID); err != nil {
			logger.L.Error().Uint("download_id", payload.ID).Err(err).Msg("PostProcessDownload failed")
		}

		return nil
	})
}
