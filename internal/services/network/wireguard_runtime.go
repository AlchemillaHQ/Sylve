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
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

const (
	wireGuardServerInterfaceName  = "wgs0"
	wireGuardClientInterfacePrefx = "wgc"
)

var (
	wireGuardRunCommand          = utils.RunCommand
	wireGuardLookupIP            = net.LookupIP
	wireGuardResolveUDP          = net.ResolveUDPAddr
	wireGuardNewWGClient         = wgctrl.New
	wireGuardConfigureWithWGCtrl = configureWireGuardDeviceWithWGCtrl
	wireGuardResolveWGBinaryPath = resolveWireGuardBinaryPath
	wireGuardListInterfaces      = net.Interfaces
	wireGuardStat                = os.Stat
	wireGuardLookPath            = exec.LookPath
	wireGuardCurrentTime         = wireGuardNow
	wireGuardRuntimeOS           = runtime.GOOS
)

func wireGuardClientInterfaceName(id uint) string {
	return fmt.Sprintf("%s%d", wireGuardClientInterfacePrefx, id)
}

func (s *Service) isWireGuardServiceEnabled() bool {
	var basic models.BasicSettings
	if err := s.DB.First(&basic).Error; err != nil {
		return false
	}

	for _, service := range basic.Services {
		if service == models.WireGuard {
			return true
		}
	}

	return false
}

func (s *Service) isWireGuardServerInitialized() (bool, error) {
	var count int64

	err := s.DB.
		Model(&networkModels.WireGuardServer{}).
		Where("private_key <> ''").
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Service) requireWireGuardServiceEnabled() error {
	if !s.isWireGuardServiceEnabled() {
		return ErrWireGuardServiceDisabled
	}

	return nil
}

func loadWireGuardKernelModule() error {
	if _, err := wireGuardRunCommand("/sbin/kldstat", "-m", "if_wg"); err == nil {
		return nil
	}

	if _, err := wireGuardRunCommand("/sbin/kldload", "-n", "if_wg"); err != nil {
		return fmt.Errorf("failed_to_load_if_wg_kernel_module: %w", err)
	}

	return nil
}

func wireGuardGeneratePrivateKey() (string, error) {
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return "", fmt.Errorf("failed_to_generate_wireguard_private_key: %w", err)
	}

	return privateKey.String(), nil
}

func wireGuardPublicKeyFromPrivate(privateKey string) (string, error) {
	parsed, err := wgtypes.ParseKey(strings.TrimSpace(privateKey))
	if err != nil {
		return "", fmt.Errorf("invalid_wireguard_private_key: %w", err)
	}

	return parsed.PublicKey().String(), nil
}

func validateWireGuardPSK(psk string) error {
	trimmed := strings.TrimSpace(psk)
	if trimmed == "" {
		return nil
	}

	if _, err := wgtypes.ParseKey(trimmed); err != nil {
		return fmt.Errorf("invalid_wireguard_psk: %w", err)
	}

	return nil
}

func parseWireGuardCIDRs(cidrs []string) error {
	for _, cidr := range cidrs {
		if strings.TrimSpace(cidr) == "" {
			return fmt.Errorf("invalid_wireguard_cidr")
		}

		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid_wireguard_cidr: %s", cidr)
		}
	}

	return nil
}

func parseAllowedIPs(cidrs []string) ([]net.IPNet, error) {
	if err := parseWireGuardCIDRs(cidrs); err != nil {
		return nil, err
	}

	allowed := make([]net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid_wireguard_cidr: %s", cidr)
		}
		allowed = append(allowed, *network)
	}

	return allowed, nil
}

func firstUsableIPFromCIDR(cidr string) (string, int, error) {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return "", 0, fmt.Errorf("invalid_wireguard_cidr: %s", cidr)
	}

	ones, bits := network.Mask.Size()
	hostIP := ip

	if ip.Equal(network.IP) && ones < bits {
		hostIP = incrementIP(network.IP)
	}

	if hostIP == nil {
		return "", 0, fmt.Errorf("failed_to_resolve_wireguard_host_ip")
	}

	return fmt.Sprintf("%s/%d", hostIP.String(), ones), bits, nil
}

func incrementIP(ip net.IP) net.IP {
	if ip4 := ip.To4(); ip4 != nil {
		out := append(net.IP(nil), ip4...)
		for i := len(out) - 1; i >= 0; i-- {
			out[i]++
			if out[i] != 0 {
				break
			}
		}
		return out
	}

	ip16 := ip.To16()
	if ip16 == nil {
		return nil
	}

	out := append(net.IP(nil), ip16...)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

func wireGuardInterfaceExists(name string) bool {
	exists, err := wireGuardInterfaceExistsNativeOrShell(name)
	if err != nil {
		logger.L.Debug().Err(err).Str("interface", name).Msg("failed to check wireguard interface state")
		return false
	}

	return exists
}

func ensureWireGuardInterface(name string) error {
	exists, err := wireGuardInterfaceExistsNativeOrShell(name)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	created, err := wireGuardCreateInterfaceNativeOrShell("wg")
	if err != nil {
		return err
	}

	if created != name {
		if err := wireGuardRenameInterfaceNativeOrShell(created, name); err != nil {
			return err
		}
	}

	return nil
}

func destroyWireGuardInterface(name string) error {
	exists, err := wireGuardInterfaceExistsNativeOrShell(name)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	if err := wireGuardDestroyInterfaceNativeOrShell(name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return nil
		}
		return err
	}

	return nil
}

func configureWireGuardInterface(name string, addresses []string, mtu uint, metric uint, fib uint) error {
	// Apply FIB before assigning addresses so connected/interface routes are
	// created in the intended routing table for policy-routed clients.
	if fib > 0 {
		if err := wireGuardSetFIBNativeOrShell(name, fib); err != nil {
			return fmt.Errorf("failed_to_set_wireguard_fib: %w", err)
		}
	}

	for _, cidr := range addresses {
		hostCIDR, bits, err := firstUsableIPFromCIDR(cidr)
		if err != nil {
			return err
		}

		if bits == 32 {
			if err := wireGuardAddAddressNativeOrShell(name, hostCIDR, false); err != nil {
				return fmt.Errorf("failed_to_set_wireguard_ipv4_address iface=%s addr=%s: %w", name, hostCIDR, err)
			}
		} else {
			if err := wireGuardAddAddressNativeOrShell(name, hostCIDR, true); err != nil {
				return fmt.Errorf("failed_to_set_wireguard_ipv6_address iface=%s addr=%s: %w", name, hostCIDR, err)
			}
		}
	}

	if mtu > 0 {
		if err := wireGuardSetMTUNativeOrShell(name, mtu); err != nil {
			return fmt.Errorf("failed_to_set_wireguard_mtu: %w", err)
		}
	}

	if metric > 0 {
		if err := wireGuardSetMetricNativeOrShell(name, metric); err != nil {
			return fmt.Errorf("failed_to_set_wireguard_metric: %w", err)
		}
	}

	if err := wireGuardSetUpNativeOrShell(name); err != nil {
		return fmt.Errorf("failed_to_set_wireguard_interface_up: %w", err)
	}

	return nil
}

func configureWireGuardDevice(interfaceName string, privateKey string, listenPort uint, peers []wgtypes.PeerConfig) error {
	parsedPrivateKey, err := wgtypes.ParseKey(strings.TrimSpace(privateKey))
	if err != nil {
		return fmt.Errorf("invalid_wireguard_private_key: %w", err)
	}

	cfg := wgtypes.Config{
		PrivateKey:   &parsedPrivateKey,
		ReplacePeers: true,
		Peers:        peers,
	}

	if listenPort > 0 {
		port := int(listenPort)
		cfg.ListenPort = &port
	}

	if wireGuardRuntimeOS == "freebsd" {
		setconfErr := configureWireGuardDeviceWithSetconf(interfaceName, parsedPrivateKey, listenPort, peers)
		if setconfErr == nil {
			return nil
		}
		return fmt.Errorf(
			"failed_to_configure_wireguard_device_%s: wg_setconf_error=%v",
			interfaceName,
			setconfErr,
		)
	}

	wgctrlErr := wireGuardConfigureWithWGCtrl(interfaceName, cfg)
	if wgctrlErr == nil {
		return nil
	}

	setconfErr := configureWireGuardDeviceWithSetconf(interfaceName, parsedPrivateKey, listenPort, peers)
	if setconfErr == nil {
		logger.L.Warn().
			Err(wgctrlErr).
			Str("interface", interfaceName).
			Msg("wgctrl configure failed; wireguard fallback via wg setconf succeeded")
		return nil
	}

	return fmt.Errorf(
		"failed_to_configure_wireguard_device_%s: wgctrl_error=%v; wg_setconf_error=%v",
		interfaceName,
		wgctrlErr,
		setconfErr,
	)
}

func configureWireGuardDeviceWithWGCtrl(interfaceName string, cfg wgtypes.Config) error {
	client, err := wireGuardNewWGClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return client.ConfigureDevice(interfaceName, cfg)
}

func resolveWireGuardBinaryPath() (string, error) {
	candidates := []string{
		"/usr/bin/wg",
		"/usr/sbin/wg",
		"/usr/local/bin/wg",
	}

	for _, candidate := range candidates {
		info, err := wireGuardStat(candidate)
		if err != nil || info == nil {
			continue
		}
		if info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	path, err := wireGuardLookPath("wg")
	if err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}

	return "", fmt.Errorf("wireguard_wg_binary_not_found")
}

func buildWireGuardSetconfConfig(privateKey wgtypes.Key, listenPort uint, peers []wgtypes.PeerConfig) string {
	var cfg strings.Builder

	cfg.WriteString("[Interface]\n")
	cfg.WriteString(fmt.Sprintf("PrivateKey = %s\n", privateKey.String()))
	if listenPort > 0 {
		cfg.WriteString(fmt.Sprintf("ListenPort = %d\n", listenPort))
	}

	for _, peer := range peers {
		if peer.Remove {
			continue
		}

		cfg.WriteString("\n[Peer]\n")
		cfg.WriteString(fmt.Sprintf("PublicKey = %s\n", peer.PublicKey.String()))

		if peer.PresharedKey != nil {
			cfg.WriteString(fmt.Sprintf("PresharedKey = %s\n", peer.PresharedKey.String()))
		}

		if peer.Endpoint != nil {
			cfg.WriteString(fmt.Sprintf("Endpoint = %s\n", peer.Endpoint.String()))
		}

		if len(peer.AllowedIPs) > 0 {
			allowedIPs := make([]string, 0, len(peer.AllowedIPs))
			for _, allowed := range peer.AllowedIPs {
				allowedIPs = append(allowedIPs, allowed.String())
			}
			sort.Strings(allowedIPs)
			cfg.WriteString(fmt.Sprintf("AllowedIPs = %s\n", strings.Join(allowedIPs, ", ")))
		}

		if peer.PersistentKeepaliveInterval != nil {
			keepaliveSeconds := int(peer.PersistentKeepaliveInterval.Seconds())
			if keepaliveSeconds < 0 {
				keepaliveSeconds = 0
			}
			cfg.WriteString(fmt.Sprintf("PersistentKeepalive = %d\n", keepaliveSeconds))
		}
	}

	return cfg.String()
}

func configureWireGuardDeviceWithSetconf(interfaceName string, privateKey wgtypes.Key, listenPort uint, peers []wgtypes.PeerConfig) error {
	wgBinary, err := wireGuardResolveWGBinaryPath()
	if err != nil {
		return fmt.Errorf("failed_to_resolve_wireguard_binary: %w", err)
	}

	configContent := buildWireGuardSetconfConfig(privateKey, listenPort, peers)

	tempFile, err := os.CreateTemp("", "sylve-wireguard-*.conf")
	if err != nil {
		return fmt.Errorf("failed_to_create_wireguard_temp_config: %w", err)
	}

	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed_to_secure_wireguard_temp_config: %w", err)
	}

	if _, err := tempFile.WriteString(configContent); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed_to_write_wireguard_temp_config: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed_to_close_wireguard_temp_config: %w", err)
	}

	if _, err := wireGuardRunCommand(wgBinary, "setconf", interfaceName, tempPath); err != nil {
		return fmt.Errorf("failed_to_apply_wireguard_setconf: %w", err)
	}

	return nil
}

func wireGuardRunRoute(fib uint, routeArgs ...string) (string, error) {
	if fib > 0 {
		args := make([]string, 0, len(routeArgs)+3)
		args = append(args, "-F", strconv.FormatUint(uint64(fib), 10), "/sbin/route")
		args = append(args, routeArgs...)
		return wireGuardRunCommand("/usr/sbin/setfib", args...)
	}
	return wireGuardRunCommand("/sbin/route", routeArgs...)
}

func isRouteNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "route has not been found") ||
		strings.Contains(lower, "not in table") ||
		strings.Contains(lower, "network is unreachable")
}

func isRouteAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "file exists") || strings.Contains(lower, "already in table")
}

func isRouteInvalidArgumentError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "invalid argument")
}

func addRouteViaInterface(cidr string, iface string, fib uint) error {
	args := []string{"-n"}
	if strings.Contains(cidr, ":") {
		args = append(args, "-6")
	}
	args = append(args, "add", "-net", cidr, "-iface", iface)

	if _, err := wireGuardRunRoute(fib, args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "file exists") || strings.Contains(lower, "already in table") {
			return nil
		}
		return fmt.Errorf("failed_to_add_wireguard_route_%s: %w", cidr, err)
	}

	return nil
}

func hasDefaultWireGuardRouteCIDR(cidrs []string) bool {
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil || network == nil {
			continue
		}

		ones, bits := network.Mask.Size()
		if ones == 0 && (bits == 32 || bits == 128) {
			return true
		}
	}

	return false
}

func expandedWireGuardRouteCIDRs(cidrs []string) []string {
	if len(cidrs) == 0 {
		return []string{}
	}

	expanded := make([]string, 0, len(cidrs)+2)
	for _, cidr := range cidrs {
		trimmed := strings.TrimSpace(cidr)
		_, network, err := net.ParseCIDR(trimmed)
		if err != nil || network == nil {
			expanded = append(expanded, trimmed)
			continue
		}

		ones, bits := network.Mask.Size()
		switch {
		case ones == 0 && bits == 32:
			expanded = append(expanded, "0.0.0.0/1", "128.0.0.0/1")
		case ones == 0 && bits == 128:
			expanded = append(expanded, "::/1", "8000::/1")
		default:
			expanded = append(expanded, trimmed)
		}
	}

	return sortedUnique(expanded)
}

func addEndpointHostRoute(endpointIP string, fib uint) error {
	target := strings.TrimSpace(endpointIP)
	if ip := net.ParseIP(target); ip == nil {
		return fmt.Errorf("invalid_wireguard_endpoint_ip: %s", endpointIP)
	}

	args := []string{"-n"}
	if strings.Contains(target, ":") {
		args = append(args, "-6")
	}

	getArgs := append(append([]string{}, args...), "get", target)
	output, err := wireGuardRunRoute(fib, getArgs...)
	if err != nil && fib > 0 && isRouteNotFoundError(err) {
		// In policy-routing setups, the endpoint can be resolvable only in fib 0
		// during bootstrap. Resolve in fib 0 and still install the endpoint host
		// route in the target client fib.
		output, err = wireGuardRunRoute(0, getArgs...)
	}
	if err != nil {
		return fmt.Errorf("failed_to_get_wireguard_endpoint_route_%s: %w", target, err)
	}

	gateway := parseRouteGetField(output, "gateway:")
	iface := parseRouteGetField(output, "interface:")

	addArgs := append(append([]string{}, args...), "add", "-host", target)
	usingGateway := gateway != "" && !strings.HasPrefix(strings.ToLower(gateway), "link#")
	if usingGateway {
		addArgs = append(addArgs, gateway)
	} else {
		if iface == "" {
			return fmt.Errorf("failed_to_resolve_wireguard_endpoint_route_target_%s", target)
		}
		addArgs = append(addArgs, "-iface", iface)
	}

	if _, err := wireGuardRunRoute(fib, addArgs...); err != nil {
		if isRouteAlreadyExistsError(err) {
			return nil
		}

		// In non-default FIBs, the resolved upstream gateway might not yet have
		// a direct host route in that FIB. Prime it via the resolved egress
		// interface and retry the endpoint host route once.
		if fib > 0 && usingGateway && iface != "" && net.ParseIP(gateway) != nil && isRouteInvalidArgumentError(err) {
			gatewayArgs := []string{"-n"}
			if strings.Contains(gateway, ":") {
				gatewayArgs = append(gatewayArgs, "-6")
			}
			gatewayArgs = append(gatewayArgs, "add", "-host", gateway, "-iface", iface)

			if _, gwErr := wireGuardRunRoute(fib, gatewayArgs...); gwErr != nil && !isRouteAlreadyExistsError(gwErr) {
				return fmt.Errorf("failed_to_add_wireguard_gateway_route_%s: %w", gateway, gwErr)
			}

			if _, retryErr := wireGuardRunRoute(fib, addArgs...); retryErr == nil || isRouteAlreadyExistsError(retryErr) {
				return nil
			} else {
				return fmt.Errorf("failed_to_add_wireguard_endpoint_route_%s: %w", target, retryErr)
			}
		}

		return fmt.Errorf("failed_to_add_wireguard_endpoint_route_%s: %w", target, err)
	}

	return nil
}

func deleteEndpointHostRoute(endpointIP string, fib uint) error {
	target := strings.TrimSpace(endpointIP)
	if ip := net.ParseIP(target); ip == nil {
		return fmt.Errorf("invalid_wireguard_endpoint_ip: %s", endpointIP)
	}

	args := []string{"-n"}
	if strings.Contains(target, ":") {
		args = append(args, "-6")
	}
	args = append(args, "delete", "-host", target)

	if _, err := wireGuardRunRoute(fib, args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not in table") || strings.Contains(lower, "no such process") {
			return nil
		}
		return fmt.Errorf("failed_to_delete_wireguard_endpoint_route_%s: %w", target, err)
	}

	return nil
}

func parseRouteGetField(output string, key string) string {
	lowerKey := strings.ToLower(key)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), lowerKey) {
			continue
		}

		value := strings.TrimSpace(trimmed[len(key):])
		if value != "" {
			return value
		}

		parts := strings.Fields(trimmed)
		if len(parts) >= 2 {
			return strings.TrimSpace(parts[1])
		}
	}

	return ""
}

func deleteRouteViaInterface(cidr string, iface string, fib uint) error {
	args := []string{"-n"}
	if strings.Contains(cidr, ":") {
		args = append(args, "-6")
	}
	args = append(args, "delete", "-net", cidr, "-iface", iface)

	if _, err := wireGuardRunRoute(fib, args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not in table") || strings.Contains(lower, "no such process") {
			return nil
		}
		return fmt.Errorf("failed_to_delete_wireguard_route_%s: %w", cidr, err)
	}

	return nil
}

func readWireGuardDevice(iface string) (*wgtypes.Device, error) {
	client, err := wireGuardNewWGClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	return client.Device(iface)
}

func (s *Service) EnableWireGuardService(ctx context.Context) error {
	if err := loadWireGuardKernelModule(); err != nil {
		return err
	}

	if err := s.syncWireGuardRuntime(); err != nil {
		return err
	}

	s.StartWireGuardMonitor(ctx)
	return nil
}

func (s *Service) DisableWireGuardService(_ context.Context) error {
	s.stopWireGuardMonitor()
	return s.teardownWireGuardRuntime()
}

func (s *Service) syncWireGuardRuntime() error {
	managedIfaces, err := listManagedWireGuardInterfaces()
	if err != nil {
		return err
	}

	for _, iface := range managedIfaces {
		if err := destroyWireGuardInterface(iface); err != nil {
			logger.L.Warn().Err(err).Str("interface", iface).Msg("failed to cleanup managed wireguard interface before runtime sync")
		}
	}

	var server networkModels.WireGuardServer
	err = s.DB.Preload("Peers").First(&server).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	} else {
		if server.Enabled {
			if err := s.applyWireGuardServerRuntime(&server); err != nil {
				return err
			}
		} else {
			if err := s.teardownWireGuardServerRuntime(&server); err != nil {
				return err
			}
		}
	}

	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		return err
	}

	for i := range clients {
		client := clients[i]
		if client.Enabled {
			if err := s.applyWireGuardClientRuntime(&client); err != nil {
				return err
			}
		} else {
			if err := s.teardownWireGuardClientRuntime(&client); err != nil {
				return err
			}
		}
	}

	return nil
}

func listManagedWireGuardInterfaces() ([]string, error) {
	ifaces, err := wireGuardListInterfaces()
	if err != nil {
		return nil, err
	}

	managed := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		name := strings.TrimSpace(iface.Name)
		if strings.HasPrefix(name, "wg") {
			managed = append(managed, name)
		}
	}

	sort.Strings(managed)
	return managed, nil
}

func (s *Service) teardownWireGuardRuntime() error {
	var clients []networkModels.WireGuardClient
	if err := s.DB.Find(&clients).Error; err != nil {
		return err
	}

	for i := range clients {
		if err := s.teardownWireGuardClientRuntime(&clients[i]); err != nil {
			logger.L.Warn().Err(err).Msg("failed to teardown wireguard client runtime")
		}
	}

	var server networkModels.WireGuardServer
	if err := s.DB.Preload("Peers").First(&server).Error; err == nil {
		if err := s.teardownWireGuardServerRuntime(&server); err != nil {
			return err
		}
	}

	return nil
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	sort.Strings(out)
	return out
}

func endpointToHostPort(host string, port uint) (string, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return "", ErrWireGuardEndpointHostRequired
	}

	if port == 0 {
		return "", ErrWireGuardEndpointPortInvalid
	}

	return net.JoinHostPort(h, strconv.Itoa(int(port))), nil
}
