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
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	dhclientStopTimeout      = 2 * time.Second
	dhclientStopPollInterval = 50 * time.Millisecond
)

var (
	dhclientRuntimeDir       = "/var/run/dhclient"
	dhclientNaturalExitGrace = 2 * time.Second
)

func dhclientPIDPath(br string) string {
	return filepath.Join(dhclientRuntimeDir, "dhclient."+br+".pid")
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
	if sw.DefaultRoute && gateway4 != "" {
		if err := deleteRouteIfPresent("delete", "default", gateway4); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 default route: %w", err))
		}
	}
	if network4 != "" && gateway4 != "" {
		if err := deleteRouteIfPresent("delete", "-net", network4, gateway4); err != nil {
			routeErrors = append(routeErrors, fmt.Errorf("delete IPv4 network route: %w", err))
		}
	}

	network6, gateway6 := sw.Network(6), sw.Gateway(6)
	if network6 != "" && gateway6 != "" {
		gateway6 = normalizeIPv6GatewayForRoute(gateway6, sw.BridgeName)
		if err := deleteRouteIfPresent("-6", "delete", "-net", network6, gateway6); err != nil {
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

	if err := os.Remove(dhclientPIDPath(br)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove dhclient PID file for %s: %w", br, err)
	}
	return nil
}
