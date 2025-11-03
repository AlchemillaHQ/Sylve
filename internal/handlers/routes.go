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
	"net/http"

	static "github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/alchemillahq/sylve/internal/assets"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authHandlers "github.com/alchemillahq/sylve/internal/handlers/auth"
	basicHandlers "github.com/alchemillahq/sylve/internal/handlers/basic"
	clusterHandlers "github.com/alchemillahq/sylve/internal/handlers/cluster"
	diskHandlers "github.com/alchemillahq/sylve/internal/handlers/disk"
	infoHandlers "github.com/alchemillahq/sylve/internal/handlers/info"
	jailHandlers "github.com/alchemillahq/sylve/internal/handlers/jail"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkHandlers "github.com/alchemillahq/sylve/internal/handlers/network"
	sambaHandlers "github.com/alchemillahq/sylve/internal/handlers/samba"
	systemHandlers "github.com/alchemillahq/sylve/internal/handlers/system"
	utilitiesHandlers "github.com/alchemillahq/sylve/internal/handlers/utilities"
	vmHandlers "github.com/alchemillahq/sylve/internal/handlers/vm"
	vncHandler "github.com/alchemillahq/sylve/internal/handlers/vnc"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	diskService "github.com/alchemillahq/sylve/internal/services/disk"
	infoService "github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/samba"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesService "github.com/alchemillahq/sylve/internal/services/utilities"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"

	zfsHandlers "github.com/alchemillahq/sylve/internal/handlers/zfs"
)

// @title           Sylve API
// @version         0.0.1
// @description     Sylve is a lightweight GUI for managing Bhyve, Jails, ZFS, networking, and more on FreeBSD.
// @termsOfService  https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE

// @contact.name   Alchemilla Ventures Pvt. Ltd.
// @contact.url    https://alchemilla.io
// @contact.email  hello@alchemilla.io

// @license.name  BSD-2-Clause
// @license.url   https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

// @host      sylve.lan:8181
// @BasePath  /api
func RegisterRoutes(r *gin.Engine,
	environment string,
	proxyToVite bool,
	authService *authService.Service,
	infoService *infoService.Service,
	zfsService *zfsService.Service,
	diskService *diskService.Service,
	networkService *networkService.Service,
	utilitiesService *utilitiesService.Service,
	systemService *systemService.Service,
	libvirtService *libvirt.Service,
	sambaService *samba.Service,
	jailService *jail.Service,
	clusterService *cluster.Service,
	fsm *clusterModels.FSMDispatcher,
	db *gorm.DB,
) {
	api := r.Group("/api")

	health := api.Group("/health")
	health.Use(middleware.EnsureAuthenticated(authService))
	{
		health.GET("/basic", BasicHealthCheckHandler(systemService))
		health.POST("/basic", BasicHealthCheckHandler(systemService))
		health.GET("/http", HTTPHealthCheckHandler)
	}

	basic := api.Group("/basic")
	basic.Use(middleware.EnsureAuthenticated(authService))
	{
		basic.GET("/settings", basicHandlers.GetBasicSettings(systemService))
		basic.POST("/initialize", basicHandlers.Initialize(systemService))
	}

	info := api.Group("/info")
	info.Use(EnsureCorrectHost(db))
	info.Use(middleware.EnsureAuthenticated(authService))
	info.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		info.GET("/basic", infoHandlers.BasicInfo(infoService))

		info.GET("/cpu", infoHandlers.RealTimeCPUInfoHandler(infoService))
		info.GET("/cpu/historical", infoHandlers.HistoricalCPUInfoHandler(infoService))

		info.GET("/ram", infoHandlers.RAMInfo(infoService))
		info.GET("/ram/historical", infoHandlers.HistoricalRAMInfoHandler(infoService))

		info.GET("/swap", infoHandlers.SwapInfo(infoService))
		info.GET("/swap/historical", infoHandlers.HistoricalSwapInfoHandler(infoService))

		info.GET("/network-interfaces/historical", infoHandlers.HistoricalNetworkInterfacesInfoHandler(infoService))

		notes := info.Group("/notes")
		{
			notes.GET("", infoHandlers.NotesHandler(infoService))
			notes.POST("", infoHandlers.NotesHandler(infoService))
			notes.DELETE("/:id", infoHandlers.NotesHandler(infoService))
			notes.PUT("/:id", infoHandlers.NotesHandler(infoService))
			notes.POST("/bulk-delete", infoHandlers.NotesHandler(infoService))
		}

		info.GET("/audit-records", infoHandlers.AuditRecords(infoService))
		info.GET("/terminal", infoHandlers.HandleTerminalWebsocket)
	}

	zfs := api.Group("/zfs")
	zfs.Use(EnsureCorrectHost(db))
	zfs.Use(middleware.EnsureAuthenticated(authService))
	zfs.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		zfs.GET("/pool/stats/:interval/:limit", zfsHandlers.PoolStats(zfsService))
		zfs.GET("/pool/io-delay", zfsHandlers.AvgIODelay(zfsService))
		zfs.GET("/pool/io-delay/historical", zfsHandlers.AvgIODelayHistorical(zfsService))

		pools := zfs.Group("/pools")
		{
			pools.GET("", zfsHandlers.GetPools(zfsService))
			pools.GET("/disks-usage", zfsHandlers.GetDisksUsage(zfsService))
			pools.POST("", zfsHandlers.CreatePool(infoService, zfsService))
			pools.PATCH("", zfsHandlers.EditPool(infoService, zfsService))
			pools.POST("/:guid/scrub", zfsHandlers.ScrubPool(infoService, zfsService))
			pools.DELETE("/:guid", zfsHandlers.DeletePool(infoService, zfsService))
			pools.POST("/:guid/replace-device", zfsHandlers.ReplaceDevice(infoService, zfsService))
		}

		datasets := zfs.Group("/datasets")
		{
			datasets.GET("", zfsHandlers.GetDatasets(zfsService))
			datasets.POST("/snapshot", zfsHandlers.CreateSnapshot(zfsService))
			datasets.POST("/snapshot/rollback", zfsHandlers.RollbackSnapshot(zfsService))
			datasets.DELETE("/snapshot/:guid", zfsHandlers.DeleteSnapshot(zfsService))

			datasets.GET("/snapshot/periodic", zfsHandlers.GetPeriodicSnapshots(zfsService))
			datasets.POST("/snapshot/periodic", zfsHandlers.CreatePeriodicSnapshot(zfsService))
			datasets.PATCH("/snapshot/periodic", zfsHandlers.ModifyPeriodicSnapshotRetention(zfsService))

			datasets.DELETE("/snapshot/periodic/:guid", zfsHandlers.DeletePeriodicSnapshot(zfsService))

			datasets.POST("/filesystem", zfsHandlers.CreateFilesystem(zfsService))
			datasets.PATCH("/filesystem", zfsHandlers.EditFilesystem(zfsService))
			datasets.DELETE("/filesystem/:guid", zfsHandlers.DeleteFilesystem(zfsService))

			datasets.POST("/volume", zfsHandlers.CreateVolume(zfsService))
			datasets.PATCH("/volume", zfsHandlers.EditVolume(zfsService))
			datasets.POST("/volume/flash", zfsHandlers.FlashVolume(zfsService))
			datasets.DELETE("/volume/:guid", zfsHandlers.DeleteVolume(zfsService))

			datasets.POST("/bulk-delete", zfsHandlers.BulkDeleteDataset(zfsService))
		}
	}

	samba := api.Group("/samba")
	samba.Use(EnsureCorrectHost(db))
	samba.Use(middleware.EnsureAuthenticated(authService))
	samba.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		samba.GET("/config", sambaHandlers.GetGlobalConfig(sambaService))
		samba.POST("/config", sambaHandlers.SetGlobalConfig(sambaService))

		samba.GET("/shares", sambaHandlers.GetShares(sambaService))
		samba.POST("/shares", sambaHandlers.CreateShare(sambaService))
		samba.PUT("/shares", sambaHandlers.UpdateShare(sambaService))
		samba.DELETE("/shares/:id", sambaHandlers.DeleteShare(sambaService))

		samba.GET("/audit-logs", sambaHandlers.GetAuditLogs(sambaService))
	}

	disk := api.Group("/disk")
	disk.Use(EnsureCorrectHost(db))
	disk.Use(middleware.EnsureAuthenticated(authService))
	disk.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		disk.GET("/list", diskHandlers.List(diskService))
		disk.POST("/wipe", diskHandlers.WipeDisk(diskService, infoService))
		disk.POST("/initialize-gpt", diskHandlers.InitializeGPT(diskService, infoService))
		disk.POST("/create-partitions", diskHandlers.CreatePartition(infoService))
		disk.POST("/delete-partition", diskHandlers.DeletePartition(infoService))
	}

	network := api.Group("/network")
	network.Use(EnsureCorrectHost(db))
	network.Use(middleware.EnsureAuthenticated(authService))
	network.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		network.GET("/object", networkHandlers.ListNetworkObjects(networkService))
		network.POST("/object", networkHandlers.CreateNetworkObject(networkService))
		network.DELETE("/object/:id", networkHandlers.DeleteNetworkObject(networkService))
		network.PUT("/object/:id", networkHandlers.EditNetworkObject(networkService))

		network.GET("/interface", networkHandlers.ListInterfaces(networkService))

		network.POST("/manual-switch", networkHandlers.CreateManualSwitch(networkService))
		network.DELETE("/manual-switch/:id", networkHandlers.DeleteManualSwitch(networkService))

		network.GET("/switch", networkHandlers.ListSwitches(networkService))
		network.POST("/switch/standard", networkHandlers.CreateStandardSwitch(networkService))
		network.DELETE("/switch/standard/:id", networkHandlers.DeleteStandardSwitch(networkService))
		network.PUT("/switch/standard", networkHandlers.UpdateStandardSwitch(networkService))

		network.GET("/dhcp/config", networkHandlers.GetDHCPConfig(networkService))
		network.PUT("/dhcp/config", networkHandlers.ModifyDHCPConfig(networkService))

		network.GET("/dhcp/range", networkHandlers.GetDHCPRanges(networkService))
		network.POST("/dhcp/range", networkHandlers.CreateDHCPRange(networkService))
		network.PUT("/dhcp/range/:id", networkHandlers.ModifyDHCPRange(networkService))
		network.DELETE("/dhcp/range/:id", networkHandlers.DeleteDHCPRange(networkService))

		network.GET("/dhcp/lease", networkHandlers.GetDHCPLeases(networkService))
		network.POST("/dhcp/lease", networkHandlers.CreateDHCPLease(networkService))
		network.PUT("/dhcp/lease", networkHandlers.UpdateDHCPLease(networkService))
		network.DELETE("/dhcp/lease/:id", networkHandlers.DeleteDHCPLease(networkService))
	}

	system := api.Group("/system")
	system.Use(EnsureCorrectHost(db))
	system.Use(middleware.EnsureAuthenticated(authService))
	system.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		system.GET("/pci-devices", systemHandlers.ListDevices())
		system.GET("/ppt-devices", systemHandlers.ListPPTDevices(systemService))
		system.POST("/ppt-devices", systemHandlers.AddPPTDevice(systemService))
		system.DELETE("/ppt-devices/:id", systemHandlers.RemovePPTDevice(systemService))
	}

	fileExplorer := system.Group("/file-explorer")
	fileExplorer.Use(EnsureCorrectHost(db))
	fileExplorer.Use(middleware.EnsureAuthenticated(authService))
	fileExplorer.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		fileExplorer.GET("", systemHandlers.Files(systemService))
		fileExplorer.POST("", systemHandlers.AddFileOrFolder(systemService))

		fileExplorer.POST("/delete", systemHandlers.DeleteFilesOrFolders(systemService))
		fileExplorer.DELETE("", systemHandlers.DeleteFileOrFolder(systemService))

		fileExplorer.POST("/rename", systemHandlers.RenameFileOrFolder(systemService))
		fileExplorer.GET("/download", systemHandlers.DownloadFile(systemService))

		fileExplorer.POST("/copy-or-move", systemHandlers.CopyOrMoveFileOrFolder(systemService))
		fileExplorer.POST("/copy-or-move-batch", systemHandlers.CopyOrMoveFilesOrFolders(systemService))

		fileExplorer.POST("/upload", systemHandlers.UploadFile(systemService))
		fileExplorer.DELETE("/upload", systemHandlers.DeleteUpload(systemService))
	}

	vm := api.Group("/vm")
	vm.Use(EnsureCorrectHost(db))
	vm.Use(middleware.EnsureAuthenticated(authService))
	vm.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		vm.POST("/:action/:id", vmHandlers.VMActionHandler(libvirtService))
		vm.GET("/simple", vmHandlers.ListVMsSimple(libvirtService))
		vm.GET("", vmHandlers.ListVMs(libvirtService))
		vm.POST("", vmHandlers.CreateVM(libvirtService))
		vm.DELETE("/:id", vmHandlers.RemoveVM(libvirtService))
		vm.GET("/domain/:id", vmHandlers.GetLvDomain(libvirtService))
		vm.GET("/stats/:vmId/:limit", vmHandlers.GetVMStats(libvirtService))
		vm.PUT("/description", vmHandlers.UpdateVMDescription(libvirtService))

		vm.POST("/storage/detach", vmHandlers.StorageDetach(libvirtService))
		vm.POST("/storage/attach", vmHandlers.StorageAttach(libvirtService))

		vm.POST("/network/detach", vmHandlers.NetworkDetach(libvirtService))
		vm.POST("/network/attach", vmHandlers.NetworkAttach(libvirtService))

		vm.PUT("/hardware/cpu/:vmid", vmHandlers.ModifyCPU(libvirtService))
		vm.PUT("/hardware/ram/:vmid", vmHandlers.ModifyRAM(libvirtService))
		vm.PUT("/hardware/vnc/:vmid", vmHandlers.ModifyVNC(libvirtService))
		vm.PUT("/hardware/ppt/:vmid", vmHandlers.ModifyPassthroughDevices(libvirtService))

		vm.PUT("/options/wol/:vmid", vmHandlers.ModifyWakeOnLan(libvirtService))
		vm.PUT("/options/boot-order/:vmid", vmHandlers.ModifyBootOrder(libvirtService))
		vm.PUT("/options/clock/:vmid", vmHandlers.ModifyClock(libvirtService))
		vm.PUT("/options/serial-console/:vmid", vmHandlers.ModifySerialConsole(libvirtService))

		vm.GET("/console", vmHandlers.HandleLibvirtTerminalWebsocket)
	}

	jail := api.Group("/jail")
	jail.Use(EnsureCorrectHost(db))
	jail.Use(middleware.EnsureAuthenticated(authService))
	jail.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		jail.GET("/simple", jailHandlers.ListJailsSimple(jailService))
		jail.GET("/state", jailHandlers.ListJailStates(jailService))
		jail.GET("", jailHandlers.ListJails(jailService))
		jail.POST("/action/:action/:ctId", jailHandlers.JailAction(jailService))
		jail.PUT("/description", jailHandlers.UpdateJailDescription(jailService))
		jail.GET("/:id/logs", jailHandlers.GetJailLogs(jailService))
		jail.PUT("/memory", jailHandlers.UpdateJailMemory(jailService))
		jail.PUT("/cpu", jailHandlers.UpdateJailCPU(jailService))
		jail.GET("/stats/:ctId/:limit", jailHandlers.GetJailStats(jailService))
		jail.PUT("/resource-limits/:ctId", jailHandlers.UpdateResourceLimits(jailService))

		jail.POST("", jailHandlers.CreateJail(jailService))
		jail.DELETE("/:ctid", jailHandlers.DeleteJail(jailService))

		jail.GET("/console", jailHandlers.HandleJailTerminalWebsocket)
		jail.POST("/network/inheritance", jailHandlers.InheritJailNetwork(jailService))
		jail.DELETE("/network/disinherit/:ctId", jailHandlers.DisinheritJailNetwork(jailService))

		jail.POST("/network", jailHandlers.AddNetwork(jailService))
		jail.DELETE("/network/:ctId/:networkId", jailHandlers.DeleteNetwork(jailService))
	}

	utilities := api.Group("/utilities")
	utilities.Use(EnsureCorrectHost(db))
	utilities.Use(middleware.EnsureAuthenticated(authService))
	utilities.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		utilities.POST("/downloads", utilitiesHandlers.DownloadFile(utilitiesService))
		utilities.GET("/downloads", utilitiesHandlers.ListDownloads(utilitiesService))
		utilities.GET("/downloads/:uuid", utilitiesHandlers.DownloadFileFromSignedURL(utilitiesService))
		utilities.DELETE("/downloads/:id", utilitiesHandlers.DeleteDownload(utilitiesService))
		utilities.POST("/downloads/bulk-delete", utilitiesHandlers.BulkDeleteDownload(utilitiesService))
		utilities.POST("/downloads/signed-url", utilitiesHandlers.GetSignedDownloadURL(utilitiesService))
	}

	auth := api.Group("/auth")
	auth.Use(middleware.EnsureAuthenticated(authService))
	auth.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		auth.POST("/login", authHandlers.LoginHandler(authService))
		auth.GET("/logout", authHandlers.LogoutHandler(authService))
	}

	users := auth.Group("/users")
	users.Use(EnsureCorrectHost(db))
	{
		users.GET("", authHandlers.ListUsersHandler(authService))
		users.POST("", authHandlers.CreateUserHandler(authService))
		users.DELETE("/:id", authHandlers.DeleteUserHandler(authService))
		users.PUT("", authHandlers.EditUserHandler(authService))
	}

	groups := auth.Group("/groups")
	groups.Use(EnsureCorrectHost(db))
	{
		groups.GET("", authHandlers.ListGroupsHandler(authService))
		groups.POST("", authHandlers.CreateGroupHandler(authService))
		groups.DELETE("/:id", authHandlers.DeleteGroupHandler(authService))
		groups.POST("/users", authHandlers.AddUsersToGroupHandler(authService))
	}

	cluster := api.Group("/cluster")
	cluster.Use(middleware.EnsureAuthenticated(authService))
	cluster.Use(middleware.RequestLoggerMiddleware(db, authService))
	{
		cluster.GET("/nodes", clusterHandlers.Nodes(clusterService))
		cluster.GET("/resources", clusterHandlers.Resources(clusterService))

		cluster.GET("", clusterHandlers.GetCluster(clusterService))
		cluster.POST("", clusterHandlers.CreateCluster(authService, clusterService, fsm))
		cluster.POST("/join", clusterHandlers.JoinCluster(authService, clusterService, fsm))
		cluster.POST("/accept-join", clusterHandlers.AcceptJoin(clusterService))
		cluster.DELETE("/reset-node", clusterHandlers.ResetRaftNode(clusterService))
		cluster.POST("/remove-peer", clusterHandlers.RemovePeer(clusterService))
	}

	clusterNotes := cluster.Group("/notes")
	{
		clusterNotes.GET("", clusterHandlers.Notes(clusterService))
		clusterNotes.POST("", clusterHandlers.CreateNote(clusterService))
		clusterNotes.DELETE("/:id", clusterHandlers.DeleteNote(clusterService))
	}

	clusterStorages := cluster.Group("/storage")
	{
		clusterStorages.GET("", clusterHandlers.Storages(clusterService))

		clusterStorages.POST("/s3", clusterHandlers.CreateS3Storage(clusterService))
		clusterStorages.DELETE("/s3/:id", clusterHandlers.DeleteS3Storage(clusterService))

		clusterStorages.POST("/directory", clusterHandlers.CreateDirStorage(clusterService))
		clusterStorages.DELETE("/directory/:id", clusterHandlers.DeleteDirStorage(clusterService))
	}

	vnc := api.Group("/vnc")
	vnc.Use(EnsureCorrectHost(db))
	vnc.Use(middleware.EnsureAuthenticated(authService))
	vnc.Use(middleware.RequestLoggerMiddleware(db, authService))
	vnc.GET("/:port", vncHandler.VNCProxyHandler)

	if proxyToVite {
		r.NoRoute(func(c *gin.Context) {
			ReverseProxy(c, "http://127.0.0.1:5173")
		})
	} else {
		files, err := static.EmbedFolder(assets.SvelteKitFiles, "web-files")
		if err != nil {
			log.Fatalln("Initialization of embed folder failed:", err)
		}

		r.Use(static.Serve("/", files))
		r.NoRoute(func(c *gin.Context) {
			indexFile, err := assets.SvelteKitFiles.ReadFile("web-files/index.html")
			if err != nil {
				c.String(http.StatusInternalServerError, "Internal Server Error")
				return
			}

			c.Data(http.StatusOK, "text/html", indexFile)
		})
	}
}
