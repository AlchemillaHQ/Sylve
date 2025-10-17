// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"crypto/tls"
	"net/http"

	"github.com/alchemillahq/sylve/internal/config"
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
