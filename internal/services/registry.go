// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package services

import (
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/disk"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/alchemillahq/sylve/internal/services/startup"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"gorm.io/gorm"
)

type ServiceRegistry struct {
	AuthService      serviceInterfaces.AuthServiceInterface
	StartupService   serviceInterfaces.StartupServiceInterface
	InfoService      infoServiceInterfaces.InfoServiceInterface
	ZfsService       zfsServiceInterfaces.ZfsServiceInterface
	DiskService      diskServiceInterfaces.DiskServiceInterface
	NetworkService   networkServiceInterfaces.NetworkServiceInterface
	LibvirtService   libvirtServiceInterfaces.LibvirtServiceInterface
	UtilitiesService utilitiesServiceInterfaces.UtilitiesServiceInterface
	SystemService    systemServiceInterfaces.SystemServiceInterface
	SambaService     sambaServiceInterfaces.SambaServiceInterface
	JailService      jailServiceInterfaces.JailServiceInterface
	ClusterService   clusterServiceInterfaces.ClusterServiceInterface
}

func NewService[T any](db *gorm.DB, dependencies ...interface{}) interface{} {
	switch any(new(T)).(type) {
	case *auth.Service:
		return auth.NewAuthService(db)
	case *system.Service:
		return system.NewSystemService(db)
	case *startup.Service:
		infoService := dependencies[0].(infoServiceInterfaces.InfoServiceInterface)
		zfsService := dependencies[1].(zfsServiceInterfaces.ZfsServiceInterface)
		networkService := dependencies[2].(networkServiceInterfaces.NetworkServiceInterface)
		libvirtService := dependencies[3].(libvirtServiceInterfaces.LibvirtServiceInterface)
		utilitiesService := dependencies[4].(utilitiesServiceInterfaces.UtilitiesServiceInterface)
		systemService := dependencies[5].(systemServiceInterfaces.SystemServiceInterface)
		sambaService := dependencies[6].(sambaServiceInterfaces.SambaServiceInterface)
		jailService := dependencies[7].(jailServiceInterfaces.JailServiceInterface)
		clusterService := dependencies[8].(clusterServiceInterfaces.ClusterServiceInterface)

		return startup.NewStartupService(db,
			infoService,
			zfsService,
			networkService,
			libvirtService,
			utilitiesService,
			systemService,
			sambaService,
			jailService,
			clusterService)
	case *info.Service:
		return info.NewInfoService(db)
	case *zfs.Service:
		return zfs.NewZfsService(db, dependencies[0].(libvirtServiceInterfaces.LibvirtServiceInterface))
	case *disk.Service:
		return disk.NewDiskService(db, dependencies[0].(zfsServiceInterfaces.ZfsServiceInterface))
	case *network.Service:
		return network.NewNetworkService(db, dependencies[0].(libvirtServiceInterfaces.LibvirtServiceInterface))
	case *libvirt.Service:
		return libvirt.NewLibvirtService(db, dependencies[0].(systemServiceInterfaces.SystemServiceInterface))
	case *utilities.Service:
		return utilities.NewUtilitiesService(db)
	case *samba.Service:
		zfsService := dependencies[0].(zfsServiceInterfaces.ZfsServiceInterface)
		return samba.NewSambaService(db, zfsService)
	case *jail.Service:
		networkService := dependencies[0].(networkServiceInterfaces.NetworkServiceInterface)
		return jail.NewJailService(db, networkService)
	case *cluster.Service:
		authService := dependencies[0].(serviceInterfaces.AuthServiceInterface)
		return cluster.NewClusterService(db, authService)
	default:
		return nil
	}
}

func NewServiceRegistry(db *gorm.DB) *ServiceRegistry {
	systemService := NewService[system.Service](db)
	authService := NewService[auth.Service](db)
	infoService := NewService[info.Service](db)
	libvirtService := NewService[libvirt.Service](db, systemService)
	zfsService := NewService[zfs.Service](db, libvirtService)
	utilitiesService := NewService[utilities.Service](db)
	sambaService := NewService[samba.Service](db, zfsService)
	networkService := NewService[network.Service](db, libvirtService)
	jailService := NewService[jail.Service](db, networkService)
	clusterService := NewService[cluster.Service](db, authService)

	return &ServiceRegistry{
		AuthService:      authService.(serviceInterfaces.AuthServiceInterface),
		StartupService:   NewService[startup.Service](db, infoService, zfsService, networkService, libvirtService, utilitiesService, systemService, sambaService, jailService, clusterService).(*startup.Service),
		InfoService:      infoService.(infoServiceInterfaces.InfoServiceInterface),
		ZfsService:       zfsService.(*zfs.Service),
		DiskService:      NewService[disk.Service](db, zfsService).(*disk.Service),
		NetworkService:   NewService[network.Service](db, libvirtService).(*network.Service),
		LibvirtService:   libvirtService.(libvirtServiceInterfaces.LibvirtServiceInterface),
		UtilitiesService: utilitiesService.(utilitiesServiceInterfaces.UtilitiesServiceInterface),
		SystemService:    systemService.(systemServiceInterfaces.SystemServiceInterface),
		SambaService:     sambaService.(sambaServiceInterfaces.SambaServiceInterface),
		JailService:      jailService.(jailServiceInterfaces.JailServiceInterface),
		ClusterService:   clusterService.(clusterServiceInterfaces.ClusterServiceInterface),
	}
}
