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
	"crypto/tls"
	"errors"
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
	consolepath "github.com/alchemillahq/sylve/internal/console"
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
	"github.com/alchemillahq/sylve/internal/services/mdns"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	notificationsService "github.com/alchemillahq/sylve/internal/services/notifications"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	portnetwork "github.com/alchemillahq/sylve/pkg/network"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli/v3"
)

func main() {
	rootCmd := cmd.NewRootCommand(daemonAction)

	err := rootCmd.Run(context.Background(), os.Args)
	if errors.Is(err, errSelfRestartRequested) {
		err = reexecCurrentProcess()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func daemonAction(ctx context.Context, c *cli.Command) error {
	configPath := c.String("config")
	console := c.Bool("console")

	if !console {
		cmd.AsciiArt(os.Stdout)
	}

	resolvedConfigPath, err := cmd.ResolveConfigPath(configPath)
	if err != nil {
		return err
	}

	cfg := config.ParseConfig(resolvedConfigPath)
	socketPath := consolepath.SocketPath(cfg.DataPath)
	historyPath := consolepath.HistoryPath(cfg.DataPath)

	startLocalSylve, attachErr := shouldStartLocalSylve(console, func() (bool, error) {
		return repl.TryAttachSocketConsole(socketPath, historyPath)
	})
	if attachErr != nil {
		return fmt.Errorf("failed to attach to running Sylve console: %w", attachErr)
	}
	if !startLocalSylve {
		return nil
	}

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

	if err := db.SetupQueue(cfg, false, logger.L); err != nil {
		logger.L.Fatal().Err(err).Msg("failed to setup queue")
	}

	operationalAtBoot, basicSettings, settingsErr := shouldStartOperationalRuntime(func() (dbModels.BasicSettings, error) {
		var settings dbModels.BasicSettings
		if err := d.First(&settings).Error; err != nil {
			return dbModels.BasicSettings{}, err
		}
		return settings, nil
	})
	if settingsErr != nil {
		logger.L.Fatal().Err(settingsErr).Msg("Failed to determine startup mode")
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
	utilitiesSvc := uS.(*utilities.Service)
	sysS := serviceRegistry.SystemService
	lvS := serviceRegistry.LibvirtService
	smbS := serviceRegistry.SambaService
	mdS := serviceRegistry.MdnsService
	ddnsS := serviceRegistry.DynamicDNSService
	certS := serviceRegistry.CertificateService
	iscsiSvc := serviceRegistry.ISCSIService.(*iscsi.Service)
	jS := serviceRegistry.JailService
	cS := serviceRegistry.ClusterService
	zeltaS := serviceRegistry.ZeltaService
	notificationService := notificationsService.NewService(d)
	notificationService.SetDiskService(dS)
	notificationFacade.SetEmitter(notificationService)

	systemSvc := sysS.(*system.Service)
	systemSvc.SetDiskService(dS)
	selfRestartRequests := make(chan struct{}, 1)
	systemSvc.SetRestartRequester(func() {
		requestSelfRestart(selfRestartRequests)
	})

	clusterSvc := cS.(*cluster.Service)
	authSvc := aS.(*auth.Service)
	if err := authSvc.SetClusterIssuerNodeID(clusterSvc.LocalNodeID()); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_configure_cluster_token_issuer")
	}
	authSvc.SetClusterIssuerVerifier(func(nodeID string) (auth.ClusterIssuerMembership, error) {
		membership, err := clusterSvc.ResolveCurrentRaftMember(nodeID)
		if err != nil {
			return auth.ClusterIssuerMembership{}, err
		}
		return auth.ClusterIssuerMembership{Suffrage: membership.Suffrage}, nil
	})
	clusterSvc.SetLeaveCompleteHook(func() {
		requestSelfRestart(selfRestartRequests)
	})
	clusterSvc.SetReaddressRestartHook(func() {
		requestSelfRestart(selfRestartRequests)
	})
	if err := clusterSvc.InitializeLeaveRuntime(); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_initialize_cluster_leave_runtime")
	}
	if err := clusterSvc.InitializeReaddressRuntime(); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_initialize_cluster_readdress_runtime")
	}
	clusterSvc.SetJoinCompleteHook(func() {
		if err := zeltaS.ReconcileBackupTargetSSHKeys(); err != nil {
			logger.L.Warn().Err(err).Msg("backup_target_ssh_reconciliation_deferred_after_join")
		}
		if err := zeltaS.ReconcileEncryptionKeys(); err != nil {
			logger.L.Warn().Err(err).Msg("encryption_key_reconciliation_deferred_after_join")
		}
	})
	if err := clusterSvc.MigrateLegacyPorts(); err != nil {
		logger.L.Fatal().Err(err).Msg("failed_to_migrate_legacy_cluster_ports")
	}

	jailSvc := jS.(*jail.Service)
	libvirtSvc := lvS.(*libvirt.Service)
	lifecycleSvc := lifecycle.NewService(d, telemetryDB, libvirtSvc, jailSvc)
	jailSvc.SetMutationAdmission(clusterSvc)
	libvirtSvc.SetMutationAdmission(clusterSvc)
	lifecycleSvc.SetMutationAdmission(clusterSvc)
	zeltaS.SetMutationAdmission(clusterSvc)
	migrationSvc := serviceRegistry.MigrationService
	migrationSvc.SetMutationAdmission(clusterSvc)
	utilitiesSvc.SetMutationAdmission(clusterSvc)
	lifecycleSvc.SetMigrationExecutor(migrationSvc.ExecuteMigration)
	lifecycleSvc.SetStartupGuestReadinessChecker(zeltaS.CanAutostartReplicationGuest)
	refreshEmitter := func(reason string) {
		clusterSvc.EmitLeftPanelRefreshClusterWide(reason)
	}
	jailSvc.SetLeftPanelRefreshEmitter(refreshEmitter)
	libvirtSvc.SetLeftPanelRefreshEmitter(refreshEmitter)

	uS.RegisterJobs()
	zeltaS.RegisterJobs()
	lifecycleSvc.RegisterJobs()
	dS.(*disk.Service).RegisterJobs()

	zfs.EncryptionKeyCreatedHook = func(uuid, keyData, keyFormat string) {
		if err := clusterSvc.ForwardEncryptionKeyToLeader(uuid, keyData, keyFormat); err != nil {
			logger.L.Warn().Err(err).Str("uuid", uuid).Msg("encryption_key_hook_register_failed")
		}
	}

	initContext, initCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer initCancel()
	if err := certS.Initialize(initContext, cfg.TLS); err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize public TLS certificates")
	}

	err = sS.Initialize(aS.(*auth.Service), initContext, qCtx)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("Failed to initialize at startup")
	}

	var clusterTLSConfig *tls.Config
	var queueDone <-chan struct{}
	dS.(*disk.Service).SetSelfTestSchedulerReady(operationalAtBoot)

	if operationalAtBoot {
		if err := notificationService.MigrateLegacyDiskSmartRecords(qCtx); err != nil {
			logger.L.Fatal().Err(err).Msg("failed_to_migrate_legacy_disk_smart_notifications")
		}

		if err := cS.InitRaft(fsm); err != nil {
			logger.L.Fatal().Err(err).Msg("Failed to initialize RAFT")
		}
		clusterSvc.StartMembershipReconcilers(qCtx)

		clusterTLSConfig, err = aS.GetClusterTLSConfig()
		if err != nil {
			logger.L.Fatal().Err(err).Msg("Failed to get cluster TLS config")
		}

		if err := utilitiesSvc.StartOperational(); err != nil {
			logger.L.Fatal().Err(err).Msg("Failed to start utilities runtime")
		}
		defer func() {
			if err := utilitiesSvc.Close(); err != nil {
				logger.L.Warn().Err(err).Msg("Failed to close utilities runtime")
			}
		}()

		if err := markOperationalStartupComplete(d); err != nil {
			logger.L.Fatal().Err(err).Msg("Failed to mark operational startup complete")
		}

		logger.L.Info().Msg("Operational startup prerequisites complete")

		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-qCtx.Done():
					return
				case <-ticker.C:
					db.RunCacheGC()
				}
			}
		}()

		if err := lifecycleSvc.PrepareStartup(initContext); err != nil {
			logger.L.Error().Err(err).Msg("failed_to_prepare_lifecycle_startup")
		}

		if err := nS.(*networkService.Service).SyncFirewallRuntimeState(); err != nil {
			logger.L.Error().Err(err).Msg("failed_to_sync_firewall_runtime_state_during_startup")
		}

		go nS.(*networkService.Service).StartObjectRefreshWorker(qCtx)
		go ddnsS.StartWorker(qCtx)
		go certS.StartManagedWorker(qCtx)
		go uS.StartUploadCleanupWorker(qCtx)
		db.StartPruneWorker(qCtx, d)

		logger.L.Info().Msg("Starting background watchers and queues")
		go sysS.StartNetlinkWatcher(qCtx)
		sysS.StartDiskSmartMonitor(qCtx)
		go dS.(*disk.Service).StartSelfTestScheduler(qCtx)

		if libvirtSvc.IsVirtualizationEnabled() {
			go libvirtSvc.StartLifecycleWatcher(qCtx)
		}

		startupMutationCtx, startupMutationRelease, startupMutationErr := clusterSvc.EnterMutation(context.Background())
		if startupMutationErr == nil {
			operationReconcileCtx, operationReconcileCancel := context.WithTimeout(startupMutationCtx, 30*time.Second)
			if err := zeltaS.ReconcileBackupJobOperationsAfterRestart(operationReconcileCtx); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_job_operations_after_restart")
			}
			if err := zeltaS.ReconcileReplicationRunsAfterRestart(operationReconcileCtx); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_replication_runs_after_restart")
			}
			if err := zeltaS.DrainScheduledRunResultOutbox(); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_drain_scheduled_run_result_outbox_after_restart")
			}
			operationReconcileCancel()
			targetRestoreReconcileCtx, targetRestoreReconcileCancel := context.WithTimeout(startupMutationCtx, 30*time.Second)
			if err := zeltaS.ReconcileBackupTargetRestoreOperationsAfterRestart(targetRestoreReconcileCtx); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_target_restore_operations_after_restart")
			}
			targetRestoreReconcileCancel()
			targetProvisionReconcileCtx, targetProvisionReconcileCancel := context.WithTimeout(startupMutationCtx, 30*time.Second)
			if err := zeltaS.ReconcileBackupTargetProvisionOperations(targetProvisionReconcileCtx); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_target_provision_operations_after_restart")
			}
			targetProvisionReconcileCancel()
			if err := zeltaS.ReconcileRestoreObservabilityAfterRestart(); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_restore_observability_after_restart")
			}
			if err := zeltaS.PrepareReplicationStartup(startupMutationCtx); err != nil {
				logger.L.Error().Err(err).Msg("replication_startup_fence_or_authority_failed")
			}
			enqueueCtx, enqueueCancel := context.WithTimeout(startupMutationCtx, 10*time.Second)
			if enqueueErr := lifecycleSvc.EnqueueStartupAutostart(enqueueCtx); enqueueErr != nil {
				logger.L.Warn().Err(enqueueErr).Msg("failed_to_enqueue_guest_autostart_sequence")
			}
			enqueueCancel()
			if err := zeltaS.ReconcileReplicationEventsAfterRestart(); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_replication_events_after_restart")
			}
			if err := zeltaS.ReconcileBackupRunAudits(); err != nil {
				logger.L.Warn().Err(err).Msg("failed_to_reconcile_backup_run_audits_after_restart")
			}
			startupMutationRelease()
		} else if !errors.Is(startupMutationErr, cluster.ErrNodeLeaveFenced) {
			logger.L.Warn().Err(startupMutationErr).Msg("failed_to_admit_cluster_runtime_reconciliation")
		}
		go zeltaS.StartBackupTargetProvisionReconciler(qCtx)
		queueRunnerDone := make(chan struct{})
		queueDone = queueRunnerDone
		go func() {
			defer close(queueRunnerDone)
			db.StartQueue(qCtx)
		}()
		if err := zelta.EnsureZeltaInstalled(); err != nil {
			logger.L.Error().Err(err).Msg("Failed to install Zelta; skipping Zelta schedulers")
		} else {
			go zeltaS.StartBackupScheduler(qCtx)
			go zeltaS.StartReplicationScheduler(qCtx)
		}

		go migrationSvc.StartRecoveryTicker(qCtx)
		go clusterSvc.StartBackupJobRunnerRebindReconciler(qCtx)
		go aS.ClearExpiredJWTTokens(qCtx)
	} else {
		logger.L.Info().
			Bool("initialized", basicSettings.Initialized).
			Bool("restarted", basicSettings.Restarted).
			Msg("Starting in bootstrap mode; operational services are disabled")
	}

	logger.L.Info().Msg("Startup initialization complete")

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard

	r := gin.Default()
	r.Use(gzip.Gzip(
		gzip.BestSpeed,
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
		mdS.(*mdns.Service),
		ddnsS,
		certS,
		iscsiSvc,
		jailSvc,
		lifecycleSvc,
		clusterSvc,
		zeltaS,
		migrationSvc,
		fsm,
		d,
		telemetryDB,
	)

	replQuitChan := make(chan os.Signal, 1)

	replCtx := &repl.Context{
		Auth:           aS.(*auth.Service),
		Cluster:        clusterSvc,
		Info:           iS.(*info.Service),
		Jail:           jailSvc,
		VirtualMachine: libvirtSvc,
		Lifecycle:      lifecycleSvc,
		Network:        nS.(*networkService.Service),
		Utilities:      uS,
		Status:         repl.NewStatusProvider(zS.(*zfs.Service), libvirtSvc, jailSvc, lifecycleSvc),
		HistoryPath:    historyPath,
		QuitChan:       replQuitChan,
	}

	replSocketServer, replSocketErr := repl.StartSocketServer(replCtx, socketPath)
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

	if console {
		go repl.Start(replCtx)
	}

	publicTLSConfig := certS.TLSConfig()

	httpsServer := &http.Server{
		Addr:      fmt.Sprintf("%s:%d", cfg.IP, cfg.Port),
		Handler:   r,
		TLSConfig: publicTLSConfig,
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
		if operationalAtBoot {
			go certS.StartRenewalWorker(qCtx)
		}
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

	var clusterHTTPSMu sync.Mutex
	var activeClusterHTTPS *http.Server

	if operationalAtBoot {
		startClusterListeners := func(clusterIP string) error {
			if err := clusterSvc.StartEmbeddedSSHServer(qCtx, clusterIP); err != nil {
				return fmt.Errorf("cluster_ssh_start_failed: %w", err)
			}

			clusterHTTPSMu.Lock()
			defer clusterHTTPSMu.Unlock()
			if activeClusterHTTPS != nil {
				return nil
			}

			srv := &http.Server{
				Addr:      fmt.Sprintf("%s:%d", clusterIP, cluster.ClusterEmbeddedHTTPSPort),
				Handler:   r,
				TLSConfig: clusterTLSConfig,
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

		var clusterRecord clusterModels.Cluster
		if err := d.First(&clusterRecord).Error; err == nil && clusterRecord.Enabled && clusterRecord.RaftIP != "" {
			if err := startClusterListeners(clusterRecord.RaftIP); err != nil {
				logger.L.Error().Err(err).Msg("failed_to_start_cluster_listeners_at_startup")
			} else {
				clusterSvc.StartReaddressReconciler(qCtx)
			}
		}
	}

	selfRestartRequested := false
	select {
	case <-qCtx.Done():
	case <-replQuitChan:
	case <-selfRestartRequests:
		selfRestartRequested = true
	}

	logger.L.Info().Msg("Shutting down servers gracefully")
	qStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, ns := range startedServers {
		if err := ns.srv.Shutdown(ctx); err != nil {
			logger.L.Error().Err(err).Msgf("%s server graceful shutdown timed out", ns.name)
			if closeErr := ns.srv.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				logger.L.Error().Err(closeErr).Msgf("%s server forced shutdown failed", ns.name)
			}
		}
	}

	clusterHTTPSMu.Lock()
	if activeClusterHTTPS != nil {
		if err := activeClusterHTTPS.Shutdown(ctx); err != nil {
			logger.L.Error().Err(err).Msg("Intra-cluster HTTPS server graceful shutdown timed out")
			if closeErr := activeClusterHTTPS.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				logger.L.Error().Err(closeErr).Msg("Intra-cluster HTTPS server forced shutdown failed")
			}
		}
	}
	clusterHTTPSMu.Unlock()

	if operationalAtBoot {
		shutdownFenceCtx, shutdownFenceCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := zeltaS.FenceReplicationShutdown(shutdownFenceCtx); err != nil {
			logger.L.Error().Err(err).Msg("replication_shutdown_fence_failed")
		}
		shutdownFenceCancel()
	}
	if queueDone != nil {
		logger.L.Info().Dur("timeout", queueShutdownTimeout).Msg("Waiting for in-flight queue jobs to finish")
		if waitForQueueShutdown(queueDone, queueShutdownTimeout) {
			logger.L.Info().Msg("Queue stopped properly")
		} else {
			logger.L.Error().Dur("timeout", queueShutdownTimeout).Msg("queue_shutdown_timed_out")
		}
	}

	wg.Wait()
	logger.L.Info().Msg("Servers exited properly")
	if selfRestartRequested {
		return errSelfRestartRequested
	}
	return nil
}
