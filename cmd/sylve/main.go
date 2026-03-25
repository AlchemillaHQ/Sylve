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
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/handlers"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/repl"
	"github.com/alchemillahq/sylve/internal/services"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
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

	if !sysU.IsRoot() {
		logger.BootstrapFatal("Root privileges required!")
	}

	cfgResult, err := cmd.ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cfg := config.ParseConfig(cfgResult.ConfigPath)
	logger.InitLogger(cfg.DataPath, cfg.LogLevel)
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
	jS := serviceRegistry.JailService
	cS := serviceRegistry.ClusterService
	zeltaS := serviceRegistry.ZeltaService

	clusterSvc := cS.(*cluster.Service)
	if err := clusterSvc.MigrateLegacyPorts(); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_migrate_legacy_cluster_ports")
	}

	jailSvc := jS.(*jail.Service)
	libvirtSvc := lvS.(*libvirt.Service)
	lifecycleSvc := lifecycle.NewService(d, libvirtSvc, jailSvc)
	refreshEmitter := func(reason string) {
		clusterSvc.EmitLeftPanelRefreshClusterWide(reason)
	}
	jailSvc.SetLeftPanelRefreshEmitter(refreshEmitter)
	libvirtSvc.SetLeftPanelRefreshEmitter(refreshEmitter)

	uS.RegisterJobs()
	zS.RegisterJobs()
	zeltaS.RegisterJobs()
	lifecycleSvc.RegisterJobs()

	initContext, initCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer initCancel()

	err = sS.Initialize(aS.(*auth.Service), initContext, qCtx)

	go sysS.StartNetlinkWatcher(qCtx)
	go sysS.NetlinkEventsCleaner(qCtx)
	go libvirtSvc.StartLifecycleWatcher(qCtx)
	go db.StartQueue(qCtx)

	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize at startup")
	} else {
		logger.L.Info().Msg("Basic initializations complete")

		enqueueCtx, enqueueCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if enqueueErr := lifecycleSvc.EnqueueStartupAutostart(enqueueCtx); enqueueErr != nil {
			logger.L.Warn().Err(enqueueErr).Msg("failed_to_enqueue_guest_autostart_sequence")
		}
		enqueueCancel()
	}

	err = cS.InitRaft(fsm)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize RAFT")
	}

	if err := clusterSvc.StartEmbeddedSSHServer(qCtx); err != nil {
		logger.L.Error().Err(err).Msg("Failed to start embedded cluster SSH server")
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
		uS.(*utilities.Service),
		sysS.(*system.Service),
		libvirtSvc,
		smbS.(*samba.Service),
		jailSvc,
		lifecycleSvc,
		clusterSvc,
		zeltaS,
		fsm,
		d,
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	if cfgResult.REPL {
		replCtx := &repl.Context{
			Auth:           aS.(*auth.Service),
			Jail:           jailSvc,
			VirtualMachine: libvirtSvc,
			Network:        nS.(*networkService.Service),
			QuitChan:       sigChan,
		}

		go repl.Start(replCtx)
	}

	tlsConfig, err := aS.GetSylveCertificate()

	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to get TLS config")
	}

	httpsServer := &http.Server{
		Addr:      fmt.Sprintf(":%d", cfg.Port),
		Handler:   r,
		TLSConfig: tlsConfig,
	}

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: r,
	}

	clusterHTTPSServer := &http.Server{
		Addr:      fmt.Sprintf(":%d", cluster.ClusterEmbeddedHTTPSPort),
		Handler:   r,
		TLSConfig: tlsConfig,
	}

	var wg sync.WaitGroup
	type namedServer struct {
		name string
		srv  *http.Server
	}
	startedServers := make([]namedServer, 0, 3)

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

	startedServers = append(startedServers, namedServer{name: "Intra-cluster HTTPS", srv: clusterHTTPSServer})
	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.L.Info().Msgf("Intra-cluster HTTPS server started on %s:%d", cfg.IP, cluster.ClusterEmbeddedHTTPSPort)
		if err := clusterHTTPSServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logger.L.Fatal().Err(err).Msg("Failed to start intra-cluster HTTPS server")
		}
	}()

	<-sigChan

	logger.L.Info().Msg("Shutting down servers gracefully")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, ns := range startedServers {
		if err := ns.srv.Shutdown(ctx); err != nil {
			logger.L.Error().Err(err).Msgf("%s server forced to shutdown", ns.name)
		}
	}

	wg.Wait()
	logger.L.Info().Msg("Servers exited properly")
}
