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
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/cavaliergopher/grab/v3"
	"github.com/cenkalti/rain/v2/torrent"
	"gorm.io/gorm"
)

var _ utilitiesServiceInterfaces.UtilitiesServiceInterface = (*Service)(nil)

// MaxRequestBodyBytes bounds Utilities JSON decoding and audit capture while
// multipart downloader uploads retain their dedicated streaming limits.
const MaxRequestBodyBytes int64 = 1 * 1024 * 1024

type torrentRuntime interface {
	AddURI(uri string, options *torrent.AddTorrentOptions) (*torrent.Torrent, error)
	GetTorrent(id string) *torrent.Torrent
	RemoveTorrent(id string, keepData bool) error
	Close() error
}

type torrentRuntimeFactory func(torrent.Config) (torrentRuntime, error)

type Service struct {
	DB           *gorm.DB
	TelemetryDB  *gorm.DB
	GrabClient   *grab.Client
	GrabInsecure *grab.Client

	torrentMu        sync.RWMutex
	torrentClient    torrentRuntime
	newTorrentClient torrentRuntimeFactory

	VMService    libvirtServiceInterfaces.LibvirtServiceInterface
	JailService  jailServiceInterfaces.JailServiceInterface
	mutationGate interface {
		EnterMutation(context.Context) (context.Context, func(), error)
	}

	wolStartVMFn   func(vm vmModels.VM) error
	wolStartJailFn func(ctid int) error

	httpRspMu     sync.Mutex
	httpResponses map[string]*grab.Response

	postq      chan uint
	workerOnce sync.Once

	inflightMu sync.Mutex
	inflight   map[uint]struct{}

	cloudInitTemplateMu sync.Mutex

	signingSecretMu     sync.RWMutex
	signingSecretInitMu sync.Mutex
	downloadSignSecret  string

	syncQueueMu        sync.Mutex
	downloadSyncQueued bool
	enqueueNoPayloadFn func(ctx context.Context, name string) error
	downloadSyncRunMu  sync.Mutex

	downloadStartRunMu     sync.Mutex
	downloadStartRunning   map[uint]struct{}
	downloadDeleting       map[uint]struct{}
	downloadStartQueueMu   sync.Mutex
	downloadStartQueued    map[uint]struct{}
	enqueueDownloadStartFn func(
		ctx context.Context,
		payload utilitiesServiceInterfaces.DownloadStartPayload,
	) error

	uploadLifecycleMu  sync.Mutex
	uploadActiveMu     sync.Mutex
	activeUploads      map[string]struct{}
	uploadNowFn        func() time.Time
	uploadHostnameFn   func() (string, error)
	uploadStagingDirFn func() string
}

func (s *Service) SetMutationAdmission(gate interface {
	EnterMutation(context.Context) (context.Context, func(), error)
}) {
	s.mutationGate = gate
}

func NewUtilitiesService(
	dbConn *gorm.DB,
	telemetryDB *gorm.DB,
	vmService libvirtServiceInterfaces.LibvirtServiceInterface,
	jailService jailServiceInterfaces.JailServiceInterface,
) utilitiesServiceInterfaces.UtilitiesServiceInterface {
	secureClient := &grab.Client{
		UserAgent:  "grab",
		HTTPClient: &http.Client{Transport: newDownloadTransport(false)},
	}
	insecureClient := &grab.Client{
		UserAgent:  "grab",
		HTTPClient: &http.Client{Transport: newDownloadTransport(true)},
	}

	return &Service{
		DB:           dbConn,
		TelemetryDB:  telemetryDB,
		GrabClient:   secureClient,
		GrabInsecure: insecureClient,
		newTorrentClient: func(cfg torrent.Config) (torrentRuntime, error) {
			return torrent.NewSession(cfg)
		},
		httpResponses:        make(map[string]*grab.Response),
		inflight:             make(map[uint]struct{}),
		VMService:            vmService,
		JailService:          jailService,
		enqueueNoPayloadFn:   db.EnqueueNoPayload,
		downloadStartRunning: make(map[uint]struct{}),
		downloadDeleting:     make(map[uint]struct{}),
		downloadStartQueued:  make(map[uint]struct{}),
		activeUploads:        make(map[string]struct{}),
	}
}

func torrentRuntimeConfig() torrent.Config {
	cfg := torrent.DefaultConfig
	cfg.Database = config.GetDownloadsPath("torrent.db")
	cfg.DataDir = config.GetDownloadsPath("torrents")

	if config.ParsedConfig == nil {
		return cfg
	}

	cfg.RPCEnabled = config.ParsedConfig.BTT.RPC.Enabled
	if cfg.RPCEnabled {
		cfg.RPCHost = config.ParsedConfig.BTT.RPC.Address
		cfg.RPCPort = config.ParsedConfig.BTT.RPC.Port
	}

	cfg.DHTEnabled = config.ParsedConfig.BTT.DHT.Enabled
	if cfg.DHTEnabled {
		cfg.DHTPort = uint16(config.ParsedConfig.BTT.DHT.Port)
	}

	return cfg
}

// StartOperational starts runtime-backed utility work after initialization has
// completed and the daemon is starting in operational mode.
func (s *Service) StartOperational() error {
	if s == nil {
		return ErrUtilitiesNotReady
	}

	s.torrentMu.Lock()
	if s.torrentClient != nil {
		s.torrentMu.Unlock()
		return nil
	}

	factory := s.newTorrentClient
	if factory == nil {
		factory = func(cfg torrent.Config) (torrentRuntime, error) {
			return torrent.NewSession(cfg)
		}
	}

	torrent.DisableLogging()
	client, err := factory(torrentRuntimeConfig())
	if err != nil {
		s.torrentMu.Unlock()
		return fmt.Errorf("start torrent runtime: %w", err)
	}
	if client == nil {
		s.torrentMu.Unlock()
		return fmt.Errorf("start torrent runtime: nil client")
	}
	s.torrentClient = client
	s.torrentMu.Unlock()

	s.CleanupStaleAuditRecords()
	s.maybeEnqueueDownloadSync()
	return nil
}

// Close stops the torrent runtime. It is safe to call more than once.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}

	s.torrentMu.Lock()
	client := s.torrentClient
	s.torrentClient = nil
	s.torrentMu.Unlock()

	if client == nil {
		return nil
	}
	return client.Close()
}

func (s *Service) activeTorrentClient() (torrentRuntime, error) {
	if s == nil {
		return nil, ErrUtilitiesNotReady
	}

	s.torrentMu.RLock()
	client := s.torrentClient
	s.torrentMu.RUnlock()
	if client == nil {
		return nil, ErrUtilitiesNotReady
	}
	return client, nil
}

func newDownloadTransport(insecure bool) *http.Transport {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if insecure {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return t
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				s.clearDownloadStartQueued(payload.ID)
				return nil
			}
			return err
		}

		s.clearDownloadStartQueued(payload.ID)
		return nil
	})

	db.QueueRegisterNoPayload("utils-download-sync", func(ctx context.Context) error {
		if err := s.SyncDownloadProgress(); err != nil {
			logger.L.Error().Err(err).Msg("SyncDownloadProgress failed")
			return err
		}

		s.clearDownloadSyncQueued()
		return nil
	})

	db.QueueRegisterJSON("utils-download-postproc", func(ctx context.Context, payload utilitiesServiceInterfaces.DownloadPostProcPayload) error {
		if err := s.StartPostProcess(&payload.ID); err != nil {
			logger.L.Error().Uint("download_id", payload.ID).Err(err).Msg("PostProcessDownload failed")
			if errors.Is(err, gorm.ErrRecordNotFound) {
				s.inflightMu.Lock()
				delete(s.inflight, payload.ID)
				s.inflightMu.Unlock()
				return nil
			}
			return err
		}

		return nil
	})

	s.registerWoLJobs()
}

func (s *Service) enqueueDownloadStart(
	ctx context.Context,
	payload utilitiesServiceInterfaces.DownloadStartPayload,
) error {
	enqueueFn := s.enqueueDownloadStartFn
	if enqueueFn != nil {
		return enqueueFn(ctx, payload)
	}
	return db.EnqueueJSON(ctx, "utils-download-start", &payload)
}

func (s *Service) enqueueDownloadStartOnce(
	ctx context.Context,
	payload utilitiesServiceInterfaces.DownloadStartPayload,
) error {
	s.downloadStartQueueMu.Lock()
	if s.downloadStartQueued == nil {
		s.downloadStartQueued = make(map[uint]struct{})
	}
	if _, queued := s.downloadStartQueued[payload.ID]; queued {
		s.downloadStartQueueMu.Unlock()
		return nil
	}
	s.downloadStartQueued[payload.ID] = struct{}{}
	s.downloadStartQueueMu.Unlock()

	if err := s.enqueueDownloadStart(ctx, payload); err != nil {
		s.clearDownloadStartQueued(payload.ID)
		return err
	}
	return nil
}

func (s *Service) clearDownloadStartQueued(id uint) {
	s.downloadStartQueueMu.Lock()
	delete(s.downloadStartQueued, id)
	s.downloadStartQueueMu.Unlock()
}

func (s *Service) beginDownloadStart(id uint) bool {
	s.downloadStartRunMu.Lock()
	defer s.downloadStartRunMu.Unlock()
	if s.downloadStartRunning == nil {
		s.downloadStartRunning = make(map[uint]struct{})
	}
	if _, exists := s.downloadStartRunning[id]; exists {
		return false
	}
	if _, deleting := s.downloadDeleting[id]; deleting {
		return false
	}
	s.downloadStartRunning[id] = struct{}{}
	return true
}

func (s *Service) endDownloadStart(id uint) {
	s.downloadStartRunMu.Lock()
	delete(s.downloadStartRunning, id)
	s.downloadStartRunMu.Unlock()
}

func (s *Service) isDownloadStartRunning(id uint) bool {
	s.downloadStartRunMu.Lock()
	_, running := s.downloadStartRunning[id]
	s.downloadStartRunMu.Unlock()
	return running
}

func (s *Service) isDownloadDeleting(id uint) bool {
	s.downloadStartRunMu.Lock()
	_, deleting := s.downloadDeleting[id]
	s.downloadStartRunMu.Unlock()
	return deleting
}

func (s *Service) beginDownloadDeletion(id uint) bool {
	s.downloadStartRunMu.Lock()
	if s.downloadDeleting == nil {
		s.downloadDeleting = make(map[uint]struct{})
	}
	if _, running := s.downloadStartRunning[id]; running {
		s.downloadStartRunMu.Unlock()
		return false
	}
	if _, deleting := s.downloadDeleting[id]; deleting {
		s.downloadStartRunMu.Unlock()
		return false
	}
	s.downloadDeleting[id] = struct{}{}

	// Keep this lock order aligned with enqueuePostProcOnce so post-processing
	// cannot be queued between the deletion fence and the active-work check.
	s.inflightMu.Lock()
	_, postProcessing := s.inflight[id]
	s.inflightMu.Unlock()
	if postProcessing {
		delete(s.downloadDeleting, id)
		s.downloadStartRunMu.Unlock()
		return false
	}
	s.downloadStartRunMu.Unlock()
	return true
}

func (s *Service) endDownloadDeletion(id uint) {
	s.downloadStartRunMu.Lock()
	delete(s.downloadDeleting, id)
	s.downloadStartRunMu.Unlock()
}

func (s *Service) clearDownloadSyncQueued() {
	s.syncQueueMu.Lock()
	s.downloadSyncQueued = false
	s.syncQueueMu.Unlock()
}

func (s *Service) maybeEnqueueDownloadSync() {
	if _, err := s.activeTorrentClient(); err != nil {
		return
	}

	s.syncQueueMu.Lock()
	if s.downloadSyncQueued {
		s.syncQueueMu.Unlock()
		return
	}
	s.downloadSyncQueued = true
	enqueueFn := s.enqueueNoPayloadFn
	s.syncQueueMu.Unlock()

	if enqueueFn == nil {
		enqueueFn = db.EnqueueNoPayload
	}

	if err := enqueueFn(context.Background(), "utils-download-sync"); err != nil {
		logger.L.Error().Err(err).Msg("Failed to enqueue utils-download-sync job")
		s.clearDownloadSyncQueued()
	}
}
