// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"

	"gorm.io/gorm"
)

var _ serviceInterfaces.StartupServiceInterface = (*Service)(nil)

type Service struct {
	DB        *gorm.DB
	Info      infoServiceInterfaces.InfoServiceInterface
	ZFS       zfsServiceInterfaces.ZfsServiceInterface
	Network   networkServiceInterfaces.NetworkServiceInterface
	Libvirt   libvirtServiceInterfaces.LibvirtServiceInterface
	Utilities utilitiesServiceInterfaces.UtilitiesServiceInterface
	System    systemServiceInterfaces.SystemServiceInterface
	Samba     sambaServiceInterfaces.SambaServiceInterface
	Jail      jailServiceInterfaces.JailServiceInterface
	Cluster   clusterServiceInterfaces.ClusterServiceInterface
}

func NewStartupService(db *gorm.DB,
	info infoServiceInterfaces.InfoServiceInterface,
	zfs zfsServiceInterfaces.ZfsServiceInterface,
	network networkServiceInterfaces.NetworkServiceInterface,
	libvirt libvirtServiceInterfaces.LibvirtServiceInterface,
	utiliies utilitiesServiceInterfaces.UtilitiesServiceInterface,
	system systemServiceInterfaces.SystemServiceInterface,
	samba sambaServiceInterfaces.SambaServiceInterface,
	jail jailServiceInterfaces.JailServiceInterface,
	cluster clusterServiceInterfaces.ClusterServiceInterface,
) serviceInterfaces.StartupServiceInterface {
	return &Service{
		DB:        db,
		Info:      info,
		ZFS:       zfs,
		Network:   network,
		Libvirt:   libvirt,
		Utilities: utiliies,
		System:    system,
		Samba:     samba,
		Jail:      jail,
		Cluster:   cluster,
	}
}

func (s *Service) InitKeys(authService serviceInterfaces.AuthServiceInterface) error {
	if err := authService.InitSecret("JWTSecret", 6); err != nil {
		return err
	}

	if err := os.MkdirAll("/etc/zfs/keys", os.ModePerm); err != nil {
		return err
	}

	return nil
}

func (s *Service) PreFlightChecklist(basicSettings models.BasicSettings) error {
	if err := s.FreeBSDCheck(); err != nil {
		return err
	}

	if err := s.CheckPackageDependencies(basicSettings); err != nil {
		return err
	}

	if err := s.CheckKernelModules(basicSettings); err != nil {
		return err
	}

	if err := s.CheckSambaSyslogConfig(basicSettings); err != nil {
		return err
	}

	if err := s.DevfsSync(); err != nil {
		return err
	}

	return nil
}

func (s *Service) Initialize(authService serviceInterfaces.AuthServiceInterface, ctx context.Context, dCtx context.Context) error {
	if err := s.InitKeys(authService); err != nil {
		return err
	}

	var basicSettings models.BasicSettings
	result := s.DB.First(&basicSettings)
	if result.Error != nil {
		logger.L.Warn().Msgf("System not initialized yet, skipping startup checks")
		return nil
	}

	if err := s.PreFlightChecklist(basicSettings); err != nil {
		return fmt.Errorf("Pre-flight check failed: %w", err)
	}

	s.SysctlSync()

	if slices.Contains(basicSettings.Services, models.Virtualization) {
		err := ensureServiceStarted("libvirtd")
		if err != nil {
			return fmt.Errorf("unable to start libvirtd")
		}

		if err := s.Libvirt.CheckVersion(); err != nil {
			return err
		}

		if err := s.Libvirt.StartTPM(); err != nil {
			return err
		}

		go s.Libvirt.StoreVMUsage()
	}

	go s.Info.Cron(dCtx)
	go s.ZFS.Cron(dCtx)
	go s.ZFS.StartSnapshotScheduler(dCtx)

	if slices.Contains(basicSettings.Services, models.Jails) {
		s.Jail.StartStatsMonitoring(dCtx)
	}

	err := s.Network.SyncStandardSwitches(nil, "sync")
	if err != nil {
		logger.L.Error().Msgf("error syncing standard switches: %v", err)
	}

	if slices.Contains(basicSettings.Services, models.Jails) {
		if err := s.Network.SyncEpairs(false); err != nil {
			return fmt.Errorf("error syncing epairs %v", err)
		}
	}

	s.Network.StartFirewallMonitor(dCtx)

	if slices.Contains(basicSettings.Services, models.WireGuard) {
		if err := s.Network.EnableWireGuardService(dCtx); err != nil {
			return fmt.Errorf("failed to enable wireguard service: %w", err)
		}
	}

	if err := s.Network.ReconcileManagedRoutes(); err != nil {
		logger.L.Error().Err(err).Msg("failed_to_reconcile_managed_routes_on_startup")
	}

	if err := s.System.ReconcilePreparedPPTDevices(); err != nil {
		return fmt.Errorf("failed to reconcile prepared passthrough devices: %w", err)
	}

	if err := s.System.SyncPPTDevices(); err != nil {
		return fmt.Errorf("failed to sync passthrough devices: %w", err)
	}

	if slices.Contains(basicSettings.Services, models.SambaServer) {
		if err := s.InitSamba(ctx); err != nil {
			return fmt.Errorf("failed to initialize Samba: %w", err)
		}

		if err := s.InitSambaAdmins(); err != nil {
			return fmt.Errorf("failed to initialize Samba admins: %w", err)
		}

		err := ensureServiceStarted("samba_server")
		if err != nil {
			logger.L.Error().Err(err).Msgf("unable to start samba server")
		}

		go s.Samba.WatchAuditLogs(dCtx)
	}

	if slices.Contains(basicSettings.Services, models.Virtualization) {
		go func() {
			for {
				if err := s.Libvirt.StoreVMUsage(); err != nil {
					logger.L.Error().Msgf("Failed to sync VM states: %v", err)
				}

				time.Sleep(5 * time.Second)
			}
		}()
	}

	s.Cluster.StartClusterMonitors()

	if slices.Contains(basicSettings.Services, models.WoLServer) {
		go s.Utilities.StartWOLServer()
	}

	if slices.Contains(basicSettings.Services, models.DHCPServer) {
		go func() {
			time.Sleep(30 * time.Second)

			err := ensureServiceStarted("dnsmasq")
			if err != nil {
				logger.L.Error().Err(err).Msg("unable to start dnsmasq")
			}
		}()
	}

	return nil
}
