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

func normalizeWireGuardManagedInterface(value string) string {
	return strings.TrimSpace(value)
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

func (s *Service) restoreWireGuardFirewallState(snapshot *wireGuardFirewallSnapshot) error {
	if snapshot == nil {
		return nil
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&networkModels.FirewallTrafficRule{}).Error; err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&networkModels.FirewallNATRule{}).Error; err != nil {
			return err
		}
		if len(snapshot.Traffic) > 0 {
			if err := tx.Create(&snapshot.Traffic).Error; err != nil {
				return err
			}
			for _, row := range snapshot.Traffic {
				if row.Visible {
					continue
				}
				if err := tx.Model(&networkModels.FirewallTrafficRule{}).Where("id = ?", row.ID).Update("visible", false).Error; err != nil {
					return err
				}
			}
		}
		if len(snapshot.NAT) > 0 {
			if err := tx.Create(&snapshot.NAT).Error; err != nil {
				return err
			}
			for _, row := range snapshot.NAT {
				if row.Visible {
					continue
				}
				if err := tx.Model(&networkModels.FirewallNATRule{}).Where("id = ?", row.ID).Update("visible", false).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
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

func (s *Service) syncWireGuardManagedFirewallRules(server *networkModels.WireGuardServer) error {
	if server == nil {
		return nil
	}

	v4Iface := normalizeWireGuardManagedInterface(server.MasqueradeIPv4Interface)
	v6Iface := normalizeWireGuardManagedInterface(server.MasqueradeIPv6Interface)
	v4CIDR := wireGuardCIDRNetworkByFamily(server.Addresses, false)
	v6CIDR := wireGuardCIDRNetworkByFamily(server.Addresses, true)

	if v4Iface != "" && v4CIDR == "" {
		return fmt.Errorf("wireguard_masquerade_ipv4_requires_server_ipv4_cidr")
	}
	if v6Iface != "" && v6CIDR == "" {
		return fmt.Errorf("wireguard_masquerade_ipv6_requires_server_ipv6_cidr")
	}

	snapshot, err := s.snapshotWireGuardFirewallState()
	if err != nil {
		return err
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if syncErr := s.reconcileManagedWireGuardTrafficRule(tx, server); syncErr != nil {
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
	}); err != nil {
		return err
	}

	if err := s.ApplyFirewallIfEnabled(); err != nil {
		_ = s.restoreWireGuardFirewallState(snapshot)
		return err
	}

	return nil
}

func (s *Service) GetWireGuardServer() (*networkModels.WireGuardServer, error) {
	inited, _ := s.isWireGuardServerInitialized()
	if !inited {
		return nil, ErrWireGuardServerNotInited
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
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var existing networkModels.WireGuardServer
	err := s.DB.First(&existing).Error
	if err == nil {
		return ErrWireGuardServerAlreadyInited
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if utils.IsPortInUse(int(req.Port)) {
		return fmt.Errorf("wireguard_port_already_in_use")
	}

	privateKey, err := wireGuardGeneratePrivateKey()
	if err != nil {
		return err
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return fmt.Errorf("wireguard_invalid_private_key: %w", parseErr)
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
	if err := parseWireGuardCIDRs(addresses); err != nil {
		return err
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

	if err := s.DB.Create(&server).Error; err != nil {
		return err
	}

	if err := s.applyWireGuardServerRuntime(&server); err != nil {
		_ = s.DB.Delete(&server).Error
		return err
	}

	if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
		_ = s.teardownWireGuardServerRuntime(&server)
		_ = s.DB.Delete(&server).Error
		return err
	}

	return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
}

func (s *Service) EditWireGuardServer(req InitWireGuardServerRequest) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerNotInited
		}
		return err
	}

	if req.Port != server.Port && utils.IsPortInUse(int(req.Port)) {
		return fmt.Errorf("wireguard_port_already_in_use")
	}

	privateKey := server.PrivateKey
	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return fmt.Errorf("wireguard_invalid_private_key: %w", parseErr)
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
	if len(addresses) == 0 {
		return ErrWireGuardAddressesRequired
	}
	if err := parseWireGuardCIDRs(addresses); err != nil {
		return err
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

	if err := s.DB.Save(&server).Error; err != nil {
		return err
	}

	if err := s.DB.Preload("Peers").First(&server, server.ID).Error; err != nil {
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
		return err
	}

	return nil
}

func (s *Service) ToggleWireGuardServer() error {
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

	server.Enabled = !server.Enabled
	if err := s.DB.Save(&server).Error; err != nil {
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
		return err
	}
	return s.teardownWireGuardServerRuntime(&server)
}

func (s *Service) DeinitWireGuardServer() error {
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

	if err := s.teardownWireGuardServerRuntime(&server); err != nil {
		return err
	}

	if err := s.DB.Where("wire_guard_server_id = ?", server.ID).Delete(&networkModels.WireGuardServerPeer{}).Error; err != nil {
		return err
	}

	server.AllowWireGuardPort = false
	server.MasqueradeIPv4Interface = ""
	server.MasqueradeIPv6Interface = ""
	if err := s.syncWireGuardManagedFirewallRules(&server); err != nil {
		return err
	}

	return s.DB.Delete(&server).Error
}

func (s *Service) AddWireGuardServerPeer(req WireGuardServerPeerRequest) error {
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

	clientIPs := sortedUnique(req.ClientIPs)
	if len(clientIPs) == 0 {
		return ErrWireGuardClientIPsRequired
	}
	if err := parseWireGuardCIDRs(clientIPs); err != nil {
		return err
	}

	routableIPs := sortedUnique(req.RoutableIPs)
	if err := parseWireGuardCIDRs(routableIPs); err != nil {
		return err
	}

	privateKey, err := wireGuardGeneratePrivateKey()
	if err != nil {
		return err
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return fmt.Errorf("wireguard_invalid_private_key: %w", parseErr)
		}
		privateKey = provided
	}

	publicKey, err := wireGuardPublicKeyFromPrivate(privateKey)
	if err != nil {
		return err
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

	routeIPs := false
	if req.RouteIPs != nil {
		routeIPs = *req.RouteIPs
	}

	persistentKeepalive := false
	if req.PersistentKeepalive != nil {
		persistentKeepalive = *req.PersistentKeepalive
	}

	peer := networkModels.WireGuardServerPeer{
		Name:                req.Name,
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

	if err := s.DB.Create(&peer).Error; err != nil {
		return err
	}

	if err := s.DB.Preload("Peers").First(&server, server.ID).Error; err != nil {
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}

func (s *Service) EditWireGuardServerPeer(req WireGuardServerPeerRequest) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if req.ID == nil {
		return ErrWireGuardServerPeerNotFound
	}

	var peer networkModels.WireGuardServerPeer
	if err := s.DB.First(&peer, *req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerPeerNotFound
		}
		return err
	}

	if strings.TrimSpace(req.Name) != "" {
		peer.Name = strings.TrimSpace(req.Name)
	}
	if req.Enabled != nil {
		peer.Enabled = *req.Enabled
	}

	if req.PreSharedKey != nil {
		preSharedKey := strings.TrimSpace(*req.PreSharedKey)
		if err := validateWireGuardPSK(preSharedKey); err != nil {
			return err
		}
		peer.PreSharedKey = preSharedKey
	}

	if req.ClientIPs != nil {
		clientIPs := sortedUnique(req.ClientIPs)
		if len(clientIPs) == 0 {
			return ErrWireGuardClientIPsRequired
		}
		if err := parseWireGuardCIDRs(clientIPs); err != nil {
			return err
		}
		peer.ClientIPs = clientIPs
	}

	if req.RoutableIPs != nil {
		routableIPs := sortedUnique(req.RoutableIPs)
		if err := parseWireGuardCIDRs(routableIPs); err != nil {
			return err
		}
		peer.RoutableIPs = routableIPs
	}

	if req.RouteIPs != nil {
		peer.RouteIPs = *req.RouteIPs
	}

	if req.PrivateKey != nil && strings.TrimSpace(*req.PrivateKey) != "" {
		provided := strings.TrimSpace(*req.PrivateKey)
		if _, parseErr := wgtypes.ParseKey(provided); parseErr != nil {
			return fmt.Errorf("wireguard_invalid_private_key: %w", parseErr)
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

	if err := s.DB.Save(&peer).Error; err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server, peer.WireGuardServerID).Error; err != nil {
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}

func (s *Service) ToggleWireGuardServerPeer(id uint) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var peer networkModels.WireGuardServerPeer
	if err := s.DB.First(&peer, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerPeerNotFound
		}
		return err
	}

	peer.Enabled = !peer.Enabled
	if err := s.DB.Save(&peer).Error; err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server, peer.WireGuardServerID).Error; err != nil {
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}

func (s *Service) RemoveWireGuardServerPeer(id uint) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	var peer networkModels.WireGuardServerPeer
	if err := s.DB.First(&peer, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWireGuardServerPeerNotFound
		}
		return err
	}

	if err := s.DB.Delete(&peer).Error; err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server, peer.WireGuardServerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
}

func (s *Service) RemoveWireGuardServerPeers(ids []uint) error {
	if err := s.requireWireGuardServiceEnabled(); err != nil {
		return err
	}

	if len(ids) == 0 {
		return nil
	}

	if err := s.DB.Where("id IN ?", ids).Delete(&networkModels.WireGuardServerPeer{}).Error; err != nil {
		return err
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	if server.Enabled {
		if err := s.applyWireGuardServerRuntime(&server); err != nil {
			return err
		}
		return s.DB.Model(&server).Update("restarted_at", wireGuardCurrentTime()).Error
	}

	return nil
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
