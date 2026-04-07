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
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

func (s *Service) StartWireGuardMonitor(ctx context.Context) {
	s.wgMonitorMutex.Lock()
	if s.wgMonitorCancel != nil {
		s.wgMonitorMutex.Unlock()
		return
	}

	if ctx == nil {
		ctx = context.Background()
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.wgMonitorCancel = cancel
	s.wgMonitorMutex.Unlock()

	go s.runWireGuardMonitor(runCtx)
}

func (s *Service) stopWireGuardMonitor() {
	s.wgMonitorMutex.Lock()
	defer s.wgMonitorMutex.Unlock()

	if s.wgMonitorCancel != nil {
		s.wgMonitorCancel()
		s.wgMonitorCancel = nil
	}
}

func (s *Service) runWireGuardMonitor(ctx context.Context) {
	metricsTicker := time.NewTicker(1 * time.Second)
	endpointTicker := time.NewTicker(30 * time.Second)
	defer metricsTicker.Stop()
	defer endpointTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-metricsTicker.C:
			if !s.isWireGuardServiceEnabled() {
				continue
			}

			inited, _ := s.isWireGuardServerInitialized()
			if !inited {
				continue
			}

			if err := s.storeWireGuardServerAndPeerMetrics(); err != nil {
				logger.L.Debug().Err(err).Msg("failed to store wireguard server metrics")
			}
			if err := s.storeWireGuardClientMetrics(); err != nil {
				logger.L.Debug().Err(err).Msg("failed to store wireguard client metrics")
			}
		case <-endpointTicker.C:
			if !s.isWireGuardServiceEnabled() {
				continue
			}
			if err := s.refreshWireGuardClientEndpoints(); err != nil {
				logger.L.Debug().Err(err).Msg("failed to refresh wireguard client endpoints")
			}
		}
	}
}

func (s *Service) storeWireGuardServerAndPeerMetrics() error {
	var server networkModels.WireGuardServer
	err := s.DB.Preload("Peers").First(&server).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	if !server.Enabled {
		return nil
	}

	if !wireGuardInterfaceExists(wireGuardServerInterfaceName) {
		return nil
	}

	dev, err := readWireGuardDevice(wireGuardServerInterfaceName)
	if err != nil {
		return err
	}

	var kernelRX uint64
	var kernelTX uint64
	lastHandshake := time.Time{}

	peerByPub := make(map[string]networkModels.WireGuardServerPeer, len(server.Peers))
	for _, peer := range server.Peers {
		peerByPub[strings.TrimSpace(peer.PublicKey)] = peer
	}

	for _, kpeer := range dev.Peers {
		kernelRX += uint64(kpeer.ReceiveBytes)
		kernelTX += uint64(kpeer.TransmitBytes)
		if kpeer.LastHandshakeTime.After(lastHandshake) {
			lastHandshake = kpeer.LastHandshakeTime
		}

		pub := strings.TrimSpace(kpeer.PublicKey.String())
		peer, ok := peerByPub[pub]
		if !ok {
			continue
		}

		currentRX := uint64(kpeer.ReceiveBytes)
		currentTX := uint64(kpeer.TransmitBytes)

		if currentRX < peer.LastKernelRX || currentTX < peer.LastKernelTX {
			peer.LastKernelRX = 0
			peer.LastKernelTX = 0
		}

		peer.RX += currentRX - peer.LastKernelRX
		peer.TX += currentTX - peer.LastKernelTX
		peer.LastKernelRX = currentRX
		peer.LastKernelTX = currentTX
		peer.LastHandshake = kpeer.LastHandshakeTime

		if err := s.DB.Model(&peer).Updates(map[string]any{
			"rx":             peer.RX,
			"tx":             peer.TX,
			"last_kernel_rx": peer.LastKernelRX,
			"last_kernel_tx": peer.LastKernelTX,
			"last_handshake": peer.LastHandshake,
		}).Error; err != nil {
			return err
		}
	}

	if kernelRX < server.LastKernelRX || kernelTX < server.LastKernelTX {
		server.LastKernelRX = 0
		server.LastKernelTX = 0
	}

	server.RX += kernelRX - server.LastKernelRX
	server.TX += kernelTX - server.LastKernelTX
	server.LastKernelRX = kernelRX
	server.LastKernelTX = kernelTX
	server.LastHandshake = lastHandshake
	if !server.RestartedAt.IsZero() {
		server.Uptime = uint64(time.Since(server.RestartedAt).Seconds())
	}

	return s.DB.Model(&server).Updates(map[string]any{
		"rx":             server.RX,
		"tx":             server.TX,
		"last_kernel_rx": server.LastKernelRX,
		"last_kernel_tx": server.LastKernelTX,
		"last_handshake": server.LastHandshake,
		"uptime":         server.Uptime,
	}).Error
}

func (s *Service) storeWireGuardClientMetrics() error {
	var clients []networkModels.WireGuardClient
	if err := s.DB.Where("enabled = ?", true).Find(&clients).Error; err != nil {
		return err
	}

	for _, client := range clients {
		interfaceName := wireGuardClientInterfaceName(client.ID)
		if !wireGuardInterfaceExists(interfaceName) {
			continue
		}

		dev, err := readWireGuardDevice(interfaceName)
		if err != nil {
			continue
		}

		var kernelRX uint64
		var kernelTX uint64
		lastHandshake := time.Time{}
		for _, peer := range dev.Peers {
			kernelRX += uint64(peer.ReceiveBytes)
			kernelTX += uint64(peer.TransmitBytes)
			if peer.LastHandshakeTime.After(lastHandshake) {
				lastHandshake = peer.LastHandshakeTime
			}
		}

		if kernelRX < client.KernelLastRX || kernelTX < client.KernelLastTX {
			client.KernelLastRX = 0
			client.KernelLastTX = 0
		}

		client.RX += kernelRX - client.KernelLastRX
		client.TX += kernelTX - client.KernelLastTX
		client.KernelLastRX = kernelRX
		client.KernelLastTX = kernelTX
		client.LastHandshake = lastHandshake
		if !client.RestartedAt.IsZero() {
			client.Uptime = uint64(time.Since(client.RestartedAt).Seconds())
		}

		if err := s.DB.Model(&client).Updates(map[string]any{
			"rx":             client.RX,
			"tx":             client.TX,
			"kernel_last_rx": client.KernelLastRX,
			"kernel_last_tx": client.KernelLastTX,
			"last_handshake": client.LastHandshake,
			"uptime":         client.Uptime,
		}).Error; err != nil {
			return err
		}
	}

	return nil
}

func equalStringSlice(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Service) refreshWireGuardClientEndpoints() error {
	var clients []networkModels.WireGuardClient
	if err := s.DB.Where("enabled = ?", true).Find(&clients).Error; err != nil {
		return err
	}

	for _, client := range clients {
		resolved, err := resolveEndpointIPs(client.EndpointHost)
		if err != nil {
			continue
		}

		cacheKey := strings.TrimSpace(client.EndpointHost)
		s.wgMonitorMutex.Lock()
		previous := append([]string(nil), s.wgEndpointCache[cacheKey]...)
		s.wgEndpointCache[cacheKey] = resolved
		s.wgMonitorMutex.Unlock()

		if len(previous) == 0 || equalStringSlice(previous, resolved) {
			continue
		}

		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			logger.L.Debug().Err(err).Msg("failed to reapply wireguard client after endpoint change")
			continue
		}

		_ = s.DB.Model(&client).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}
