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
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authHandlers "github.com/alchemillahq/sylve/internal/handlers/auth"
	basicHandlers "github.com/alchemillahq/sylve/internal/handlers/basic"
	clusterHandlers "github.com/alchemillahq/sylve/internal/handlers/cluster"
	diskHandlers "github.com/alchemillahq/sylve/internal/handlers/disk"
	eventsHandlers "github.com/alchemillahq/sylve/internal/handlers/events"
	infoHandlers "github.com/alchemillahq/sylve/internal/handlers/info"
	jailHandlers "github.com/alchemillahq/sylve/internal/handlers/jail"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	networkHandlers "github.com/alchemillahq/sylve/internal/handlers/network"
	sambaHandlers "github.com/alchemillahq/sylve/internal/handlers/samba"
	systemHandlers "github.com/alchemillahq/sylve/internal/handlers/system"
	taskHandlers "github.com/alchemillahq/sylve/internal/handlers/task"
	utilitiesHandlers "github.com/alchemillahq/sylve/internal/handlers/utilities"
	vmHandlers "github.com/alchemillahq/sylve/internal/handlers/vm"
	vncHandler "github.com/alchemillahq/sylve/internal/handlers/vnc"
	zfsHandlers "github.com/alchemillahq/sylve/internal/handlers/zfs"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	diskService "github.com/alchemillahq/sylve/internal/services/disk"
	infoService "github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/samba"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesService "github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
)

// @title           Sylve API
// @version         0.2.2
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
	environment internal.Environment,
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
	lifecycleService *lifecycle.Service,
	clusterService *cluster.Service,
	zeltaService *zelta.Service,
	fsm *clusterModels.FSMDispatcher,
	db *gorm.DB,
	telemetryDB *gorm.DB,
) {
	api := r.Group("/api")
	api.GET("/auth/login/config", authHandlers.LoginConfigHandler())

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
		basic.PUT("/system/reboot", basicHandlers.RebootSystem(systemService))
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

		notes := info.Group("/notes")
		{
			notes.GET("", infoHandlers.NotesHandler(infoService))
			notes.POST("", infoHandlers.NotesHandler(infoService))
			notes.DELETE("/:id", infoHandlers.NotesHandler(infoService))
			notes.PUT("/:id", infoHandlers.NotesHandler(infoService))
			notes.POST("/bulk-delete", infoHandlers.NotesHandler(infoService))
		}

		info.GET("/audit-records", infoHandlers.AuditRecords(infoService))
		info.GET("/terminal", infoHandlers.HandleHostTerminal)

		info.GET("/node", infoHandlers.NodeInfo(infoService))
	}

	zfs := api.Group("/zfs")
	zfs.Use(middleware.EnsureAuthenticated(authService))
	zfs.Use(EnsureCorrectHost(db, authService))
	zfs.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		zfs.GET("/pool/stats/:interval/:limit", zfsHandlers.PoolStats(zfsService))
		pools := zfs.Group("/pools")
		{
			pools.GET("", zfsHandlers.GetPools(zfsService, systemService))
			pools.GET("/disks-usage", zfsHandlers.GetDisksUsage(zfsService))
			pools.POST("", zfsHandlers.CreatePool(infoService, zfsService))
			pools.PATCH("", zfsHandlers.EditPool(infoService, zfsService))
			pools.GET("/:guid/status", zfsHandlers.GetPoolStatus(zfsService))
			pools.POST("/:guid/scrub", zfsHandlers.ScrubPool(infoService, zfsService))
			pools.DELETE("/:guid", zfsHandlers.DeletePool(infoService, zfsService))
			pools.PATCH("/:guid/replace-device", zfsHandlers.ReplaceDevice(infoService, zfsService))
		}

		datasets := zfs.Group("/datasets")
		{
			datasets.GET("", zfsHandlers.GetDatasets(zfsService))
			datasets.GET("/paginated", zfsHandlers.GetPaginatedDatasets(zfsService))

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
			datasets.POST("/bulk-delete-by-names", zfsHandlers.BulkDeleteDatasetsByName(zfsService))
		}
	}

	samba := api.Group("/samba")
	samba.Use(middleware.EnsureAuthenticated(authService))
	samba.Use(EnsureCorrectHost(db, authService))
	samba.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
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
	disk.Use(middleware.EnsureAuthenticated(authService))
	disk.Use(EnsureCorrectHost(db, authService))
	disk.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		disk.GET("/list", diskHandlers.List(diskService))
		disk.POST("/wipe", diskHandlers.WipeDisk(diskService, infoService))
		disk.POST("/initialize-gpt", diskHandlers.InitializeGPT(diskService, infoService))
		disk.POST("/create-partitions", diskHandlers.CreatePartition(infoService))
		disk.POST("/delete-partition", diskHandlers.DeletePartition(infoService))
	}

	network := api.Group("/network")
	network.Use(middleware.EnsureAuthenticated(authService))
	network.Use(EnsureCorrectHost(db, authService))
	network.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
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
		network.POST("/dhcp/lease/dynamic", networkHandlers.DeleteDynamicDHCPLease(networkService))
	}

	system := api.Group("/system")
	system.Use(middleware.EnsureAuthenticated(authService))
	system.Use(EnsureCorrectHost(db, authService))
	system.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		system.GET("/pci-devices", systemHandlers.ListDevices())
		system.GET("/ppt-devices", systemHandlers.ListPPTDevices(systemService))
		system.POST("/ppt-devices", systemHandlers.AddPPTDevice(systemService))
		system.POST("/ppt-devices/prepare", systemHandlers.PreparePPTDevice(systemService))
		system.POST("/ppt-devices/import", systemHandlers.ImportPPTDevice(systemService))
		system.DELETE("/ppt-devices/:id", systemHandlers.RemovePPTDevice(systemService))
		system.PUT("/basic-settings/pools", systemHandlers.AddUsablePools(systemService))
		system.PUT("/basic-settings/services/:service/toggle", systemHandlers.ToggleService(systemService))
	}

	fileExplorer := system.Group("/file-explorer")
	fileExplorer.Use(middleware.EnsureAuthenticated(authService))
	fileExplorer.Use(EnsureCorrectHost(db, authService))
	fileExplorer.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
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
	vm.Use(middleware.EnsureAuthenticated(authService))
	vm.Use(EnsureCorrectHost(db, authService))
	vm.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		vm.POST("/:action/:rid", vmHandlers.VMActionHandler(lifecycleService))
		vm.GET("/simple", vmHandlers.ListVMsSimple(libvirtService))
		vm.GET("/templates/simple", vmHandlers.ListVMTemplatesSimple(libvirtService))
		vm.GET("/templates/:id", vmHandlers.GetVMTemplateByID(libvirtService))
		vm.POST("/templates/convert/:rid", vmHandlers.ConvertVMToTemplate(libvirtService, lifecycleService))
		vm.POST("/templates/create/:id", vmHandlers.CreateVMFromTemplate(libvirtService, lifecycleService))
		vm.DELETE("/templates/:id", vmHandlers.DeleteVMTemplate(libvirtService))
		vm.GET("/simple/:id", vmHandlers.GetSimpleVMByIdentifier(libvirtService))
		vm.GET("/snapshots/:id", vmHandlers.ListVMSnapshots(libvirtService))
		vm.POST("/snapshots/:id", vmHandlers.CreateVMSnapshot(libvirtService))
		vm.POST("/snapshots/rollback/:id/:snapshotId", vmHandlers.RollbackVMSnapshot(libvirtService))
		vm.DELETE("/snapshots/:id/:snapshotId", vmHandlers.DeleteVMSnapshot(libvirtService))
		vm.GET("/:id", vmHandlers.GetVMByIdentifier(libvirtService))
		vm.GET("", vmHandlers.ListVMs(libvirtService))
		vm.POST("", vmHandlers.CreateVM(libvirtService))
		vm.DELETE("/:id", vmHandlers.RemoveVM(libvirtService))
		vm.GET("/domain/:rid", vmHandlers.GetLvDomain(libvirtService))
		vm.GET("/logs/:rid", vmHandlers.GetVMLogs(libvirtService))
		vm.GET("/stats/:rid/:step", vmHandlers.GetVMStats(libvirtService))
		vm.PUT("/description", vmHandlers.UpdateVMDescription(libvirtService))
		vm.PUT("/name", vmHandlers.UpdateVMName(libvirtService, clusterService))

		vm.POST("/storage/detach", vmHandlers.StorageDetach(libvirtService))
		vm.POST("/storage/attach", vmHandlers.StorageAttach(libvirtService))
		vm.PUT("/storage/update", vmHandlers.StorageUpdate(libvirtService))

		vm.POST("/network/detach", vmHandlers.NetworkDetach(libvirtService))
		vm.POST("/network/attach", vmHandlers.NetworkAttach(libvirtService))
		vm.PUT("/network/update", vmHandlers.NetworkUpdate(libvirtService))

		vm.PUT("/hardware/cpu/:rid", vmHandlers.ModifyCPU(libvirtService))
		vm.PUT("/hardware/ram/:rid", vmHandlers.ModifyRAM(libvirtService))
		vm.PUT("/hardware/vnc/:rid", vmHandlers.ModifyVNC(libvirtService))
		vm.PUT("/hardware/ppt/:rid", vmHandlers.ModifyPassthroughDevices(libvirtService))

		vm.PUT("/options/wol/:rid", vmHandlers.ModifyWakeOnLan(libvirtService))
		vm.PUT("/options/boot-order/:rid", vmHandlers.ModifyBootOrder(libvirtService))
		vm.PUT("/options/clock/:rid", vmHandlers.ModifyClock(libvirtService))
		vm.PUT("/options/serial-console/:rid", vmHandlers.ModifySerialConsole(libvirtService))
		vm.PUT("/options/shutdown-wait-time/:rid", vmHandlers.ModifyShutdownWaitTime(libvirtService))
		vm.PUT("/options/cloud-init/:rid", vmHandlers.ModifyCloudInitData(libvirtService))
		vm.PUT("/options/ignore-umsrs/:rid", vmHandlers.ModifyIgnoreUMSRs(libvirtService))
		vm.PUT("/options/qemu-guest-agent/:rid", vmHandlers.ModifyQemuGuestAgent(libvirtService))
		vm.PUT("/options/tpm/:rid", vmHandlers.ModifyTPM(libvirtService))
		vm.GET("/qga/:rid", vmHandlers.GetQemuGuestAgentInfo(libvirtService))

		vm.GET("/console", vmHandlers.HandleLibvirtTerminalWebsocket)
	}

	jail := api.Group("/jail")
	jail.Use(middleware.EnsureAuthenticated(authService))
	jail.Use(EnsureCorrectHost(db, authService))
	jail.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		jail.GET("/simple", jailHandlers.ListJailsSimple(jailService))
		jail.GET("/templates/simple", jailHandlers.ListJailTemplatesSimple(jailService))
		jail.GET("/templates/:id", jailHandlers.GetJailTemplateByID(jailService))
		jail.POST("/templates/convert/:ctid", jailHandlers.ConvertJailToTemplate(jailService, lifecycleService))
		jail.POST("/templates/create/:id", jailHandlers.CreateJailFromTemplate(jailService, lifecycleService))
		jail.DELETE("/templates/:id", jailHandlers.DeleteJailTemplate(jailService))
		jail.GET("/simple/:id", jailHandlers.GetSimpleJailByIdentifier(jailService))
		jail.GET("/state", jailHandlers.ListJailStates(jailService))
		jail.GET("/state/:id", jailHandlers.GetJailState(jailService))
		jail.GET("", jailHandlers.ListJails(jailService))
		jail.GET("/:id", jailHandlers.GetJailByIdentifier(jailService))
		jail.GET("/snapshots/:id", jailHandlers.ListJailSnapshots(jailService))
		jail.POST("/snapshots/:id", jailHandlers.CreateJailSnapshot(jailService))
		jail.POST("/snapshots/rollback/:id/:snapshotId", jailHandlers.RollbackJailSnapshot(jailService))
		jail.DELETE("/snapshots/:id/:snapshotId", jailHandlers.DeleteJailSnapshot(jailService))
		jail.POST("/action/:action/:ctId", jailHandlers.JailAction(jailService, lifecycleService))
		jail.PUT("/description", jailHandlers.UpdateJailDescription(jailService))
		jail.PUT("/name", jailHandlers.UpdateJailName(jailService, clusterService))
		jail.GET("/:id/logs", jailHandlers.GetJailLogs(jailService))
		jail.PUT("/memory", jailHandlers.UpdateJailMemory(jailService))
		jail.PUT("/cpu", jailHandlers.UpdateJailCPU(jailService))
		jail.GET("/stats/:ctId/:step", jailHandlers.GetJailStats(jailService))
		jail.PUT("/resource-limits/:ctId", jailHandlers.UpdateResourceLimits(jailService))

		jail.POST("", jailHandlers.CreateJail(jailService))
		jail.DELETE("/:ctid", jailHandlers.DeleteJail(jailService))

		jail.GET("/console", jailHandlers.HandleJailTerminalWebsocket(jailService))
		jail.PUT("/network/inheritance/:ctId", jailHandlers.SetNetworkInheritance(jailService))
		jail.PUT("/network/disinheritance/:ctId", jailHandlers.SetNetworkInheritance(jailService))

		jail.POST("/network", jailHandlers.AddNetwork(jailService))
		jail.PUT("/network", jailHandlers.EditNetwork(jailService))
		jail.DELETE("/network/:ctId/:networkId", jailHandlers.DeleteNetwork(jailService))

		jail.PUT("/options/wol/:rid", jailHandlers.ModifyWakeOnLan(jailService))
		jail.PUT("/options/boot-order/:rid", jailHandlers.ModifyBootOrder(jailService))
		jail.PUT("/options/fstab/:rid", jailHandlers.ModifyFstab(jailService))
		jail.PUT("/options/resolv-conf/:rid", jailHandlers.ModifyResolvConf(jailService))
		jail.PUT("/options/devfs-rules/:rid", jailHandlers.ModifyDevFSRules(jailService))
		jail.PUT("/options/additional-options/:rid", jailHandlers.ModifyAdditionalOptions(jailService))
		jail.PUT("/options/allowed-options/:rid", jailHandlers.ModifyAllowedOptions(jailService))
		jail.PUT("/options/metadata/:rid", jailHandlers.ModifyMetadata(jailService))
		jail.PUT("/options/lifecycle-hooks/:rid", jailHandlers.ModifyLifecycleHooks(jailService))
	}

	utilities := api.Group("/utilities")
	utilities.Use(middleware.EnsureAuthenticated(authService))
	utilities.Use(EnsureCorrectHost(db, authService))
	utilities.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		utilities.POST("/downloads", utilitiesHandlers.DownloadFile(utilitiesService))
		utilities.GET("/downloads", utilitiesHandlers.ListDownloads(utilitiesService))
		utilities.GET("/downloads/paths", utilitiesHandlers.GetDownloadPaths())
		utilities.GET("/downloads/utype", utilitiesHandlers.ListDownloadsByUType(utilitiesService))
		utilities.GET("/downloads/:uuid", utilitiesHandlers.DownloadFileFromSignedURL(utilitiesService))
		utilities.DELETE("/downloads/:id", utilitiesHandlers.DeleteDownload(utilitiesService))
		utilities.POST("/downloads/bulk-delete", utilitiesHandlers.BulkDeleteDownload(utilitiesService))
		utilities.POST("/downloads/signed-url", utilitiesHandlers.GetSignedDownloadURL(utilitiesService))

		utilities.GET("/cloud-init/templates", utilitiesHandlers.ListCloudInitTemplates(utilitiesService))
		utilities.POST("/cloud-init/templates", utilitiesHandlers.AddCloudInitTemplate(utilitiesService))
		utilities.PUT("/cloud-init/templates/:id", utilitiesHandlers.EditCloudInitTemplate(utilitiesService))
		utilities.DELETE("/cloud-init/templates/:id", utilitiesHandlers.DeleteCloudInitTemplate(utilitiesService))
	}

	auth := api.Group("/auth")
	auth.Use(middleware.EnsureAuthenticated(authService))
	auth.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		auth.POST("/login", authHandlers.LoginHandler(authService))
		auth.POST("/passkeys/login/begin", authHandlers.BeginPasskeyLoginHandler(authService))
		auth.POST("/passkeys/login/finish", authHandlers.FinishPasskeyLoginHandler(authService))
		auth.GET("/logout", authHandlers.LogoutHandler(authService))
		auth.GET("/sse-token", eventsHandlers.CreateSSEToken(authService))
	}

	events := api.Group("/events")
	events.Use(middleware.EnsureAuthenticated(authService))
	{
		events.GET("/stream", eventsHandlers.StreamSSE(authService))
	}

	users := auth.Group("/users")
	users.Use(EnsureCorrectHost(db, authService))
	users.Use(middleware.RequireLocalAdmin(authService))
	{
		users.GET("", authHandlers.ListUsersHandler(authService))
		users.POST("", authHandlers.CreateUserHandler(authService))
		users.DELETE("/:id", authHandlers.DeleteUserHandler(authService))
		users.PUT("", authHandlers.EditUserHandler(authService))
	}

	groups := auth.Group("/groups")
	groups.Use(EnsureCorrectHost(db, authService))
	groups.Use(middleware.RequireLocalAdmin(authService))
	{
		groups.GET("", authHandlers.ListGroupsHandler(authService))
		groups.POST("", authHandlers.CreateGroupHandler(authService))
		groups.DELETE("/:id", authHandlers.DeleteGroupHandler(authService))
		groups.POST("/users", authHandlers.AddUsersToGroupHandler(authService))
		groups.PUT("/users", authHandlers.UpdateGroupMembersHandler(authService))
	}

	passkeys := auth.Group("/passkeys")
	passkeys.Use(EnsureCorrectHost(db, authService))
	passkeys.Use(middleware.RequireLocalAdmin(authService))
	{
		passkeys.POST("/register/begin", authHandlers.BeginPasskeyRegistrationHandler(authService))
		passkeys.POST("/register/finish", authHandlers.FinishPasskeyRegistrationHandler(authService))
		passkeys.GET("/users/:id", authHandlers.ListUserPasskeysHandler(authService))
		passkeys.DELETE("/users/:id/:credentialId", authHandlers.DeleteUserPasskeyHandler(authService))
	}

	intraCluster := api.Group("/intra-cluster")
	intraCluster.Use(middleware.EnsureAuthenticated(authService))
	intraCluster.Use(middleware.RequireClusterScope())
	{
		intraCluster.POST("/sync-health", clusterHandlers.SyncHealth(clusterService))
		intraCluster.POST("/events/left-panel-refresh", clusterHandlers.EmitLeftPanelRefreshLocal(clusterService))
		intraCluster.POST("/ssh-identity", clusterHandlers.UpsertClusterSSHIdentityInternal(clusterService))
		intraCluster.POST("/ssh-reconcile", clusterHandlers.ReconcileClusterSSHNow(clusterService))
		intraCluster.POST("/run", clusterHandlers.RunReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/activate", clusterHandlers.ActivateReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/demote", clusterHandlers.DemoteReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/catchup", clusterHandlers.CatchupReplicationPolicyInternal(clusterService, zeltaService))
		intraCluster.POST("/cleanup-policy-delete", clusterHandlers.CleanupReplicationPolicyDeleteInternal(clusterService, zeltaService))
		intraCluster.POST("/replication-receipt", clusterHandlers.UpsertReplicationReceiptInternal(clusterService))
		intraCluster.POST("/backup-job-state", clusterHandlers.UpdateBackupJobStateInternal(clusterService))
		intraCluster.POST("/backup-job-friendly-source", clusterHandlers.UpdateBackupJobFriendlySourceInternal(clusterService))
	}

	cluster := api.Group("/cluster")
	cluster.Use(middleware.EnsureAuthenticated(authService))
	cluster.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		cluster.GET("/nodes", clusterHandlers.Nodes(clusterService))
		cluster.GET("/resources", clusterHandlers.Resources(clusterService))

		cluster.GET("", clusterHandlers.GetCluster(clusterService))
		cluster.POST("", clusterHandlers.CreateCluster(authService, clusterService, fsm))
		cluster.POST("/join", clusterHandlers.JoinCluster(authService, clusterService, zeltaService, fsm))
		cluster.POST("/accept-join", clusterHandlers.AcceptJoin(clusterService))
		cluster.POST("/resync-state", clusterHandlers.ResyncClusterState(clusterService, zeltaService))
		cluster.DELETE("/reset-node", clusterHandlers.ResetRaftNode(clusterService))
		cluster.POST("/remove-peer", clusterHandlers.RemovePeer(clusterService))
	}

	clusterNotes := cluster.Group("/notes")
	{
		clusterNotes.GET("", clusterHandlers.Notes(clusterService))
		clusterNotes.POST("", clusterHandlers.CreateNote(clusterService))
		clusterNotes.PUT("/:id", clusterHandlers.UpdateNote(clusterService))
		clusterNotes.DELETE("/:id", clusterHandlers.DeleteNote(clusterService))
	}

	clusterBackups := cluster.Group("/backups")
	{
		targets := clusterBackups.Group("/targets")
		{
			targets.GET("", clusterHandlers.BackupTargets(clusterService))
			targets.POST("", clusterHandlers.CreateBackupTarget(clusterService, zeltaService))
			targets.PUT("/:id", clusterHandlers.UpdateBackupTarget(clusterService, zeltaService))
			targets.DELETE("/:id", clusterHandlers.DeleteBackupTarget(clusterService, zeltaService))
			targets.POST("/validate/:id", clusterHandlers.ValidateBackupTarget(clusterService, zeltaService))
			targets.GET("/:id/datasets", clusterHandlers.BackupTargetDatasets(zeltaService))
			targets.GET("/:id/datasets/snapshots", clusterHandlers.BackupTargetDatasetSnapshots(zeltaService))
			targets.GET("/:id/datasets/jail-metadata", clusterHandlers.BackupTargetDatasetJailMetadata(zeltaService))
			targets.GET("/:id/datasets/vm-metadata", clusterHandlers.BackupTargetDatasetVMMetadata(zeltaService))
			targets.POST("/:id/restore", clusterHandlers.RestoreBackupTargetDataset(clusterService, zeltaService))
		}

		jobs := clusterBackups.Group("/jobs")
		{
			jobs.GET("", clusterHandlers.BackupJobs(clusterService))
			jobs.POST("", clusterHandlers.CreateBackupJob(clusterService))
			jobs.PUT("/:id", clusterHandlers.UpdateBackupJob(clusterService))
			jobs.DELETE("/:id", clusterHandlers.DeleteBackupJob(clusterService))
			jobs.POST("/run/:id", clusterHandlers.RunBackupJobNow(clusterService, zeltaService))
			jobs.GET("/:id/snapshots", clusterHandlers.BackupJobSnapshots(clusterService, zeltaService))
			jobs.POST("/:id/restore", clusterHandlers.RestoreBackupJob(clusterService, zeltaService))
		}

		clusterBackups.GET("/events", clusterHandlers.BackupEvents(clusterService, zeltaService))
		clusterBackups.GET("/events/remote", clusterHandlers.BackupEventsRemote(clusterService, zeltaService))
		clusterBackups.GET("/events/:id", clusterHandlers.BackupEventByID(clusterService, zeltaService))
		clusterBackups.GET("/events/:id/progress", clusterHandlers.BackupEventProgressByID(clusterService, zeltaService))
	}

	clusterReplication := cluster.Group("/replication")
	{
		clusterReplication.GET("/policies", clusterHandlers.ReplicationPolicies(clusterService))
		clusterReplication.POST("/policies", clusterHandlers.CreateReplicationPolicy(clusterService))
		clusterReplication.PUT("/policies/:id", clusterHandlers.UpdateReplicationPolicy(clusterService))
		clusterReplication.DELETE("/policies/:id", clusterHandlers.DeleteReplicationPolicy(clusterService, zeltaService))
		clusterReplication.POST("/policies/:id/run", clusterHandlers.RunReplicationPolicyNow(clusterService, zeltaService))
		clusterReplication.POST("/policies/:id/failover", clusterHandlers.FailoverReplicationPolicy(clusterService, zeltaService))

		clusterReplication.GET("/events", clusterHandlers.ReplicationEvents(clusterService))
		clusterReplication.GET("/events/:id", clusterHandlers.ReplicationEventByID(clusterService))
		clusterReplication.GET("/events/:id/progress", clusterHandlers.ReplicationEventProgressByID(clusterService, zeltaService))
		clusterReplication.GET("/receipts", clusterHandlers.ReplicationReceipts(clusterService))
	}

	vnc := api.Group("/vnc")
	vnc.Use(middleware.EnsureAuthenticated(authService))
	vnc.Use(EnsureCorrectHost(db, authService))
	vnc.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	vnc.GET("/:port", vncHandler.VNCProxyHandler)

	tasks := api.Group("/tasks")
	tasks.Use(middleware.EnsureAuthenticated(authService))
	tasks.Use(EnsureCorrectHost(db, authService))
	tasks.Use(middleware.RequestLoggerMiddleware(telemetryDB, authService))
	{
		lifecycleTasks := tasks.Group("/lifecycle")
		{
			lifecycleTasks.GET("/active", taskHandlers.ActiveLifecycleTasks(lifecycleService))
			lifecycleTasks.GET("/active/:guestType/:guestId", taskHandlers.ActiveLifecycleTaskForGuest(lifecycleService))
			lifecycleTasks.GET("/recent", taskHandlers.RecentLifecycleTasks(lifecycleService))
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
