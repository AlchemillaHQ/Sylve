// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
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
	staticRouteRunCommand = utils.RunCommand
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
	output, err := staticRouteRunCommand("/sbin/sysctl", "-n", "net.fibs")
	if err != nil {
		return 1
	}

	count, parseErr := strconv.Atoi(strings.TrimSpace(output))
	if parseErr != nil || count <= 0 {
		return 1
	}

	return count
}

func validateStaticRouteRequest(req *networkServiceInterfaces.UpsertStaticRouteRequest) (networkModels.StaticRoute, error) {
	if req == nil {
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_request")
	}

	route := networkModels.StaticRoute{
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		DestinationType: normalizeStaticRouteDestinationType(req.DestinationType),
		Destination:     strings.TrimSpace(req.Destination),
		Family:          normalizeStaticRouteFamily(req.Family),
		NextHopMode:     normalizeStaticRouteNextHopMode(req.NextHopMode),
		Gateway:         strings.TrimSpace(req.Gateway),
		Interface:       strings.TrimSpace(req.Interface),
	}

	if req.Enabled == nil {
		route.Enabled = true
	} else {
		route.Enabled = *req.Enabled
	}
	if req.FIB == nil {
		route.FIB = 0
	} else {
		route.FIB = *req.FIB
	}

	if route.Name == "" {
		return networkModels.StaticRoute{}, fmt.Errorf("route_name_required")
	}
	if route.DestinationType == "" {
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_destination_type")
	}
	if route.Family == "" {
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_family")
	}
	if route.NextHopMode == "" {
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_next_hop_mode")
	}

	fibs := getNetFIBCount()
	if int(route.FIB) >= fibs {
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_fib: fib=%d valid_range=0..%d", route.FIB, fibs-1)
	}

	switch route.DestinationType {
	case staticRouteDestinationHost:
		if strings.Contains(route.Destination, "/") {
			return networkModels.StaticRoute{}, fmt.Errorf("host_destination_must_not_contain_cidr")
		}
		ip := net.ParseIP(route.Destination)
		if ip == nil {
			return networkModels.StaticRoute{}, fmt.Errorf("invalid_host_destination")
		}
		if !staticRouteIPMatchesFamily(ip, route.Family) {
			return networkModels.StaticRoute{}, fmt.Errorf("destination_family_mismatch")
		}
		route.Destination = ip.String()
	case staticRouteDestinationNetwork:
		_, network, err := net.ParseCIDR(route.Destination)
		if err != nil || network == nil {
			return networkModels.StaticRoute{}, fmt.Errorf("invalid_network_destination")
		}
		if !staticRouteNetworkMatchesFamily(network, route.Family) {
			return networkModels.StaticRoute{}, fmt.Errorf("destination_family_mismatch")
		}
		route.Destination = network.String()
	default:
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_destination_type")
	}

	switch route.NextHopMode {
	case staticRouteNextHopGateway:
		if route.Gateway == "" {
			return networkModels.StaticRoute{}, fmt.Errorf("route_gateway_required_for_next_hop_mode_gateway")
		}
		if route.Interface != "" {
			return networkModels.StaticRoute{}, fmt.Errorf("route_interface_not_allowed_for_next_hop_mode_gateway")
		}

		gw := net.ParseIP(route.Gateway)
		if gw == nil {
			return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_gateway")
		}
		if !staticRouteIPMatchesFamily(gw, route.Family) {
			return networkModels.StaticRoute{}, fmt.Errorf("gateway_family_mismatch")
		}
		route.Gateway = gw.String()
	case staticRouteNextHopInterface:
		if route.Interface == "" {
			return networkModels.StaticRoute{}, fmt.Errorf("route_interface_required_for_next_hop_mode_interface")
		}
		if route.Gateway != "" {
			return networkModels.StaticRoute{}, fmt.Errorf("route_gateway_not_allowed_for_next_hop_mode_interface")
		}
	default:
		return networkModels.StaticRoute{}, fmt.Errorf("invalid_route_next_hop_mode")
	}

	return route, nil
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
		args = append(args, route.Gateway)
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

func addManagedRoute(route *networkModels.StaticRoute) error {
	args := staticRouteCommandArgs(route, "add")
	if _, err := staticRouteRunWithFIB(route.FIB, args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "file exists") || strings.Contains(lower, "already in table") {
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
		a.Interface == b.Interface
}

func applyStaticRouteDiff(current, next *networkModels.StaticRoute) error {
	if current == nil || next == nil {
		return fmt.Errorf("invalid_route_diff")
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
				if rollbackErr := addManagedRoute(current); rollbackErr != nil {
					logger.L.Error().
						Err(rollbackErr).
						Uint("route_id", current.ID).
						Msg("failed_to_restore_previous_static_route_after_apply_failure")
				}
			}
			return fmt.Errorf("failed_to_apply_updated_static_route: %w", err)
		}
	}

	return nil
}

func (s *Service) GetStaticRoutes() ([]networkModels.StaticRoute, error) {
	var routes []networkModels.StaticRoute
	if err := s.DB.Order("id asc").Find(&routes).Error; err != nil {
		return nil, err
	}
	return routes, nil
}

func (s *Service) CreateStaticRoute(req *networkServiceInterfaces.UpsertStaticRouteRequest) (uint, error) {
	route, err := validateStaticRouteRequest(req)
	if err != nil {
		return 0, err
	}

	if txErr := s.DB.Transaction(func(tx *gorm.DB) error {
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
	normalized, err := validateStaticRouteRequest(req)
	if err != nil {
		return err
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var current networkModels.StaticRoute
		if err := tx.First(&current, id).Error; err != nil {
			return err
		}

		next := current
		next.Name = normalized.Name
		next.Description = normalized.Description
		next.Enabled = normalized.Enabled
		next.FIB = normalized.FIB
		next.DestinationType = normalized.DestinationType
		next.Destination = normalized.Destination
		next.Family = normalized.Family
		next.NextHopMode = normalized.NextHopMode
		next.Gateway = normalized.Gateway
		next.Interface = normalized.Interface

		if err := applyStaticRouteDiff(&current, &next); err != nil {
			return err
		}

		if err := tx.Save(&next).Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *Service) DeleteStaticRoute(id uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var route networkModels.StaticRoute
		if err := tx.First(&route, id).Error; err != nil {
			return err
		}

		if route.Enabled {
			if err := deleteManagedRoute(&route); err != nil {
				return fmt.Errorf("failed_to_remove_static_route: %w", err)
			}
		}

		if err := tx.Delete(&route).Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *Service) ReconcileManagedRoutes() error {
	var routes []networkModels.StaticRoute
	if err := s.DB.Order("id asc").Find(&routes).Error; err != nil {
		return err
	}

	var errs []string
	for _, route := range routes {
		var err error
		if route.Enabled {
			err = addManagedRoute(&route)
		} else {
			err = deleteManagedRoute(&route)
		}
		if err != nil {
			logger.L.Error().
				Err(err).
				Uint("route_id", route.ID).
				Str("name", route.Name).
				Msg("failed_to_reconcile_managed_route")
			errs = append(errs, fmt.Sprintf("route=%d: %v", route.ID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("managed_route_reconcile_failed: %s", strings.Join(errs, "; "))
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
	key := candidate.DestinationType + "|" + candidate.Destination + "|" + candidate.SourceHint
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

func resolveRouteSuggestionPath(candidate staticRouteCandidate, family string) (mode string, gateway string, iface string, err error) {
	target := routeProbeTargetForCandidate(candidate)
	if target == "" {
		return "", "", "", fmt.Errorf("failed_to_determine_probe_target")
	}

	args := []string{"-n"}
	if family == staticRouteFamilyINET6 {
		args = append(args, "-6")
	}
	args = append(args, "get", target)

	output, routeErr := staticRouteRunWithFIB(0, args...)
	if routeErr != nil {
		return "", "", "", routeErr
	}

	gateway = strings.TrimSpace(parseRouteGetField(output, "gateway:"))
	iface = strings.TrimSpace(parseRouteGetField(output, "interface:"))
	if gateway == "" || strings.HasPrefix(strings.ToLower(gateway), "link#") {
		if iface == "" {
			return "", "", "", fmt.Errorf("failed_to_resolve_route_interface")
		}
		return staticRouteNextHopInterface, "", iface, nil
	}
	return staticRouteNextHopGateway, gateway, iface, nil
}

func fibFromPolicyRoutedNATEgressInterface(tx *gorm.DB, egressInterface string) uint {
	trimmed := strings.TrimSpace(egressInterface)
	if !strings.HasPrefix(trimmed, wireGuardClientInterfacePrefx) {
		return 0
	}

	idText := strings.TrimPrefix(trimmed, wireGuardClientInterfacePrefx)
	id, err := strconv.ParseUint(idText, 10, 64)
	if err != nil {
		return 0
	}

	var client networkModels.WireGuardClient
	if err := tx.Select("id,fib").First(&client, uint(id)).Error; err != nil {
		return 0
	}

	return client.FIB
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
	var rule networkModels.FirewallNATRule
	err := s.DB.
		Preload("SourceObj.Entries").
		First(&rule, ruleID).Error
	if err != nil {
		return nil, err
	}

	natType := normalizeNATType(rule.NATType)
	if natType != "snat" && natType != "binat" {
		return nil, fmt.Errorf("nat_rule_must_be_snat_or_binat_for_route_suggestions")
	}
	if !rule.PolicyRoutingEnabled {
		return nil, fmt.Errorf("nat_rule_policy_routing_must_be_enabled_for_route_suggestions")
	}
	egress := normalizeInterfaceList(rule.EgressInterfaces)
	if len(egress) != 1 {
		return nil, fmt.Errorf("nat_rule_must_have_exactly_one_egress_interface_for_route_suggestions")
	}
	family := normalizeStaticRouteFamily(rule.Family)
	if family == "" {
		return nil, fmt.Errorf("nat_rule_family_must_be_inet_or_inet6_for_route_suggestions")
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
		return nil, fmt.Errorf("failed_to_derive_route_suggestions_from_nat_source")
	}

	fib := fibFromPolicyRoutedNATEgressInterface(s.DB, egress[0])
	suggestions := make([]networkServiceInterfaces.StaticRouteSuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		nextHopMode, gateway, iface, pathErr := resolveRouteSuggestionPath(candidate, family)
		if pathErr != nil {
			logger.L.Warn().
				Err(pathErr).
				Uint("nat_rule_id", rule.ID).
				Str("candidate", candidate.Destination).
				Msg("failed_to_resolve_route_suggestion_path")
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
			Interface:       iface,
			SourceHint:      candidate.SourceHint,
		})
	}

	if len(suggestions) == 0 {
		return nil, fmt.Errorf("failed_to_build_any_route_suggestions_from_nat_rule")
	}

	return suggestions, nil
}
