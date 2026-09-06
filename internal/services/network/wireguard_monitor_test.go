// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

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
	previousReadDevice := wireGuardReadDevice
	previousCurrentTime := wireGuardCurrentTime
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardReadDevice = previousReadDevice
		wireGuardCurrentTime = previousCurrentTime
	})
	wireGuardCurrentTime = func() time.Time { return observedAt }
	wireGuardRunCommand = func(command string, args ...string) (string, error) {
		if command != "/sbin/ifconfig" || len(args) != 1 || args[0] != wireGuardClientInterfaceName(client.ID) {
			t.Fatalf("unexpected interface check: %s %v", command, args)
		}
		return args[0], nil
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
	previousReadDevice := wireGuardReadDevice
	previousCurrentTime := wireGuardCurrentTime
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardReadDevice = previousReadDevice
		wireGuardCurrentTime = previousCurrentTime
	})

	observedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	wireGuardCurrentTime = func() time.Time { return observedAt }
	svc := &Service{wgClientMetricsCache: map[uint]*wgClientMetricsCache{
		7: {id: 7, runtimeState: networkModels.WireGuardClientRuntimeUnknown},
	}}

	wireGuardRunCommand = func(string, ...string) (string, error) {
		return "", errors.New("interface does not exist")
	}
	wireGuardReadDevice = func(*Service, string) (*wgtypes.Device, error) {
		t.Fatal("missing interface must not be read")
		return nil, nil
	}
	svc.collectWireGuardClientMetrics()
	if got := svc.wgClientMetricsCache[7].runtimeState; got != networkModels.WireGuardClientRuntimeMissing {
		t.Fatalf("runtime state = %q, want missing", got)
	}

	wireGuardRunCommand = func(string, ...string) (string, error) { return "wgc7", nil }
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
