// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"sync"
	"time"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"

	"gorm.io/gorm"
)

var _ networkServiceInterfaces.NetworkServiceInterface = (*Service)(nil)

type Service struct {
	DB                        *gorm.DB
	TelemetryDB               *gorm.DB
	syncMutex                 sync.Mutex
	epairSyncMutex            sync.Mutex
	firewallMutex             sync.Mutex
	firewallMonOnce           sync.Once
	firewallTelOnce           sync.Once
	wgMonitorMutex            sync.Mutex
	wgMonitorCancel           context.CancelFunc
	wgEndpointCache           map[string][]string
	listSnapshotMigrationOnce sync.Once

	LibVirt            libvirtServiceInterfaces.LibvirtServiceInterface
	OnJailObjectUpdate func(jailIDs []uint)
	firewallTelemetry  *firewallTelemetryRuntime
}

func (s *Service) RegisterOnJailObjectUpdateCallback(cb func(jailIDs []uint)) {
	s.OnJailObjectUpdate = cb
}

func NewNetworkService(db *gorm.DB, telemetryDB *gorm.DB, libvirt libvirtServiceInterfaces.LibvirtServiceInterface) networkServiceInterfaces.NetworkServiceInterface {
	svc := &Service{
		DB:                db,
		TelemetryDB:       telemetryDB,
		LibVirt:           libvirt,
		firewallTelemetry: newFirewallTelemetryRuntime(),
		wgEndpointCache:   map[string][]string{},
	}

	svc.ensureListSnapshotMigration()
	return svc
}

func wireGuardNow() time.Time {
	return time.Now()
}
