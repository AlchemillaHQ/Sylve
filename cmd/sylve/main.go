// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	dbModels "github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers"
	"github.com/alchemillahq/sylve/internal/logger"
	notificationFacade "github.com/alchemillahq/sylve/internal/notifications"
	"github.com/alchemillahq/sylve/internal/repl"
	"github.com/alchemillahq/sylve/internal/services"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	notificationsService "github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	portnetwork "github.com/alchemillahq/sylve/pkg/network"
	sysU "github.com/alchemillahq/sylve/pkg/system"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func main() {
	cmd.AsciiArt(os.Stdout)

	cfgResult, err := cmd.ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if cfgResult.ShowHelp {
		cmd.PrintUsage(os.Stdout)
		return
	}

	if cfgResult.ShowVersion {
		return
	}

	if !sysU.IsRoot() {
		logger.BootstrapFatal("Root privileges required!")
	}

	startLocalSylve, attachErr := shouldStartLocalSylve(cfgResult.REPL, repl.TryAttachSocketConsole)
	if attachErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach to running Sylve console: %v\n", attachErr)
		os.Exit(1)
	}
	if !startLocalSylve {
		return
	}

	resolvedConfigPath, err := cmd.ResolveConfigPath(cfgResult.ConfigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cfg := config.ParseConfig(resolvedConfigPath)
	logger.InitLogger(cfg.Environment, cfg.DataPath, cfg.LogLevel)
	logger.L.Info().
		Str("environment", string(cfg.Environment)).
		Int8("logLevel", cfg.LogLevel).
		Str("dataPath", cfg.DataPath).
		Msg("Sylve configuration loaded")

	if cfg.Profile {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(5)

		go func() {
			addr := "127.0.0.1:6060"

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				logger.L.Error().Err(err).Str("addr", addr).Msg("failed_to_start_pprof")
				return
			}

			logger.L.Info().Str("addr", addr).Msg("pprof_server_started")

			if err := http.Serve(ln, nil); err != nil && err != http.ErrServerClosed {
				logger.L.Error().Err(err).Msg("pprof_server_failed")
			}
		}()
	}

	logger.L.Info().Msgf("Sylve logs: %s/logs.json", cfg.DataPath)

	if err := preflightRequiredPorts(cfg, portnetwork.TryBindToPort); err != nil {
		logger.L.Fatal().Err(err).Msg("startup_port_preflight_failed")
	}

	d := db.SetupDatabase(cfg, false)
	telemetryDB := db.SetupTelemetryDatabase(cfg, d, false)
	_ = db.SetupCache(cfg)

	go func() {
		for {
			time.Sleep(5 * time.Minute)
			db.RunCacheGC()
		}
	}()

	if err := db.SetupQueue(cfg, false, logger.L); err != nil {
		logger.L.Fatal().Err(err).Msg("failed to setup queue")
	}

	qCtx, qStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer qStop()

	fsm := clusterModels.NewFSMDispatcher(d)
	clusterModels.RegisterDefaultHandlers(fsm)

	serviceRegistry := services.NewServiceRegistry(d, telemetryDB)
	aS := serviceRegistry.AuthService
	sS := serviceRegistry.StartupService
	iS := serviceRegistry.InfoService
	zS := serviceRegistry.ZfsService
	dS := serviceRegistry.DiskService
	nS := serviceRegistry.NetworkService
	uS := serviceRegistry.UtilitiesService
	sysS := serviceRegistry.SystemService
	lvS := serviceRegistry.LibvirtService
	smbS := serviceRegistry.SambaService
	iscsiSvc := serviceRegistry.ISCSIService.(*iscsi.Service)
	jS := serviceRegistry.JailService
	cS := serviceRegistry.ClusterService
	zeltaS := serviceRegistry.ZeltaService
	notificationService := notificationsService.NewService(d)
	notificationFacade.SetEmitter(notificationService)

	clusterSvc := cS.(*cluster.Service)
	if err := clusterSvc.MigrateLegacyPorts(); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_migrate_legacy_cluster_ports")
	}

	jailSvc := jS.(*jail.Service)
	libvirtSvc := lvS.(*libvirt.Service)
	lifecycleSvc := lifecycle.NewService(d, telemetryDB, libvirtSvc, jailSvc)
	refreshEmitter := func(reason string) {
		clusterSvc.EmitLeftPanelRefreshClusterWide(reason)
	}
	jailSvc.SetLeftPanelRefreshEmitter(refreshEmitter)
	libvirtSvc.SetLeftPanelRefreshEmitter(refreshEmitter)

	uS.RegisterJobs()
	zS.RegisterJobs()
	zeltaS.RegisterJobs()
	lifecycleSvc.RegisterJobs()

	zfs.EncryptionKeyCreatedHook = func(uuid, keyData, keyFormat string) {
		if err := clusterSvc.ForwardEncryptionKeyToLeader(uuid, keyData, keyFormat); err != nil {
			logger.L.Warn().Err(err).Str("uuid", uuid).Msg("encryption_key_hook_register_failed")
		}
	}

	zfs.EncryptionKeyDeletedHook = func(uuid string) {
		clusterSvc.ForwardEncryptionKeyDeleteToLeader(uuid)
	}

	initContext, initCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer initCancel()

	err = sS.Initialize(aS.(*auth.Service), initContext, qCtx)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize at startup")
	}

	logger.L.Info().Msg("Basic initializations complete")

	if err := nS.(*networkService.Service).SyncFirewallRuntimeState(); err != nil {
		logger.L.Error().Err(err).Msg("failed_to_sync_firewall_runtime_state_during_startup")
	}

	go nS.(*networkService.Service).StartObjectRefreshWorker(qCtx)

	startAdvancedStartupWorkers, basicSettings, settingsErr := shouldStartAdvancedStartupWorkers(func() (dbModels.BasicSettings, error) {
		var settings dbModels.BasicSettings
		if err := d.First(&settings).Error; err != nil {
			return dbModels.BasicSettings{}, err
		}
		return settings, nil
	})
	if settingsErr != nil {
		logger.L.Fatal().Err(settingsErr).Msg("Failed to evaluate startup readiness")
	}

	go db.StartQueue(qCtx)
	db.StartPruneWorker(qCtx, d)

	if startAdvancedStartupWorkers {
		logger.L.Info().Msg("Starting background watchers and queues")
		go sysS.StartNetlinkWatcher(qCtx)
		go sysS.NetlinkEventsCleaner(qCtx)

		if libvirtSvc.IsVirtualizationEnabled() {
			go libvirtSvc.StartLifecycleWatcher(qCtx)
		}

		enqueueCtx, enqueueCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if enqueueErr := lifecycleSvc.EnqueueStartupAutostart(enqueueCtx); enqueueErr != nil {
			logger.L.Warn().Err(enqueueErr).Msg("failed_to_enqueue_guest_autostart_sequence")
		}
		enqueueCancel()
	} else {
		logger.L.Info().
			Bool("initialized", basicSettings.Initialized).
			Bool("restarted", basicSettings.Restarted).
			Msg("System initialization not finalized; skipping advanced watchers and autostart queue")
	}

	err = cS.InitRaft(fsm)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize RAFT")
	}

	if err := zelta.EnsureZeltaInstalled(); err != nil {
		logger.L.Error().Err(err).Msg("Failed to install Zelta")
	}

	go zeltaS.StartBackupScheduler(qCtx)
	go zeltaS.StartReplicationScheduler(qCtx)
	go aS.ClearExpiredJWTTokens(qCtx)

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard

	r := gin.Default()
	r.Use(gzip.Gzip(
		gzip.DefaultCompression,
		gzip.WithExcludedPaths([]string{"/api/utilities/downloads"}),
	))

	handlers.RegisterRoutes(r,
		cfg.Environment,
		cfg.ProxyToVite,
		aS.(*auth.Service),
		iS.(*info.Service),
		zS.(*zfs.Service),
		dS.(*disk.Service),
		nS.(*networkService.Service),
		notificationService,
		uS.(*utilities.Service),
		sysS.(*system.Service),
		libvirtSvc,
		smbS.(*samba.Service),
		iscsiSvc,
		jailSvc,
		lifecycleSvc,
		clusterSvc,
		zeltaS,
		fsm,
		d,
		telemetryDB,
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	replCtx := &repl.Context{
		Auth:           aS.(*auth.Service),
		Jail:           jailSvc,
		VirtualMachine: libvirtSvc,
		Network:        nS.(*networkService.Service),
		QuitChan:       sigChan,
	}

	replSocketServer, replSocketErr := repl.StartSocketServer(replCtx)
	if replSocketErr != nil {
		logger.L.Warn().Err(replSocketErr).Msg("Failed to start REPL socket server")
	}
	defer func() {
		if replSocketServer != nil {
			if err := replSocketServer.Close(); err != nil {
				logger.L.Warn().Err(err).Msg("Failed to close REPL socket server")
			}
		}
	}()

	if cfgResult.REPL {
		go repl.Start(replCtx)
	}

	tlsConfig, err := aS.GetSylveCertificate()

	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to get TLS config")
	}

	httpsServer := &http.Server{
		Addr:      fmt.Sprintf("%s:%d", cfg.IP, cfg.Port),
		Handler:   r,
		TLSConfig: tlsConfig,
	}

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.IP, cfg.HTTPPort),
		Handler: r,
	}

	var wg sync.WaitGroup
	type namedServer struct {
		name string
		srv  *http.Server
	}
	startedServers := make([]namedServer, 0, 2)
	logger.L.Info().
		Int("https", cfg.Port).
		Int("http", cfg.HTTPPort).
		Int("cluster_https", cluster.ClusterEmbeddedHTTPSPort).
		Int("cluster_ssh", cluster.ClusterEmbeddedSSHPort).
		Int("raft", cluster.ClusterRaftPort).
		Msg("Listener ports")

	if cfg.Port != 0 {
		startedServers = append(startedServers, namedServer{name: "HTTPS", srv: httpsServer})
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.L.Info().Msgf("HTTPS server started on %s:%d", cfg.IP, cfg.Port)
			if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logger.L.Fatal().Err(err).Msg("Failed to start HTTPS server")
			}
		}()
	}

	if cfg.HTTPPort != 0 {
		startedServers = append(startedServers, namedServer{name: "HTTP", srv: httpServer})
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.L.Info().Msgf("HTTP server started on %s:%d", cfg.IP, cfg.HTTPPort)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.L.Fatal().Err(err).Msg("Failed to start HTTP server")
			}
		}()
	}

	// clusterHTTPS holds the intra-cluster HTTPS server when started; guarded by clusterHTTPSMu.
	var clusterHTTPSMu sync.Mutex
	var activeClusterHTTPS *http.Server

	startClusterListeners := func(clusterIP string) error {
		if err := clusterSvc.StartEmbeddedSSHServer(qCtx, clusterIP); err != nil {
			return fmt.Errorf("cluster_ssh_start_failed: %w", err)
		}

		clusterHTTPSMu.Lock()
		defer clusterHTTPSMu.Unlock()
		if activeClusterHTTPS != nil {
			return nil // already running
		}

		srv := &http.Server{
			Addr:      fmt.Sprintf("%s:%d", clusterIP, cluster.ClusterEmbeddedHTTPSPort),
			Handler:   r,
			TLSConfig: tlsConfig,
		}
		activeClusterHTTPS = srv
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.L.Info().Msgf("Intra-cluster HTTPS server started on %s:%d", clusterIP, cluster.ClusterEmbeddedHTTPSPort)
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logger.L.Fatal().Err(err).Msg("Failed to start intra-cluster HTTPS server")
			}
		}()
		return nil
	}

	clusterSvc.SetClusterStartHook(startClusterListeners)

	// If this node is already part of a cluster, start the cluster listeners immediately.
	var clusterRecord clusterModels.Cluster
	if err := d.First(&clusterRecord).Error; err == nil && clusterRecord.Enabled && clusterRecord.RaftIP != "" {
		if err := startClusterListeners(clusterRecord.RaftIP); err != nil {
			logger.L.Error().Err(err).Msg("failed_to_start_cluster_listeners_at_startup")
		}
	}

	<-sigChan

	logger.L.Info().Msg("Shutting down servers gracefully")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, ns := range startedServers {
		if err := ns.srv.Shutdown(ctx); err != nil {
			logger.L.Error().Err(err).Msgf("%s server forced to shutdown", ns.name)
		}
	}

	clusterHTTPSMu.Lock()
	if activeClusterHTTPS != nil {
		if err := activeClusterHTTPS.Shutdown(ctx); err != nil {
			logger.L.Error().Err(err).Msg("Intra-cluster HTTPS server forced to shutdown")
		}
	}
	clusterHTTPSMu.Unlock()

	wg.Wait()
	logger.L.Info().Msg("Servers exited properly")
}
