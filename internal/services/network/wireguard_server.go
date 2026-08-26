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
	"github.com/alchemillahq/sylve/pkg/utils"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

const (
	wireGuardManagedAllowRuleName  = "Allow WireGuard"
	wireGuardManagedMasqV4RuleName = "Masquerade WG"
	wireGuardManagedMasqV6RuleName = "Masquerade WG v6"
)

type wireGuardFirewallSnapshot struct {
	Traffic []networkModels.FirewallTrafficRule
	NAT     []networkModels.FirewallNATRule
}

func (s *Service) isWireGuardUDPPortInUse(port int) bool {
	if s.wireGuardUDPPortInUse != nil {
		return s.wireGuardUDPPortInUse(port)
	}
	return utils.IsUDPPortInUse(port)
}

func normalizeWireGuardManagedInterface(value string) string {
	return strings.TrimSpace(value)
}

func cloneWireGuardServer(server *networkModels.WireGuardServer) networkModels.WireGuardServer {
	if server == nil {
		return networkModels.WireGuardServer{}
	}

	cloned := *server
	cloned.Addresses = append([]string(nil), server.Addresses...)
	cloned.Peers = append([]networkModels.WireGuardServerPeer(nil), server.Peers...)
	for i := range cloned.Peers {
		cloned.Peers[i].ClientIPs = append([]string(nil), server.Peers[i].ClientIPs...)
		cloned.Peers[i].RoutableIPs = append([]string(nil), server.Peers[i].RoutableIPs...)
	}
	return cloned
}

func (s *Service) persistWireGuardServerConfig(server *networkModels.WireGuardServer) error {
	if server == nil || server.ID == 0 {
		return fmt.Errorf("invalid wireguard server persistence target")
	}
	return s.DB.Model(&networkModels.WireGuardServer{}).
		Where("id = ?", server.ID).
		Select(
			"Enabled",
			"Port",
			"Addresses",
			"AllowWireGuardPort",
			"MasqueradeIPv4Interface",
			"MasqueradeIPv6Interface",
			"PrivateKey",
			"PublicKey",
			"MTU",
			"Metric",
			"RestartedAt",
		).
		Updates(server).Error
}

func (s *Service) validateWireGuardServerConfig(server *networkModels.WireGuardServer) error {
	if server == nil {
		return invalidWireGuardServer("wireguard_server_required", nil)
	}
	if server.Port == 0 || server.Port > 65535 {
		return invalidWireGuardServer("wireguard_invalid_port", nil)
	}
	if len(server.Addresses) == 0 {
		return invalidWireGuardServer("wireguard_addresses_required", nil)
	}
	if server.MTU < 576 || server.MTU > 9000 {
		return invalidWireGuardServer("wireguard_invalid_mtu", nil)
	}
	if _, err := wgtypes.ParseKey(strings.TrimSpace(server.PrivateKey)); err != nil {
		return invalidWireGuardServer("wireguard_invalid_private_key", err)
	}

	hasIPv4 := false
	hasIPv6 := false
	for _, address := range server.Addresses {
		ip, _, err := net.ParseCIDR(strings.TrimSpace(address))
		if err != nil {
			return invalidWireGuardServer("wireguard_invalid_address", err)
		}
		if ip.To4() == nil {
			hasIPv6 = true
		} else {
			hasIPv4 = true
		}
	}
	if hasIPv6 && server.MTU < 1280 {
		return invalidWireGuardServer("wireguard_ipv6_mtu_too_small", nil)
	}

	v4Iface := normalizeWireGuardManagedInterface(server.MasqueradeIPv4Interface)
	v6Iface := normalizeWireGuardManagedInterface(server.MasqueradeIPv6Interface)
	if v4Iface != "" && !hasIPv4 {
		return invalidWireGuardServer("wireguard_masquerade_ipv4_requires_server_ipv4_cidr", nil)
	}
	if v6Iface != "" && !hasIPv6 {
		return invalidWireGuardServer("wireguard_masquerade_ipv6_requires_server_ipv6_cidr", nil)
	}
	for _, iface := range []string{v4Iface, v6Iface} {
		if iface == "" {
			continue
		}
		if len(iface) > MaxFirewallNATRuleInterfaceBytes || !firewallInterfaceNamePattern.MatchString(iface) {
			return invalidWireGuardServer("wireguard_invalid_masquerade_interface", nil)
		}
		if iface == wireGuardServerInterfaceName {
			return invalidWireGuardServer("wireguard_masquerade_interface_cannot_be_server", nil)
		}
	}
	if v4Iface == "" && v6Iface == "" {
		return nil
	}

	interfaces, err := wireGuardListInterfaces()
	if err != nil {
		return fmt.Errorf("failed to list interfaces for wireguard validation: %w", err)
	}
	existing := make(map[string]struct{}, len(interfaces))
	for _, iface := range interfaces {
		existing[strings.TrimSpace(iface.Name)] = struct{}{}
	}
	for _, iface := range []string{v4Iface, v6Iface} {
		if iface == "" {
			continue
		}
		if _, ok := existing[iface]; !ok {
			return invalidWireGuardServer("wireguard_masquerade_interface_not_found", nil)
		}
	}

	return nil
}

func wireGuardCIDRNetworkByFamily(cidrs []string, wantV6 bool) string {
	for _, cidr := range cidrs {
		trimmed := strings.TrimSpace(cidr)
		if trimmed == "" {
			continue
		}
		_, network, err := net.ParseCIDR(trimmed)
		if err != nil || network == nil {
			continue
		}
		isV6 := network.IP.To4() == nil
		if isV6 == wantV6 {
			return network.String()
		}
	}
	return ""
}

func (s *Service) snapshotWireGuardFirewallState() (*wireGuardFirewallSnapshot, error) {
	out := &wireGuardFirewallSnapshot{}
	if err := s.DB.Order("id ASC").Find(&out.Traffic).Error; err != nil {
		return nil, err
	}
	if err := s.DB.Order("id ASC").Find(&out.NAT).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) reconcileManagedWireGuardTrafficRule(tx *gorm.DB, server *networkModels.WireGuardServer) error {
	var existing []networkModels.FirewallTrafficRule
	if err := tx.Where("visible = ? AND name = ?", false, wireGuardManagedAllowRuleName).Order("id ASC").Find(&existing).Error; err != nil {
		return err
	}

	if !server.AllowWireGuardPort {
		if len(existing) > 0 {
			ids := make([]uint, 0, len(existing))
			for _, row := range existing {
				ids = append(ids, row.ID)
			}
			if err := tx.Where("id IN ?", ids).Delete(&networkModels.FirewallTrafficRule{}).Error; err != nil {
				return err
			}
		}
		return nil
	}

	rule := networkModels.FirewallTrafficRule{
		Name:              wireGuardManagedAllowRuleName,
		Description:       "",
		Visible:           false,
		Enabled:           true,
		Log:               false,
		Quick:             true,
		Priority:          1,
		Action:            "pass",
		Direction:         "in",
		Protocol:          "udp",
		IngressInterfaces: []string{},
		EgressInterfaces:  []string{},
		Family:            "any",
		SourceRaw:         "",
		SourceObjID:       nil,
		DestRaw:           "",
		DestObjID:         nil,
		SrcPortsRaw:       "",
		SrcPortObjID:      nil,
		DstPortsRaw:       fmt.Sprintf("%d", server.Port),
		DstPortObjID:      nil,
	}

	if len(existing) == 0 {
		if err := s.shiftTrafficRulesDownFrom(tx, 1, 0); err != nil {
			return err
		}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		return tx.Model(&rule).Update("visible", false).Error
	}

	current := existing[0]
	if len(existing) > 1 {
		extraIDs := make([]uint, 0, len(existing)-1)
		for _, row := range existing[1:] {
			extraIDs = append(extraIDs, row.ID)
		}
		if err := tx.Where("id IN ?", extraIDs).Delete(&networkModels.FirewallTrafficRule{}).Error; err != nil {
			return err
		}
	}

	if err := s.moveTrafficRulePriority(tx, current.ID, current.Priority, 1); err != nil {
		return err
	}
	current.Name = rule.Name
	current.Description = rule.Description
	current.Visible = rule.Visible
	current.Enabled = rule.Enabled
	current.Log = rule.Log
	current.Quick = rule.Quick
	current.Priority = 1
	current.Action = rule.Action
	current.Direction = rule.Direction
	current.Protocol = rule.Protocol
	current.IngressInterfaces = rule.IngressInterfaces
	current.EgressInterfaces = rule.EgressInterfaces
	current.Family = rule.Family
	current.SourceRaw = rule.SourceRaw
	current.SourceObjID = rule.SourceObjID
	current.DestRaw = rule.DestRaw
	current.DestObjID = rule.DestObjID
	current.SrcPortsRaw = rule.SrcPortsRaw
	current.SrcPortObjID = rule.SrcPortObjID
	current.DstPortsRaw = rule.DstPortsRaw
	current.DstPortObjID = rule.DstPortObjID
	return tx.Save(&current).Error
}

func (s *Service) upsertManagedWireGuardNATRule(
	tx *gorm.DB,
	name string,
	priority int,
	sourceCIDR string,
	egressInterface string,
) error {
	var existing []networkModels.FirewallNATRule
	if err := tx.Where("visible = ? AND name = ?", false, name).Order("id ASC").Find(&existing).Error; err != nil {
		return err
	}

	rule := networkModels.FirewallNATRule{
		Name:                 name,
		Description:          "",
		Visible:              false,
		Enabled:              true,
		Log:                  false,
		Priority:             priority,
		NATType:              "snat",
		PolicyRoutingEnabled: false,
		PolicyRouteGateway:   "",
		IngressInterfaces:    []string{},
		EgressInterfaces:     []string{egressInterface},
		Family:               "any",
		Protocol:             "any",
		SourceRaw:            sourceCIDR,
		SourceObjID:          nil,
		DestRaw:              "",
		DestObjID:            nil,
		TranslateMode:        "interface",
		TranslateToRaw:       "",
		TranslateToObjID:     nil,
		DNATTargetRaw:        "",
		DNATTargetObjID:      nil,
		DstPortsRaw:          "",
		DstPortObjID:         nil,
		RedirectPortsRaw:     "",
		RedirectPortObjID:    nil,
	}

	if len(existing) == 0 {
		if err := s.shiftNATRulesDownFrom(tx, priority, 0); err != nil {
			return err
		}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		return tx.Model(&rule).Update("visible", false).Error
	}

	current := existing[0]
	if len(existing) > 1 {
		extraIDs := make([]uint, 0, len(existing)-1)
		for _, row := range existing[1:] {
			extraIDs = append(extraIDs, row.ID)
		}
		if err := tx.Where("id IN ?", extraIDs).Delete(&networkModels.FirewallNATRule{}).Error; err != nil {
			return err
		}
	}

	if err := s.moveNATRulePriority(tx, current.ID, current.Priority, priority); err != nil {
		return err
	}
	current.Name = rule.Name
	current.Description = rule.Description
	current.Visible = rule.Visible
	current.Enabled = rule.Enabled
	current.Log = rule.Log
	current.Priority = rule.Priority
	current.NATType = rule.NATType
	current.PolicyRoutingEnabled = rule.PolicyRoutingEnabled
	current.PolicyRouteGateway = rule.PolicyRouteGateway
	current.IngressInterfaces = rule.IngressInterfaces
	current.EgressInterfaces = rule.EgressInterfaces
	current.Family = rule.Family
	current.Protocol = rule.Protocol
	current.SourceRaw = rule.SourceRaw
	current.SourceObjID = rule.SourceObjID
	current.DestRaw = rule.DestRaw
	current.DestObjID = rule.DestObjID
	current.TranslateMode = rule.TranslateMode
	current.TranslateToRaw = rule.TranslateToRaw
	current.TranslateToObjID = rule.TranslateToObjID
	current.DNATTargetRaw = rule.DNATTargetRaw
	current.DNATTargetObjID = rule.DNATTargetObjID
	current.DstPortsRaw = rule.DstPortsRaw
	current.DstPortObjID = rule.DstPortObjID
	current.RedirectPortsRaw = rule.RedirectPortsRaw
	current.RedirectPortObjID = rule.RedirectPortObjID
	return tx.Save(&current).Error
}

func (s *Service) deleteManagedWireGuardNATRule(tx *gorm.DB, name string) error {
	return tx.Where("visible = ? AND name = ?", false, name).Delete(&networkModels.FirewallNATRule{}).Error
}

func (s *Service) reconcileWireGuardManagedFirewallRows(server *networkModels.WireGuardServer, active bool) error {
	desired := cloneWireGuardServer(server)
	if !active {
		desired.AllowWireGuardPort = false
		desired.MasqueradeIPv4Interface = ""
		desired.MasqueradeIPv6Interface = ""
	}

	v4Iface := normalizeWireGuardManagedInterface(desired.MasqueradeIPv4Interface)
	v6Iface := normalizeWireGuardManagedInterface(desired.MasqueradeIPv6Interface)
	v4CIDR := wireGuardCIDRNetworkByFamily(desired.Addresses, false)
	v6CIDR := wireGuardCIDRNetworkByFamily(desired.Addresses, true)

	if v4Iface != "" && v4CIDR == "" {
		return fmt.Errorf("wireguard_masquerade_ipv4_requires_server_ipv4_cidr")
	}
	if v6Iface != "" && v6CIDR == "" {
		return fmt.Errorf("wireguard_masquerade_ipv6_requires_server_ipv6_cidr")
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if syncErr := s.reconcileManagedWireGuardTrafficRule(tx, &desired); syncErr != nil {
			return syncErr
		}

		nextPriority := 1
		if v4Iface != "" {
			if upsertErr := s.upsertManagedWireGuardNATRule(tx, wireGuardManagedMasqV4RuleName, nextPriority, v4CIDR, v4Iface); upsertErr != nil {
				return upsertErr
			}
			nextPriority++
		} else if delErr := s.deleteManagedWireGuardNATRule(tx, wireGuardManagedMasqV4RuleName); delErr != nil {
			return delErr
		}

		if v6Iface != "" {
			if upsertErr := s.upsertManagedWireGuardNATRule(tx, wireGuardManagedMasqV6RuleName, nextPriority, v6CIDR, v6Iface); upsertErr != nil {
				return upsertErr
			}
		} else if delErr := s.deleteManagedWireGuardNATRule(tx, wireGuardManagedMasqV6RuleName); delErr != nil {
			return delErr
		}

		return nil
	})
}

func (s *Service) syncWireGuardManagedFirewallRules(server *networkModels.WireGuardServer, active bool) error {
	s.firewallTrafficMutationMutex.Lock()
	defer s.firewallTrafficMutationMutex.Unlock()
	s.firewallNATMutationMutex.Lock()
	defer s.firewallNATMutationMutex.Unlock()

	snapshot, err := s.snapshotWireGuardFirewallState()
	if err != nil {
		return err
	}
	if err := s.reconcileWireGuardManagedFirewallRows(server, active); err != nil {
		return err
	}

	if err := s.ApplyFirewallIfEnabled(); err != nil {
		rollbackErr := s.DB.Transaction(func(tx *gorm.DB) error {
			if restoreErr := restoreFirewallTrafficRules(tx, snapshot.Traffic); restoreErr != nil {
				return restoreErr
			}
			return restoreFirewallNATRules(tx, snapshot.NAT)
		})
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("wireguard firewall database rollback failed: %w", rollbackErr))
		}
		if reapplyErr := s.ApplyFirewallIfEnabled(); reapplyErr != nil {
			return errors.Join(err, fmt.Errorf("wireguard firewall runtime rollback failed: %w", reapplyErr))
		}
		return err
	}

	return nil
}

func (s *Service) reconcileWireGuardManagedFirewallRowsForCurrentState() error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	serviceEnabled, err := s.wireGuardServiceState()
	if err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	err = s.DB.First(&server).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	s.firewallTrafficMutationMutex.Lock()
	defer s.firewallTrafficMutationMutex.Unlock()
	s.firewallNATMutationMutex.Lock()
	defer s.firewallNATMutationMutex.Unlock()

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.reconcileWireGuardManagedFirewallRows(nil, false)
	}
	return s.reconcileWireGuardManagedFirewallRows(&server, serviceEnabled && server.Enabled)
}

func (s *Service) restoreWireGuardServerOperationalState(snapshot *networkModels.WireGuardServer) error {
	if snapshot == nil || snapshot.ID == 0 {
		return nil
	}

	var rollbackErrors []error
	if err := s.persistWireGuardServerConfig(snapshot); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard server database state: %w", err))
	}
	if err := s.applyWireGuardServerRuntime(snapshot); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard server runtime state: %w", err))
	}
	if err := s.syncWireGuardManagedFirewallRules(snapshot, snapshot.Enabled); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard server firewall state: %w", err))
	}
	return errors.Join(rollbackErrors...)
}

func (s *Service) rollbackWireGuardServerInitialization(server *networkModels.WireGuardServer) error {
	if server == nil || server.ID == 0 {
		return nil
	}

	var rollbackErrors []error
	if err := s.syncWireGuardManagedFirewallRules(server, false); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("remove initialized wireguard firewall state: %w", err))
	}
	if err := s.teardownWireGuardServerRuntime(server); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("remove initialized wireguard runtime state: %w", err))
	}
	if err := s.DB.Delete(&networkModels.WireGuardServer{}, server.ID).Error; err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("remove initialized wireguard database state: %w", err))
	}
	return errors.Join(rollbackErrors...)
}

func (s *Service) GetWireGuardServer() (*networkModels.WireGuardServer, error) {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return nil, err
	}

	var server networkModels.WireGuardServer
	err := s.DB.Preload("Peers").First(&server).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWireGuardServerNotInited
	}
	if err != nil {
		return nil, err
	}

	return &server, nil
}

func (s *Service) InitWireGuardServer(req *InitWireGuardServerRequest) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}
	if req == nil {
		return invalidWireGuardServer("wireguard_server_request_required", nil)
	}

	var existing networkModels.WireGuardServer
	err := s.DB.First(&existing).Error
	if err == nil {
		return ErrWireGuardServerAlreadyInited
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	privateKey, err := wireGuardGeneratePrivateKey()
	if err != nil {
		return err
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return invalidWireGuardServer("wireguard_invalid_private_key", parseErr)
		}
		privateKey = provided
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return err
	}

	addresses := sortedUnique(req.Addresses)
	if len(addresses) == 0 {
		addresses = []string{"10.210.0.1/24"}
	}
	mtu := uint(1420)
	if req.MTU != nil {
		mtu = *req.MTU
	}

	server := networkModels.WireGuardServer{
		Enabled:                 true,
		Port:                    req.Port,
		Addresses:               addresses,
		PrivateKey:              privateKey,
		PublicKey:               publicKey,
		MTU:                     mtu,
		AllowWireGuardPort:      req.AllowWireGuardPort,
		MasqueradeIPv4Interface: normalizeWireGuardManagedInterface(req.MasqueradeIPv4Interface),
		MasqueradeIPv6Interface: normalizeWireGuardManagedInterface(req.MasqueradeIPv6Interface),
	}
	if err := s.validateWireGuardServerConfig(&server); err != nil {
		return err
	}
	if s.isWireGuardUDPPortInUse(int(server.Port)) {
		return wireGuardServerConflict("wireguard_port_already_in_use", nil)
	}

	if err := s.DB.Create(&server).Error; err != nil {
		return err
	}

	if err := s.applyWireGuardServerRuntime(&server); err != nil {
		return errors.Join(err, s.rollbackWireGuardServerInitialization(&server))
	}

	if err := s.syncWireGuardManagedFirewallRules(&server, true); err != nil {
		return errors.Join(err, s.rollbackWireGuardServerInitialization(&server))
	}

	restartedAt := wireGuardCurrentTime()
	if err := s.DB.Model(&server).Update("restarted_at", restartedAt).Error; err != nil {
		return errors.Join(err, s.rollbackWireGuardServerInitialization(&server))
	}
	server.RestartedAt = restartedAt
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) EditWireGuardServer(req InitWireGuardServerRequest) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerNotInited
		}
		return err
	}
	previous := cloneWireGuardServer(&server)

	privateKey := server.PrivateKey
	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return invalidWireGuardServer("wireguard_invalid_private_key", parseErr)
		}
		privateKey = provided
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return err
	}

	addresses := server.Addresses
	if len(req.Addresses) > 0 {
		addresses = sortedUnique(req.Addresses)
	}
	mtu := server.MTU
	if req.MTU != nil {
		mtu = *req.MTU
	}

	server.Port = req.Port
	server.Addresses = addresses
	server.PrivateKey = privateKey
	server.PublicKey = publicKey
	server.MTU = mtu
	server.AllowWireGuardPort = req.AllowWireGuardPort
	server.MasqueradeIPv4Interface = normalizeWireGuardManagedInterface(req.MasqueradeIPv4Interface)
	server.MasqueradeIPv6Interface = normalizeWireGuardManagedInterface(req.MasqueradeIPv6Interface)
	if err := s.validateWireGuardServerConfig(&server); err != nil {
		return err
	}
	if server.Port != previous.Port && s.isWireGuardUDPPortInUse(int(server.Port)) {
		return wireGuardServerConflict("wireguard_port_already_in_use", nil)
	}

	if err := s.persistWireGuardServerConfig(&server); err != nil {
		return err
	}

	if err := s.applyWireGuardServerRuntime(&server); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}
	if err := s.syncWireGuardManagedFirewallRules(&server, server.Enabled); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}
	if server.Enabled {
		restartedAt := wireGuardCurrentTime()
		if err := s.DB.Model(&server).Update("restarted_at", restartedAt).Error; err != nil {
			return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
		}
		server.RestartedAt = restartedAt
	}

	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) SetWireGuardServerEnabled(enabled bool) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerNotInited
		}
		return err
	}
	if server.Enabled == enabled {
		return nil
	}
	previous := cloneWireGuardServer(&server)

	server.Enabled = enabled
	if enabled {
		if err := s.validateWireGuardServerConfig(&server); err != nil {
			return err
		}
		if s.isWireGuardUDPPortInUse(int(server.Port)) {
			return wireGuardServerConflict("wireguard_port_already_in_use", nil)
		}
	}
	if err := s.DB.Model(&networkModels.WireGuardServer{}).
		Where("id = ?", server.ID).
		Update("enabled", enabled).Error; err != nil {
		return err
	}

	if enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
		}
		if err := s.syncWireGuardManagedFirewallRules(&server, true); err != nil {
			return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
		}
		restartedAt := wireGuardCurrentTime()
		if err := s.DB.Model(&server).Update("restarted_at", restartedAt).Error; err != nil {
			return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
		}
		server.RestartedAt = restartedAt
		s.flushWireGuardMetricsOnConfigChange()
		return nil
	}

	if err := s.syncWireGuardManagedFirewallRules(&server, false); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}
	if err := s.teardownWireGuardServerRuntime(&server); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) DeinitWireGuardServer() error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	previous := cloneWireGuardServer(&server)

	if err := s.syncWireGuardManagedFirewallRules(&server, false); err != nil {
		return err
	}
	if err := s.teardownWireGuardServerRuntime(&server); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if deleteErr := tx.Where("wire_guard_server_id = ?", server.ID).Delete(&networkModels.WireGuardServerPeer{}).Error; deleteErr != nil {
			return deleteErr
		}
		return tx.Delete(&networkModels.WireGuardServer{}, server.ID).Error
	}); err != nil {
		return errors.Join(err, s.restoreWireGuardServerOperationalState(&previous))
	}
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func cloneWireGuardServerPeer(peer *networkModels.WireGuardServerPeer) networkModels.WireGuardServerPeer {
	if peer == nil {
		return networkModels.WireGuardServerPeer{}
	}

	cloned := *peer
	cloned.ClientIPs = append([]string(nil), peer.ClientIPs...)
	cloned.RoutableIPs = append([]string(nil), peer.RoutableIPs...)
	return cloned
}

func normalizeWireGuardServerPeerName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", invalidWireGuardServer("wireguard_peer_name_required", nil)
	}
	if len(name) > MaxWireGuardServerPeerNameBytes {
		return "", invalidWireGuardServer("wireguard_peer_name_too_long", nil)
	}
	return name, nil
}

func normalizeWireGuardServerPeerCIDRs(values []string, required bool, tooManyCode string) ([]string, error) {
	if len(values) > MaxWireGuardServerPeerCIDRs {
		return nil, invalidWireGuardServer(tooManyCode, nil)
	}

	normalized := sortedUnique(values)
	if required && len(normalized) == 0 {
		return nil, invalidWireGuardServer("wireguard_peer_client_ips_required", nil)
	}
	for _, cidr := range normalized {
		if len(cidr) > MaxWireGuardServerPeerCIDRBytes {
			return nil, invalidWireGuardServer("wireguard_peer_cidr_too_long", nil)
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, invalidWireGuardServer("wireguard_invalid_peer_cidr", err)
		}
	}
	return normalized, nil
}

func wireGuardServerPeerAllowedNetworks(peer *networkModels.WireGuardServerPeer) (map[string]struct{}, error) {
	networks := make(map[string]struct{}, len(peer.ClientIPs)+len(peer.RoutableIPs))
	values := append(append([]string{}, peer.ClientIPs...), peer.RoutableIPs...)
	for _, cidr := range values {
		_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return nil, err
		}
		networks[network.String()] = struct{}{}
	}
	return networks, nil
}

func validateWireGuardServerPeerCandidate(candidate *networkModels.WireGuardServerPeer, peers []networkModels.WireGuardServerPeer) error {
	if candidate == nil {
		return invalidWireGuardServer("wireguard_peer_required", nil)
	}
	if _, err := normalizeWireGuardServerPeerName(candidate.Name); err != nil {
		return err
	}
	if _, err := normalizeWireGuardServerPeerCIDRs(
		candidate.ClientIPs,
		true,
		"wireguard_peer_too_many_client_ips",
	); err != nil {
		return err
	}
	if _, err := normalizeWireGuardServerPeerCIDRs(
		candidate.RoutableIPs,
		false,
		"wireguard_peer_too_many_routable_ips",
	); err != nil {
		return err
	}

	if _, err := wgtypes.ParseKey(strings.TrimSpace(candidate.PrivateKey)); err != nil {
		return invalidWireGuardServer("wireguard_invalid_peer_private_key", err)
	}
	candidatePublicKey, err := wgtypes.ParseKey(strings.TrimSpace(candidate.PublicKey))
	if err != nil {
		return invalidWireGuardServer("wireguard_invalid_peer_public_key", err)
	}
	if err := validateWireGuardPSK(candidate.PreSharedKey); err != nil {
		return invalidWireGuardServer("wireguard_invalid_peer_preshared_key", err)
	}

	var candidateNetworks map[string]struct{}
	if candidate.Enabled {
		candidateNetworks, err = wireGuardServerPeerAllowedNetworks(candidate)
		if err != nil {
			return invalidWireGuardServer("wireguard_invalid_peer_cidr", err)
		}
	}

	for i := range peers {
		existing := &peers[i]
		if existing.ID == candidate.ID {
			continue
		}

		existingPublicKey, parseErr := wgtypes.ParseKey(strings.TrimSpace(existing.PublicKey))
		if parseErr != nil {
			return fmt.Errorf("invalid stored wireguard peer public key %d: %w", existing.ID, parseErr)
		}
		if existingPublicKey.String() == candidatePublicKey.String() {
			return wireGuardServerConflict("wireguard_peer_public_key_conflict", nil)
		}

		if !candidate.Enabled || !existing.Enabled {
			continue
		}
		existingNetworks, networkErr := wireGuardServerPeerAllowedNetworks(existing)
		if networkErr != nil {
			return fmt.Errorf("invalid stored wireguard peer CIDR %d: %w", existing.ID, networkErr)
		}
		for network := range candidateNetworks {
			if _, exists := existingNetworks[network]; exists {
				return wireGuardServerConflict("wireguard_peer_allowed_ip_conflict", nil)
			}
		}
	}

	return nil
}

func (s *Service) persistWireGuardServerPeerConfig(peer *networkModels.WireGuardServerPeer) error {
	if peer == nil || peer.ID == 0 {
		return fmt.Errorf("invalid wireguard server peer persistence target")
	}
	return s.DB.Model(&networkModels.WireGuardServerPeer{}).
		Where("id = ?", peer.ID).
		Select(
			"Name",
			"Enabled",
			"PrivateKey",
			"PublicKey",
			"PreSharedKey",
			"ClientIPs",
			"RoutableIPs",
			"RouteIPs",
			"PersistentKeepalive",
		).
		Updates(peer).Error
}

func (s *Service) createWireGuardServerPeer(peer *networkModels.WireGuardServerPeer) error {
	if peer == nil {
		return fmt.Errorf("invalid wireguard server peer creation target")
	}

	enabled := peer.Enabled
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(peer).Error; err != nil {
			return err
		}
		if enabled {
			return nil
		}
		return tx.Model(&networkModels.WireGuardServerPeer{}).
			Where("id = ?", peer.ID).
			Update("enabled", false).Error
	}); err != nil {
		return err
	}
	peer.Enabled = enabled
	return nil
}

func (s *Service) loadWireGuardServerPeer(id uint) (*networkModels.WireGuardServerPeer, *networkModels.WireGuardServer, error) {
	var peer networkModels.WireGuardServerPeer
	if err := s.DB.First(&peer, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrWireGuardServerPeerNotFound
		}
		return nil, nil, err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server, peer.WireGuardServerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrWireGuardServerNotInited
		}
		return nil, nil, err
	}
	return &peer, &server, nil
}

func (s *Service) restoreWireGuardServerPeerMutation(
	previous *networkModels.WireGuardServer,
	rollbackDatabase func() error,
) error {
	var rollbackErrors []error
	if rollbackDatabase != nil {
		if err := rollbackDatabase(); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard peer database state: %w", err))
		}
	}
	if previous != nil && previous.Enabled {
		if err := s.applyWireGuardServerRuntime(previous); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore wireguard peer runtime state: %w", err))
		}
	}
	s.flushWireGuardMetricsOnConfigChange()
	return errors.Join(rollbackErrors...)
}

func (s *Service) applyWireGuardServerPeerMutation(
	server *networkModels.WireGuardServer,
	previous *networkModels.WireGuardServer,
	runtimeChanged bool,
	rollbackDatabase func() error,
) error {
	if server == nil || !server.Enabled || !runtimeChanged {
		s.flushWireGuardMetricsOnConfigChange()
		return nil
	}

	if err := s.applyWireGuardServerRuntime(server); err != nil {
		return errors.Join(err, s.restoreWireGuardServerPeerMutation(previous, rollbackDatabase))
	}

	restartedAt := wireGuardCurrentTime()
	if err := s.DB.Model(&networkModels.WireGuardServer{}).
		Where("id = ?", server.ID).
		Update("restarted_at", restartedAt).Error; err != nil {
		return errors.Join(err, s.restoreWireGuardServerPeerMutation(previous, rollbackDatabase))
	}
	server.RestartedAt = restartedAt
	s.flushWireGuardMetricsOnConfigChange()
	return nil
}

func (s *Service) AddWireGuardServerPeer(req WireGuardServerPeerRequest) (uint, error) {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return 0, err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrWireGuardServerNotInited
		}
		return 0, err
	}
	previous := cloneWireGuardServer(&server)

	name, err := normalizeWireGuardServerPeerName(req.Name)
	if err != nil {
		return 0, err
	}
	clientIPs, err := normalizeWireGuardServerPeerCIDRs(
		req.ClientIPs,
		true,
		"wireguard_peer_too_many_client_ips",
	)
	if err != nil {
		return 0, err
	}
	routableIPs, err := normalizeWireGuardServerPeerCIDRs(
		req.RoutableIPs,
		false,
		"wireguard_peer_too_many_routable_ips",
	)
	if err != nil {
		return 0, err
	}

	privateKey, err := wireGuardGeneratePrivateKey()
	if err != nil {
		return 0, err
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return 0, invalidWireGuardServer("wireguard_invalid_peer_private_key", parseErr)
		}
		privateKey = provided
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return 0, err
	}

	preSharedKey := ""
	if req.PreSharedKey != nil {
		preSharedKey = strings.TrimSpace(*req.PreSharedKey)
	}
	if err := validateWireGuardPSK(preSharedKey); err != nil {
		return 0, invalidWireGuardServer("wireguard_invalid_peer_preshared_key", err)
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	routeIPs := false
	if req.RouteIPs != nil {
		routeIPs = *req.RouteIPs
	}

	persistentKeepalive := false
	if req.PersistentKeepalive != nil {
		persistentKeepalive = *req.PersistentKeepalive
	}

	peer := networkModels.WireGuardServerPeer{
		Name:                name,
		Enabled:             enabled,
		WireGuardServerID:   server.ID,
		PrivateKey:          privateKey,
		PublicKey:           publicKey,
		PreSharedKey:        preSharedKey,
		ClientIPs:           clientIPs,
		RoutableIPs:         routableIPs,
		RouteIPs:            routeIPs,
		PersistentKeepalive: persistentKeepalive,
	}
	if err := validateWireGuardServerPeerCandidate(&peer, server.Peers); err != nil {
		return 0, err
	}

	if err := s.createWireGuardServerPeer(&peer); err != nil {
		return 0, err
	}
	rollbackDatabase := func() error {
		return s.DB.Delete(&networkModels.WireGuardServerPeer{}, peer.ID).Error
	}

	if err := s.DB.Preload("Peers").First(&server, server.ID).Error; err != nil {
		return 0, errors.Join(err, rollbackDatabase())
	}

	if err := s.applyWireGuardServerPeerMutation(&server, &previous, peer.Enabled, rollbackDatabase); err != nil {
		return 0, err
	}
	return peer.ID, nil
}

func (s *Service) EditWireGuardServerPeer(req WireGuardServerPeerRequest) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if req.ID == nil || *req.ID == 0 {
		return ErrWireGuardServerPeerNotFound
	}

	peer, server, err := s.loadWireGuardServerPeer(*req.ID)
	if err != nil {
		return err
	}
	previousPeer := cloneWireGuardServerPeer(peer)
	previousServer := cloneWireGuardServer(server)

	name, err := normalizeWireGuardServerPeerName(req.Name)
	if err != nil {
		return err
	}
	peer.Name = name
	if req.Enabled != nil {
		peer.Enabled = *req.Enabled
	}

	if req.PreSharedKey != nil {
		preSharedKey := strings.TrimSpace(*req.PreSharedKey)
		if err := validateWireGuardPSK(preSharedKey); err != nil {
			return invalidWireGuardServer("wireguard_invalid_peer_preshared_key", err)
		}
		peer.PreSharedKey = preSharedKey
	}

	clientIPs, err := normalizeWireGuardServerPeerCIDRs(
		req.ClientIPs,
		true,
		"wireguard_peer_too_many_client_ips",
	)
	if err != nil {
		return err
	}
	peer.ClientIPs = clientIPs

	routableIPs, err := normalizeWireGuardServerPeerCIDRs(
		req.RoutableIPs,
		false,
		"wireguard_peer_too_many_routable_ips",
	)
	if err != nil {
		return err
	}
	peer.RoutableIPs = routableIPs

	if req.RouteIPs != nil {
		peer.RouteIPs = *req.RouteIPs
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return invalidWireGuardServer("wireguard_invalid_peer_private_key", parseErr)
		}
		newPublicKey, pkErr := wireGuardPublicKeyFromPrivate(provided)
		if pkErr != nil {
			return pkErr
		}
		peer.PrivateKey = provided
		peer.PublicKey = newPublicKey
	}

	if req.PersistentKeepalive != nil {
		peer.PersistentKeepalive = *req.PersistentKeepalive
	}
	if err := validateWireGuardServerPeerCandidate(peer, server.Peers); err != nil {
		return err
	}

	if err := s.persistWireGuardServerPeerConfig(peer); err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.persistWireGuardServerPeerConfig(&previousPeer)
	}

	if err := s.DB.Preload("Peers").First(server, server.ID).Error; err != nil {
		return errors.Join(err, rollbackDatabase())
	}
	return s.applyWireGuardServerPeerMutation(
		server,
		&previousServer,
		previousPeer.Enabled || peer.Enabled,
		rollbackDatabase,
	)
}

func (s *Service) SetWireGuardServerPeerEnabled(id uint, enabled bool) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if id == 0 {
		return ErrWireGuardServerPeerNotFound
	}
	peer, server, err := s.loadWireGuardServerPeer(id)
	if err != nil {
		return err
	}
	if peer.Enabled == enabled {
		return nil
	}
	previousPeer := cloneWireGuardServerPeer(peer)
	previousServer := cloneWireGuardServer(server)

	peer.Enabled = enabled
	if err := validateWireGuardServerPeerCandidate(peer, server.Peers); err != nil {
		return err
	}
	if err := s.DB.Model(&networkModels.WireGuardServerPeer{}).
		Where("id = ?", peer.ID).
		Update("enabled", enabled).Error; err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.DB.Model(&networkModels.WireGuardServerPeer{}).
			Where("id = ?", previousPeer.ID).
			Update("enabled", previousPeer.Enabled).Error
	}

	if err := s.DB.Preload("Peers").First(server, server.ID).Error; err != nil {
		return errors.Join(err, rollbackDatabase())
	}
	return s.applyWireGuardServerPeerMutation(server, &previousServer, true, rollbackDatabase)
}

func (s *Service) RemoveWireGuardServerPeer(id uint) error {
	s.wireGuardServerMutationMutex.Lock()
	defer s.wireGuardServerMutationMutex.Unlock()

	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if id == 0 {
		return ErrWireGuardServerPeerNotFound
	}
	peer, server, err := s.loadWireGuardServerPeer(id)
	if err != nil {
		return err
	}
	previousPeer := cloneWireGuardServerPeer(peer)
	previousServer := cloneWireGuardServer(server)

	if err := s.DB.Delete(peer).Error; err != nil {
		return err
	}
	rollbackDatabase := func() error {
		return s.createWireGuardServerPeer(&previousPeer)
	}

	if err := s.DB.Preload("Peers").First(server, server.ID).Error; err != nil {
		return errors.Join(err, rollbackDatabase())
	}
	return s.applyWireGuardServerPeerMutation(server, &previousServer, previousPeer.Enabled, rollbackDatabase)
}

func buildWireGuardServerPeers(peers []networkModels.WireGuardServerPeer) ([]wgtypes.PeerConfig, error) {
	peerConfigs := make([]wgtypes.PeerConfig, 0, len(peers))

	for _, peer := range peers {
		if !peer.Enabled {
			continue
		}

		publicKey, err := wgtypes.ParseKey(strings.TrimSpace(peer.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("invalid_wireguard_peer_public_key_%d: %w", peer.ID, err)
		}

		allowedIPs, err := parseAllowedIPs(sortedUnique(append(append([]string{}, peer.ClientIPs...), peer.RoutableIPs...)))
		if err != nil {
			return nil, err
		}

		peerConfig := wgtypes.PeerConfig{
			PublicKey:         publicKey,
			AllowedIPs:        allowedIPs,
			ReplaceAllowedIPs: true,
		}

		if strings.TrimSpace(peer.PreSharedKey) != "" {
			preSharedKey, err := wgtypes.ParseKey(strings.TrimSpace(peer.PreSharedKey))
			if err != nil {
				return nil, fmt.Errorf("invalid_wireguard_peer_preshared_key_%d: %w", peer.ID, err)
			}
			peerConfig.PresharedKey = &preSharedKey
		}

		if peer.PersistentKeepalive {
			interval := 25 * time.Second
			peerConfig.PersistentKeepaliveInterval = &interval
		}

		peerConfigs = append(peerConfigs, peerConfig)
	}

	return peerConfigs, nil
}

func (s *Service) applyWireGuardServerRuntime(server *networkModels.WireGuardServer) (err error) {
	if server == nil {
		return nil
	}

	if !server.Enabled {
		return s.teardownWireGuardServerRuntime(server)
	}

	if err = parseWireGuardCIDRs(server.Addresses); err != nil {
		return err
	}

	if err = destroyWireGuardInterface(wireGuardServerInterfaceName); err != nil {
		return err
	}

	if err = ensureWireGuardInterface(wireGuardServerInterfaceName); err != nil {
		return err
	}

	cleanupOnFailure := true
	defer func() {
		if err == nil || !cleanupOnFailure {
			return
		}
		if teardownErr := s.teardownWireGuardServerRuntime(server); teardownErr != nil {
			logger.L.Warn().Err(teardownErr).Msg("failed to rollback wireguard server runtime after apply error")
		}
	}()

	if err = configureWireGuardInterface(wireGuardServerInterfaceName, server.Addresses, server.MTU, server.Metric, 0); err != nil {
		return err
	}

	var peerConfigs []wgtypes.PeerConfig
	peerConfigs, err = buildWireGuardServerPeers(server.Peers)
	if err != nil {
		return err
	}

	if err = configureWireGuardDevice(wireGuardServerInterfaceName, server.PrivateKey, server.Port, peerConfigs); err != nil {
		return err
	}

	for _, peer := range server.Peers {
		networks := sortedUnique(append(append([]string{}, peer.ClientIPs...), peer.RoutableIPs...))
		for _, cidr := range networks {
			_ = deleteRouteViaInterface(cidr, wireGuardServerInterfaceName, 0)
		}
		if !peer.Enabled || !peer.RouteIPs {
			continue
		}
		for _, cidr := range networks {
			if err = addRouteViaInterface(cidr, wireGuardServerInterfaceName, 0); err != nil {
				return err
			}
		}
	}

	cleanupOnFailure = false
	return nil
}

func (s *Service) teardownWireGuardServerRuntime(server *networkModels.WireGuardServer) error {
	if server != nil {
		for _, peer := range server.Peers {
			networks := sortedUnique(append(append([]string{}, peer.ClientIPs...), peer.RoutableIPs...))
			for _, cidr := range networks {
				_ = deleteRouteViaInterface(cidr, wireGuardServerInterfaceName, 0)
			}
		}
	}

	return destroyWireGuardInterface(wireGuardServerInterfaceName)
}
