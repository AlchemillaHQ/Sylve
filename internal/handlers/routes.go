// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"log"

	"github.com/gin-gonic/gin"

	"sylve/internal/assets"

	static "github.com/soulteary/gin-static"

	diskHandlers "sylve/internal/handlers/disk"

	infoHandlers "sylve/internal/handlers/info"
	"sylve/internal/handlers/middleware"

	zfsHandlers "sylve/internal/handlers/zfs"

	authService "sylve/internal/services/auth"

	diskService "sylve/internal/services/disk"

	infoService "sylve/internal/services/info"

	zfsService "sylve/internal/services/zfs"
)

func RegisterRoutes(r *gin.Engine,
	environment string,
	proxyToVite bool,
	authService *authService.Service,
	infoService *infoService.Service,
	zfsService *zfsService.Service,
	diskService *diskService.Service,
) {
	api := r.Group("/api")

	health := api.Group("/health")
	health.Use(middleware.EnsureAuthenticated(authService))
	{
		health.GET("/basic", BasicHealthCheckHandler)
		health.GET("/http", HTTPHealthCheckHandler)
	}

	info := api.Group("/info")
	info.Use(middleware.EnsureAuthenticated(authService))
	{
		info.GET("/basic", infoHandlers.BasicInfo(infoService))

		info.GET("/cpu", infoHandlers.CurrentCPUInfo(infoService))
		info.GET("/cpu/historical", infoHandlers.HistoricalCPUInfo(infoService))

		info.GET("/ram", infoHandlers.RAMInfo(infoService))
		info.GET("/swap", infoHandlers.SwapInfo(infoService))

		notes := info.Group("/notes")
		{
			notes.GET("", infoHandlers.NotesHandler(infoService))
			notes.POST("", infoHandlers.NotesHandler(infoService))
			notes.DELETE("/:id", infoHandlers.NotesHandler(infoService))
			notes.PUT("/:id", infoHandlers.NotesHandler(infoService))
		}

		info.GET("/audit-logs", infoHandlers.AuditLogs(infoService))
	}

	zfs := api.Group("/zfs")
	zfs.Use(middleware.EnsureAuthenticated(authService))
	{
		zfs.GET("/pool/list", zfsHandlers.GetPools(zfsService))
		zfs.GET("/pool/io-delay", zfsHandlers.AvgIODelay(zfsService))
	}

	disk := api.Group("/disk")
	disk.Use(middleware.EnsureAuthenticated(authService))
	{
		disk.GET("/list", diskHandlers.List(diskService))
		disk.POST("/wipe", diskHandlers.WipeDisk(diskService))
		disk.POST("/initialize-gpt", diskHandlers.InitializeGPT(diskService))
	}

	auth := api.Group("/auth")
	{
		auth.POST("/login", LoginHandler(authService))
		auth.Any("/logout", LogoutHandler(authService))
	}

	if proxyToVite {
		r.NoRoute(func(c *gin.Context) {
			ReverseProxy(c, "http://127.0.0.1:5173")
		})
	} else {
		staticFiles, err := static.EmbedFolder(assets.SvelteKitFiles, "web-files")
		if err != nil {
			log.Fatalln("Initialization of embed folder failed:", err)
		} else {
			r.Use(static.Serve("/", staticFiles))
			r.NoRoute(func(c *gin.Context) {
				c.FileFromFS("200.html", staticFiles)
			})
		}
	}
}
