// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package services

import (
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	libvirtService "github.com/alchemillahq/sylve/internal/services/libvirt"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	startupService "github.com/alchemillahq/sylve/internal/services/startup"
	systemService "github.com/alchemillahq/sylve/internal/services/system"
	utilitiesService "github.com/alchemillahq/sylve/internal/services/utilities"
	zfsService "github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"gorm.io/gorm"
)

func newRegistryTestDB(t *testing.T) *gorm.DB {
	return testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
}

func TestNewServiceRegistryReusesNetworkServiceInstance(t *testing.T) {
	oldCfg := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{
		DataPath: t.TempDir(),
		BTT: internal.BTT{
			DHT: internal.DHTConfig{Enabled: false},
		},
	}
	if err := config.SetupDataPath(); err != nil {
		t.Fatalf("failed to set up data path for registry test: %v", err)
	}
	t.Cleanup(func() {
		config.ParsedConfig = oldCfg
	})

	mainDB := newRegistryTestDB(t)
	registry := NewServiceRegistry(mainDB, mainDB)
	if registry.DynamicDNSService == nil {
		t.Fatal("expected dynamic DNS service to be registered")
	}
	if registry.CertificateService == nil {
		t.Fatal("expected certificate service to be registered")
	}
	if registry.NFSService == nil || registry.NFSService.DB != mainDB || registry.NFSService.KerberosState == nil {
		t.Fatal("expected NFS configuration to use the registry database and Directory Services credentials")
	}
	if registry.DynamicDNSService.DB != mainDB || registry.CertificateService.DB != mainDB {
		t.Fatal("expected node-local services to use the registry database")
	}

	apiNetwork, ok := registry.NetworkService.(*networkService.Service)
	if !ok {
		t.Fatalf("expected registry NetworkService to be *network.Service, got %T", registry.NetworkService)
	}

	jailSvc, ok := registry.JailService.(*jailService.Service)
	if !ok {
		t.Fatalf("expected registry JailService to be *jail.Service, got %T", registry.JailService)
	}

	jailNetwork, ok := jailSvc.NetworkService.(*networkService.Service)
	if !ok {
		t.Fatalf("expected jail service network dependency to be *network.Service, got %T", jailSvc.NetworkService)
	}

	startupSvc, ok := registry.StartupService.(*startupService.Service)
	if !ok {
		t.Fatalf("expected registry StartupService to be *startup.Service, got %T", registry.StartupService)
	}
	if startupSvc.NFS != registry.NFSService || registry.SystemService.(*systemService.Service).NFS != registry.NFSService || registry.ZfsService.(*zfsService.Service).StorageTopologyGuard == nil {
		t.Fatal("NFS startup, service settings and ZFS mutations must share one runtime coordinator")
	}

	startupNetwork, ok := startupSvc.Network.(*networkService.Service)
	if !ok {
		t.Fatalf("expected startup service network dependency to be *network.Service, got %T", startupSvc.Network)
	}

	zeltaNetwork, ok := registry.ZeltaService.Network.(*networkService.Service)
	if !ok {
		t.Fatalf("expected zelta service network dependency to be *network.Service, got %T", registry.ZeltaService.Network)
	}

	if apiNetwork != jailNetwork {
		t.Fatal("expected API network service and jail network service to share the same instance")
	}
	if apiNetwork != startupNetwork {
		t.Fatal("expected API network service and startup network service to share the same instance")
	}
	if apiNetwork != zeltaNetwork {
		t.Fatal("expected API network service and zelta network service to share the same instance")
	}
	resolver := registry.UtilitiesService.(*utilitiesService.Service).DownloadStorage
	if resolver == nil || resolver != jailSvc.DownloadStorage ||
		resolver != registry.LibvirtService.(*libvirtService.Service).DownloadStorage ||
		resolver != registry.SystemService.(*systemService.Service).DownloadStorage ||
		resolver != registry.ZfsService.(*zfsService.Service).DownloadStorage || resolver != registry.ZeltaService.DownloadStorage {
		t.Fatal("download payload users and destructive operations must share one storage gate")
	}
	t.Run("NFS cleanup runs even if Samba cleanup fails", func(t *testing.T) {
		if err := mainDB.AutoMigrate(&nfsModels.NFSShare{}, &nfsModels.NFSRuntime{}, &nfsModels.NFSSettings{}); err != nil {
			t.Fatal(err)
		}
		share := nfsModels.NFSShare{NFSShareConfig: nfsModels.NFSShareConfig{
			Dataset: "1", Enabled: true, ReadOnly: true, Clients: []string{"192.0.2.1"},
		}}
		if err := mainDB.Create(&share).Error; err != nil {
			t.Fatal(err)
		}
		// The Samba tables are deliberately absent from this test database.
		callback := registry.ZfsService.(*zfsService.Service).OnDatasetsDeleted
		if err := callback(t.Context(), []string{"1"}); err == nil {
			t.Fatal("Samba cleanup error was discarded")
		}
		var saved nfsModels.NFSShare
		if err := mainDB.First(&saved, share.ID).Error; err != nil || saved.Enabled {
			t.Fatalf("NFS cleanup was skipped after Samba failure: %#v, %v", saved, err)
		}
	})
}
