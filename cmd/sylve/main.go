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
	_ "sylve/docs/swagger"
	"sylve/internal/cmd"
	"sylve/internal/config"
	"sylve/internal/db"
	"sylve/internal/handlers"
	"sylve/internal/logger"
	"sylve/internal/services"
	"sylve/internal/services/auth"
	"sylve/internal/services/disk"
	"sylve/internal/services/info"
	"sylve/internal/services/zfs"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// @contact.name   Alchemilla
// @contact.url    https://github.com/AlchemillaHQ/Sylve
// @contact.email  hello@alchemilla.io

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

// @license.name  BSD-2-Clause
// @license.url   https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE
// @x-logo {"url": "https://downloads-bucket.difuse.io/c55d3cfe_redoc.png", "backgroundColor": "#FFFFFF", "altText": "redoc-logo"}

func main() {
	cmd.AsciiArt()
	cfg := config.ParseConfig(cmd.ParseFlags())
	logger.InitLogger(cfg.DataPath, cfg.LogLevel)

	d := db.SetupDatabase(cfg, false)

	serviceRegistry := services.NewServiceRegistry(d)
	aS := serviceRegistry.AuthService
	sS := serviceRegistry.StartupService
	iS := serviceRegistry.InfoService
	zS := serviceRegistry.ZfsService
	dS := serviceRegistry.DiskService

	err := sS.Initialize(aS.(*auth.Service))
	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize at startup")
	} else {
		logger.L.Info().Msg("Basic initializations complete")
	}

	go aS.ClearExpiredJWTTokens()

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard

	r := gin.Default()
	r.Use(gzip.Gzip(gzip.DefaultCompression))

	handlers.RegisterRoutes(r,
		cfg.Environment,
		cfg.ProxyToVite,
		aS.(*auth.Service),
		iS.(*info.Service),
		zS.(*zfs.Service),
		dS.(*disk.Service),
	)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		logger.L.Info().Msgf("Server started on %s:%d", cfg.IP, cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.L.Info().Msg("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.L.Error().Err(err).Msg("Server forced to shutdown")
	}

	wg.Wait()
	logger.L.Info().Msg("Server exited properly")
}
