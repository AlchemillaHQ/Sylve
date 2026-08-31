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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	dhclientStopTimeout      = 2 * time.Second
	dhclientStopPollInterval = 50 * time.Millisecond
)

var (
	dhclientRuntimeDir       = "/var/run/dhclient"
	dhclientSystemConfigPath = "/etc/dhclient.conf"
	dhclientNaturalExitGrace = 2 * time.Second
)

func desiredStandardSwitchMAC(sw networkModels.StandardSwitch) (string, error) {
	switch sw.BridgeMACMode {
	case networkModels.StandardSwitchMACModePort:
		sourceSelected := false
		for _, port := range sw.Ports {
			if port.Name == sw.BridgeMACSourcePort {
				sourceSelected = true
				break
			}
		}
		if !sourceSelected {
			return "", fmt.Errorf("bridge MAC source port %q is not selected", sw.BridgeMACSourcePort)
		}

		interfaceObj, err := syncIfaceGet(sw.BridgeMACSourcePort)
		if err != nil {
			return "", fmt.Errorf("inspect bridge MAC source port %q: %w", sw.BridgeMACSourcePort, err)
		}
		if interfaceObj == nil {
			return "", fmt.Errorf("bridge MAC source port %q not found", sw.BridgeMACSourcePort)
		}
		mac := interfaceObj.Ether
		if strings.TrimSpace(mac) == "" {
			mac = interfaceObj.HWAddr
		}
		normalized, err := normalizeStandardSwitchMAC(mac)
		if err != nil {
			return "", fmt.Errorf("invalid MAC on source port %q: %w", sw.BridgeMACSourcePort, err)
		}
		return normalized, nil

	case networkModels.StandardSwitchMACModeObject:
		if sw.BridgeMACObjectID == nil || *sw.BridgeMACObjectID == 0 {
			return "", fmt.Errorf("bridge MAC object is missing")
		}
		if sw.BridgeMACObject == nil || sw.BridgeMACObject.ID != *sw.BridgeMACObjectID {
			return "", fmt.Errorf("bridge MAC object %d is not loaded", *sw.BridgeMACObjectID)
		}
		if sw.BridgeMACObject.Type != "Mac" || len(sw.BridgeMACObject.Entries) != 1 {
			return "", fmt.Errorf("bridge MAC object %d must contain exactly one MAC", *sw.BridgeMACObjectID)
		}
		normalized, err := normalizeStandardSwitchMAC(sw.BridgeMACObject.Entries[0].Value)
		if err != nil {
			return "", fmt.Errorf("invalid bridge MAC object %d: %w", *sw.BridgeMACObjectID, err)
		}
		return normalized, nil

	default:
		return "", fmt.Errorf("invalid bridge MAC source mode %q", sw.BridgeMACMode)
	}
}

func currentInterfaceMAC(interfaceObj *iface.Interface) (string, error) {
	if interfaceObj == nil {
		return "", fmt.Errorf("interface not found")
	}
	mac := interfaceObj.Ether
	if strings.TrimSpace(mac) == "" {
		mac = interfaceObj.HWAddr
	}
	return normalizeStandardSwitchMAC(mac)
}

func applyStandardSwitchMAC(sw networkModels.StandardSwitch) (bool, error) {
	desired, err := desiredStandardSwitchMAC(sw)
	if err != nil {
		return false, err
	}

	currentInterface, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		return false, fmt.Errorf("inspect bridge %q MAC: %w", sw.BridgeName, err)
	}
	current, currentErr := currentInterfaceMAC(currentInterface)
	if currentErr == nil && current == desired {
		return false, nil
	}

	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "ether", desired); err != nil {
		return false, fmt.Errorf("set bridge %q MAC to %s: %w", sw.BridgeName, desired, err)
	}
	verifiedInterface, err := syncIfaceGet(sw.BridgeName)
	if err != nil {
		return false, fmt.Errorf("verify bridge %q MAC: %w", sw.BridgeName, err)
	}
	verified, err := currentInterfaceMAC(verifiedInterface)
	if err != nil || verified != desired {
		return false, fmt.Errorf("verify bridge %q MAC: got %q, want %q", sw.BridgeName, verified, desired)
	}
	return true, nil
}

const dhclientNoDefaultRouteConfig = "ignore routers, classless-routes;\n"

func desiredDhclientRoutePolicy() ([]byte, error) {
	systemConfig, err := os.ReadFile(dhclientSystemConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	config := make([]byte, 0, len(dhclientNoDefaultRouteConfig)+len(systemConfig))
	config = append(config, dhclientNoDefaultRouteConfig...)
	config = append(config, systemConfig...)
	return config, nil
}

func dhclientPIDPath(br string) string {
	return filepath.Join(dhclientRuntimeDir, "dhclient."+br+".pid")
}

func dhclientConfigPath(br string) string {
	return filepath.Join(dhclientRuntimeDir, "dhclient."+br+".no-default.conf")
}

func dhclientRoutePolicyMatches(br string, useDefaultRoute bool) (bool, error) {
	contents, err := os.ReadFile(dhclientConfigPath(br))
	if errors.Is(err, os.ErrNotExist) {
		return useDefaultRoute, nil
	}
	if err != nil {
		return false, err
	}
	if useDefaultRoute {
		return false, nil
	}
	desired, err := desiredDhclientRoutePolicy()
	if err != nil {
		return false, err
	}
	return string(contents) == string(desired), nil
}

func configureDhclientRoutePolicy(br string, useDefaultRoute bool) error {
	path := dhclientConfigPath(br)
	if useDefaultRoute {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	desired, err := desiredDhclientRoutePolicy()
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(path, desired, 0o600)
}

func ensureDhclientRuntimeDir() error {
	if err := os.MkdirAll(dhclientRuntimeDir, 0o755); err != nil {
		return err
	}
	return os.Chmod(dhclientRuntimeDir, 0o755)
}

func dhclientProcessPatterns(br string) (string, string) {
	main := regexp.QuoteMeta("dhclient: " + br)
	return main, regexp.QuoteMeta("dhclient: " + br + " [priv]")
}

func processMatches(args ...string) (bool, error) {
	output, err := syncRunCommandAllowExitCode("/bin/pgrep", []int{1}, args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func dhclientRunning(br string) (running, managed bool, retErr error) {
	mainPattern, _ := dhclientProcessPatterns(br)
	pidPath := dhclientPIDPath(br)
	if _, err := os.Stat(pidPath); err == nil {
		matched, matchErr := processMatches("-F", pidPath, "-f", "-x", mainPattern)
		if matchErr == nil && matched {
			return true, true, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, false, err
	}

	matched, err := processMatches("-f", "-x", mainPattern)
	if err != nil {
		return false, false, err
	}
	return matched, false, nil
}

func dhclientProcessesRunning(br string) (bool, error) {
	mainPattern, privilegedPattern := dhclientProcessPatterns(br)
	return processMatches("-f", "-x", mainPattern, privilegedPattern)
}

func signalDhclient(br string, managed bool) error {
	mainPattern, _ := dhclientProcessPatterns(br)
	var pidErr error
	if managed {
		_, pidErr = syncRunCommandAllowExitCode(
			"/bin/pkill",
			[]int{1},
			"-TERM", "-F", dhclientPIDPath(br), "-f", "-x", mainPattern,
		)
	}

	_, exactErr := syncRunCommandAllowExitCode(
		"/bin/pkill",
		[]int{1},
		"-TERM", "-f", "-x", mainPattern,
	)
	if exactErr != nil {
		return errors.Join(pidErr, exactErr)
	}
	return nil
}

func waitForDhclientProcesses(br string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		running, err := dhclientProcessesRunning(br)
		if err != nil {
			return false, err
		}
		if !running {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(dhclientStopPollInterval)
	}
}

func routeAlreadyExists(output string, err error) bool {
	message := strings.ToLower(output)
	if err != nil {
		message += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(message, "already in table") || strings.Contains(message, "file exists")
}

func routeIsMissing(output string, err error) bool {
	message := strings.ToLower(output)
	if err != nil {
		message += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(message, "not in table") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "no such process")
}

func addRouteIfMissing(args ...string) (bool, error) {
	output, err := syncRunCommand("/sbin/route", args...)
	if err == nil {
		return true, nil
	}
	if routeAlreadyExists(output, err) {
		return false, nil
	}
	return false, err
}

func routeGetField(output, name string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(strings.TrimSuffix(fields[0], ":"), name) {
			return fields[1], true
		}
	}
	return "", false
}

func addDefaultRouteIfMissing(gateway, bridgeName string) (bool, error) {
	output, err := syncRunCommand("/sbin/route", "add", "default", gateway)
	if err == nil {
		return true, nil
	}
	if !routeAlreadyExists(output, err) {
		return false, err
	}

	existingOutput, inspectErr := syncRunCommand("/sbin/route", "-n", "get", "default")
	if inspectErr != nil {
		return false, fmt.Errorf("inspect existing default route: %w", inspectErr)
	}
	existingGateway, found := routeGetField(existingOutput, "gateway")
	if !found {
		return false, fmt.Errorf("inspect existing default route: gateway not found")
	}
	if existingGateway != gateway {
		return false, fmt.Errorf(
			"default route already exists via %s, requested %s",
			existingGateway,
			gateway,
		)
	}
	existingInterface, found := routeGetField(existingOutput, "interface")
	if !found {
		return false, fmt.Errorf("inspect existing default route: interface not found")
	}
	if existingInterface != bridgeName {
		return false, fmt.Errorf(
			"default route already exists on interface %s, requested %s",
			existingInterface,
			bridgeName,
		)
	}
	return false, nil
}

func addDefaultRoute6IfMissing(gateway, bridgeName string) (bool, error) {
	output, err := syncRunCommand("/sbin/route", "-6", "add", "default", gateway)
	if err == nil {
		return true, nil
	}
	if !routeAlreadyExists(output, err) {
		return false, err
	}

	existingOutput, inspectErr := syncRunCommand("/sbin/route", "-6", "-n", "get", "default")
	if inspectErr != nil {
		return false, fmt.Errorf("inspect existing IPv6 default route: %w", inspectErr)
	}
	existingGateway, found := routeGetField(existingOutput, "gateway")
	if !found {
		return false, fmt.Errorf("inspect existing IPv6 default route: gateway not found")
	}
	if existingGateway != gateway {
		return false, fmt.Errorf(
			"IPv6 default route already exists via %s, requested %s",
			existingGateway,
			gateway,
		)
	}
	existingInterface, found := routeGetField(existingOutput, "interface")
	if !found {
		return false, fmt.Errorf("inspect existing IPv6 default route: interface not found")
	}
	if existingInterface != bridgeName {
		return false, fmt.Errorf(
			"IPv6 default route already exists on interface %s, requested %s",
			existingInterface,
			bridgeName,
		)
	}
	return false, nil
}

func removeDefaultRouteForInterface(familyFlag, interfaceName string) (bool, error) {
	getArgs := []string{"-n"}
	if familyFlag != "" {
		getArgs = append([]string{familyFlag}, getArgs...)
	}
	getArgs = append(getArgs, "get", "default")
	output, err := syncRunCommand("/sbin/route", getArgs...)
	if err != nil {
		if routeIsMissing(output, err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect default route: %w", err)
	}

	currentInterface, found := routeGetField(output, "interface")
	if !found {
		return false, fmt.Errorf("inspect default route: interface not found")
	}
	if currentInterface != interfaceName {
		return false, nil
	}

	deleteArgs := make([]string, 0, 4)
	if familyFlag != "" {
		deleteArgs = append(deleteArgs, familyFlag)
	}
	deleteArgs = append(deleteArgs, "delete", "default")
	if gateway, gatewayFound := routeGetField(output, "gateway"); gatewayFound {
		deleteArgs = append(deleteArgs, gateway)
	}
	deleteOutput, deleteErr := syncRunCommand("/sbin/route", deleteArgs...)
	if deleteErr != nil && !routeIsMissing(deleteOutput, deleteErr) {
		return false, deleteErr
	}
	return deleteErr == nil, nil
}

func defaultRouteInterface(familyFlag string) (string, bool, error) {
	getArgs := []string{"-n"}
	if familyFlag != "" {
		getArgs = append([]string{familyFlag}, getArgs...)
	}
	getArgs = append(getArgs, "get", "default")
	output, err := syncRunCommand("/sbin/route", getArgs...)
	if err != nil {
		if routeIsMissing(output, err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect default route: %w", err)
	}

	currentInterface, found := routeGetField(output, "interface")
	if !found {
		return "", false, fmt.Errorf("inspect default route: interface not found")
	}
	return currentInterface, true, nil
}

func deleteRouteIfPresent(args ...string) error {
	output, err := syncRunCommand("/sbin/route", args...)
	if err != nil && !routeIsMissing(output, err) {
		return err
	}
	return nil
}

func removeStandardSwitchRoutes(sw networkModels.StandardSwitch) error {
	var routeErrors []error
	network4, gateway4 := sw.Network(4), sw.Gateway(4)
	if sw.DefaultRoute {
		if _, err := removeDefaultRouteForInterface("", sw.BridgeName); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 default route: %w", err))
		}
	}
	if network4 != "" && gateway4 != "" {
		if err := deleteRouteIfPresent("delete", "-net", network4, gateway4); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 network route: %w", err))
		}
	}

	network6, gateway6 := sw.Network(6), sw.Gateway(6)
	routeGateway6 := normalizeIPv6GatewayForRoute(gateway6, sw.BridgeName)
	if sw.DefaultRoute6 {
		if _, err := removeDefaultRouteForInterface("-6", sw.BridgeName); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv6 default route: %w", err))
		}
	}
	if network6 != "" && routeGateway6 != "" {
		if err := deleteRouteIfPresent("-6", "delete", "-net", network6, routeGateway6); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv6 network route: %w", err))
		}
	}

	return errors.Join(routeErrors...)
}

func isManagedStandardSwitchVLAN(name string) (bool, error) {
	interfaceObj, err := syncIfaceGet(name)
	if err != nil {
		if isInterfaceMissingError(err) {
			return false, nil
		}
		return false, err
	}
	if interfaceObj == nil {
		return false, nil
	}
	return utils.Contains(interfaceObj.Groups, "svm-vlan"), nil
}

func destroyStandardSwitchRuntimeInterfaces(sw networkModels.StandardSwitch) error {
	var destroyErrors []error
	if _, err := syncRunCommand("/sbin/ifconfig", sw.BridgeName, "destroy"); err != nil && !isInterfaceMissingError(err) {
		destroyErrors = append(destroyErrors, fmt.Errorf("destroy bridge %s: %w", sw.BridgeName, err))
	}

	if sw.VLAN > 0 {
		seen := make(map[string]struct{}, len(sw.Ports))
		for _, port := range sw.Ports {
			vlanName := fmt.Sprintf("%s.%d", port.Name, sw.VLAN)
			if _, duplicate := seen[vlanName]; duplicate {
				continue
			}
			seen[vlanName] = struct{}{}

			managed, err := isManagedStandardSwitchVLAN(vlanName)
			if err != nil {
				destroyErrors = append(destroyErrors, fmt.Errorf("inspect VLAN interface %s: %w", vlanName, err))
				continue
			}
			if !managed {
				continue
			}
			if _, err := syncRunCommand("/sbin/ifconfig", vlanName, "destroy"); err != nil && !isInterfaceMissingError(err) {
				destroyErrors = append(destroyErrors, fmt.Errorf("destroy VLAN interface %s: %w", vlanName, err))
			}
		}
	}

	return errors.Join(destroyErrors...)
}

func stopDhclient(br string) error {
	running, managed, err := dhclientRunning(br)
	if err != nil {
		return fmt.Errorf("inspect dhclient for %s: %w", br, err)
	}
	allRunning, err := dhclientProcessesRunning(br)
	if err != nil {
		return fmt.Errorf("inspect dhclient processes for %s: %w", br, err)
	}
	if running || allRunning {
		stopped, waitErr := waitForDhclientProcesses(br, dhclientNaturalExitGrace)
		if waitErr != nil {
			return fmt.Errorf("wait for dhclient cleanup on %s: %w", br, waitErr)
		}
		if !stopped {
			if err := signalDhclient(br, managed); err != nil {
				return fmt.Errorf("signal dhclient for %s: %w", br, err)
			}
			stopped, waitErr = waitForDhclientProcesses(br, dhclientStopTimeout)
			if waitErr != nil {
				return fmt.Errorf("wait for dhclient on %s: %w", br, waitErr)
			}
			if !stopped {
				return fmt.Errorf("stop dhclient for %s: timed out", br)
			}
		}
	}

	var cleanupErrors []error
	if err := os.Remove(dhclientPIDPath(br)); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove dhclient PID file for %s: %w", br, err))
	}
	if err := os.Remove(dhclientConfigPath(br)); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove dhclient config for %s: %w", br, err))
	}
	return errors.Join(cleanupErrors...)
}
