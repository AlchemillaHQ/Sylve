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
	"strings"

	static "github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/assets"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authHandlers "github.com/alchemillahq/sylve/internal/handlers/auth"
	basicHandlers "github.com/alchemillahq/sylve/internal/handlers/basic"
	certificateHandlers "github.com/alchemillahq/sylve/internal/handlers/certificates"
	clusterHandlers "github.com/alchemillahq/sylve/internal/handlers/cluster"
	diskHandlers "github.com/alchemillahq/sylve/internal/handlers/disk"
	dynamicDNSHandlers "github.com/alchemillahq/sylve/internal/handlers/dynamicdns"
	eventsHandlers "github.com/alchemillahq/sylve/internal/handlers/events"
	infoHandlers "github.com/alchemillahq/sylve/internal/handlers/info"
	iscsiHandlers "github.com/alchemillahq/sylve/internal/handlers/iscsi"
	jailHandlers "github.com/alchemillahq/sylve/internal/handlers/jail"
	mdnsHandlers "github.com/alchemillahq/sylve/internal/handlers/mdns"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	migrationHandlers "github.com/alchemillahq/sylve/internal/handlers/migration"
	networkHandlers "github.com/alchemillahq/sylve/internal/handlers/network"
	notificationsHandlers "github.com/alchemillahq/sylve/internal/handlers/notifications"
	sambaHandlers "github.com/alchemillahq/sylve/internal/handlers/samba"
	systemHandlers "github.com/alchemillahq/sylve/internal/handlers/system"
	taskHandlers "github.com/alchemillahq/sylve/internal/handlers/task"
	utilitiesHandlers "github.com/alchemillahq/sylve/internal/handlers/utilities"
	vmHandlers "github.com/alchemillahq/sylve/internal/handlers/vm"
	vncHandler "github.com/alchemillahq/sylve/internal/handlers/vnc"
	zfsHandlers "github.com/alchemillahq/sylve/internal/handlers/zfs"
	authServicePkg "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/certificates"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	diskServicePkg "github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
	infoService "github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	jailServicePkg "github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/mdns"
	"github.com/alchemillahq/sylve/internal/services/migration"
	networkServicePkg "github.com/alchemillahq/sylve/internal/services/network"
	notificationsService "github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/services/samba"
	systemServicePkg "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesServicePkg "github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"

	"golang.org/x/sync/semaphore"
)

// @title           Sylve API
// @version         0.3.0
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

// @securityDefinitions.apikey ClusterKeyAuth
// @in header
// @name X-Cluster-Key
// @description Exact enabled cluster join key.

// @securityDefinitions.apikey ClusterTokenAuth
// @in header
// @name X-Cluster-Token
// @description Short-lived server-issued cluster token.

// @host      sylve.lan:8181
// @BasePath  /api
func RegisterRoutes(r *gin.Engine,
	environment internal.Environment,
	proxyToVite bool,
	authService *authServicePkg.Service,
	infoService *infoService.Service,
	zfsService *zfsService.Service,
	diskService *diskServicePkg.Service,
	networkService *networkServicePkg.Service,
	notificationService *notificationsService.Service,
	utilitiesService *utilitiesServicePkg.Service,
	systemService *systemServicePkg.Service,
	libvirtService *libvirt.Service,
	sambaService *samba.Service,
	mdnsService *mdns.Service,
	dynamicDNSService *dynamicdns.Service,
	certificateService *certificates.Service,
	iscsiService *iscsi.Service,
	jailService *jailServicePkg.Service,
	lifecycleService *lifecycle.Service,
	clusterService *cluster.Service,
	zeltaService *zelta.Service,
	migrationService *migration.Service,
	fsm *clusterModels.FSMDispatcher,
	db *gorm.DB,
	telemetryDB *gorm.DB,
) {
	api := r.Group("/api")
	uploadAdmission := semaphore.NewWeighted(config.GetUploadsConfig().MaxConcurrentTransfers)
	api.GET("/auth/login/config", authHandlers.LoginConfigHandler())
	publicAuth := api.Group("/auth")
	publicAuth.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	publicAuth.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		publicAuth.POST("/login", authHandlers.LoginHandler(authService))
		publicAuth.POST("/passkeys/login/begin", authHandlers.BeginPasskeyLoginHandler(authService))
		publicAuth.POST("/passkeys/login/finish", authHandlers.FinishPasskeyLoginHandler(authService))
	}

	health := api.Group("/health")
	{
		health.GET("/basic", middleware.AuthenticateBasicHealth(authService), BasicHealthCheckHandler(systemService))
		health.GET("/http", middleware.EnsureAuthenticated(authService), HTTPHealthCheckHandler)
	}

	basic := api.Group("/basic")
	basic.Use(middleware.EnsureAuthenticated(authService))
	requireBasicAdmin := middleware.RequireLocalAdmin(authService)
	{
		basic.GET("/settings", basicHandlers.GetBasicSettings(systemService))
		basic.POST("/initialize", requireBasicAdmin, basicHandlers.Initialize(systemService))
		basic.POST("/system/reboot", requireBasicAdmin, basicHandlers.RebootSystem(systemService))
	}

	info := api.Group("/info")
	info.Use(middleware.EnsureAuthenticated(authService))
	info.Use(EnsureCorrectHost(db, authService))
	info.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		info.GET("/basic", infoHandlers.BasicInfo(infoService))

		info.GET("/cpu", infoHandlers.RealTimeCPUInfoHandler(infoService))
		info.GET("/cpu/historical", infoHandlers.HistoricalCPUInfoHandler(infoService))

		info.GET("/ram", infoHandlers.RAMInfo(infoService))
		info.GET("/ram/historical", infoHandlers.HistoricalRAMInfoHandler(infoService))

		info.GET("/swap", infoHandlers.SwapInfo(infoService))
		info.GET("/swap/historical", infoHandlers.HistoricalSwapInfoHandler(infoService))

		info.GET("/network-interfaces/historical", infoHandlers.HistoricalNetworkInterfacesInfoHandler(infoService))
		info.GET("/summary/history", infoHandlers.SummaryHistoryHandler(infoService))
		info.GET("/summary/history/delta", infoHandlers.SummaryHistoryDeltaHandler(infoService))

		notes := info.Group("/notes")
		{
			notes.GET("", infoHandlers.NotesHandler(infoService))
			notes.POST("", infoHandlers.NotesHandler(infoService))
			notes.DELETE("", infoHandlers.NotesHandler(infoService))
			notes.DELETE("/:id", infoHandlers.NotesHandler(infoService))
			notes.PUT("/:id", infoHandlers.NotesHandler(infoService))
		}

		info.GET("/audit-records", infoHandlers.AuditRecords(infoService))
		info.GET("/node", infoHandlers.NodeInfo(infoService))
	}
	hostTerminal := api.Group("/info")
	hostTerminal.Use(middleware.EnsureAuthenticated(authService))
	hostTerminal.Use(middleware.RequireLocalAdmin(authService))
	hostTerminal.Use(EnsureCorrectHost(db, authService))
	hostTerminal.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	hostTerminal.GET("/terminal", infoHandlers.HandleHostTerminal)

	zfs := api.Group("/zfs")
	zfs.Use(middleware.EnsureAuthenticated(authService))
	zfs.Use(EnsureCorrectHost(db, authService))
	zfs.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		zfs.GET("/dashboard/snapshot", zfsHandlers.DashboardSnapshot(zfsService))
		zfs.GET("/dashboard/history", zfsHandlers.DashboardHistory(zfsService))
		zfs.GET("/dashboard/history/delta", zfsHandlers.DashboardHistoryDelta(zfsService))
		pools := zfs.Group("/pools")
		{
			pools.GET("", zfsHandlers.GetPools(zfsService, systemService))
			pools.GET("/disks-usage", zfsHandlers.GetDisksUsage(zfsService))
			pools.POST("", zfsHandlers.CreatePool(infoService, zfsService))
			pools.PATCH("/:guid", zfsHandlers.EditPool(infoService, zfsService))
			pools.GET("/:guid/status", zfsHandlers.GetPoolStatus(zfsService))
			pools.POST("/:guid/scrub", zfsHandlers.ScrubPool(infoService, zfsService))
			pools.DELETE("/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardPoolGUID),
				zfsHandlers.DeletePool(infoService, zfsService),
			)
			pools.POST("/:guid/replace-device", zfsHandlers.ReplaceDevice(infoService, zfsService))
			pools.POST("/:guid/detach", zfsHandlers.DetachDevice(infoService, zfsService))
		}

		datasets := zfs.Group("/datasets")
		{
			datasets.GET("", zfsHandlers.GetDatasets(zfsService))
			datasets.GET("/paginated", zfsHandlers.GetPaginatedDatasets(zfsService))

			datasets.POST("/snapshot", zfsHandlers.CreateSnapshot(zfsService))
			datasets.POST("/snapshot/:guid/rollback",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardRollbackSnapshot),
				zfsHandlers.RollbackSnapshot(zfsService),
			)
			datasets.DELETE("/snapshot/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardDatasetGUID),
				zfsHandlers.DeleteSnapshot(zfsService),
			)

			datasets.GET("/snapshot/periodic", zfsHandlers.GetPeriodicSnapshots(zfsService))
			datasets.POST("/snapshot/periodic", zfsHandlers.CreatePeriodicSnapshot(zfsService))
			datasets.PATCH("/snapshot/periodic/:id", zfsHandlers.ModifyPeriodicSnapshotRetention(zfsService))
			datasets.DELETE("/snapshot/periodic/:id", zfsHandlers.DeletePeriodicSnapshot(zfsService))

			datasets.POST("/filesystem",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardCreateFilesystem),
				zfsHandlers.CreateFilesystem(zfsService),
			)
			datasets.PATCH("/filesystem/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardEditFilesystem),
				zfsHandlers.EditFilesystem(zfsService),
			)
			datasets.DELETE("/filesystem/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardDatasetGUID),
				zfsHandlers.DeleteFilesystem(zfsService),
			)

			datasets.POST("/volume",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardCreateVolume),
				zfsHandlers.CreateVolume(zfsService),
			)
			datasets.PATCH("/volume/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardEditVolume),
				zfsHandlers.EditVolume(zfsService),
			)
			datasets.POST("/volume/:guid/flash",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardFlashVolume),
				zfsHandlers.FlashVolume(zfsService),
			)
			datasets.DELETE("/volume/:guid",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardDatasetGUID),
				zfsHandlers.DeleteVolume(zfsService),
			)

			datasets.DELETE("",
				zfsHandlers.ReplicationDatasetMutationGuard(zfsService, zfsHandlers.ReplicationGuardBulkTargets),
				zfsHandlers.BulkDeleteDataset(zfsService),
			)
		}
	}

	samba := api.Group("/samba")
	samba.Use(middleware.EnsureAuthenticated(authService))
	samba.Use(EnsureCorrectHost(db, authService))
	samba.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		samba.GET("/config", sambaHandlers.GetGlobalConfig(sambaService))
		samba.PUT("/config", sambaHandlers.SetGlobalConfig(sambaService))

		samba.GET("/shares", sambaHandlers.GetShares(sambaService))
		samba.POST("/shares", sambaHandlers.CreateShare(sambaService))
		samba.PUT("/shares/:id", sambaHandlers.UpdateShare(sambaService))
		samba.PUT("/shares/:id/enabled", sambaHandlers.SetShareEnabled(sambaService))
		samba.DELETE("/shares/:id", sambaHandlers.DeleteShare(sambaService))

		samba.GET("/audit-logs", sambaHandlers.GetAuditLogs(sambaService))
	}

	mdnsGroup := api.Group("/mdns")
	mdnsGroup.Use(middleware.EnsureAuthenticated(authService))
	mdnsGroup.Use(EnsureCorrectHost(db, authService))
	mdnsGroup.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		mdnsGroup.GET("/config", mdnsHandlers.GetSettings(mdnsService))
		mdnsGroup.PUT("/config", mdnsHandlers.SetSettings(mdnsService))

		mdnsGroup.GET("/records", mdnsHandlers.GetRecords(mdnsService))
		mdnsGroup.POST("/records", mdnsHandlers.CreateRecord(mdnsService))
		mdnsGroup.PUT("/records/:id", mdnsHandlers.UpdateRecord(mdnsService))
		mdnsGroup.DELETE("/records/:id", mdnsHandlers.DeleteRecord(mdnsService))
	}

	dynamicDNSGroup := api.Group("/dynamic-dns")
	dynamicDNSGroup.Use(middleware.EnsureAuthenticated(authService))
	dynamicDNSGroup.Use(middleware.RequireLocalAdminForWrites(authService))
	dynamicDNSGroup.Use(EnsureCorrectHost(db, authService))
	dynamicDNSGroup.Use(middleware.LimitRequestBody(dynamicdns.MaxRequestBodyBytes))
	dynamicDNSGroup.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		dynamicDNSGroup.GET("/entries", dynamicDNSHandlers.ListEntries(dynamicDNSService))
		dynamicDNSGroup.POST("/entries", dynamicDNSHandlers.CreateEntry(dynamicDNSService))
		dynamicDNSGroup.PUT("/entries/:id", dynamicDNSHandlers.UpdateEntry(dynamicDNSService))
		dynamicDNSGroup.DELETE("/entries/:id", dynamicDNSHandlers.DeleteEntry(dynamicDNSService))
		dynamicDNSGroup.POST("/entries/:id/sync", dynamicDNSHandlers.SyncEntry(dynamicDNSService))
	}

	certificateGroup := api.Group("/certificates")
	certificateGroup.Use(middleware.EnsureAuthenticated(authService))
	certificateGroup.Use(middleware.RequireLocalAdminForWrites(authService))
	certificateGroup.Use(EnsureCorrectHost(db, authService))
	certificateGroup.Use(middleware.LimitRequestBody(certificates.MaxRequestBodyBytes))
	certificateGroup.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		certificateGroup.GET("", certificateHandlers.List(certificateService))
		certificateGroup.POST("", certificateHandlers.Create(certificateService))
		certificateGroup.GET("/domain-check", certificateHandlers.CheckDomain(certificateService))
		certificateGroup.PATCH("/:id", certificateHandlers.Update(certificateService))
		certificateGroup.DELETE("/:id", certificateHandlers.Delete(certificateService))
		certificateGroup.POST("/:id/activate", certificateHandlers.Activate(certificateService))
		certificateGroup.DELETE("/:id/activate", certificateHandlers.CancelActivation(certificateService))
		certificateGroup.POST("/:id/renew", certificateHandlers.Renew(certificateService))
		certificateGroup.POST("/:id/retry", certificateHandlers.Retry(certificateService))
		certificateGroup.GET("/:id/archive", middleware.RequireLocalAdmin(authService), certificateHandlers.Download(certificateService))
	}

	iscsiGroup := api.Group("/iscsi")
	iscsiGroup.Use(middleware.EnsureAuthenticated(authService))
	iscsiGroup.Use(middleware.RequireLocalAdminForWrites(authService))
	iscsiGroup.Use(EnsureCorrectHost(db, authService))
	iscsiGroup.Use(middleware.LimitRequestBody(iscsi.MaxRequestBodyBytes))
	iscsiGroup.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		iscsiGroup.GET("/initiators", iscsiHandlers.GetInitiators(iscsiService))
		iscsiGroup.POST("/initiators", iscsiHandlers.CreateInitiator(iscsiService))
		iscsiGroup.PUT("/initiators/:id", iscsiHandlers.UpdateInitiator(iscsiService))
		iscsiGroup.DELETE("/initiators/:id", iscsiHandlers.DeleteInitiator(iscsiService))
		iscsiGroup.POST("/initiators/:id/connect", iscsiHandlers.ConnectInitiator(iscsiService))
		iscsiGroup.GET("/status", iscsiHandlers.GetStatus(iscsiService))

		iscsiGroup.GET("/targets", iscsiHandlers.GetTargets(iscsiService))
		iscsiGroup.POST("/targets", iscsiHandlers.CreateTarget(iscsiService))
		iscsiGroup.PUT("/targets/:targetId", iscsiHandlers.UpdateTarget(iscsiService))
		iscsiGroup.DELETE("/targets/:targetId", iscsiHandlers.DeleteTarget(iscsiService))
		iscsiGroup.POST("/targets/:targetId/portals", iscsiHandlers.AddPortal(iscsiService))
		iscsiGroup.DELETE("/targets/:targetId/portals/:portalId", iscsiHandlers.RemovePortal(iscsiService))
		iscsiGroup.POST("/targets/:targetId/luns", iscsiHandlers.AddLUN(iscsiService))
		iscsiGroup.DELETE("/targets/:targetId/luns/:lunId", iscsiHandlers.RemoveLUN(iscsiService))
		iscsiGroup.GET("/target-sessions", iscsiHandlers.GetTargetSessions(iscsiService))
	}

	disk := api.Group("/disk")
	disk.Use(middleware.EnsureAuthenticated(authService))
	disk.Use(EnsureCorrectHost(db, authService))
	disk.Use(middleware.RequireLocalAdminForWrites(authService))
	disk.Use(middleware.LimitRequestBody(diskServicePkg.MaxRequestBodyBytes))
	disk.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		disk.GET("", diskHandlers.List(diskService))
		disk.GET("/smart/self-test", diskHandlers.GetSelfTestInfo(diskService))
		disk.POST("/smart/self-test", diskHandlers.StartSelfTest(diskService))
		disk.POST("/smart/self-test/abort", diskHandlers.StopSelfTest(diskService))
		disk.GET("/smart/self-test/schedules", diskHandlers.ListSelfTestSchedules(diskService))
		disk.POST("/smart/self-test/schedules", diskHandlers.CreateSelfTestSchedule(diskService))
		disk.PUT("/smart/self-test/schedules/:id", diskHandlers.UpdateSelfTestSchedule(diskService))
		disk.DELETE("/smart/self-test/schedules/:id", diskHandlers.DeleteSelfTestSchedule(diskService))
		disk.DELETE("/partitions/:partition", diskHandlers.DeletePartition(diskService))
		disk.POST("/:device/partition-table", diskHandlers.InitializeGPT(diskService))
		disk.DELETE("/:device/partition-table", diskHandlers.ClearPartitionTable(diskService))
		disk.POST("/:device/partitions", diskHandlers.CreatePartitions(diskService))
	}

	network := api.Group("/network")
	network.Use(middleware.EnsureAuthenticated(authService))
	network.Use(EnsureCorrectHost(db, authService))
	network.Use(middleware.LimitRequestBody(networkServicePkg.MaxRequestBodyBytes))
	network.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		objects := network.Group("/object")
		objects.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			objects.GET("", networkHandlers.ListNetworkObjects(networkService))
			objects.POST("", networkHandlers.CreateNetworkObject(networkService))
			objects.DELETE("", networkHandlers.BulkDeleteNetworkObjects(networkService))
			objects.DELETE("/:id", networkHandlers.DeleteNetworkObject(networkService))
			objects.PUT("/:id", networkHandlers.EditNetworkObject(networkService))
		}

		traffic := network.Group("/firewall/traffic")
		traffic.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			traffic.GET("", networkHandlers.ListFirewallTrafficRules(networkService))
			traffic.GET("/counters", networkHandlers.ListFirewallTrafficRuleCounters(networkService))
			traffic.POST("", networkHandlers.CreateFirewallTrafficRule(networkService))
			traffic.DELETE("", networkHandlers.BulkDeleteFirewallTrafficRules(networkService))
			traffic.PUT("/reorder", networkHandlers.ReorderFirewallTrafficRules(networkService))
			traffic.PUT("/:id", networkHandlers.EditFirewallTrafficRule(networkService))
			traffic.DELETE("/:id", networkHandlers.DeleteFirewallTrafficRule(networkService))
		}

		nat := network.Group("/firewall/nat")
		nat.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			nat.GET("", networkHandlers.ListFirewallNATRules(networkService))
			nat.GET("/counters", networkHandlers.ListFirewallNATRuleCounters(networkService))
			nat.GET("/:id/route-suggestions", networkHandlers.SuggestStaticRoutesFromNATRule(networkService))
			nat.POST("", networkHandlers.CreateFirewallNATRule(networkService))
			nat.DELETE("", networkHandlers.BulkDeleteFirewallNATRules(networkService))
			nat.PUT("/reorder", networkHandlers.ReorderFirewallNATRules(networkService))
			nat.PUT("/:id", networkHandlers.EditFirewallNATRule(networkService))
			nat.DELETE("/:id", networkHandlers.DeleteFirewallNATRule(networkService))
		}
		network.GET("/firewall/logs/live", middleware.RequireLocalAdmin(authService), networkHandlers.ListFirewallLiveHits(networkService))

		advanced := network.Group("/firewall/advanced")
		advanced.Use(middleware.RequireLocalAdmin(authService))
		{
			advanced.GET("", networkHandlers.GetFirewallAdvancedSettings(networkService))
			advanced.PUT("", networkHandlers.UpdateFirewallAdvancedSettings(networkService))
			advanced.POST("/preview", networkHandlers.PreviewRenderedConfig(networkService))
			advanced.GET("/rendered", networkHandlers.GetRenderedConfig(networkService))
		}

		routes := network.Group("/route")
		routes.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			routes.GET("", networkHandlers.ListStaticRoutes(networkService))
			routes.POST("", networkHandlers.CreateStaticRoute(networkService))
			routes.PUT("/:id", networkHandlers.EditStaticRoute(networkService))
			routes.DELETE("/:id", networkHandlers.DeleteStaticRoute(networkService))
		}

		wireGuardServer := network.Group("/wireguard/server")
		wireGuardServer.Use(middleware.RequireLocalAdmin(authService))
		{
			wireGuardServer.GET("", networkHandlers.GetWireGuardServer(networkService))
			wireGuardServer.POST("", networkHandlers.InitWireGuardServer(networkService))
			wireGuardServer.PUT("", networkHandlers.EditWireGuardServer(networkService))
			wireGuardServer.PATCH("", networkHandlers.SetWireGuardServerEnabled(networkService))
			wireGuardServer.DELETE("", networkHandlers.DeinitWireGuardServer(networkService))

			wireGuardServer.POST("/peer", networkHandlers.AddWireGuardServerPeer(networkService))
			wireGuardServer.PUT("/peer/:peerId", networkHandlers.EditWireGuardServerPeer(networkService))
			wireGuardServer.PATCH("/peer/:peerId", networkHandlers.SetWireGuardServerPeerEnabled(networkService))
			wireGuardServer.DELETE("/peer/:peerId", networkHandlers.RemoveWireGuardServerPeer(networkService))
		}

		wireGuardClients := network.Group("/wireguard/clients")
		wireGuardClients.Use(middleware.RequireLocalAdmin(authService))
		{
			wireGuardClients.GET("", networkHandlers.GetWireGuardClients(networkService))
			wireGuardClients.POST("", networkHandlers.CreateWireGuardClient(networkService))
			wireGuardClients.PUT("/:clientId", networkHandlers.EditWireGuardClient(networkService))
			wireGuardClients.PATCH("/:clientId", networkHandlers.SetWireGuardClientEnabled(networkService))
			wireGuardClients.DELETE("/:clientId", networkHandlers.DeleteWireGuardClient(networkService))
		}

		network.GET("/interface", networkHandlers.ListInterfaces())

		manualSwitches := network.Group("/switch/manual")
		manualSwitches.Use(middleware.RequireLocalAdmin(authService))
		{
			manualSwitches.POST("", networkHandlers.CreateManualSwitch(networkService))
			manualSwitches.DELETE("/:id", networkHandlers.DeleteManualSwitch(networkService))
		}

		network.GET("/switch", networkHandlers.ListSwitches(networkService))

		standardSwitches := network.Group("/switch/standard")
		standardSwitches.Use(middleware.RequireLocalAdmin(authService))
		{
			standardSwitches.POST("", networkHandlers.CreateStandardSwitch(networkService))
			standardSwitches.PUT("/:id", networkHandlers.UpdateStandardSwitch(networkService))
			standardSwitches.DELETE("/:id", networkHandlers.DeleteStandardSwitch(networkService))
		}

		dhcpConfig := network.Group("/dhcp/config")
		dhcpConfig.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			dhcpConfig.GET("", networkHandlers.GetDHCPConfig(networkService))
			dhcpConfig.PUT("", networkHandlers.ModifyDHCPConfig(networkService))
		}

		dhcpRanges := network.Group("/dhcp/range")
		dhcpRanges.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			dhcpRanges.GET("", networkHandlers.GetDHCPRanges(networkService))
			dhcpRanges.POST("", networkHandlers.CreateDHCPRange(networkService))
			dhcpRanges.PUT("/:id", networkHandlers.ModifyDHCPRange(networkService))
			dhcpRanges.DELETE("/:id", networkHandlers.DeleteDHCPRange(networkService))
		}

		dhcpLeases := network.Group("/dhcp/lease")
		dhcpLeases.Use(middleware.RequireLocalAdminForWrites(authService))
		{
			dhcpLeases.GET("", networkHandlers.GetDHCPLeases(networkService))
			dhcpLeases.POST("", networkHandlers.CreateDHCPLease(networkService))
			dhcpLeases.PUT("/:id", networkHandlers.UpdateDHCPLease(networkService))
			dhcpLeases.DELETE("/dynamic", networkHandlers.DeleteDynamicDHCPLease(networkService))
			dhcpLeases.DELETE("/:id", networkHandlers.DeleteDHCPLease(networkService))
		}
	}

	system := api.Group("/system")
	system.Use(middleware.EnsureAuthenticated(authService))
	system.Use(middleware.RequireLocalAdminForWrites(authService))
	system.Use(EnsureCorrectHost(db, authService))

	systemJSON := system.Group("")
	systemJSON.Use(middleware.LimitRequestBody(systemServicePkg.MaxRequestBodyBytes))
	systemJSON.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		systemJSON.GET("/pci-devices", systemHandlers.ListDevices())
		systemJSON.GET("/ppt-devices", systemHandlers.ListPPTDevices(systemService))
		systemJSON.POST("/ppt-devices", systemHandlers.AddPPTDevice(systemService))
		systemJSON.POST("/ppt-devices/prepare", systemHandlers.PreparePPTDevice(systemService))
		systemJSON.POST("/ppt-devices/import", systemHandlers.ImportPPTDevice(systemService))
		systemJSON.DELETE("/ppt-devices/:id", systemHandlers.RemovePPTDevice(systemService))
		systemJSON.GET("/basic-settings", systemHandlers.BasicSettings(systemService))
		systemJSON.PUT("/basic-settings/pools", systemHandlers.AddUsablePools(systemService))
		systemJSON.PATCH("/basic-settings/services/:service", systemHandlers.SetServiceState(systemService, networkService))
		systemJSON.GET("/tunables/remote", systemHandlers.TunablesRemote(systemService))
		systemJSON.PUT("/tunables", systemHandlers.SetTunable(systemService))
	}

	fileExplorer := system.Group("/file-explorer")
	fileExplorer.Use(middleware.RequireLocalAdmin(authService))
	{
		fileExplorerCore := fileExplorer.Group("")
		fileExplorerCore.Use(middleware.LimitRequestBody(systemServicePkg.MaxRequestBodyBytes))
		fileExplorerCore.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
		{
			fileExplorerCore.GET("", systemHandlers.Files(systemService))
			fileExplorerCore.POST("", systemHandlers.AddFileOrFolder(systemService))
			fileExplorerCore.POST("/delete", systemHandlers.DeleteFilesOrFolders(systemService))
			fileExplorerCore.POST("/rename", systemHandlers.RenameFileOrFolder(systemService))
			fileExplorerCore.POST("/copy-or-move-batch", systemHandlers.CopyOrMoveFilesOrFolders(systemService))
		}

		fileExplorerTransfer := fileExplorer.Group("")
		fileExplorerTransfer.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
		{
			fileExplorerTransfer.GET("/download", systemHandlers.DownloadFile(systemService))
			fileExplorerTransfer.POST("/upload", systemHandlers.UploadFile(systemService, uploadAdmission))
		}

		fileExplorerRevert := fileExplorer.Group("")
		fileExplorerRevert.Use(middleware.LimitRequestBody(systemServicePkg.MaxRequestBodyBytes))
		fileExplorerRevert.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
		{
			fileExplorerRevert.DELETE("/upload", systemHandlers.DeleteUpload(systemService))
		}
	}

	vm := api.Group("/vm")
	vm.Use(middleware.EnsureAuthenticated(authService))
	vm.Use(middleware.RequireLocalAdminForWrites(authService))
	vm.Use(EnsureCorrectHost(db, authService))
	vm.Use(middleware.LimitRequestBody(libvirt.MaxRequestBodyBytes))
	vm.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		vm.POST("/:rid/migrations", migrationHandlers.MigrateVM(migrationService, lifecycleService))
		vm.POST("/:rid/actions/:action", vmHandlers.VMActionHandler(libvirtService, lifecycleService))
		vm.GET("/simple", vmHandlers.ListVMsSimple(libvirtService))
		vm.GET("/templates", vmHandlers.ListVMTemplatesSimple(libvirtService))
		vm.GET("/templates/:templateId", vmHandlers.GetVMTemplateByID(libvirtService))
		vm.POST("/:rid/templates", vmHandlers.ConvertVMToTemplate(libvirtService, lifecycleService))
		vm.POST("/templates/:templateId/vms", vmHandlers.CreateVMFromTemplate(libvirtService, lifecycleService))
		vm.DELETE("/templates/:templateId", vmHandlers.DeleteVMTemplate(libvirtService, lifecycleService))
		vm.GET("/simple/:rid", vmHandlers.GetSimpleVMByRID(libvirtService))
		vm.GET("/:rid/snapshots", vmHandlers.ListVMSnapshots(libvirtService))
		vm.POST("/:rid/snapshots", vmHandlers.CreateVMSnapshot(libvirtService))
		vm.POST("/:rid/snapshots/:snapshotId/rollback",
			vmHandlers.RequireVMReplicationTopologyMutable(libvirtService, "rid"),
			vmHandlers.RollbackVMSnapshot(libvirtService),
		)
		vm.DELETE("/:rid/snapshots/:snapshotId",
			vmHandlers.RequireVMReplicationTopologyMutable(libvirtService, "rid"),
			vmHandlers.DeleteVMSnapshot(libvirtService),
		)
		vm.GET("/:rid", vmHandlers.GetVMByRID(libvirtService))
		vm.GET("", vmHandlers.ListVMs(libvirtService))
		vm.POST("", vmHandlers.CreateVM(libvirtService))
		vm.DELETE("/:rid/registration",
			vmHandlers.RequireVMDeletionDetached(libvirtService, "rid"),
			vmHandlers.RequireVMReplicationTopologyMutable(libvirtService, "rid"),
			vmHandlers.PurgeVMRegistration(libvirtService),
		)
		vm.DELETE("/:rid",
			vmHandlers.RequireVMDeletionDetached(libvirtService, "rid"),
			vmHandlers.RequireVMReplicationTopologyMutable(libvirtService, "rid"),
			vmHandlers.RemoveVM(libvirtService),
		)
		vm.GET("/:rid/domain", vmHandlers.GetLvDomain(libvirtService, lifecycleService))
		vm.GET("/:rid/logs", vmHandlers.GetVMLogs(libvirtService))
		vm.GET("/:rid/stats", vmHandlers.GetVMStatsBootstrap(libvirtService))
		vm.GET("/:rid/stats/:step", vmHandlers.GetVMStats(libvirtService))
		vm.PATCH("/:rid/description", vmHandlers.UpdateVMDescription(libvirtService))
		vm.PATCH("/:rid/name", vmHandlers.UpdateVMName(libvirtService, clusterService))

		vm.POST("/:rid/storage", vmHandlers.StorageAttach(libvirtService))
		vm.PATCH("/:rid/storage/:storageId", vmHandlers.StorageUpdate(libvirtService))
		vm.DELETE("/:rid/storage/:storageId", vmHandlers.StorageDetach(libvirtService))

		vm.POST("/:rid/networks", vmHandlers.NetworkAttach(libvirtService))
		vm.PATCH("/:rid/networks/:networkId", vmHandlers.NetworkUpdate(libvirtService))
		vm.DELETE("/:rid/networks/:networkId", vmHandlers.NetworkDetach(libvirtService))

		vm.PUT("/:rid/hardware/cpu", vmHandlers.ModifyCPU(libvirtService))
		vm.PUT("/:rid/hardware/ram", vmHandlers.ModifyRAM(libvirtService))
		vm.PUT("/:rid/hardware/vnc", vmHandlers.ModifyVNC(libvirtService))
		vm.PUT("/:rid/hardware/pci-devices", vmHandlers.ModifyPassthroughDevices(libvirtService))

		vm.PUT("/:rid/options/wol", vmHandlers.ModifyWakeOnLan(libvirtService))
		vm.PUT("/:rid/options/boot-order", vmHandlers.ModifyBootOrder(libvirtService))
		vm.PUT("/:rid/options/clock", vmHandlers.ModifyClock(libvirtService))
		vm.PUT("/:rid/options/serial-console", vmHandlers.ModifySerialConsole(libvirtService))
		vm.PUT("/:rid/options/shutdown-wait-time", vmHandlers.ModifyShutdownWaitTime(libvirtService))
		vm.PUT("/:rid/options/cloud-init", vmHandlers.ModifyCloudInitData(libvirtService))
		vm.PUT("/:rid/options/boot-rom", vmHandlers.ModifyBootROM(libvirtService))
		vm.PUT("/:rid/options/extra-bhyve-options", vmHandlers.ModifyExtraBhyveOptions(libvirtService))
		vm.PUT("/:rid/options/ignore-umsrs", vmHandlers.ModifyIgnoreUMSRs(libvirtService))
		vm.PUT("/:rid/options/qemu-guest-agent", vmHandlers.ModifyQemuGuestAgent(libvirtService))
		vm.PUT("/:rid/options/tpm", vmHandlers.ModifyTPM(libvirtService))
		vm.GET("/:rid/guest-agent", vmHandlers.GetQemuGuestAgentInfo(libvirtService))

		vm.GET("/:rid/console", middleware.RequireLocalAdmin(authService), vmHandlers.HandleLibvirtTerminalWebsocket(libvirtService))
	}

	jail := api.Group("/jail")
	jail.Use(middleware.EnsureAuthenticated(authService))
	jail.Use(middleware.RequireLocalAdminForWrites(authService))
	jail.Use(EnsureCorrectHost(db, authService))
	jail.Use(middleware.LimitRequestBody(jailServicePkg.MaxRequestBodyBytes))
	jail.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		jail.GET("/simple", jailHandlers.ListJailsSimple(jailService))
		jail.GET("/bootstraps", jailHandlers.ListBootstraps(jailService))
		jail.POST("/bootstraps", jailHandlers.CreateBootstrap(jailService))
		jail.DELETE("/bootstraps/:name", jailHandlers.DeleteBootstrap(jailService))
		jail.GET("/templates", jailHandlers.ListJailTemplatesSimple(jailService))
		jail.GET("/templates/:templateId", jailHandlers.GetJailTemplateByID(jailService))
		jail.POST("/:ctid/templates", jailHandlers.ConvertJailToTemplate(jailService, lifecycleService))
		jail.POST("/templates/:templateId/jails", jailHandlers.CreateJailFromTemplate(jailService, lifecycleService))
		jail.DELETE("/templates/:templateId", jailHandlers.DeleteJailTemplate(jailService, lifecycleService))
		jail.GET("/simple/:ctid", jailHandlers.GetSimpleJailByCTID(jailService))
		jail.GET("", jailHandlers.ListJails(jailService))
		jail.GET("/:ctid/snapshots", jailHandlers.ListJailSnapshots(jailService))
		jail.POST("/:ctid/snapshots", jailHandlers.CreateJailSnapshot(jailService))
		jail.POST("/:ctid/snapshots/:snapshotId/rollback",
			jailHandlers.RequireJailReplicationTopologyMutable(jailService, "ctid"),
			jailHandlers.RollbackJailSnapshot(jailService),
		)
		jail.DELETE("/:ctid/snapshots/:snapshotId",
			jailHandlers.RequireJailReplicationTopologyMutable(jailService, "ctid"),
			jailHandlers.DeleteJailSnapshot(jailService),
		)
		jail.GET("/:ctid", jailHandlers.GetJailByCTID(jailService))
		jail.POST("/:ctid/migrations", migrationHandlers.MigrateJail(migrationService, lifecycleService))
		jail.POST("/:ctid/actions/:action", jailHandlers.JailAction(jailService, lifecycleService))
		jail.PATCH("/:ctid/description", jailHandlers.UpdateJailDescription(jailService))
		jail.PATCH("/:ctid/name", jailHandlers.UpdateJailName(jailService, clusterService))
		jail.GET("/:ctid/state", jailHandlers.GetJailState(jailService, lifecycleService))
		jail.GET("/:ctid/logs", jailHandlers.GetJailLogs(jailService))
		jail.GET("/:ctid/stats", jailHandlers.GetJailStatsBootstrap(jailService))
		jail.GET("/:ctid/stats/:step", jailHandlers.GetJailStats(jailService))
		jail.GET("/:ctid/console",
			middleware.RequireLocalAdmin(authService),
			jailHandlers.HandleJailTerminalWebsocket(jailService),
		)
		jail.PUT("/:ctid/hardware/ram", jailHandlers.UpdateJailMemory(jailService))
		jail.PUT("/:ctid/hardware/cpu", jailHandlers.UpdateJailCPU(jailService))
		jail.PUT("/:ctid/hardware/resource-limits", jailHandlers.UpdateResourceLimits(jailService))

		jail.POST("", jailHandlers.CreateJail(jailService))
		jail.DELETE("/:ctid",
			jailHandlers.RequireJailDeletionDetached(jailService, "ctid"),
			jailHandlers.RequireJailReplicationTopologyMutable(jailService, "ctid"),
			jailHandlers.DeleteJail(jailService),
		)

		jail.PUT("/:ctid/network/inheritance", jailHandlers.SetNetworkInheritance(jailService))
		jail.POST("/:ctid/networks", jailHandlers.AddNetwork(jailService))
		jail.PATCH("/:ctid/networks/:networkId", jailHandlers.EditNetwork(jailService))
		jail.DELETE("/:ctid/networks/:networkId", jailHandlers.DeleteNetwork(jailService))

		jail.PUT("/:ctid/options/wol", jailHandlers.ModifyWakeOnLan(jailService))
		jail.PUT("/:ctid/options/boot-order", jailHandlers.ModifyBootOrder(jailService))
		jail.PUT("/:ctid/options/fstab", jailHandlers.ModifyFstab(jailService))
		jail.PUT("/:ctid/options/resolv-conf", jailHandlers.ModifyResolvConf(jailService))
		jail.PUT("/:ctid/options/devfs-rules", jailHandlers.ModifyDevFSRules(jailService))
		jail.PUT("/:ctid/options/additional-options", jailHandlers.ModifyAdditionalOptions(jailService))
		jail.PUT("/:ctid/options/allowed-options", jailHandlers.ModifyAllowedOptions(jailService))
		jail.PUT("/:ctid/options/metadata", jailHandlers.ModifyMetadata(jailService))
		jail.PUT("/:ctid/options/lifecycle-hooks", jailHandlers.ModifyLifecycleHooks(jailService))
	}

	utilities := api.Group("/utilities")
	utilities.Use(middleware.EnsureAuthenticated(authService))
	utilities.Use(middleware.RequireLocalAdminForWrites(authService))
	utilities.Use(EnsureCorrectHost(db, authService))
	utilities.POST(
		"/downloader-uploads",
		middleware.RequestLoggerMiddleware(telemetryDB, authService),
		utilitiesHandlers.UploadDownloaderFile(utilitiesService, uploadAdmission),
	)

	utilitiesJSON := utilities.Group("")
	utilitiesJSON.Use(middleware.LimitRequestBody(utilitiesServicePkg.MaxRequestBodyBytes))
	utilitiesJSON.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		utilitiesJSON.POST("/downloader-uploads/:id/complete", utilitiesHandlers.CompleteDownloaderUpload(utilitiesService))
		utilitiesJSON.DELETE("/downloader-uploads/:id", utilitiesHandlers.AbortDownloaderUpload(utilitiesService))

		utilitiesJSON.POST("/downloads", utilitiesHandlers.DownloadFile(utilitiesService))
		utilitiesJSON.GET("/downloads", utilitiesHandlers.ListDownloads(utilitiesService))
		utilitiesJSON.GET("/downloads/paths", utilitiesHandlers.GetDownloadPaths())
		utilitiesJSON.GET("/downloads/utype", utilitiesHandlers.ListDownloadsByUType(utilitiesService))
		utilitiesJSON.PATCH("/downloads/:id", utilitiesHandlers.UpdateDownload(utilitiesService))
		utilitiesJSON.DELETE("/downloads/:id", utilitiesHandlers.DeleteDownload(utilitiesService))
		utilitiesJSON.POST("/downloads/bulk-delete", utilitiesHandlers.BulkDeleteDownload(utilitiesService))
		utilitiesJSON.POST("/downloads/signed-url", utilitiesHandlers.GetSignedDownloadURL(utilitiesService))

		utilitiesJSON.GET("/cloud-init/templates", utilitiesHandlers.ListCloudInitTemplates(utilitiesService))
		utilitiesJSON.POST("/cloud-init/templates", utilitiesHandlers.AddCloudInitTemplate(utilitiesService))
		utilitiesJSON.PUT("/cloud-init/templates/:templateId", utilitiesHandlers.EditCloudInitTemplate(utilitiesService))
		utilitiesJSON.DELETE("/cloud-init/templates/:templateId", utilitiesHandlers.DeleteCloudInitTemplate(utilitiesService))
	}

	api.GET("/utilities/downloads/:uuid", EnsurePublicDownloadHost(db), utilitiesHandlers.DownloadFileFromSignedURL(utilitiesService))

	authSession := api.Group("/auth")
	authSession.Use(middleware.EnsureAuthenticated(authService))
	authSession.Use(middleware.RequireLocalSession())
	authSession.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	authSession.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		authSession.POST("/logout", authHandlers.LogoutHandler(authService))
		authSession.POST("/sse-tokens", eventsHandlers.CreateSSEToken(authService))
	}

	authManagement := api.Group("/auth")
	authManagement.Use(middleware.EnsureAuthenticated(authService))
	authManagement.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	authManagement.Use(middleware.RequireLocalAdmin(authService))
	authManagement.Use(EnsureCorrectHost(db, authService))
	authManagement.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))

	events := api.Group("/events")
	events.Use(middleware.AuthenticateSSE(authService))
	{
		events.GET("/stream", eventsHandlers.StreamSSE())
	}

	notifications := api.Group("/notifications")
	notifications.Use(middleware.EnsureAuthenticated(authService))
	notifications.Use(middleware.RequireLocalAdminForWrites(authService))
	notifications.Use(EnsureCorrectHost(db, authService))
	notifications.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		notifications.GET("", notificationsHandlers.List(notificationService))
		notifications.GET("/count", notificationsHandlers.Count(notificationService))
		notifications.POST("/dismiss-all", notificationsHandlers.DismissAll(notificationService))
		notifications.POST("/:id/dismiss", notificationsHandlers.Dismiss(notificationService))
	}

	notificationSettings := api.Group("/notifications")
	notificationSettings.Use(middleware.EnsureAuthenticated(authService))
	notificationSettings.Use(middleware.RequireLocalAdmin(authService))
	notificationSettings.Use(EnsureCorrectHost(db, authService))
	notificationSettings.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	notificationSettings.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		notificationSettings.GET("/transports", notificationsHandlers.GetTransports(notificationService))
		notificationSettings.POST("/transports", notificationsHandlers.CreateTransport(notificationService))
		notificationSettings.PUT("/transports/:id", notificationsHandlers.UpdateTransport(notificationService))
		notificationSettings.DELETE("/transports/:id", notificationsHandlers.DeleteTransport(notificationService))
		notificationSettings.POST("/transports/:id/test", notificationsHandlers.TestTransport(notificationService))
		notificationSettings.GET("/rules", notificationsHandlers.GetRules(notificationService))
		notificationSettings.POST("/rules/test", notificationsHandlers.TestRule(notificationService))
		notificationSettings.POST("/rules", notificationsHandlers.CreateRule(notificationService))
		notificationSettings.PUT("/rules", notificationsHandlers.UpdateRules(notificationService))
		notificationSettings.PUT("/rules/:id", notificationsHandlers.UpdateRule(notificationService))
		notificationSettings.DELETE("/rules/:id", notificationsHandlers.DeleteRule(notificationService))
		notificationSettings.POST("/rules/bulk-delete", notificationsHandlers.BulkDeleteRules(notificationService))
		notificationSettings.POST("/rules/bulk-update", notificationsHandlers.BulkUpdateRules(notificationService))
	}

	users := authManagement.Group("/users")
	{
		users.GET("", authHandlers.ListUsersHandler(authService))
		users.GET("/uid/next", authHandlers.GetNextUIDHandler(authService))
		users.GET("/capabilities", authHandlers.UserCapabilitiesHandler())
		users.GET("/importable", authHandlers.ListImportableUsersHandler(authService))
		users.POST("", authHandlers.CreateUserHandler(authService))
		users.POST("/import", authHandlers.ImportUserHandler(authService))
		users.POST("/pam", authHandlers.CreatePamUserHandler(authService))
		users.GET("/:userId/passkeys", authHandlers.ListUserPasskeysHandler(authService))
		users.DELETE("/:userId/passkeys/:credentialId", authHandlers.DeleteUserPasskeyHandler(authService))
		users.DELETE("/:userId", authHandlers.DeleteUserHandler(authService))
		users.PUT("/:userId", authHandlers.EditUserHandler(authService))
	}

	groups := authManagement.Group("/groups")
	{
		groups.GET("", authHandlers.ListGroupsHandler(authService))
		groups.POST("", authHandlers.CreateGroupHandler(authService))
		groups.DELETE("/:groupId", authHandlers.DeleteGroupHandler(authService))
		groups.PUT("/:groupId/members", authHandlers.UpdateGroupMembersHandler(authService))
	}

	passkeys := authManagement.Group("/passkeys")
	{
		passkeys.POST("/register/begin", authHandlers.BeginPasskeyRegistrationHandler(authService))
		passkeys.POST("/register/finish", authHandlers.FinishPasskeyRegistrationHandler(authService))
	}

	intraCluster := api.Group("/intra-cluster")
	intraCluster.Use(middleware.EnsureAuthenticated(authService))
	intraCluster.Use(middleware.RequireClusterScope())
	{
		intraCluster.POST("/migration/import-vm", migrationHandlers.IntraClusterImportVM(zeltaService, libvirtService))
		intraCluster.POST("/migration/import-jail", migrationHandlers.IntraClusterImportJail(zeltaService, jailService))
		intraCluster.POST("/migration/check-vm-target", migrationHandlers.IntraClusterCheckVMTarget(libvirtService))
		intraCluster.POST("/sync-health", clusterHandlers.SyncHealth(clusterService))
		intraCluster.POST("/events/left-panel-refresh", clusterHandlers.EmitLeftPanelRefreshLocal(clusterService))
		intraCluster.POST("/ssh-identity", clusterHandlers.UpsertClusterSSHIdentityInternal(clusterService))
		intraCluster.POST("/ssh-reconcile", clusterHandlers.ReconcileClusterSSHNow(clusterService))
		intraCluster.GET("/guest-identity-inventory", clusterHandlers.GuestIdentityInventoryInternal(clusterService))
		intraCluster.GET("/replicated-state", clusterHandlers.ReplicatedStateInternal(clusterService))
		intraCluster.POST("/replicated-state-repair", clusterHandlers.ReplicatedStateRepairInternal(clusterService, zeltaService))
		intraCluster.POST("/backup-job-safety-validation", clusterHandlers.ValidateBackupJobSafetyInternal(clusterService))
		intraCluster.POST("/backup-target-validation", clusterHandlers.ValidateBackupTargetInternal(clusterService))
		intraCluster.POST("/run", clusterHandlers.RunReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/activate", clusterHandlers.ActivateReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/demote", clusterHandlers.DemoteReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/catchup", clusterHandlers.CatchupReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-runtime-state", clusterHandlers.ReplicationPolicyRuntimeStateInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-run-claim", clusterHandlers.ReplicationRunClaimInternal(clusterService))
		intraCluster.POST("/replication-target-readiness", clusterHandlers.UpdateReplicationTargetReadinessInternal(clusterService))
		intraCluster.POST("/backup-job-runner-rebind-prepare", clusterHandlers.PrepareBackupJobRunnerRebindInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-reassign-owner", clusterHandlers.ReassignReplicationOwnerInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-guest-operation", clusterHandlers.ReplicationGuestOperationInternal(clusterService))
		intraCluster.POST("/replication-guest-operation-status", clusterHandlers.ReplicationGuestOperationStatusInternal(clusterService))
		intraCluster.POST("/cleanup-policy-delete", clusterHandlers.CleanupReplicationPolicyDeleteInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-failover-enqueue", clusterHandlers.EnqueueFailoverInternal(zeltaService))
		intraCluster.POST("/backup-job-state", clusterHandlers.UpdateBackupJobStateInternal(clusterService))
		intraCluster.POST("/backup-job-operation", clusterHandlers.BackupJobOperationInternal(clusterService))
		intraCluster.POST("/backup-target-restore-operation", clusterHandlers.BackupTargetRestoreOperationInternal(clusterService))
		intraCluster.POST("/replication-policy-state", clusterHandlers.UpdateReplicationPolicyStateInternal(clusterService))
		intraCluster.POST("/backup-job-friendly-source", clusterHandlers.UpdateBackupJobFriendlySourceInternal(clusterService))
		intraCluster.POST("/encryption-key/discover", clusterHandlers.DiscoverEncryptionKeyInternal(clusterService))
		intraCluster.POST("/remove-peer", clusterHandlers.RemovePeer(clusterService))
	}

	clusterAdmission := api.Group("/cluster")
	clusterAdmission.Use(middleware.AuthenticateClusterKey(authService))
	clusterAdmission.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	clusterAdmission.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	clusterAdmission.POST("/accept-join", clusterHandlers.AcceptJoin(clusterService))

	clusterLocal := api.Group("/cluster")
	clusterLocal.Use(middleware.EnsureAuthenticated(authService))
	clusterLocal.Use(middleware.RequireLocalSession())
	clusterLocal.Use(middleware.RequireLocalAdmin(authService))
	clusterLocal.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	clusterLocal.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		clusterLocal.GET("/join-key", clusterHandlers.GetJoinKey(authService))
		clusterLocal.POST("", clusterHandlers.CreateCluster(clusterService, fsm))
		clusterLocal.POST("/join", clusterHandlers.JoinCluster(clusterService, zeltaService, fsm))
		clusterLocal.DELETE("/reset-node", clusterHandlers.ResetRaftNode(clusterService))
	}

	clusterAdmin := api.Group("/cluster")
	clusterAdmin.Use(middleware.EnsureAuthenticated(authService))
	clusterAdmin.Use(middleware.RequireLocalAdmin(authService))
	clusterAdmin.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	clusterAdmin.POST("/resync-state", clusterHandlers.ResyncClusterState(clusterService, zeltaService))

	cluster := api.Group("/cluster")
	cluster.Use(middleware.EnsureAuthenticated(authService))
	cluster.Use(middleware.LimitRequestBody(authServicePkg.MaxRequestBodyBytes))
	cluster.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		cluster.GET("/nodes", clusterHandlers.Nodes(clusterService))
		cluster.GET("/resources", clusterHandlers.Resources(clusterService))

		cluster.GET("", clusterHandlers.GetCluster(clusterService))
	}

	clusterNotes := cluster.Group("/notes")
	clusterNotes.Use(middleware.RequireLocalAdmin(authService))
	{
		clusterNotes.GET("", clusterHandlers.Notes(clusterService))
		clusterNotes.POST("", clusterHandlers.CreateNote(clusterService))
		clusterNotes.PUT("/:id", clusterHandlers.UpdateNote(clusterService))
		clusterNotes.DELETE("/:id", clusterHandlers.DeleteNote(clusterService))
		clusterNotes.POST("/bulk-delete", clusterHandlers.BulkDeleteNotes(clusterService))
	}

	clusterBackups := cluster.Group("/backups")
	clusterBackups.Use(middleware.RequireLocalAdmin(authService))
	{
		targets := clusterBackups.Group("/targets")
		{
			targets.GET("", clusterHandlers.BackupTargets(clusterService))
			targets.POST("", clusterHandlers.CreateBackupTarget(clusterService, zeltaService))
			targets.PUT("/:id", clusterHandlers.UpdateBackupTarget(clusterService, zeltaService))
			targets.DELETE("/:id", clusterHandlers.DeleteBackupTarget(clusterService, zeltaService))
			targets.POST("/:id/validate", clusterHandlers.ValidateBackupTarget(clusterService, zeltaService))
			targets.GET("/:id/readiness", clusterHandlers.BackupTargetReadiness(clusterService))
			targets.GET("/:id/datasets", clusterHandlers.BackupTargetDatasets(zeltaService))
			targets.GET("/:id/datasets/snapshots", clusterHandlers.BackupTargetDatasetSnapshots(zeltaService))
			targets.GET("/:id/datasets/jail-metadata", clusterHandlers.BackupTargetDatasetJailMetadata(zeltaService))
			targets.GET("/:id/datasets/vm-metadata", clusterHandlers.BackupTargetDatasetVMMetadata(zeltaService))
			targets.GET("/:id/running-jobs", clusterHandlers.BackupTargetRunningJobIDs(clusterService))
			targets.POST("/:id/restore", clusterHandlers.RestoreBackupTargetDataset(clusterService, zeltaService))
		}

		jobs := clusterBackups.Group("/jobs")
		{
			jobs.GET("", clusterHandlers.BackupJobs(clusterService))
			jobs.POST("", clusterHandlers.CreateBackupJob(clusterService))
			jobs.PUT("/:id", clusterHandlers.UpdateBackupJob(clusterService))
			jobs.DELETE("/:id", clusterHandlers.DeleteBackupJob(clusterService))
			jobs.POST("/:id/run", clusterHandlers.RunBackupJobNow(clusterService, zeltaService))
			jobs.GET("/:id/snapshots", clusterHandlers.BackupJobSnapshots(clusterService, zeltaService))
			jobs.POST("/:id/restore", clusterHandlers.RestoreBackupJob(clusterService, zeltaService))
		}

		clusterBackups.GET("/events", clusterHandlers.BackupEvents(clusterService, zeltaService))
		clusterBackups.GET("/events/remote", clusterHandlers.BackupEventsRemote(clusterService, zeltaService))
		clusterBackups.GET("/events/:id", clusterHandlers.BackupEventByID(clusterService, zeltaService))
		clusterBackups.GET("/events/:id/progress", clusterHandlers.BackupEventProgressByID(clusterService, zeltaService))
	}

	clusterReplication := cluster.Group("/replication")
	clusterReplication.Use(middleware.RequireLocalAdmin(authService))
	{
		clusterReplication.GET("/policies", clusterHandlers.ReplicationPolicies(clusterService))
		clusterReplication.POST("/policies", clusterHandlers.CreateReplicationPolicy(clusterService))
		clusterReplication.PUT("/policies/:id", clusterHandlers.UpdateReplicationPolicy(clusterService, zeltaService))
		clusterReplication.DELETE("/policies/:id", clusterHandlers.DeleteReplicationPolicy(clusterService, zeltaService))
		clusterReplication.POST("/policies/:id/run", clusterHandlers.RunReplicationPolicyNow(clusterService, zeltaService))
		clusterReplication.POST("/policies/:id/failover", clusterHandlers.FailoverReplicationPolicy(clusterService, zeltaService))

		clusterReplication.GET("/events", clusterHandlers.ReplicationEvents(clusterService))
		clusterReplication.GET("/events/:id", clusterHandlers.ReplicationEventByID(clusterService))
		clusterReplication.GET("/events/:id/progress", clusterHandlers.ReplicationEventProgressByID(clusterService, zeltaService))
	}

	vnc := api.Group("/vnc")
	vnc.Use(middleware.EnsureAuthenticated(authService))
	vnc.Use(middleware.RequireLocalAdmin(authService))
	vnc.Use(EnsureCorrectHost(db, authService))
	vnc.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	vnc.GET("/:port", vncHandler.VNCProxyHandler(libvirtService))

	tasks := api.Group("/tasks")
	tasks.Use(middleware.EnsureAuthenticated(authService))
	tasks.Use(middleware.RequireLocalAdminForWrites(authService))
	tasks.Use(EnsureCorrectHost(db, authService))
	tasks.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		lifecycleTasks := tasks.Group("/lifecycle")
		{
			lifecycleTasks.GET("/active", taskHandlers.ActiveLifecycleTasks(lifecycleService))
			lifecycleTasks.GET("/active/:guestType/:guestId", taskHandlers.ActiveLifecycleTaskForGuest(lifecycleService))
			lifecycleTasks.GET("/recent", taskHandlers.RecentLifecycleTasks(lifecycleService))
		}

		migrationTasks := tasks.Group("/migration")
		{
			migrationTasks.POST("/:taskId/cancel", migrationHandlers.CancelMigration(migrationService))
			migrationTasks.GET("/validate", migrationHandlers.ValidateMigration(migrationService))
		}
	}

	if proxyToVite {
		r.NoRoute(func(c *gin.Context) {
			ReverseProxy(c, "http://[::1]:5173")
		})
	} else {
		files, err := static.EmbedFolder(assets.SvelteKitFiles, "web-files")
		if err != nil {
			log.Fatalln("Initialization of embed folder failed:", err)
		}

		r.Use(func(c *gin.Context) {
			path := c.Request.URL.Path

			if strings.HasPrefix(path, "/_app/immutable/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}

			c.Next()
		})

		r.Use(static.Serve("/", files))

		r.NoRoute(func(c *gin.Context) {
			indexFile, err := assets.SvelteKitFiles.ReadFile("web-files/index.html")
			if err != nil {
				c.String(http.StatusInternalServerError, "Internal Server Error")
				return
			}

			c.Header("Cache-Control", "no-store")
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexFile)
		})
	}
}
