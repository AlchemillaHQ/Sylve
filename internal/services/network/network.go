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

	"golang.zx2c4.com/wireguard/wgctrl"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"

	"gorm.io/gorm"
)

var _ networkServiceInterfaces.NetworkServiceInterface = (*Service)(nil)

// wgPeerMetrics holds in-memory RX/TX state for a single WireGuard server peer.
type wgPeerMetrics struct {
	id            uint
	rx            uint64
	tx            uint64
	lastKernelRX  uint64
	lastKernelTX  uint64
	lastHandshake time.Time
	dirty         bool
}

// wgServerMetricsCache holds in-memory RX/TX state for the WireGuard server and
// all its peers. It is updated every 5 s and flushed to DB every 1 minute.
type wgServerMetricsCache struct {
	id            uint
	rx            uint64
	tx            uint64
	lastKernelRX  uint64
	lastKernelTX  uint64
	lastHandshake time.Time
	restartedAt   time.Time
	dirty         bool
	peers         map[string]*wgPeerMetrics // key: trimmed public key
}

// wgClientMetricsCache holds in-memory RX/TX state for a single outbound
// WireGuard client interface.
type wgClientMetricsCache struct {
	id            uint
	rx            uint64
	tx            uint64
	kernelLastRX  uint64
	kernelLastTX  uint64
	lastHandshake time.Time
	restartedAt   time.Time
	dirty         bool
}

type Service struct {
	DB                         *gorm.DB
	TelemetryDB                *gorm.DB
	syncMutex                  sync.Mutex
	epairSyncMutex             sync.Mutex
	firewallMutex              sync.Mutex
	firewallCounterSampleMutex sync.Mutex
	firewallCounterStateMutex  sync.Mutex
	firewallMonOnce            sync.Once
	firewallTelOnce            sync.Once
	wgMonitorMutex             sync.Mutex
	wgMonitorCancel            context.CancelFunc
	wgClient                   *wgctrl.Client
	wgClientMutex              sync.Mutex
	wgMetricsMutex             sync.Mutex
	wgEndpointCache            map[string][]string
	wgServerCache              *wgServerMetricsCache
	wgClientMetricsCache       map[uint]*wgClientMetricsCache
	listSnapshotMigrationOnce  sync.Once
	wireGuardUDPPortInUse      func(port int) bool

	LibVirt            libvirtServiceInterfaces.LibvirtServiceInterface
	OnJailObjectUpdate func(jailIDs []uint)
	firewallTelemetry  *firewallTelemetryRuntime
}

func (s *Service) RegisterOnJailObjectUpdateCallback(cb func(jailIDs []uint)) {
	s.OnJailObjectUpdate = cb
}

func NewNetworkService(db *gorm.DB, telemetryDB *gorm.DB, libvirt libvirtServiceInterfaces.LibvirtServiceInterface) networkServiceInterfaces.NetworkServiceInterface {
	svc := &Service{
		DB:                   db,
		TelemetryDB:          telemetryDB,
		LibVirt:              libvirt,
		firewallTelemetry:    newFirewallTelemetryRuntime(),
		wgEndpointCache:      map[string][]string{},
		wgClientMetricsCache: make(map[uint]*wgClientMetricsCache),
	}

	svc.ensureListSnapshotMigration()
	return svc
}

func wireGuardNow() time.Time {
	return time.Now()
}
