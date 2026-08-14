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

	s.wgClientMutex.Lock()
	if client, err := wireGuardNewWGClient(); err != nil {
		logger.L.Warn().Err(err).Msg("failed to create persistent wgctrl client for monitor")
	} else {
		s.wgClient = client
	}
	s.wgClientMutex.Unlock()

	go s.runWireGuardMonitor(runCtx)
}

func (s *Service) stopWireGuardMonitor() {
	s.wgMonitorMutex.Lock()
	if s.wgMonitorCancel != nil {
		s.wgMonitorCancel()
		s.wgMonitorCancel = nil
	}
	s.wgMonitorMutex.Unlock()

	s.wgClientMutex.Lock()
	if s.wgClient != nil {
		s.wgClient.Close()
		s.wgClient = nil
	}
	s.wgClientMutex.Unlock()

	s.flushWireGuardMetrics()
}

func (s *Service) flushWireGuardMetricsOnConfigChange() {
	s.wgMonitorMutex.Lock()
	running := s.wgMonitorCancel != nil
	s.wgMonitorMutex.Unlock()

	if !running {
		return
	}

	s.flushWireGuardMetrics()
}

func (s *Service) runWireGuardMonitor(ctx context.Context) {
	s.seedWireGuardMetricsCache()

	metricsTicker := time.NewTicker(5 * time.Second)
	flushTicker := time.NewTicker(1 * time.Minute)
	endpointTicker := time.NewTicker(30 * time.Second)
	defer metricsTicker.Stop()
	defer flushTicker.Stop()
	defer endpointTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushWireGuardMetrics()
			return
		case <-metricsTicker.C:
			if !s.isWireGuardServiceEnabled() {
				continue
			}

			inited, _ := s.isWireGuardServerInitialized()
			if !inited {
				continue
			}

			s.collectWireGuardServerMetrics()
			s.collectWireGuardClientMetrics()
		case <-flushTicker.C:
			if !s.isWireGuardServiceEnabled() {
				continue
			}
			s.flushWireGuardMetrics()
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

// seedWireGuardMetricsCache loads current DB state into memory once so that
// subsequent collect ticks never need to read from the DB.
func (s *Service) seedWireGuardMetricsCache() {
	s.wgMetricsMutex.Lock()
	defer s.wgMetricsMutex.Unlock()

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			logger.L.Debug().Err(err).Msg("failed to seed wireguard server metrics cache")
		}
		s.wgServerCache = nil
	} else {
		peerCache := make(map[string]*wgPeerMetrics, len(server.Peers))
		for _, peer := range server.Peers {
			peerCache[strings.TrimSpace(peer.PublicKey)] = &wgPeerMetrics{
				id:            peer.ID,
				rx:            peer.RX,
				tx:            peer.TX,
				lastKernelRX:  peer.LastKernelRX,
				lastKernelTX:  peer.LastKernelTX,
				lastHandshake: peer.LastHandshake,
			}
		}
		s.wgServerCache = &wgServerMetricsCache{
			id:            server.ID,
			rx:            server.RX,
			tx:            server.TX,
			lastKernelRX:  server.LastKernelRX,
			lastKernelTX:  server.LastKernelTX,
			lastHandshake: server.LastHandshake,
			restartedAt:   server.RestartedAt,
			peers:         peerCache,
		}
	}

	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		logger.L.Debug().Err(err).Msg("failed to seed wireguard client metrics cache")
		return
	}
	s.wgClientMetricsCache = make(map[uint]*wgClientMetricsCache, len(clients))
	for _, c := range clients {
		s.wgClientMetricsCache[c.ID] = &wgClientMetricsCache{
			id:            c.ID,
			rx:            c.RX,
			tx:            c.TX,
			kernelLastRX:  c.KernelLastRX,
			kernelLastTX:  c.KernelLastTX,
			lastHandshake: c.LastHandshake,
			restartedAt:   c.RestartedAt,
		}
	}
}

// collectWireGuardServerMetrics reads the WireGuard server device state via the
// persistent wgctrl client and updates in-memory counters. No DB I/O.
func (s *Service) collectWireGuardServerMetrics() {
	s.wgMetricsMutex.Lock()
	defer s.wgMetricsMutex.Unlock()

	if !wireGuardInterfaceExists(wireGuardServerInterfaceName) {
		return
	}

	dev, err := s.readWireGuardDeviceWithClient(wireGuardServerInterfaceName)
	if err != nil {
		return
	}

	if s.wgServerCache == nil {
		// server was initialized after the monitor started; seed it now
		var server networkModels.WireGuardServer
		if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
			return
		}
		peerCache := make(map[string]*wgPeerMetrics, len(server.Peers))
		for _, peer := range server.Peers {
			peerCache[strings.TrimSpace(peer.PublicKey)] = &wgPeerMetrics{
				id:            peer.ID,
				rx:            peer.RX,
				tx:            peer.TX,
				lastKernelRX:  peer.LastKernelRX,
				lastKernelTX:  peer.LastKernelTX,
				lastHandshake: peer.LastHandshake,
			}
		}
		s.wgServerCache = &wgServerMetricsCache{
			id:            server.ID,
			rx:            server.RX,
			tx:            server.TX,
			lastKernelRX:  server.LastKernelRX,
			lastKernelTX:  server.LastKernelTX,
			lastHandshake: server.LastHandshake,
			restartedAt:   server.RestartedAt,
			peers:         peerCache,
		}
	}

	cache := s.wgServerCache
	var totalKernelRX, totalKernelTX uint64
	lastHandshake := time.Time{}

	for _, kpeer := range dev.Peers {
		totalKernelRX += uint64(kpeer.ReceiveBytes)
		totalKernelTX += uint64(kpeer.TransmitBytes)
		if kpeer.LastHandshakeTime.After(lastHandshake) {
			lastHandshake = kpeer.LastHandshakeTime
		}

		pub := strings.TrimSpace(kpeer.PublicKey.String())
		pc, ok := cache.peers[pub]
		if !ok {
			// peer added at runtime; load from DB once
			var peer networkModels.WireGuardServerPeer
			if err := s.DB.Where("public_key = ?", pub).First(&peer).Error; err != nil {
				continue
			}
			pc = &wgPeerMetrics{
				id:            peer.ID,
				rx:            peer.RX,
				tx:            peer.TX,
				lastKernelRX:  peer.LastKernelRX,
				lastKernelTX:  peer.LastKernelTX,
				lastHandshake: peer.LastHandshake,
			}
			cache.peers[pub] = pc
		}

		currentRX := uint64(kpeer.ReceiveBytes)
		currentTX := uint64(kpeer.TransmitBytes)

		if currentRX < pc.lastKernelRX || currentTX < pc.lastKernelTX {
			pc.lastKernelRX = 0
			pc.lastKernelTX = 0
		}

		pc.rx += currentRX - pc.lastKernelRX
		pc.tx += currentTX - pc.lastKernelTX
		pc.lastKernelRX = currentRX
		pc.lastKernelTX = currentTX
		pc.lastHandshake = kpeer.LastHandshakeTime
		pc.dirty = true
	}

	if totalKernelRX < cache.lastKernelRX || totalKernelTX < cache.lastKernelTX {
		cache.lastKernelRX = 0
		cache.lastKernelTX = 0
	}

	cache.rx += totalKernelRX - cache.lastKernelRX
	cache.tx += totalKernelTX - cache.lastKernelTX
	cache.lastKernelRX = totalKernelRX
	cache.lastKernelTX = totalKernelTX
	cache.lastHandshake = lastHandshake
	cache.dirty = true
}

// collectWireGuardClientMetrics reads each outbound client's device state and
// updates in-memory counters. No DB I/O for clients already in the cache.
func (s *Service) collectWireGuardClientMetrics() {
	s.wgMetricsMutex.Lock()
	defer s.wgMetricsMutex.Unlock()

	for id, cc := range s.wgClientMetricsCache {
		interfaceName := wireGuardClientInterfaceName(id)
		if !wireGuardInterfaceExists(interfaceName) {
			continue
		}

		dev, err := s.readWireGuardDeviceWithClient(interfaceName)
		if err != nil {
			continue
		}

		var kernelRX, kernelTX uint64
		lastHandshake := time.Time{}
		for _, peer := range dev.Peers {
			kernelRX += uint64(peer.ReceiveBytes)
			kernelTX += uint64(peer.TransmitBytes)
			if peer.LastHandshakeTime.After(lastHandshake) {
				lastHandshake = peer.LastHandshakeTime
			}
		}

		if kernelRX < cc.kernelLastRX || kernelTX < cc.kernelLastTX {
			cc.kernelLastRX = 0
			cc.kernelLastTX = 0
		}

		cc.rx += kernelRX - cc.kernelLastRX
		cc.tx += kernelTX - cc.kernelLastTX
		cc.kernelLastRX = kernelRX
		cc.kernelLastTX = kernelTX
		cc.lastHandshake = lastHandshake
		cc.dirty = true
	}
}

// flushWireGuardMetrics writes all dirty in-memory cache entries to the DB and
// then resyncs the cache to pick up new/deleted peers and clients.
func (s *Service) flushWireGuardMetrics() {
	s.wgMetricsMutex.Lock()
	defer s.wgMetricsMutex.Unlock()

	if s.wgServerCache != nil && s.wgServerCache.dirty {
		uptime := uint64(0)
		if !s.wgServerCache.restartedAt.IsZero() {
			uptime = uint64(time.Since(s.wgServerCache.restartedAt).Seconds())
		}
		if err := s.DB.Model(&networkModels.WireGuardServer{ID: s.wgServerCache.id}).Updates(map[string]any{
			"rx":             s.wgServerCache.rx,
			"tx":             s.wgServerCache.tx,
			"last_kernel_rx": s.wgServerCache.lastKernelRX,
			"last_kernel_tx": s.wgServerCache.lastKernelTX,
			"last_handshake": s.wgServerCache.lastHandshake,
			"uptime":         uptime,
		}).Error; err != nil {
			logger.L.Debug().Err(err).Msg("failed to flush wireguard server metrics")
		} else {
			s.wgServerCache.dirty = false
		}

		for _, pc := range s.wgServerCache.peers {
			if !pc.dirty {
				continue
			}
			if err := s.DB.Model(&networkModels.WireGuardServerPeer{ID: pc.id}).Updates(map[string]any{
				"rx":             pc.rx,
				"tx":             pc.tx,
				"last_kernel_rx": pc.lastKernelRX,
				"last_kernel_tx": pc.lastKernelTX,
				"last_handshake": pc.lastHandshake,
			}).Error; err != nil {
				logger.L.Debug().Err(err).Msg("failed to flush wireguard peer metrics")
			} else {
				pc.dirty = false
			}
		}
	}

	for _, cc := range s.wgClientMetricsCache {
		if !cc.dirty {
			continue
		}
		uptime := uint64(0)
		if !cc.restartedAt.IsZero() {
			uptime = uint64(time.Since(cc.restartedAt).Seconds())
		}
		if err := s.DB.Model(&networkModels.WireGuardClient{ID: cc.id}).Updates(map[string]any{
			"rx":             cc.rx,
			"tx":             cc.tx,
			"kernel_last_rx": cc.kernelLastRX,
			"kernel_last_tx": cc.kernelLastTX,
			"last_handshake": cc.lastHandshake,
			"uptime":         uptime,
		}).Error; err != nil {
			logger.L.Debug().Err(err).Msg("failed to flush wireguard client metrics")
		} else {
			cc.dirty = false
		}
	}

	s.resyncMetricsCacheAfterFlush()
}

// resyncMetricsCacheAfterFlush refreshes the cache to pick up peers/clients
// that were added or removed since the last seed or flush.
func (s *Service) resyncMetricsCacheAfterFlush() {
	// Resync server peers
	if s.wgServerCache != nil {
		var server networkModels.WireGuardServer
		if err := s.DB.Preload("Peers").First(&server).Error; err == nil {
			s.wgServerCache.restartedAt = server.RestartedAt

			currentPubs := make(map[string]struct{}, len(server.Peers))
			for _, peer := range server.Peers {
				pub := strings.TrimSpace(peer.PublicKey)
				currentPubs[pub] = struct{}{}
				if _, ok := s.wgServerCache.peers[pub]; !ok {
					s.wgServerCache.peers[pub] = &wgPeerMetrics{
						id:            peer.ID,
						rx:            peer.RX,
						tx:            peer.TX,
						lastKernelRX:  peer.LastKernelRX,
						lastKernelTX:  peer.LastKernelTX,
						lastHandshake: peer.LastHandshake,
					}
				}
			}
			for pub := range s.wgServerCache.peers {
				if _, ok := currentPubs[pub]; !ok {
					delete(s.wgServerCache.peers, pub)
				}
			}
		}
	}

	// Resync clients
	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		return
	}
	seen := make(map[uint]struct{}, len(clients))
	for _, c := range clients {
		seen[c.ID] = struct{}{}
		if _, ok := s.wgClientMetricsCache[c.ID]; !ok {
			s.wgClientMetricsCache[c.ID] = &wgClientMetricsCache{
				id:            c.ID,
				rx:            c.RX,
				tx:            c.TX,
				kernelLastRX:  c.KernelLastRX,
				kernelLastTX:  c.KernelLastTX,
				lastHandshake: c.LastHandshake,
				restartedAt:   c.RestartedAt,
			}
		} else {
			s.wgClientMetricsCache[c.ID].restartedAt = c.RestartedAt
		}
	}
	for id := range s.wgClientMetricsCache {
		if _, ok := seen[id]; !ok {
			delete(s.wgClientMetricsCache, id)
		}
	}
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
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

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
