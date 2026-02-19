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
	"strings"
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
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	sysU "github.com/alchemillahq/sylve/pkg/system"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func main() {
	cmd.AsciiArt()

	if !sysU.IsRoot() {
		logger.BootstrapFatal("Root privileges required!")
	}

	cfgPath, enableRepl := cmd.ParseFlags()

	cfg := config.ParseConfig(cfgPath)
	logger.InitLogger(cfg.DataPath, cfg.LogLevel)

	d := db.SetupDatabase(cfg, false)
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

	serviceRegistry := services.NewServiceRegistry(d)
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

	uS.RegisterJobs()
	zS.RegisterJobs()
	zeltaS.RegisterJobs()

	go sysS.StartDevdParser(qCtx)
	go sysS.DevdEventsCleaner(qCtx)
	go db.StartQueue(qCtx)

	initContext, initCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer initCancel()

	err := sS.Initialize(aS.(*auth.Service), initContext)

	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize at startup")
	} else {
		logger.L.Info().Msg("Basic initializations complete")
	}

	err = cS.InitRaft(fsm)
	if err != nil {
		if !strings.Contains(err.Error(), "record not found") {
			logger.L.Error().Err(err).Msg("Failed to initialize RAFT")
		} else {
			logger.L.Info().Msg("Not initializing RAFT")
		}
	}

	if err := zelta.EnsureZeltaInstalled(); err != nil {
		logger.L.Error().Err(err).Msg("Failed to install Zelta")
	}

	go zeltaS.StartBackupScheduler(qCtx)
	go aS.ClearExpiredJWTTokens()

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
		nS.(*network.Service),
		uS.(*utilities.Service),
		sysS.(*system.Service),
		lvS.(*libvirt.Service),
		smbS.(*samba.Service),
		jS.(*jail.Service),
		cS.(*cluster.Service),
		zeltaS,
		fsm,
		d,
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	if enableRepl {
		replCtx := &repl.Context{
			Auth:           aS.(*auth.Service),
			Jail:           jS.(*jail.Service),
			VirtualMachine: lvS.(*libvirt.Service),
			Network:        nS.(*network.Service),
			QuitChan:       sigChan,
		}

		go repl.Start(replCtx)
	}

	tlsConfig, err := aS.GetSylveCertificate()

	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to get TLS config")
	}

	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", cfg.Port),
		Handler:   r,
		TLSConfig: tlsConfig,
	}

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: r,
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		logger.L.Info().Msgf("HTTPS server started on %s:%d", cfg.IP, cfg.Port)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logger.L.Fatal().Err(err).Msg("Failed to start HTTPS server")
		}
	}()

	go func() {
		defer wg.Done()
		logger.L.Info().Msgf("HTTP server started on %s:%d", cfg.IP, cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L.Fatal().Err(err).Msg("Failed to start HTTP server")
		}
	}()

	<-sigChan

	logger.L.Info().Msg("Shutting down servers gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.L.Error().Err(err).Msg("HTTPS server forced to shutdown")
	}
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.L.Error().Err(err).Msg("HTTP server forced to shutdown")
	}

	wg.Wait()
	logger.L.Info().Msg("Servers exited properly")
}
