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
	"net/netip"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

func (s *Service) GetWireGuardClients() ([]networkModels.WireGuardClient, error) {
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return nil, err
	}

	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		return nil, err
	}

	return clients, nil
}

func cloneWireGuardClient(client *networkModels.WireGuardClient) networkModels.WireGuardClient {
	if client == nil {
		return networkModels.WireGuardClient{}
	}

	cloned := *client
	cloned.AllowedIPs = append([]string(nil), client.AllowedIPs...)
	cloned.Addresses = append([]string(nil), client.Addresses...)
	return cloned
}

func normalizeWireGuardClientName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", invalidWireGuardClient("wireguard_client_name_required", nil)
	}
	if len(name) > MaxWireGuardClientNameBytes {
		return "", invalidWireGuardClient("wireguard_client_name_too_long", nil)
	}
	return name, nil
}

func normalizeWireGuardEndpointHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", ErrWireGuardEndpointHostRequired
	}
	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") || len(host) < 3 {
			return "", invalidWireGuardClient("invalid_wireguard_endpoint_host", nil)
		}
		host = host[1 : len(host)-1]
	}
	if len(host) > MaxWireGuardClientEndpointBytes {
		return "", invalidWireGuardClient("wireguard_endpoint_host_too_long", nil)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host, nil
	}

	domain := strings.TrimSuffix(host, ".")
	if domain == "" {
		return "", invalidWireGuardClient("invalid_wireguard_endpoint_host", nil)
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalidWireGuardClient("invalid_wireguard_endpoint_host", nil)
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') ||
				(char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') ||
				char == '-' {
				continue
			}
			return "", invalidWireGuardClient("invalid_wireguard_endpoint_host", nil)
		}
	}
	return host, nil
}

func normalizeWireGuardClientCIDRs(values []string, required error, tooManyCode string) ([]string, error) {
	if len(values) > MaxWireGuardClientCIDRs {
		return nil, invalidWireGuardClient(tooManyCode, nil)
	}

	normalized := sortedUnique(values)
	if len(normalized) == 0 {
		return nil, required
	}
	for _, cidr := range normalized {
		if len(cidr) > MaxWireGuardClientCIDRBytes {
			return nil, invalidWireGuardClient("wireguard_client_cidr_too_long", nil)
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, invalidWireGuardClient("invalid_wireguard_cidr", err)
		}
	}
	return normalized, nil
}

func normalizeWireGuardClient(client *networkModels.WireGuardClient) error {
	if client == nil {
		return invalidWireGuardClient("wireguard_client_required", nil)
	}

	name, err := normalizeWireGuardClientName(client.Name)
	if err != nil {
		return err
	}
	client.Name = name

	client.EndpointHost, err = normalizeWireGuardEndpointHost(client.EndpointHost)
	if err != nil {
		return err
	}
	if client.EndpointPort == 0 || client.EndpointPort > 65535 {
		return ErrWireGuardEndpointPortInvalid
	}
	if client.ListenPort > 65535 {
		return invalidWireGuardClient("invalid_wireguard_listen_port", nil)
	}
	if client.MTU == 0 {
		client.MTU = 1280
	} else if client.MTU < 576 || client.MTU > 9000 {
		return invalidWireGuardClient("invalid_wireguard_mtu", nil)
	}

	client.PrivateKey = strings.TrimSpace(client.PrivateKey)
	if client.PrivateKey == "" {
		return ErrWireGuardClientPrivateKeyReq
	}
	client.PublicKey, err = wireGuardPublicKeyFromPrivate(client.PrivateKey)
	if err != nil {
		return invalidWireGuardClient("invalid_wireguard_private_key", err)
	}

	client.PeerPublicKey = strings.TrimSpace(client.PeerPublicKey)
	if client.PeerPublicKey == "" {
		return ErrWireGuardPeerPublicKeyRequired
	}
	peerPublicKey, err := wgtypes.ParseKey(client.PeerPublicKey)
	if err != nil {
		return invalidWireGuardClient("invalid_wireguard_peer_public_key", err)
	}
	client.PeerPublicKey = peerPublicKey.String()

	client.PreSharedKey = strings.TrimSpace(client.PreSharedKey)
	if err := validateWireGuardPSK(client.PreSharedKey); err != nil {
		return invalidWireGuardClient("invalid_wireguard_psk", err)
	}

	client.AllowedIPs, err = normalizeWireGuardClientCIDRs(
		client.AllowedIPs,
		ErrWireGuardAllowedIPsRequired,
		"wireguard_client_too_many_allowed_ips",
	)
	if err != nil {
		return err
	}
	client.Addresses, err = normalizeWireGuardClientCIDRs(
		client.Addresses,
		ErrWireGuardAddressesRequired,
		"wireguard_client_too_many_addresses",
	)
	return err
}

func (s *Service) ensureWireGuardClientNameAvailable(name string, exceptID uint) error {
	query := s.DB.Model(&networkModels.WireGuardClient{}).Where("name = ?", name)
	if exceptID != 0 {
		query = query.Where("id <> ?", exceptID)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return wireGuardClientConflict("wireguard_client_name_conflict", nil)
	}
	return nil
}

func (s *Service) createWireGuardClient(client *networkModels.WireGuardClient) error {
	if client == nil {
		return invalidWireGuardClient("wireguard_client_required", nil)
	}

	enabled := client.Enabled
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(client).Error; err != nil {
			return err
		}
		if enabled {
			return nil
		}
		return tx.Model(&networkModels.WireGuardClient{}).
			Where("id = ?", client.ID).
			Update("enabled", false).Error
	}); err != nil {
		return err
	}
	client.Enabled = enabled
	return nil
}

func (s *Service) persistWireGuardClient(client *networkModels.WireGuardClient, includeOperationalState bool) error {
	if client == nil || client.ID == 0 {
		return ErrWireGuardClientNotFound
	}

	fields := []string{
		"Name",
		"EndpointHost",
		"EndpointPort",
		"ListenPort",
		"PrivateKey",
		"PublicKey",
		"PeerPublicKey",
		"PreSharedKey",
		"AllowedIPs",
		"Addresses",
		"RouteAllowedIPs",
		"MTU",
		"Metric",
		"FIB",
		"PersistentKeepalive",
	}
	if includeOperationalState {
		fields = append(fields, "Enabled", "RestartedAt")
	}

	return s.DB.Model(&networkModels.WireGuardClient{}).
		Where("id = ?", client.ID).
		Select(fields).
		Updates(client).Error
}

func (s *Service) loadWireGuardClient(id uint) (*networkModels.WireGuardClient, error) {
	var client networkModels.WireGuardClient
	if err := s.DB.First(&client, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWireGuardClientNotFound
		}
		return nil, err
	}
	return &client, nil
}

func (s *Service) restoreWireGuardClientMutation(
	previous *networkModels.WireGuardClient,
	current *networkModels.WireGuardClient,
	rollbackDatabase func() error,
) error {
	var rollbackErrors []error
	if current != nil {
		if err := s.teardownWireGuardClientRuntime(current); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove failed wireguard client runtime state: %w", err))
		}
	}
	if rollbackDatabase != nil {
		if err := rollbackDatabase(); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard client database state: %w", err))
		}
	}
	if previous != nil {
		if previous.Enabled {
			if err := s.applyWireGuardClientRuntime(previous); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard client runtime state: %w", err))
			}
		} else if err := s.teardownWireGuardClientRuntime(previous); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore disabled wireguard client runtime state: %w", err))
		}
	}
	s.flushWireGuardMetricsOnConfigChange()
	return errors.Join(rollbackErrors...)
}

func (s *Service) CreateWireGuardClient(req *WireGuardClientRequest) (uint, error) {
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return 0, err
	}
	if req == nil {
		return 0, invalidWireGuardClient("wireguard_client_required", nil)
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

	mtu := uint(1280)
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
		Name:                req.Name,
		EndpointHost:        req.EndpointHost,
		EndpointPort:        req.EndpointPort,
		ListenPort:          listenPort,
		PrivateKey:          req.PrivateKey,
		PeerPublicKey:       req.PeerPublicKey,
		AllowedIPs:          req.AllowedIPs,
		Addresses:           req.Addresses,
		RouteAllowedIPs:     routeAllowedIPs,
		MTU:                 mtu,
		Metric:              metric,
		FIB:                 fib,
		PersistentKeepalive: persistentKeepalive,
	}
	if req.PreSharedKey != nil {
		client.PreSharedKey = *req.PreSharedKey
	}
	if err := normalizeWireGuardClient(&client); err != nil {
		return 0, err
	}
	if err := s.ensureWireGuardClientNameAvailable(client.Name, 0); err != nil {
		return 0, err
	}

	if err := s.createWireGuardClient(&client); err != nil {
		return 0, err
	}
	rollbackDatabase := func() error {
		return s.DB.Delete(&networkModels.WireGuardClient{}, client.ID).Error
	}

	if client.Enabled {
		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			return 0, errors.Join(err, s.restoreWireGuardClientMutation(nil, &client, rollbackDatabase))
		}
		restartedAt := wireGuardCurrentTime()
		if err := s.DB.Model(&client).Update("restarted_at", restartedAt).Error; err != nil {
			return 0, errors.Join(err, s.restoreWireGuardClientMutation(nil, &client, rollbackDatabase))
		}
		client.RestartedAt = restartedAt
		s.flushWireGuardMetricsOnConfigChange()
		return client.ID, nil
	}

	s.flushWireGuardMetricsOnConfigChange()
	return client.ID, nil
}

func (s *Service) EditWireGuardClient(req *WireGuardClientRequest) error {
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if req == nil || req.ID == nil {
		return ErrWireGuardClientNotFound
	}

	stored, err := s.loadWireGuardClient(*req.ID)
	if err != nil {
		return err
	}
	previous := cloneWireGuardClient(stored)
	client := cloneWireGuardClient(stored)

	privateKey := strings.TrimSpace(req.PrivateKey)
	if privateKey == "" {
		return ErrWireGuardClientPrivateKeyReq
	}
	client.PrivateKey = privateKey

	if strings.TrimSpace(req.Name) != "" {
		client.Name = strings.TrimSpace(req.Name)
	}

	if strings.TrimSpace(req.EndpointHost) != "" {
		client.EndpointHost = strings.TrimSpace(req.EndpointHost)
	}
	if req.EndpointPort > 0 {
		client.EndpointPort = req.EndpointPort
	}
	if req.ListenPort != nil {
		client.ListenPort = *req.ListenPort
	}

	if strings.TrimSpace(req.PeerPublicKey) != "" {
		client.PeerPublicKey = strings.TrimSpace(req.PeerPublicKey)
	}

	if req.PreSharedKey != nil {
		client.PreSharedKey = strings.TrimSpace(*req.PreSharedKey)
	}

	if req.AllowedIPs != nil {
		client.AllowedIPs = req.AllowedIPs
	}

	if req.Addresses != nil {
		client.Addresses = req.Addresses
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

	if err := normalizeWireGuardClient(&client); err != nil {
		return err
	}
	if err := s.ensureWireGuardClientNameAvailable(client.Name, client.ID); err != nil {
		return err
	}
	if err := s.persistWireGuardClient(&client, false); err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.persistWireGuardClient(&previous, true)
	}

	if !client.Enabled {
		s.flushWireGuardMetricsOnConfigChange()
		return nil
	}

	if err := s.teardownWireGuardClientRuntime(&previous); err != nil {
		return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
	}
	if err := s.applyWireGuardClientRuntime(&client); err != nil {
		return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
	}
	restartedAt := wireGuardCurrentTime()
	if err := s.DB.Model(&client).Update("restarted_at", restartedAt).Error; err != nil {
		return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
	}
	client.RestartedAt = restartedAt
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) SetWireGuardClientEnabled(id uint, enabled bool) error {
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	stored, err := s.loadWireGuardClient(id)
	if err != nil {
		return err
	}
	previous := cloneWireGuardClient(stored)
	if previous.Enabled == enabled {
		return nil
	}

	client := cloneWireGuardClient(stored)
	client.Enabled = enabled
	if err := normalizeWireGuardClient(&client); err != nil {
		return err
	}
	if err := s.DB.Model(&networkModels.WireGuardClient{}).
		Where("id = ?", client.ID).
		Update("enabled", enabled).Error; err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.persistWireGuardClient(&previous, true)
	}

	if client.Enabled {
		if err := s.applyWireGuardClientRuntime(&client); err != nil {
			return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
		}
		restartedAt := wireGuardCurrentTime()
		if err := s.DB.Model(&client).Update("restarted_at", restartedAt).Error; err != nil {
			return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
		}
		client.RestartedAt = restartedAt
		s.flushWireGuardMetricsOnConfigChange()
		return nil
	}

	if err := s.teardownWireGuardClientRuntime(&previous); err != nil {
		return errors.Join(err, s.restoreWireGuardClientMutation(&previous, &client, rollbackDatabase))
	}
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) DeleteWireGuardClient(id uint) error {
	s.wireGuardClientMutationMutex.Lock()
	defer s.wireGuardClientMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	stored, err := s.loadWireGuardClient(id)
	if err != nil {
		return err
	}
	client := cloneWireGuardClient(stored)

	if err := s.DB.Delete(&networkModels.WireGuardClient{}, client.ID).Error; err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.createWireGuardClient(&client)
	}
	if err := s.teardownWireGuardClientRuntime(&client); err != nil {
		return errors.Join(err, s.restoreWireGuardClientMutation(&client, &client, rollbackDatabase))
	}
	s.flushWireGuardMetricsOnConfigChange()
	return nil
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
	if client.RouteAllowedIPs && hasDefaultWireGuardRouteCIDR(client.AllowedIPs) {
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
