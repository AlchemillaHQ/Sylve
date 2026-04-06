// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

func (s *Service) GetWireGuardClients() ([]networkModels.WireGuardClient, error) {
	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		return nil, err
	}

	return clients, nil
}

func (s *Service) CreateWireGuardClient(req *WireGuardClientRequest) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if strings.TrimSpace(req.PeerPublicKey) == "" {
		return ErrWireGuardPeerPublicKeyRequired
	}

	allowedIPs := sortedUnique(req.AllowedIPs)
	if len(allowedIPs) == 0 {
		return ErrWireGuardAllowedIPsRequired
	}
	if err := parseWireGuardCIDRs(allowedIPs); err != nil {
		return err
	}

	addresses := sortedUnique(req.Addresses)
	if len(addresses) == 0 {
		return ErrWireGuardAddressesRequired
	}
	if err := parseWireGuardCIDRs(addresses); err != nil {
		return err
	}

	privateKey := strings.TrimSpace(req.PrivateKey)
	if privateKey == "" {
		return ErrWireGuardClientPrivateKeyReq
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return err
	}

	if _, err := endpointToHostPort(req.EndpointHost, req.EndpointPort); err != nil {
		return err
	}

	peerPublicKey := strings.TrimSpace(req.PeerPublicKey)
	if _, err := wgtypes.ParseKey(peerPublicKey); err != nil {
		return fmt.Errorf("invalid_wireguard_peer_public_key: %w", err)
	}

	preSharedKey := ""
	if req.PreSharedKey != nil {
		preSharedKey = strings.TrimSpace(*req.PreSharedKey)
	}
	if err := validateWireGuardPSK(preSharedKey); err != nil {
		return err
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	listenPort := uint(0)
	if req.ListenPort != nil {
		listenPort = *req.ListenPort
	}

	routeAllowedIPs := true
	if req.RouteAllowedIPs != nil {
		routeAllowedIPs = *req.RouteAllowedIPs
	}

	mtu := uint(1420)
	if req.MTU != nil {
		mtu = *req.MTU
	}

	metric := uint(0)
	if req.Metric != nil {
		metric = *req.Metric
	}

	fib := uint(0)
	if req.FIB != nil {
		fib = *req.FIB
	}

	persistentKeepalive := false
	if req.PersistentKeepalive != nil {
		persistentKeepalive = *req.PersistentKeepalive
	}

	client := networkModels.WireGuardClient{
		Enabled:             enabled,
		Name:                strings.TrimSpace(req.Name),
		EndpointHost:        strings.TrimSpace(req.EndpointHost),
		EndpointPort:        req.EndpointPort,
		ListenPort:          listenPort,
		PrivateKey:          privateKey,
		PublicKey:           publicKey,
		PeerPublicKey:       peerPublicKey,
		PreSharedKey:        preSharedKey,
		AllowedIPs:          allowedIPs,
		Addresses:           addresses,
		RouteAllowedIPs:     routeAllowedIPs,
		MTU:                 mtu,
		Metric:              metric,
		FIB:                 fib,
		PersistentKeepalive: persistentKeepalive,
	}

	if err := s.DB.Create(&client).Error; err != nil {
		return err
	}

	if client.Enabled {
		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			_ = s.DB.Delete(&client).Error
			return err
		}
		return s.DB.Model(&client).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}

func (s *Service) EditWireGuardClient(req *WireGuardClientRequest) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if req.ID == nil {
		return ErrWireGuardClientNotFound
	}

	var client networkModels.WireGuardClient
	if err := s.DB.First(&client, *req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardClientNotFound
		}
		return err
	}

	privateKey := strings.TrimSpace(req.PrivateKey)
	if privateKey == "" {
		return ErrWireGuardClientPrivateKeyReq
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return err
	}

	client.PrivateKey = privateKey
	client.PublicKey = publicKey

	if strings.TrimSpace(req.Name) != "" {
		client.Name = strings.TrimSpace(req.Name)
	}
	if req.Enabled != nil {
		client.Enabled = *req.Enabled
	}

	if strings.TrimSpace(req.EndpointHost) != "" {
		client.EndpointHost = strings.TrimSpace(req.EndpointHost)
	}
	if req.EndpointPort > 0 {
		client.EndpointPort = req.EndpointPort
	}
	if _, err := endpointToHostPort(client.EndpointHost, client.EndpointPort); err != nil {
		return err
	}

	if req.ListenPort != nil {
		client.ListenPort = *req.ListenPort
	}

	if strings.TrimSpace(req.PeerPublicKey) != "" {
		peerPublicKey := strings.TrimSpace(req.PeerPublicKey)
		if _, err := wgtypes.ParseKey(peerPublicKey); err != nil {
			return fmt.Errorf("invalid_wireguard_peer_public_key: %w", err)
		}
		client.PeerPublicKey = peerPublicKey
	}

	if req.PreSharedKey != nil {
		preSharedKey := strings.TrimSpace(*req.PreSharedKey)
		if err := validateWireGuardPSK(preSharedKey); err != nil {
			return err
		}
		client.PreSharedKey = preSharedKey
	}

	if req.AllowedIPs != nil {
		allowedIPs := sortedUnique(req.AllowedIPs)
		if len(allowedIPs) == 0 {
			return ErrWireGuardAllowedIPsRequired
		}
		if err := parseWireGuardCIDRs(allowedIPs); err != nil {
			return err
		}
		client.AllowedIPs = allowedIPs
	}

	if req.Addresses != nil {
		addresses := sortedUnique(req.Addresses)
		if len(addresses) == 0 {
			return ErrWireGuardAddressesRequired
		}
		if err := parseWireGuardCIDRs(addresses); err != nil {
			return err
		}
		client.Addresses = addresses
	}

	if req.RouteAllowedIPs != nil {
		client.RouteAllowedIPs = *req.RouteAllowedIPs
	}
	if req.MTU != nil {
		client.MTU = *req.MTU
	}
	if req.Metric != nil {
		client.Metric = *req.Metric
	}
	if req.FIB != nil {
		client.FIB = *req.FIB
	}
	if req.PersistentKeepalive != nil {
		client.PersistentKeepalive = *req.PersistentKeepalive
	}

	if err := s.DB.Save(&client).Error; err != nil {
		return err
	}

	if client.Enabled {
		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			return err
		}
		return s.DB.Model(&client).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return s.teardownWireGuardClientRuntime(&client)
}

func (s *Service) ToggleWireGuardClient(id uint) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var client networkModels.WireGuardClient
	if err := s.DB.First(&client, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardClientNotFound
		}
		return err
	}

	client.Enabled = !client.Enabled
	if err := s.DB.Save(&client).Error; err != nil {
		return err
	}

	if client.Enabled {
		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			return err
		}
		return s.DB.Model(&client).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return s.teardownWireGuardClientRuntime(&client)
}

func (s *Service) DeleteWireGuardClient(id uint) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var client networkModels.WireGuardClient
	if err := s.DB.First(&client, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardClientNotFound
		}
		return err
	}

	if err := s.teardownWireGuardClientRuntime(&client); err != nil {
		return err
	}

	return s.DB.Delete(&client).Error
}

func buildWireGuardClientPeer(client *networkModels.WireGuardClient) (wgtypes.PeerConfig, error) {
	peerPublicKey, err := wgtypes.ParseKey(strings.TrimSpace(client.PeerPublicKey))
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("invalid_wireguard_peer_public_key: %w", err)
	}

	allowedIPs, err := parseAllowedIPs(client.AllowedIPs)
	if err != nil {
		return wgtypes.PeerConfig{}, err
	}

	endpointAddr, err := endpointToHostPort(client.EndpointHost, client.EndpointPort)
	if err != nil {
		return wgtypes.PeerConfig{}, err
	}

	resolvedEndpoint, err := wireGuardResolveUDP("udp", endpointAddr)
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("failed_to_resolve_wireguard_endpoint: %w", err)
	}

	peer := wgtypes.PeerConfig{
		PublicKey:         peerPublicKey,
		Endpoint:          resolvedEndpoint,
		AllowedIPs:        allowedIPs,
		ReplaceAllowedIPs: true,
	}

	if strings.TrimSpace(client.PreSharedKey) != "" {
		preSharedKey, err := wgtypes.ParseKey(strings.TrimSpace(client.PreSharedKey))
		if err != nil {
			return wgtypes.PeerConfig{}, fmt.Errorf("invalid_wireguard_preshared_key: %w", err)
		}
		peer.PresharedKey = &preSharedKey
	}

	if client.PersistentKeepalive {
		interval := 25 * time.Second
		peer.PersistentKeepaliveInterval = &interval
	}

	return peer, nil
}

func (s *Service) applyWireGuardClientRuntime(client *networkModels.WireGuardClient) (err error) {
	if client == nil {
		return nil
	}

	if !client.Enabled {
		return s.teardownWireGuardClientRuntime(client)
	}

	if err = parseWireGuardCIDRs(client.Addresses); err != nil {
		return err
	}

	if err = destroyWireGuardInterface(wireGuardClientInterfaceName(client.ID)); err != nil {
		return err
	}

	interfaceName := wireGuardClientInterfaceName(client.ID)
	if err = ensureWireGuardInterface(interfaceName); err != nil {
		return err
	}

	cleanupOnFailure := true
	defer func() {
		if err == nil || !cleanupOnFailure {
			return
		}
		if teardownErr := s.teardownWireGuardClientRuntime(client); teardownErr != nil {
			logger.L.Warn().Err(teardownErr).Uint("client_id", client.ID).Msg("failed to rollback wireguard client runtime after apply error")
		}
	}()

	if err = configureWireGuardInterface(interfaceName, client.Addresses, client.MTU, client.Metric, client.FIB); err != nil {
		return err
	}

	var peerConfig wgtypes.PeerConfig
	peerConfig, err = buildWireGuardClientPeer(client)
	if err != nil {
		return err
	}

	if err = configureWireGuardDevice(interfaceName, client.PrivateKey, client.ListenPort, []wgtypes.PeerConfig{peerConfig}); err != nil {
		return err
	}

	routeCIDRs := expandedWireGuardRouteCIDRs(client.AllowedIPs)
	for _, cidr := range routeCIDRs {
		_ = deleteRouteViaInterface(cidr, interfaceName, client.FIB)
	}

	if client.RouteAllowedIPs {
		if hasDefaultWireGuardRouteCIDR(client.AllowedIPs) &&
			peerConfig.Endpoint != nil &&
			peerConfig.Endpoint.IP != nil {
			if err = addEndpointHostRoute(peerConfig.Endpoint.IP.String(), client.FIB); err != nil {
				return err
			}
		}

		for _, cidr := range routeCIDRs {
			if err = addRouteViaInterface(cidr, interfaceName, client.FIB); err != nil {
				return err
			}
		}
	}

	cleanupOnFailure = false
	return nil
}

func (s *Service) teardownWireGuardClientRuntime(client *networkModels.WireGuardClient) error {
	if client == nil {
		return nil
	}

	interfaceName := wireGuardClientInterfaceName(client.ID)
	for _, cidr := range expandedWireGuardRouteCIDRs(client.AllowedIPs) {
		_ = deleteRouteViaInterface(cidr, interfaceName, client.FIB)
	}
	if hasDefaultWireGuardRouteCIDR(client.AllowedIPs) {
		if endpointIPs, err := resolveEndpointIPs(client.EndpointHost); err == nil {
			for _, endpointIP := range endpointIPs {
				_ = deleteEndpointHostRoute(endpointIP, client.FIB)
			}
		}
	}

	return destroyWireGuardInterface(interfaceName)
}

func resolveEndpointIPs(host string) ([]string, error) {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return nil, nil
	}

	if ip := net.ParseIP(trimmed); ip != nil {
		return []string{ip.String()}, nil
	}

	resolved, err := wireGuardLookupIP(trimmed)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(resolved))
	for _, ip := range resolved {
		out = append(out, ip.String())
	}

	return sortedUnique(out), nil
}
