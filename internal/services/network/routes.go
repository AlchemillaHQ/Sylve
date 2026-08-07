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
	"regexp"
	"strconv"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
	"gorm.io/gorm"
)

const (
	staticRouteDestinationHost    = "host"
	staticRouteDestinationNetwork = "network"
	staticRouteFamilyINET         = "inet"
	staticRouteFamilyINET6        = "inet6"
	staticRouteNextHopGateway     = "gateway"
	staticRouteNextHopInterface   = "interface"
)

var (
	staticRouteRunCommand           = utils.RunCommand
	staticRouteInterfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
	getNetFIBCountFunc              = func() (int64, error) {
		return sysctl.GetInt64("net.fibs")
	}
)

type staticRouteCandidate struct {
	DestinationType string
	Destination     string
	SourceHint      string
}

func normalizeStaticRouteDestinationType(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case staticRouteDestinationHost:
		return staticRouteDestinationHost
	case staticRouteDestinationNetwork:
		return staticRouteDestinationNetwork
	default:
		return ""
	}
}

func normalizeStaticRouteFamily(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case staticRouteFamilyINET:
		return staticRouteFamilyINET
	case staticRouteFamilyINET6:
		return staticRouteFamilyINET6
	default:
		return ""
	}
}

func normalizeStaticRouteNextHopMode(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case staticRouteNextHopGateway:
		return staticRouteNextHopGateway
	case staticRouteNextHopInterface:
		return staticRouteNextHopInterface
	default:
		return ""
	}
}

func staticRouteIPMatchesFamily(ip net.IP, family string) bool {
	if ip == nil {
		return false
	}
	switch family {
	case staticRouteFamilyINET:
		return ip.To4() != nil
	case staticRouteFamilyINET6:
		return ip.To4() == nil && ip.To16() != nil
	default:
		return false
	}
}

func staticRouteNetworkMatchesFamily(network *net.IPNet, family string) bool {
	if network == nil {
		return false
	}
	return staticRouteIPMatchesFamily(network.IP, family)
}

func getNetFIBCount() int {
	val, err := getNetFIBCountFunc()
	if err != nil || val <= 0 {
		return 1
	}

	return int(val)
}

func invalidStaticRoutef(format string, args ...any) error {
	return invalidStaticRoute(fmt.Errorf(format, args...))
}

func validateStaticRouteRequestBounds(req *networkServiceInterfaces.UpsertStaticRouteRequest) error {
	if req == nil {
		return invalidStaticRoutef("invalid_route_request")
	}

	fields := []struct {
		name  string
		value string
		max   int
	}{
		{name: "name", value: req.Name, max: MaxStaticRouteNameBytes},
		{name: "description", value: req.Description, max: MaxStaticRouteDescriptionBytes},
		{name: "destination", value: req.Destination, max: MaxStaticRouteAddressBytes},
		{name: "destination_raw", value: req.DestinationRaw, max: MaxStaticRouteAddressBytes},
		{name: "gateway", value: req.Gateway, max: MaxStaticRouteAddressBytes},
		{name: "gateway_raw", value: req.GatewayRaw, max: MaxStaticRouteAddressBytes},
		{name: "gateway_zone", value: req.GatewayZone, max: MaxStaticRouteInterfaceBytes},
		{name: "interface", value: req.Interface, max: MaxStaticRouteInterfaceBytes},
	}
	for _, field := range fields {
		if len(field.value) > field.max {
			return invalidStaticRoutef("route_%s_too_long", field.name)
		}
	}

	if req.DestinationObjID != nil && *req.DestinationObjID == 0 {
		return invalidStaticRoutef("invalid_destination_object_id")
	}
	if req.GatewayObjID != nil && *req.GatewayObjID == 0 {
		return invalidStaticRoutef("invalid_gateway_object_id")
	}

	return nil
}

func validateStaticRouteRuntime(route networkModels.StaticRoute) (networkModels.StaticRoute, error) {
	route.DestinationType = normalizeStaticRouteDestinationType(route.DestinationType)
	route.Destination = strings.TrimSpace(route.Destination)
	route.Family = normalizeStaticRouteFamily(route.Family)
	route.NextHopMode = normalizeStaticRouteNextHopMode(route.NextHopMode)
	route.Gateway = strings.TrimSpace(route.Gateway)
	route.GatewayZone = strings.TrimSpace(route.GatewayZone)
	route.Interface = strings.TrimSpace(route.Interface)

	if len(route.Destination) > MaxStaticRouteAddressBytes {
		return networkModels.StaticRoute{}, invalidStaticRoutef("route_destination_too_long")
	}
	if len(route.Gateway) > MaxStaticRouteAddressBytes {
		return networkModels.StaticRoute{}, invalidStaticRoutef("route_gateway_too_long")
	}
	if len(route.GatewayZone) > MaxStaticRouteInterfaceBytes {
		return networkModels.StaticRoute{}, invalidStaticRoutef("route_gateway_zone_too_long")
	}
	if len(route.Interface) > MaxStaticRouteInterfaceBytes {
		return networkModels.StaticRoute{}, invalidStaticRoutef("route_interface_too_long")
	}
	if route.DestinationType == "" {
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_destination_type")
	}
	if route.Family == "" {
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_family")
	}
	if route.NextHopMode == "" {
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_next_hop_mode")
	}

	fibs := getNetFIBCount()
	if route.FIB >= uint(fibs) {
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_fib: fib=%d valid_range=0..%d", route.FIB, fibs-1)
	}

	switch route.DestinationType {
	case staticRouteDestinationHost:
		if strings.Contains(route.Destination, "/") {
			return networkModels.StaticRoute{}, invalidStaticRoutef("host_destination_must_not_contain_cidr")
		}
		ip := net.ParseIP(route.Destination)
		if ip == nil {
			return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_host_destination")
		}
		if !staticRouteIPMatchesFamily(ip, route.Family) {
			return networkModels.StaticRoute{}, invalidStaticRoutef("destination_family_mismatch")
		}
		route.Destination = ip.String()
	case staticRouteDestinationNetwork:
		_, network, err := net.ParseCIDR(route.Destination)
		if err != nil || network == nil {
			return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_network_destination")
		}
		if !staticRouteNetworkMatchesFamily(network, route.Family) {
			return networkModels.StaticRoute{}, invalidStaticRoutef("destination_family_mismatch")
		}
		route.Destination = network.String()
	default:
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_destination_type")
	}

	switch route.NextHopMode {
	case staticRouteNextHopGateway:
		if route.Gateway == "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("route_gateway_required_for_next_hop_mode_gateway")
		}
		if route.Interface != "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("route_interface_not_allowed_for_next_hop_mode_gateway")
		}
		if strings.Contains(route.Gateway, "%") {
			return networkModels.StaticRoute{}, invalidStaticRoutef("route_gateway_must_not_include_zone")
		}

		gw := net.ParseIP(route.Gateway)
		if gw == nil {
			return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_gateway")
		}
		if !staticRouteIPMatchesFamily(gw, route.Family) {
			return networkModels.StaticRoute{}, invalidStaticRoutef("gateway_family_mismatch")
		}
		route.Gateway = gw.String()

		if route.Family == staticRouteFamilyINET6 && gw.IsLinkLocalUnicast() && route.GatewayZone == "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("gateway_zone_required_for_link_local")
		}
		if route.GatewayZone != "" {
			if !staticRouteInterfaceNamePattern.MatchString(route.GatewayZone) {
				return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_gateway_zone")
			}
			if route.Family != staticRouteFamilyINET6 {
				return networkModels.StaticRoute{}, invalidStaticRoutef("gateway_zone_requires_inet6")
			}
			if !gw.IsLinkLocalUnicast() {
				return networkModels.StaticRoute{}, invalidStaticRoutef("gateway_zone_requires_link_local")
			}
		}
	case staticRouteNextHopInterface:
		if route.Interface == "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("route_interface_required_for_next_hop_mode_interface")
		}
		if !staticRouteInterfaceNamePattern.MatchString(route.Interface) {
			return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_interface")
		}
		if route.Gateway != "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("route_gateway_not_allowed_for_next_hop_mode_interface")
		}
		if route.GatewayZone != "" {
			return networkModels.StaticRoute{}, invalidStaticRoutef("gateway_zone_not_allowed_for_next_hop_mode_interface")
		}
	default:
		return networkModels.StaticRoute{}, invalidStaticRoutef("invalid_route_next_hop_mode")
	}

	return route, nil
}

func validateStaticRouteRequest(req *networkServiceInterfaces.UpsertStaticRouteRequest) (networkModels.StaticRoute, error) {
	if err := validateStaticRouteRequestBounds(req); err != nil {
		return networkModels.StaticRoute{}, err
	}

	route := networkModels.StaticRoute{
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		DestinationType: req.DestinationType,
		Destination:     req.Destination,
		Family:          req.Family,
		NextHopMode:     req.NextHopMode,
		Gateway:         req.Gateway,
		GatewayZone:     req.GatewayZone,
		Interface:       req.Interface,
	}
	if req.Enabled == nil {
		route.Enabled = true
	} else {
		route.Enabled = *req.Enabled
	}
	if req.FIB != nil {
		route.FIB = *req.FIB
	}

	if route.Name == "" {
		return networkModels.StaticRoute{}, invalidStaticRoutef("route_name_required")
	}

	return validateStaticRouteRuntime(route)
}

func staticRouteCommandArgs(route *networkModels.StaticRoute, action string) []string {
	args := []string{"-n"}
	if route.Family == staticRouteFamilyINET6 {
		args = append(args, "-6")
	}
	args = append(args, action)

	if route.DestinationType == staticRouteDestinationHost {
		args = append(args, "-host")
	} else {
		args = append(args, "-net")
	}
	args = append(args, route.Destination)

	if route.NextHopMode == staticRouteNextHopGateway {
		gateway := route.Gateway
		if route.GatewayZone != "" {
			gateway = gateway + "%" + route.GatewayZone
		}
		args = append(args, gateway)
	} else {
		args = append(args, "-iface", route.Interface)
	}

	return args
}

func staticRouteRunWithFIB(fib uint, args ...string) (string, error) {
	if fib > 0 {
		cmd := []string{"-F", strconv.FormatUint(uint64(fib), 10), "/sbin/route"}
		cmd = append(cmd, args...)
		return staticRouteRunCommand("/usr/sbin/setfib", cmd...)
	}
	return staticRouteRunCommand("/sbin/route", args...)
}

func staticRouteAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "file exists") || strings.Contains(lower, "already in table")
}

func addManagedRoute(route *networkModels.StaticRoute) error {
	args := staticRouteCommandArgs(route, "add")
	if _, err := staticRouteRunWithFIB(route.FIB, args...); err != nil {
		if staticRouteAlreadyExistsError(err) {
			return staticRouteConflict(err)
		}
		return err
	}
	return nil
}

func ensureManagedRoute(route *networkModels.StaticRoute) error {
	args := staticRouteCommandArgs(route, "add")
	if _, err := staticRouteRunWithFIB(route.FIB, args...); err != nil {
		if staticRouteAlreadyExistsError(err) {
			return nil
		}
		return err
	}
	return nil
}

func deleteManagedRoute(route *networkModels.StaticRoute) error {
	args := staticRouteCommandArgs(route, "delete")
	if _, err := staticRouteRunWithFIB(route.FIB, args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not in table") || strings.Contains(lower, "no such process") {
			return nil
		}
		return err
	}
	return nil
}

func equalStaticRouteRuntime(a, b *networkModels.StaticRoute) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Enabled == b.Enabled &&
		a.FIB == b.FIB &&
		a.DestinationType == b.DestinationType &&
		a.Destination == b.Destination &&
		a.Family == b.Family &&
		a.NextHopMode == b.NextHopMode &&
		a.Gateway == b.Gateway &&
		a.GatewayZone == b.GatewayZone &&
		a.Interface == b.Interface
}

func applyStaticRouteDiff(current, next *networkModels.StaticRoute) error {
	if current == nil || next == nil {
		return invalidStaticRoutef("invalid_route_diff")
	}
	if equalStaticRouteRuntime(current, next) {
		return nil
	}

	if current.Enabled {
		if err := deleteManagedRoute(current); err != nil {
			return fmt.Errorf("failed_to_remove_previous_static_route: %w", err)
		}
	}

	if next.Enabled {
		if err := addManagedRoute(next); err != nil {
			if current.Enabled {
				if rollbackErr := ensureManagedRoute(current); rollbackErr != nil {
					logger.L.Error().
						Err(rollbackErr).
						Uint("route_id", current.ID).
						Msg("failed_to_restore_previous_static_route_after_apply_failure")
					return errors.Join(
						fmt.Errorf("failed_to_apply_updated_static_route: %w", err),
						fmt.Errorf("failed_to_restore_previous_static_route: %w", rollbackErr),
					)
				}
			}
			return fmt.Errorf("failed_to_apply_updated_static_route: %w", err)
		}
	}

	return nil
}

func (s *Service) resolveStaticRouteRefs(req *networkServiceInterfaces.UpsertStaticRouteRequest) (*networkServiceInterfaces.UpsertStaticRouteRequest, error) {
	if err := validateStaticRouteRequestBounds(req); err != nil {
		return nil, err
	}

	resolved := *req
	resolved.Destination = strings.TrimSpace(resolved.Destination)
	resolved.DestinationRaw = strings.TrimSpace(resolved.DestinationRaw)
	resolved.Gateway = strings.TrimSpace(resolved.Gateway)
	resolved.GatewayRaw = strings.TrimSpace(resolved.GatewayRaw)

	if resolved.DestinationObjID != nil {
		if resolved.Destination != "" || resolved.DestinationRaw != "" {
			return nil, invalidStaticRoutef("destination_object_and_raw_are_mutually_exclusive")
		}

		var obj networkModels.Object
		if err := s.DB.Preload("Entries").First(&obj, *resolved.DestinationObjID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, invalidStaticRoutef("invalid_destination_object_id")
			}
			return nil, err
		}
		if len(obj.Entries) != 1 || strings.TrimSpace(obj.Entries[0].Value) == "" {
			return nil, invalidStaticRoutef("destination_object_requires_exactly_one_entry")
		}

		expectedType := ""
		switch normalizeStaticRouteDestinationType(resolved.DestinationType) {
		case staticRouteDestinationHost:
			expectedType = "Host"
		case staticRouteDestinationNetwork:
			expectedType = "Network"
		default:
			return nil, invalidStaticRoutef("invalid_route_destination_type")
		}
		if obj.Type != expectedType {
			return nil, invalidStaticRoutef("invalid_destination_object_type")
		}
		resolved.Destination = strings.TrimSpace(obj.Entries[0].Value)
		resolved.DestinationRaw = ""
	} else if resolved.DestinationRaw != "" {
		resolved.Destination = resolved.DestinationRaw
	} else if resolved.Destination == "" {
		return nil, invalidStaticRoutef("route_destination_required")
	} else {
		resolved.DestinationRaw = resolved.Destination
	}

	if resolved.GatewayObjID != nil {
		if normalizeStaticRouteNextHopMode(resolved.NextHopMode) != staticRouteNextHopGateway {
			return nil, invalidStaticRoutef("gateway_object_requires_gateway_next_hop_mode")
		}
		if resolved.Gateway != "" || resolved.GatewayRaw != "" {
			return nil, invalidStaticRoutef("gateway_object_and_raw_are_mutually_exclusive")
		}

		var obj networkModels.Object
		if err := s.DB.Preload("Entries").First(&obj, *resolved.GatewayObjID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, invalidStaticRoutef("invalid_gateway_object_id")
			}
			return nil, err
		}
		if obj.Type != "Host" {
			return nil, invalidStaticRoutef("invalid_gateway_object_type")
		}
		if len(obj.Entries) != 1 || strings.TrimSpace(obj.Entries[0].Value) == "" {
			return nil, invalidStaticRoutef("gateway_object_requires_exactly_one_entry")
		}
		resolved.Gateway = strings.TrimSpace(obj.Entries[0].Value)
		resolved.GatewayRaw = ""
	} else if resolved.GatewayRaw != "" {
		resolved.Gateway = resolved.GatewayRaw
	} else if resolved.Gateway != "" {
		resolved.GatewayRaw = resolved.Gateway
	}

	return &resolved, nil
}

func (s *Service) GetStaticRoutes() ([]networkModels.StaticRoute, error) {
	var routes []networkModels.StaticRoute
	if err := s.DB.Order("id asc").Find(&routes).Error; err != nil {
		return nil, err
	}
	return routes, nil
}

func staticRouteRuntimeIdentity(route *networkModels.StaticRoute) string {
	if route == nil {
		return ""
	}

	destination := route.Destination
	if route.DestinationType == staticRouteDestinationHost {
		if ip := net.ParseIP(route.Destination); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			destination = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
	}

	return fmt.Sprintf("%s|%d|%s", route.Family, route.FIB, destination)
}

func (s *Service) ensureNoStaticRouteConflict(tx *gorm.DB, route *networkModels.StaticRoute, excludeID uint) error {
	if route == nil || !route.Enabled {
		return nil
	}

	var existing []networkModels.StaticRoute
	query := tx.Where("enabled = ? AND family = ? AND fib = ?", true, route.Family, route.FIB)
	if excludeID > 0 {
		query = query.Where("id != ?", excludeID)
	}
	if err := query.Find(&existing).Error; err != nil {
		return err
	}

	wantedIdentity := staticRouteRuntimeIdentity(route)
	for _, candidate := range existing {
		normalized, err := validateStaticRouteRuntime(candidate)
		if err != nil {
			continue
		}
		if staticRouteRuntimeIdentity(&normalized) == wantedIdentity {
			return staticRouteConflict(fmt.Errorf("route_destination_already_managed_by_id_%d", candidate.ID))
		}
	}

	return nil
}

func restoreStaticRouteRuntime(applied, previous *networkModels.StaticRoute) error {
	if applied == nil || previous == nil {
		return invalidStaticRoutef("invalid_route_restore")
	}
	if equalStaticRouteRuntime(applied, previous) {
		return nil
	}
	if applied.Enabled {
		if err := deleteManagedRoute(applied); err != nil {
			return fmt.Errorf("failed_to_remove_applied_static_route: %w", err)
		}
	}
	if previous.Enabled {
		if err := ensureManagedRoute(previous); err != nil {
			return fmt.Errorf("failed_to_restore_previous_static_route: %w", err)
		}
	}
	return nil
}

func (s *Service) CreateStaticRoute(req *networkServiceInterfaces.UpsertStaticRouteRequest) (uint, error) {
	s.staticRouteMutationMutex.Lock()
	defer s.staticRouteMutationMutex.Unlock()

	resolved, err := s.resolveStaticRouteRefs(req)
	if err != nil {
		return 0, err
	}

	route, err := validateStaticRouteRequest(resolved)
	if err != nil {
		return 0, err
	}

	route.DestinationRaw = strings.TrimSpace(resolved.DestinationRaw)
	route.DestinationObjID = resolved.DestinationObjID
	route.GatewayRaw = strings.TrimSpace(resolved.GatewayRaw)
	route.GatewayObjID = resolved.GatewayObjID

	if txErr := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.ensureNoStaticRouteConflict(tx, &route, 0); err != nil {
			return err
		}
		if err := tx.Create(&route).Error; err != nil {
			return err
		}
		if route.Enabled {
			if err := addManagedRoute(&route); err != nil {
				return fmt.Errorf("failed_to_apply_static_route: %w", err)
			}
		}
		return nil
	}); txErr != nil {
		return 0, txErr
	}

	return route.ID, nil
}

func (s *Service) EditStaticRoute(id uint, req *networkServiceInterfaces.UpsertStaticRouteRequest) error {
	if id == 0 {
		return invalidStaticRoutef("invalid_static_route_id")
	}

	s.staticRouteMutationMutex.Lock()
	defer s.staticRouteMutationMutex.Unlock()

	resolved, err := s.resolveStaticRouteRefs(req)
	if err != nil {
		return err
	}

	normalized, err := validateStaticRouteRequest(resolved)
	if err != nil {
		return err
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var current networkModels.StaticRoute
		if err := tx.First(&current, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return staticRouteNotFound(err)
			}
			return err
		}

		next := current
		next.Name = normalized.Name
		next.Description = normalized.Description
		next.Enabled = normalized.Enabled
		next.FIB = normalized.FIB
		next.DestinationType = normalized.DestinationType
		next.Destination = normalized.Destination
		next.DestinationRaw = strings.TrimSpace(resolved.DestinationRaw)
		next.DestinationObjID = resolved.DestinationObjID
		next.Family = normalized.Family
		next.NextHopMode = normalized.NextHopMode
		next.Gateway = normalized.Gateway
		next.GatewayRaw = strings.TrimSpace(resolved.GatewayRaw)
		next.GatewayObjID = resolved.GatewayObjID
		next.GatewayZone = normalized.GatewayZone
		next.Interface = normalized.Interface

		if err := s.ensureNoStaticRouteConflict(tx, &next, id); err != nil {
			return err
		}

		runtimeChanged := !equalStaticRouteRuntime(&current, &next)
		if err := applyStaticRouteDiff(&current, &next); err != nil {
			return err
		}

		if err := tx.Save(&next).Error; err != nil {
			if runtimeChanged {
				if rollbackErr := restoreStaticRouteRuntime(&next, &current); rollbackErr != nil {
					return errors.Join(err, rollbackErr)
				}
			}
			return err
		}

		return nil
	})
}

func (s *Service) DeleteStaticRoute(id uint) error {
	if id == 0 {
		return invalidStaticRoutef("invalid_static_route_id")
	}

	s.staticRouteMutationMutex.Lock()
	defer s.staticRouteMutationMutex.Unlock()

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var route networkModels.StaticRoute
		if err := tx.First(&route, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return staticRouteNotFound(err)
			}
			return err
		}

		if route.Enabled {
			if err := deleteManagedRoute(&route); err != nil {
				return fmt.Errorf("failed_to_remove_static_route: %w", err)
			}
		}

		if err := tx.Delete(&route).Error; err != nil {
			if route.Enabled {
				if rollbackErr := ensureManagedRoute(&route); rollbackErr != nil {
					return errors.Join(err, fmt.Errorf("failed_to_restore_deleted_static_route: %w", rollbackErr))
				}
			}
			return err
		}

		return nil
	})
}

func (s *Service) ReconcileManagedRoutes() error {
	s.staticRouteMutationMutex.Lock()
	defer s.staticRouteMutationMutex.Unlock()

	var routes []networkModels.StaticRoute
	if err := s.DB.Order("id asc").Find(&routes).Error; err != nil {
		return err
	}

	var errs []error
	seen := make(map[string]uint)
	for _, route := range routes {
		if !route.Enabled {
			continue
		}

		normalized, err := validateStaticRouteRuntime(route)
		if err == nil {
			identity := staticRouteRuntimeIdentity(&normalized)
			if existingID, exists := seen[identity]; exists {
				err = staticRouteConflict(fmt.Errorf("routes_%d_and_%d_share_a_destination", existingID, route.ID))
			} else {
				seen[identity] = route.ID
				err = ensureManagedRoute(&normalized)
			}
		}
		if err != nil {
			logger.L.Error().
				Err(err).
				Uint("route_id", route.ID).
				Str("name", route.Name).
				Msg("failed_to_reconcile_managed_route")
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("managed_route_reconcile_failed: %w", errors.Join(errs...))
	}

	return nil
}

func (s *Service) ReconcileObjectStaticRoutes(objID uint) error {
	if objID == 0 {
		return invalidStaticRoutef("invalid_network_object_id")
	}

	s.staticRouteMutationMutex.Lock()
	defer s.staticRouteMutationMutex.Unlock()

	var routes []networkModels.StaticRoute
	if err := s.DB.Where("destination_obj_id = ? OR gateway_obj_id = ?", objID, objID).
		Find(&routes).Error; err != nil {
		return err
	}
	if len(routes) == 0 {
		return nil
	}

	var obj networkModels.Object
	if err := s.DB.Preload("Entries").First(&obj, objID).Error; err != nil {
		return fmt.Errorf("failed_to_load_object_for_route_reconciliation: %w", err)
	}
	if len(obj.Entries) != 1 || strings.TrimSpace(obj.Entries[0].Value) == "" {
		return invalidStaticRoutef("route_object_requires_exactly_one_entry")
	}

	var errs []error
	for _, route := range routes {
		current, err := validateStaticRouteRuntime(route)
		if err != nil {
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
			continue
		}

		next := current
		changed := false

		if route.DestinationObjID != nil && *route.DestinationObjID == objID {
			expectedType := "Host"
			if current.DestinationType == staticRouteDestinationNetwork {
				expectedType = "Network"
			}
			if obj.Type != expectedType {
				errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, invalidStaticRoutef("invalid_destination_object_type")))
				continue
			}
			next.Destination = strings.TrimSpace(obj.Entries[0].Value)
			changed = true
		}

		if route.GatewayObjID != nil && *route.GatewayObjID == objID {
			if obj.Type != "Host" {
				errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, invalidStaticRoutef("invalid_gateway_object_type")))
				continue
			}
			next.Gateway = strings.TrimSpace(obj.Entries[0].Value)
			changed = true
		}

		if !changed {
			continue
		}

		next, err = validateStaticRouteRuntime(next)
		if err != nil {
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
			continue
		}
		if err := s.ensureNoStaticRouteConflict(s.DB, &next, route.ID); err != nil {
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
			continue
		}

		if err := applyStaticRouteDiff(&current, &next); err != nil {
			logger.L.Error().
				Err(err).
				Uint("route_id", route.ID).
				Msg("failed_to_apply_route_diff_during_object_reconciliation")
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
			continue
		}

		if err := s.DB.Model(&networkModels.StaticRoute{}).Where("id = ?", route.ID).Updates(map[string]interface{}{
			"destination": next.Destination,
			"gateway":     next.Gateway,
		}).Error; err != nil {
			logger.L.Error().
				Err(err).
				Uint("route_id", route.ID).
				Msg("failed_to_update_route_during_object_reconciliation")
			if rollbackErr := restoreStaticRouteRuntime(&next, &current); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			errs = append(errs, fmt.Errorf("route=%d: %w", route.ID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("reconcile_object_static_routes_failed: %w", errors.Join(errs...))
	}

	return nil
}

func candidateFamily(candidate staticRouteCandidate) string {
	if strings.Contains(candidate.Destination, ":") {
		return staticRouteFamilyINET6
	}
	return staticRouteFamilyINET
}

func addRouteCandidate(out *[]staticRouteCandidate, seen map[string]struct{}, candidate staticRouteCandidate, expectedFamily string) {
	if candidate.DestinationType == "" || candidate.Destination == "" {
		return
	}
	if candidateFamily(candidate) != expectedFamily {
		return
	}
	key := candidate.DestinationType + "|" + candidate.Destination
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, candidate)
}

func routeCandidateFromRawValue(value string) (staticRouteCandidate, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return staticRouteCandidate{}, false
	}
	if ip := net.ParseIP(v); ip != nil {
		return staticRouteCandidate{
			DestinationType: staticRouteDestinationHost,
			Destination:     ip.String(),
			SourceHint:      v,
		}, true
	}
	if _, network, err := net.ParseCIDR(v); err == nil && network != nil {
		return staticRouteCandidate{
			DestinationType: staticRouteDestinationNetwork,
			Destination:     network.String(),
			SourceHint:      v,
		}, true
	}
	return staticRouteCandidate{}, false
}

func routeProbeTargetForCandidate(candidate staticRouteCandidate) string {
	if candidate.DestinationType == staticRouteDestinationHost {
		return candidate.Destination
	}
	_, network, err := net.ParseCIDR(candidate.Destination)
	if err != nil || network == nil {
		return ""
	}
	ip := network.IP
	if ip == nil {
		return ""
	}
	ones, bits := network.Mask.Size()
	if ones == bits {
		return ip.String()
	}
	if ip.To4() != nil {
		probe := incrementIP(ip.To4())
		if probe == nil {
			return ip.String()
		}
		return probe.String()
	}
	probe := incrementIP(ip.To16())
	if probe == nil {
		return ip.String()
	}
	return probe.String()
}

func resolveRouteSuggestionPath(candidate staticRouteCandidate, family string) (mode string, gateway string, gatewayZone string, iface string, err error) {
	target := routeProbeTargetForCandidate(candidate)
	if target == "" {
		return "", "", "", "", fmt.Errorf("failed_to_determine_probe_target")
	}

	args := []string{"-n"}
	if family == staticRouteFamilyINET6 {
		args = append(args, "-6")
	}
	args = append(args, "get", target)

	output, routeErr := staticRouteRunWithFIB(0, args...)
	if routeErr != nil {
		return "", "", "", "", routeErr
	}

	gateway = strings.TrimSpace(parseRouteGetField(output, "gateway:"))
	iface = strings.TrimSpace(parseRouteGetField(output, "interface:"))
	if gateway == "" || strings.HasPrefix(strings.ToLower(gateway), "link#") {
		if iface == "" {
			return "", "", "", "", fmt.Errorf("failed_to_resolve_route_interface")
		}
		return staticRouteNextHopInterface, "", "", iface, nil
	}

	if family == staticRouteFamilyINET6 {
		if separator := strings.LastIndex(gateway, "%"); separator > 0 && separator < len(gateway)-1 {
			gatewayZone = gateway[separator+1:]
			gateway = gateway[:separator]
		}
	}
	return staticRouteNextHopGateway, gateway, gatewayZone, "", nil
}

func fibFromPolicyRoutedNATEgressInterface(tx *gorm.DB, egressInterface string) (uint, error) {
	trimmed := strings.TrimSpace(egressInterface)
	if !strings.HasPrefix(trimmed, wireGuardClientInterfacePrefx) {
		return 0, nil
	}

	idText := strings.TrimPrefix(trimmed, wireGuardClientInterfacePrefx)
	id, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || id == 0 {
		return 0, staticRouteSuggestionUnavailable(fmt.Errorf("invalid_wireguard_client_egress_interface"))
	}

	var client networkModels.WireGuardClient
	if err := tx.Select("id,fib").First(&client, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, staticRouteSuggestionUnavailable(fmt.Errorf("wireguard_client_for_egress_interface_not_found"))
		}
		return 0, err
	}

	return client.FIB, nil
}

func candidatesFromNATSource(rule *networkModels.FirewallNATRule, family string, dynamicValues []string) []staticRouteCandidate {
	candidates := make([]staticRouteCandidate, 0)
	seen := map[string]struct{}{}

	if candidate, ok := routeCandidateFromRawValue(rule.SourceRaw); ok {
		addRouteCandidate(&candidates, seen, candidate, family)
	}

	if rule.SourceObj != nil {
		for _, entry := range rule.SourceObj.Entries {
			if candidate, ok := routeCandidateFromRawValue(entry.Value); ok {
				candidate.SourceHint = "object_entry:" + entry.Value
				addRouteCandidate(&candidates, seen, candidate, family)
			}
		}
		for _, value := range dynamicValues {
			if candidate, ok := routeCandidateFromRawValue(value); ok {
				candidate.SourceHint = "object_resolution:" + value
				addRouteCandidate(&candidates, seen, candidate, family)
			}
		}
	}

	return candidates
}

func (s *Service) SuggestStaticRoutesFromNATRule(ruleID uint) ([]networkServiceInterfaces.StaticRouteSuggestion, error) {
	if ruleID == 0 {
		return nil, invalidStaticRoutef("invalid_firewall_nat_rule_id")
	}

	var rule networkModels.FirewallNATRule
	err := s.DB.
		Preload("SourceObj.Entries").
		First(&rule, ruleID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, firewallNATRuleNotFound(err)
		}
		return nil, err
	}

	natType := normalizeNATType(rule.NATType)
	if natType != "snat" && natType != "binat" {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("nat_rule_must_be_snat_or_binat_for_route_suggestions"))
	}
	if !rule.PolicyRoutingEnabled {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("nat_rule_policy_routing_must_be_enabled_for_route_suggestions"))
	}
	egress := normalizeInterfaceList(rule.EgressInterfaces)
	if len(egress) != 1 {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("nat_rule_must_have_exactly_one_egress_interface_for_route_suggestions"))
	}
	family := normalizeStaticRouteFamily(rule.Family)
	if family == "" {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("nat_rule_family_must_be_inet_or_inet6_for_route_suggestions"))
	}

	dynamicValues := []string{}
	if rule.SourceObj != nil {
		valuesByID, err := s.loadDynamicValuesForObjects(map[uint]*networkModels.Object{
			rule.SourceObj.ID: rule.SourceObj,
		})
		if err != nil {
			return nil, err
		}
		dynamicValues = valuesByID[rule.SourceObj.ID]
	}

	candidates := candidatesFromNATSource(&rule, family, dynamicValues)
	if len(candidates) == 0 {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("failed_to_derive_route_suggestions_from_nat_source"))
	}

	fib, err := fibFromPolicyRoutedNATEgressInterface(s.DB, egress[0])
	if err != nil {
		return nil, err
	}
	if fib >= uint(getNetFIBCount()) {
		return nil, staticRouteSuggestionUnavailable(fmt.Errorf("wireguard_client_fib_is_out_of_range"))
	}

	suggestions := make([]networkServiceInterfaces.StaticRouteSuggestion, 0, len(candidates))
	pathErrors := make([]error, 0)
	for _, candidate := range candidates {
		nextHopMode, gateway, gatewayZone, iface, pathErr := resolveRouteSuggestionPath(candidate, family)
		if pathErr != nil {
			logger.L.Warn().
				Err(pathErr).
				Uint("nat_rule_id", rule.ID).
				Str("candidate", candidate.Destination).
				Msg("failed_to_resolve_route_suggestion_path")
			pathErrors = append(pathErrors, fmt.Errorf("candidate=%s: %w", candidate.Destination, pathErr))
			continue
		}

		suggestions = append(suggestions, networkServiceInterfaces.StaticRouteSuggestion{
			Name:            fmt.Sprintf("Return for NAT %d", rule.ID),
			Description:     fmt.Sprintf("Suggested return route for NAT rule %q", strings.TrimSpace(rule.Name)),
			Enabled:         true,
			FIB:             fib,
			DestinationType: candidate.DestinationType,
			Destination:     candidate.Destination,
			Family:          family,
			NextHopMode:     nextHopMode,
			Gateway:         gateway,
			GatewayZone:     gatewayZone,
			Interface:       iface,
			SourceHint:      candidate.SourceHint,
		})
	}

	if len(suggestions) == 0 {
		return nil, fmt.Errorf("failed_to_build_any_route_suggestions_from_nat_rule: %w", errors.Join(pathErrors...))
	}

	return suggestions, nil
}
