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
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	networkService "github.com/alchemillahq/sylve/internal/services/network"
	startupService "github.com/alchemillahq/sylve/internal/services/startup"
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
			RPC: internal.BTTRPC{Enabled: false},
			DHT: internal.DHTConfig{Enabled: false},
		},
	}
	if err := config.SetupDataPath(); err != nil {
		t.Fatalf("failed to set up data path for registry test: %v", err)
	}
	t.Cleanup(func() {
		config.ParsedConfig = oldCfg
	})

	registry := NewServiceRegistry(newRegistryTestDB(t))

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
}
