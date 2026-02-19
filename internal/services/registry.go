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
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/alchemillahq/sylve/internal/services/zfs"

	"github.com/alchemillahq/gzfs"
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
	ZeltaService     *zelta.Service
}

func NewService[T any](db *gorm.DB, dependencies ...interface{}) interface{} {
	switch any(new(T)).(type) {
	case *auth.Service:
		return auth.NewAuthService(db)
	case *system.Service:
		return system.NewSystemService(db, dependencies[0].(*gzfs.Client))
	case *info.Service:
		return info.NewInfoService(db, dependencies[0].(*gzfs.Client))
	case *libvirt.Service:
		return libvirt.NewLibvirtService(db,
			dependencies[0].(systemServiceInterfaces.SystemServiceInterface),
			dependencies[1].(*gzfs.Client),
		)
	case *zfs.Service:
		return zfs.NewZfsService(db,
			dependencies[0].(libvirtServiceInterfaces.LibvirtServiceInterface),
			dependencies[1].(*gzfs.Client),
		)
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
	case *disk.Service:
		zfsService := dependencies[0].(zfsServiceInterfaces.ZfsServiceInterface)
		gzfs := dependencies[1].(*gzfs.Client)

		return disk.NewDiskService(db, zfsService, gzfs)
	case *network.Service:
		return network.NewNetworkService(db, dependencies[0].(libvirtServiceInterfaces.LibvirtServiceInterface))
	case *utilities.Service:
		return utilities.NewUtilitiesService(db)
	case *samba.Service:
		zfsService := dependencies[0].(zfsServiceInterfaces.ZfsServiceInterface)
		gzfs := dependencies[1].(*gzfs.Client)
		return samba.NewSambaService(db, zfsService, gzfs)
	case *jail.Service:
		networkService := dependencies[0].(networkServiceInterfaces.NetworkServiceInterface)
		systemService := dependencies[1].(systemServiceInterfaces.SystemServiceInterface)
		gzfs := dependencies[2].(*gzfs.Client)
		return jail.NewJailService(db, networkService, systemService, gzfs)
	case *cluster.Service:
		authService := dependencies[0].(serviceInterfaces.AuthServiceInterface)
		return cluster.NewClusterService(db, authService)
	case *zelta.Service:
		clusterService := dependencies[0].(*cluster.Service)
		return zelta.NewService(db, clusterService)
	default:
		return nil
	}
}

func NewServiceRegistry(db *gorm.DB) *ServiceRegistry {
	gzfs := gzfs.NewClient(gzfs.Options{
		Sudo:               false,
		ZDBCacheTTLSeconds: 0,
	})

	authService := NewService[auth.Service](db)
	systemService := NewService[system.Service](db, gzfs)
	libvirtService := NewService[libvirt.Service](db, systemService, gzfs)
	networkService := NewService[network.Service](db, libvirtService)
	infoService := NewService[info.Service](db, gzfs)
	zfsService := NewService[zfs.Service](db, libvirtService, gzfs)
	utilitiesService := NewService[utilities.Service](db)
	sambaService := NewService[samba.Service](db, zfsService, gzfs)
	jailService := NewService[jail.Service](db, networkService, systemService, gzfs)
	clusterService := NewService[cluster.Service](db, authService)
	diskService := NewService[disk.Service](db, zfsService, gzfs)
	zeltaService := NewService[zelta.Service](db, clusterService)

	return &ServiceRegistry{
		AuthService:      authService.(serviceInterfaces.AuthServiceInterface),
		StartupService:   NewService[startup.Service](db, infoService, zfsService, networkService, libvirtService, utilitiesService, systemService, sambaService, jailService, clusterService).(*startup.Service),
		InfoService:      infoService.(infoServiceInterfaces.InfoServiceInterface),
		ZfsService:       zfsService.(*zfs.Service),
		DiskService:      diskService.(*disk.Service),
		NetworkService:   NewService[network.Service](db, libvirtService).(*network.Service),
		LibvirtService:   libvirtService.(libvirtServiceInterfaces.LibvirtServiceInterface),
		UtilitiesService: utilitiesService.(utilitiesServiceInterfaces.UtilitiesServiceInterface),
		SystemService:    systemService.(systemServiceInterfaces.SystemServiceInterface),
		SambaService:     sambaService.(sambaServiceInterfaces.SambaServiceInterface),
		JailService:      jailService.(jailServiceInterfaces.JailServiceInterface),
		ClusterService:   clusterService.(clusterServiceInterfaces.ClusterServiceInterface),
		ZeltaService:     zeltaService.(*zelta.Service),
	}
}
