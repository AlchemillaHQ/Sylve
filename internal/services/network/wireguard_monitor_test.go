// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

func TestWireGuardServerMetricsAvailableBeforeDatabaseFlush(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{},
	)
	seedWireGuardServiceEnabled(t, db)
	observedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	handshakeAt := observedAt.Add(-10 * time.Second)
	oldHandshake := observedAt.Add(-10 * time.Minute)
	server := networkModels.WireGuardServer{
		Enabled:       true,
		Port:          61820,
		PrivateKey:    "server-private-key",
		RX:            100,
		TX:            200,
		LastKernelRX:  10,
		LastKernelTX:  20,
		LastHandshake: oldHandshake,
		RestartedAt:   observedAt.Add(-time.Minute),
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	key := mustGenerateWireGuardPrivateKey(t).PublicKey()
	peer := networkModels.WireGuardServerPeer{
		WireGuardServerID: server.ID,
		Enabled:           true,
		Name:              "connected",
		PublicKey:         key.String(),
		PrivateKey:        "peer-private-key",
		RX:                10,
		TX:                20,
		LastKernelRX:      5,
		LastKernelTX:      10,
		LastHandshake:     oldHandshake,
	}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatal(err)
	}
	noHandshakeKey := mustGenerateWireGuardPrivateKey(t).PublicKey()
	noHandshakePeer := networkModels.WireGuardServerPeer{
		WireGuardServerID: server.ID,
		Enabled:           true,
		Name:              "no-handshake",
		PublicKey:         noHandshakeKey.String(),
		LastHandshake:     oldHandshake,
	}
	if err := db.Create(&noHandshakePeer).Error; err != nil {
		t.Fatal(err)
	}
	svc.seedWireGuardMetricsCache()

	previousListInterfaces := wireGuardListInterfaces
	previousReadDevice := wireGuardReadDevice
	previousCurrentTime := wireGuardCurrentTime
	t.Cleanup(func() {
		wireGuardListInterfaces = previousListInterfaces
		wireGuardReadDevice = previousReadDevice
		wireGuardCurrentTime = previousCurrentTime
	})
	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: wireGuardServerInterfaceName}}, nil
	}
	wireGuardCurrentTime = func() time.Time { return observedAt }
	wireGuardReadDevice = func(_ *Service, iface string) (*wgtypes.Device, error) {
		if iface != wireGuardServerInterfaceName {
			t.Fatalf("unexpected device read: %s", iface)
		}
		return &wgtypes.Device{Peers: []wgtypes.Peer{
			{PublicKey: key, ReceiveBytes: 25, TransmitBytes: 50, LastHandshakeTime: handshakeAt},
			{PublicKey: noHandshakeKey},
		}}, nil
	}
	svc.collectWireGuardMetrics()

	got, err := svc.GetWireGuardServer()
	if err != nil {
		t.Fatal(err)
	}
	if got.RX != 115 || got.TX != 230 || !got.LastHandshake.Equal(handshakeAt) || got.Uptime != 60 {
		t.Fatalf("server did not expose cached metrics: %+v", got)
	}
	if got.Port != server.Port || got.PrivateKey != server.PrivateKey || !got.Enabled {
		t.Fatalf("server configuration changed in the response: %+v", got)
	}
	if len(got.Peers) != 2 {
		t.Fatalf("peer count = %d, want 2", len(got.Peers))
	}
	for _, gotPeer := range got.Peers {
		switch gotPeer.ID {
		case peer.ID:
			if gotPeer.RX != 30 || gotPeer.TX != 60 || !gotPeer.LastHandshake.Equal(handshakeAt) {
				t.Fatalf("peer did not expose cached metrics: %+v", gotPeer)
			}
			if gotPeer.Name != peer.Name || gotPeer.PrivateKey != peer.PrivateKey || !gotPeer.Enabled {
				t.Fatalf("peer configuration changed in the response: %+v", gotPeer)
			}
		case noHandshakePeer.ID:
			if !gotPeer.LastHandshake.IsZero() {
				t.Fatalf("zero kernel handshake did not replace the persisted handshake: %s", gotPeer.LastHandshake)
			}
		default:
			t.Fatalf("unexpected peer: %+v", gotPeer)
		}
	}

	var storedServer networkModels.WireGuardServer
	if err := db.First(&storedServer, server.ID).Error; err != nil {
		t.Fatal(err)
	}
	var storedPeer networkModels.WireGuardServerPeer
	if err := db.First(&storedPeer, peer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedServer.RX != server.RX || storedServer.TX != server.TX || !storedServer.LastHandshake.Equal(oldHandshake) {
		t.Fatalf("server metrics were flushed before the persistence tick: %+v", storedServer)
	}
	if storedPeer.RX != peer.RX || storedPeer.TX != peer.TX || !storedPeer.LastHandshake.Equal(oldHandshake) {
		t.Fatalf("peer metrics were flushed before the persistence tick: %+v", storedPeer)
	}

	svc.flushWireGuardMetrics()
	if err := db.First(&storedPeer, peer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedPeer.RX != 30 || storedPeer.TX != 60 || !storedPeer.LastHandshake.Equal(handshakeAt) {
		t.Fatalf("explicit flush did not persist cached metrics: %+v", storedPeer)
	}
}

func TestGetWireGuardServerFallsBackToPersistedMetricsWithoutSnapshot(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "missing snapshot"
		if mismatch {
			name = "different server"
		}
		t.Run(name, func(t *testing.T) {
			svc, db := newNetworkServiceForTest(t,
				&models.BasicSettings{}, &networkModels.WireGuardServer{}, &networkModels.WireGuardServerPeer{},
			)
			seedWireGuardServiceEnabled(t, db)
			handshake := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
			server := networkModels.WireGuardServer{RX: 100, TX: 200, Uptime: 123, LastHandshake: handshake}
			if err := db.Create(&server).Error; err != nil {
				t.Fatal(err)
			}
			peer := networkModels.WireGuardServerPeer{WireGuardServerID: server.ID, RX: 10, TX: 20, LastHandshake: handshake}
			if err := db.Create(&peer).Error; err != nil {
				t.Fatal(err)
			}
			if mismatch {
				svc.wgServerCache = &wgServerMetricsCache{id: server.ID + 1, rx: 999, tx: 999}
			}
			got, err := svc.GetWireGuardServer()
			if err != nil {
				t.Fatal(err)
			}
			if got.RX != server.RX || got.TX != server.TX || got.Uptime != server.Uptime || !got.LastHandshake.Equal(handshake) {
				t.Fatalf("persisted server metrics were not preserved: %+v", got)
			}
			if len(got.Peers) != 1 || got.Peers[0].RX != peer.RX || got.Peers[0].TX != peer.TX || !got.Peers[0].LastHandshake.Equal(handshake) {
				t.Fatalf("persisted peer metrics were not preserved: %+v", got.Peers)
			}
		})
	}
}

func TestWireGuardServerRuntimeOverlayMatchesPeerIdentity(t *testing.T) {
	oldHandshake := time.Date(2026, time.October, 8, 11, 0, 0, 0, time.UTC)
	freshHandshake := oldHandshake.Add(time.Hour)
	server := networkModels.WireGuardServer{
		ID: 1,
		Peers: []networkModels.WireGuardServerPeer{
			{ID: 1, PublicKey: "  matching-key  ", RX: 1, LastHandshake: oldHandshake},
			{ID: 2, PublicKey: "reused-key", RX: 2, LastHandshake: oldHandshake},
			{ID: 3, PublicKey: "new-key", RX: 3, LastHandshake: oldHandshake},
		},
	}
	svc := &Service{wgServerCache: &wgServerMetricsCache{
		id: 1,
		peers: map[string]*wgPeerMetrics{
			"matching-key": {id: 1, rx: 100, lastHandshake: freshHandshake},
			"reused-key":   {id: 99, rx: 999, lastHandshake: freshHandshake},
		},
	}}
	svc.overlayWireGuardServerRuntime(&server)
	if server.Peers[0].RX != 100 || !server.Peers[0].LastHandshake.Equal(freshHandshake) {
		t.Fatalf("matching peer snapshot was not overlaid: %+v", server.Peers[0])
	}
	for _, peer := range server.Peers[1:] {
		if peer.RX != uint64(peer.ID) || !peer.LastHandshake.Equal(oldHandshake) {
			t.Fatalf("unmatched peer did not keep persisted metrics: %+v", peer)
		}
	}
}

func TestWireGuardMetricsCacheResyncDropsReplacedServerAndPeers(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.WireGuardServer{}, &networkModels.WireGuardServerPeer{}, &networkModels.WireGuardClient{},
	)
	server := networkModels.WireGuardServer{}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	peer := networkModels.WireGuardServerPeer{WireGuardServerID: server.ID, PublicKey: "reused-key", RX: 100}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatal(err)
	}
	svc.seedWireGuardMetricsCache()
	if err := db.Delete(&peer).Error; err != nil {
		t.Fatal(err)
	}
	replacementPeer := networkModels.WireGuardServerPeer{WireGuardServerID: server.ID, PublicKey: peer.PublicKey, RX: 200}
	if err := db.Create(&replacementPeer).Error; err != nil {
		t.Fatal(err)
	}
	svc.resyncMetricsCacheAfterFlush()
	cachedPeer := svc.wgServerCache.peers[peer.PublicKey]
	if cachedPeer.id != replacementPeer.ID || cachedPeer.rx != replacementPeer.RX {
		t.Fatalf("replacement peer retained the old snapshot: %+v", cachedPeer)
	}

	if err := db.Delete(&server).Error; err != nil {
		t.Fatal(err)
	}
	svc.resyncMetricsCacheAfterFlush()
	if svc.wgServerCache != nil {
		t.Fatal("deleted server retained its cached metrics")
	}
	replacementServer := networkModels.WireGuardServer{}
	if err := db.Create(&replacementServer).Error; err != nil {
		t.Fatal(err)
	}
	svc.wgServerCache = &wgServerMetricsCache{id: server.ID, rx: 999}
	svc.resyncMetricsCacheAfterFlush()
	if svc.wgServerCache != nil {
		t.Fatal("replacement server retained another server's cached metrics")
	}
}

func TestWireGuardClientMetricsAvailableBeforeDatabaseFlushWithoutServer(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{},
	)
	seedWireGuardServiceEnabled(t, db)

	observedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	handshakeAt := observedAt.Add(-10 * time.Second)
	restartedAt := observedAt.Add(-time.Minute)
	client := networkModels.WireGuardClient{
		Enabled:       true,
		Name:          "client-only",
		RX:            100,
		TX:            200,
		KernelLastRX:  10,
		KernelLastTX:  20,
		LastHandshake: time.Time{},
		RestartedAt:   restartedAt,
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed wireguard client: %v", err)
	}
	svc.wgClientMetricsCache = map[uint]*wgClientMetricsCache{
		client.ID: {
			id:            client.ID,
			rx:            client.RX,
			tx:            client.TX,
			kernelLastRX:  client.KernelLastRX,
			kernelLastTX:  client.KernelLastTX,
			lastHandshake: client.LastHandshake,
			restartedAt:   client.RestartedAt,
			runtimeState:  networkModels.WireGuardClientRuntimeUnknown,
		},
	}

	previousRunCommand := wireGuardRunCommand
	previousListInterfaces := wireGuardListInterfaces
	previousReadDevice := wireGuardReadDevice
	previousCurrentTime := wireGuardCurrentTime
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardListInterfaces = previousListInterfaces
		wireGuardReadDevice = previousReadDevice
		wireGuardCurrentTime = previousCurrentTime
	})
	wireGuardCurrentTime = func() time.Time { return observedAt }
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		t.Fatalf("metrics collection must not run a command: %s %v", command, args)
		return "", nil
	}
	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: wireGuardClientInterfaceName(client.ID)}}, nil
	}
	readCalls := 0
	wireGuardReadDevice = func(_ *Service, iface string) (*wgtypes.Device, error) {
		readCalls++
		if iface != wireGuardClientInterfaceName(client.ID) {
			t.Fatalf("unexpected wireguard device read: %s", iface)
		}
		return &wgtypes.Device{Peers: []wgtypes.Peer{{
			ReceiveBytes:      25,
			TransmitBytes:     50,
			LastHandshakeTime: handshakeAt,
		}}}, nil
	}

	// No server row exists. Client collection must still run.
	svc.collectWireGuardMetrics()
	if readCalls != 1 {
		t.Fatalf("wireguard client device reads = %d, want 1", readCalls)
	}

	var stored networkModels.WireGuardClient
	if err := db.First(&stored, client.ID).Error; err != nil {
		t.Fatalf("failed to reload stored client: %v", err)
	}
	if stored.RX != 100 || stored.TX != 200 || !stored.LastHandshake.IsZero() {
		t.Fatalf("metrics were unexpectedly flushed to the database: %+v", stored)
	}

	clients, err := svc.GetWireGuardClients()
	if err != nil {
		t.Fatalf("failed to get wireguard clients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("client count = %d, want 1", len(clients))
	}
	got := clients[0]
	if got.RX != 115 || got.TX != 230 {
		t.Fatalf("runtime counters = (%d, %d), want (115, 230)", got.RX, got.TX)
	}
	if !got.LastHandshake.Equal(handshakeAt) {
		t.Fatalf("last handshake = %s, want %s", got.LastHandshake, handshakeAt)
	}
	if got.RuntimeState != networkModels.WireGuardClientRuntimeAvailable {
		t.Fatalf("runtime state = %q, want available", got.RuntimeState)
	}
	if got.RuntimeObservedAt == nil || !got.RuntimeObservedAt.Equal(observedAt) {
		t.Fatalf("runtime observation = %v, want %s", got.RuntimeObservedAt, observedAt)
	}
	if got.Uptime != 60 {
		t.Fatalf("runtime uptime = %d, want 60", got.Uptime)
	}
}

func TestGetWireGuardClientsFallsBackToPersistedMetricsWithoutSnapshot(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)

	handshakeAt := time.Date(2026, time.September, 6, 11, 59, 0, 0, time.UTC)
	client := networkModels.WireGuardClient{
		Enabled:       true,
		Name:          "persisted-only",
		RX:            321,
		TX:            654,
		LastHandshake: handshakeAt,
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed wireguard client: %v", err)
	}

	clients, err := svc.GetWireGuardClients()
	if err != nil {
		t.Fatalf("failed to get wireguard clients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("client count = %d, want 1", len(clients))
	}
	got := clients[0]
	if got.RX != client.RX || got.TX != client.TX || !got.LastHandshake.Equal(handshakeAt) {
		t.Fatalf("persisted metrics were not preserved: %+v", got)
	}
	if got.RuntimeState != networkModels.WireGuardClientRuntimeUnknown {
		t.Fatalf("runtime state = %q, want unknown", got.RuntimeState)
	}
	if got.RuntimeObservedAt != nil {
		t.Fatalf("runtime observation = %v, want nil", got.RuntimeObservedAt)
	}
}

func TestWireGuardClientRuntimeStateReportsMissingAndReadErrors(t *testing.T) {
	previousRunCommand := wireGuardRunCommand
	previousListInterfaces := wireGuardListInterfaces
	previousReadDevice := wireGuardReadDevice
	previousCurrentTime := wireGuardCurrentTime
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardListInterfaces = previousListInterfaces
		wireGuardReadDevice = previousReadDevice
		wireGuardCurrentTime = previousCurrentTime
	})

	observedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	wireGuardCurrentTime = func() time.Time { return observedAt }
	svc := &Service{wgClientMetricsCache: map[uint]*wgClientMetricsCache{
		7: {id: 7, runtimeState: networkModels.WireGuardClientRuntimeUnknown},
	}}

	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		t.Fatalf("metrics collection must not run a command: %s %v", command, args)
		return "", nil
	}
	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return nil, nil
	}
	wireGuardReadDevice = func(*Service, string) (*wgtypes.Device, error) {
		t.Fatal("missing interface must not be read")
		return nil, nil
	}
	svc.collectWireGuardClientMetrics()
	if got := svc.wgClientMetricsCache[7].runtimeState; got != networkModels.WireGuardClientRuntimeMissing {
		t.Fatalf("runtime state = %q, want missing", got)
	}

	wireGuardListInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "wgc7"}}, nil
	}
	wireGuardReadDevice = func(*Service, string) (*wgtypes.Device, error) {
		return nil, errors.New("injected device read failure")
	}
	svc.collectWireGuardClientMetrics()
	if got := svc.wgClientMetricsCache[7].runtimeState; got != networkModels.WireGuardClientRuntimeError {
		t.Fatalf("runtime state = %q, want error", got)
	}
	if got := svc.wgClientMetricsCache[7].runtimeObservedAt; !got.Equal(observedAt) {
		t.Fatalf("runtime observation = %s, want %s", got, observedAt)
	}
}

func stubWireGuardMonitorClient(t *testing.T) *atomic.Int32 {
	t.Helper()
	previousFactory := wireGuardNewWGClient
	t.Cleanup(func() { wireGuardNewWGClient = previousFactory })
	calls := &atomic.Int32{}
	wireGuardNewWGClient = func() (*wgctrl.Client, error) {
		calls.Add(1)
		return &wgctrl.Client{}, nil
	}
	return calls
}

func waitForWireGuardMonitorExit(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("wireguard monitor did not exit")
	}
}

func TestWireGuardMonitorCancellationAllowsRestart(t *testing.T) {
	svc, _ := newNetworkServiceForTest(t,
		&networkModels.WireGuardServer{}, &networkModels.WireGuardServerPeer{}, &networkModels.WireGuardClient{},
	)
	calls := stubWireGuardMonitorClient(t)
	t.Cleanup(svc.stopWireGuardMonitor)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartWireGuardMonitor(ctx)
	svc.wgMonitorMutex.Lock()
	firstDone := svc.wgMonitorDone
	svc.wgMonitorMutex.Unlock()
	cancel()
	waitForWireGuardMonitorExit(t, firstDone)
	if svc.wgMonitorCancel != nil || svc.wgMonitorDone != nil || svc.wgClient != nil {
		t.Fatal("exited monitor retained its running marker or metrics client")
	}

	svc.StartWireGuardMonitor(context.Background())
	svc.wgMonitorMutex.Lock()
	secondDone := svc.wgMonitorDone
	svc.wgMonitorMutex.Unlock()
	svc.StartWireGuardMonitor(context.Background())
	if secondDone == nil || secondDone == firstDone || calls.Load() != 2 {
		t.Fatalf("monitor was not restarted exactly once: factory calls = %d", calls.Load())
	}
	svc.stopWireGuardMonitor()
	waitForWireGuardMonitorExit(t, secondDone)
}

func TestWireGuardMonitorDoesNotStartWithCancelledContext(t *testing.T) {
	calls := stubWireGuardMonitorClient(t)
	svc := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.StartWireGuardMonitor(ctx)
	if svc.wgMonitorCancel != nil || svc.wgMonitorDone != nil || calls.Load() != 0 {
		t.Fatal("cancelled context started a monitor")
	}
}

func TestWireGuardMonitorSurvivesServiceStateRequests(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{}, &networkModels.WireGuardServer{}, &networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{}, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{},
	)
	basic := models.BasicSettings{Services: []models.AvailableService{}}
	if err := db.Create(&basic).Error; err != nil {
		t.Fatal(err)
	}
	stubWireGuardServerRuntime(t)
	calls := stubWireGuardMonitorClient(t)
	t.Cleanup(svc.stopWireGuardMonitor)
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	defer cancelDaemon()
	svc.StartWireGuardMonitor(daemonCtx)
	svc.wgMonitorMutex.Lock()
	done := svc.wgMonitorDone
	svc.wgMonitorMutex.Unlock()

	basic.Services = []models.AvailableService{models.WireGuard}
	if err := db.Save(&basic).Error; err != nil {
		t.Fatal(err)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	if err := svc.EnableWireGuardService(requestCtx); err != nil {
		cancelRequest()
		t.Fatal(err)
	}
	cancelRequest()
	basic.Services = []models.AvailableService{}
	if err := db.Save(&basic).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableWireGuardService(requestCtx); err != nil {
		t.Fatal(err)
	}
	svc.wgMonitorMutex.Lock()
	unchanged := svc.wgMonitorDone == done && svc.wgMonitorCancel != nil
	svc.wgMonitorMutex.Unlock()
	if !unchanged || calls.Load() != 1 {
		t.Fatal("service state request stopped or replaced the daemon-owned monitor")
	}
	select {
	case <-done:
		t.Fatal("request cancellation stopped the monitor")
	default:
	}
	cancelDaemon()
	waitForWireGuardMonitorExit(t, done)
}

func TestWireGuardMetricsClientRetriesFailedInitializationAndReconnect(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		name := "initialization"
		if reconnect {
			name = "reconnect"
		}
		t.Run(name, func(t *testing.T) {
			previousFactory := wireGuardNewWGClient
			t.Cleanup(func() { wireGuardNewWGClient = previousFactory })
			wantErr := errors.New("injected wgctrl connection failure")
			calls := 0
			wireGuardNewWGClient = func() (*wgctrl.Client, error) {
				calls++
				return nil, wantErr
			}
			svc := &Service{}
			if reconnect {
				svc.wgClient = &wgctrl.Client{}
			}
			for range 3 {
				if _, err := svc.readWireGuardDeviceWithClient("wgs0"); !errors.Is(err, wantErr) {
					t.Fatalf("device read error = %v, want connection failure", err)
				}
			}
			if calls != 3 || svc.wgClient != nil {
				t.Fatalf("missing metrics client was not retried: factory calls = %d", calls)
			}
		})
	}
}

func TestWireGuardRecoveryWaitsForFirewallAfterStartupFailure(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{}, &networkModels.WireGuardServer{}, &networkModels.WireGuardServerPeer{},
		&networkModels.WireGuardClient{}, &networkModels.FirewallTrafficRule{}, &networkModels.FirewallNATRule{},
		&networkModels.StaticRoute{},
	)
	seedWireGuardServiceEnabled(t, db)
	runtime := stubWireGuardServerRuntime(t)
	stubWireGuardMonitorClient(t)
	t.Cleanup(svc.stopWireGuardMonitor)
	svc.wgEndpointCache = map[string][]string{}
	svc.wgClientMetricsCache = make(map[uint]*wgClientMetricsCache)
	privateKey := mustGenerateWireGuardPrivateKey(t)
	server := networkModels.WireGuardServer{
		Enabled: true, PrivateKey: privateKey.String(), PublicKey: privateKey.PublicKey().String(),
		Port: 61820, Addresses: []string{"10.210.0.1/24"}, MTU: 1420,
		AllowWireGuardPort: true, MasqueradeIPv4Interface: "bridge0",
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatal(err)
	}
	client := wireGuardRuntimeClient(t, 1, "198.51.100.20")
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	blocked := &atomic.Bool{}
	blocked.Store(true)
	wantErr := errors.New("injected firewall startup failure")
	if err := db.Callback().Query().Before("gorm:query").Register("wireguard:firewall-failure", func(tx *gorm.DB) {
		if blocked.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.Name == "FirewallTrafficRule" {
			tx.AddError(wantErr)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableWireGuardService(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("startup error = %v, want firewall failure", err)
	}
	svc.StartWireGuardMonitor(context.Background())
	if svc.wgMonitorCancel == nil || !svc.wgRuntimeSyncPending {
		t.Fatal("startup failure did not leave monitoring and recovery available")
	}
	if runtime.interfaceExists("wgs0") || runtime.interfaceExists("wgc1") {
		t.Fatal("startup failure left managed wireguard interfaces active")
	}
	createdBeforeRetry := runtime.ifaceCounter
	svc.retryWireGuardRuntime()
	if runtime.ifaceCounter != createdBeforeRetry || !svc.wgRuntimeSyncPending {
		t.Fatal("recovery recreated a tunnel before the firewall could be reconciled")
	}

	blocked.Store(false)
	svc.retryWireGuardRuntime()
	if svc.wgRuntimeSyncPending || !runtime.interfaceExists("wgs0") || !runtime.interfaceExists("wgc1") {
		t.Fatal("wireguard runtime did not recover after the firewall failure was repaired")
	}
	assertManagedWireGuardRuleCounts(t, db, 1, 1)

	var basic models.BasicSettings
	if err := db.First(&basic).Error; err != nil {
		t.Fatal(err)
	}
	basic.Services = []models.AvailableService{}
	if err := db.Save(&basic).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableWireGuardService(context.Background()); err != nil {
		t.Fatal(err)
	}
	createdBeforeRetry = runtime.ifaceCounter
	svc.retryWireGuardRuntime()
	if runtime.ifaceCounter != createdBeforeRetry || runtime.interfaceExists("wgs0") || runtime.interfaceExists("wgc1") {
		t.Fatal("recovery recreated a tunnel while the wireguard service was disabled")
	}
}
