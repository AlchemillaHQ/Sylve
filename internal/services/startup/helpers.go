// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/rcconf"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

func (s *Service) SysctlSync() error {
	intVals := map[string]int32{
		"net.inet.ip.forwarding":            1,
		"net.link.bridge.inherit_mac":       1,
		"kern.geom.label.disk_ident.enable": 0,
		"kern.geom.label.gptid.enable":      0,
	}

	for k, v := range intVals {
		_, err := sysctl.GetInt64(k)
		if err != nil {
			logger.L.Error().Msgf("Error getting sysctl %s: %v, skipping!", k, err)
			continue
		}

		err = sysctl.SetInt32(k, v)
		if err != nil {
			logger.L.Error().Msgf("Error setting sysctl %s: %v", k, err)
		}
	}

	return nil
}

func (s *Service) InitFirewall() error {
	return nil
}

func (s *Service) FreeBSDCheck() error {
	minMajor := uint64(14)
	minMinor := uint64(3)

	output, err := utils.RunCommand("uname", "-r")
	output = strings.TrimSpace(output)

	if err != nil {
		return fmt.Errorf("failed to run uname command: %w", err)
	}

	parts := strings.Split(output, "-")
	if len(parts) < 1 {
		return fmt.Errorf("unexpected output from uname command: %s", output)
	}

	versionParts := strings.Split(parts[0], ".")
	if len(versionParts) < 2 {
		return fmt.Errorf("unexpected version format: %s", parts[0])
	}

	majorVersion := utils.StringToUint64(versionParts[0])
	minorVersion := utils.StringToUint64(versionParts[1])

	if majorVersion < minMajor || (majorVersion == minMajor && minorVersion < minMinor) {
		return fmt.Errorf("unsupported FreeBSD version: %s, minimum required is %d.%d", output, minMajor, minMinor)
	}

	return nil
}

func (s *Service) CheckPackageDependencies() error {
	requiredPackages := []string{
		"libvirt",
		"bhyve-firmware",
		"smartmontools",
		"tmux",
	}

    // Check for samba419 or samba420 (both are available as FreeBSD packages)
    samba419Installed := pkg.IsPackageInstalled("samba419")
    samba420Installed := pkg.IsPackageInstalled("samba420")
    if !samba419Installed && !samba420Installed {
        // Ask for new version first
        requiredPackages = append(requiredPackages, "samba420")
    }
	
	var wg sync.WaitGroup
	errCh := make(chan error, len(requiredPackages))

	for _, p := range requiredPackages {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !pkg.IsPackageInstalled(p) {
				errCh <- fmt.Errorf("Required package %s is not installed, run the command 'pkg install libvirt bhyve-firmware smartmontools tmux samba419' to install all required packages", p)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) CheckServiceDependencies() error {
	const rcConfPath = "/etc/rc.conf"

	enabledServices := []string{
		"zfs_enable",
		"libvirtd_enable",
		"dnsmasq_enable",
		"rpcbind_enable",
		"samba_server_enable",
	}

	serviceNames := map[string]string{
		"libvirtd_enable":     "libvirtd",
		"dnsmasq_enable":      "dnsmasq",
		"rpcbind_enable":      "rpcbind",
		"samba_server_enable": "samba_server",
	}

	config, err := rcconf.Parse(rcConfPath)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", rcConfPath, err)
	}

	for _, key := range enabledServices {
		val, ok := config[key]
		if !ok || val != "YES" {
			return fmt.Errorf("required service %s is not enabled in %s", key, rcConfPath)
		}

		if key == "zfs_enable" || key == "samba_server_enable" {
			continue
		}

		service := serviceNames[key]
		if err := ensureServiceRunning(service); err != nil {
			return fmt.Errorf("failed to ensure service %s is running: %w", service, err)
		}
	}

	return nil
}

func (s *Service) CheckKernelModules() error {
	requiredModules := []string{
		"vmm",
		"nmdm",
		"if_bridge",
		"zfs",
		"cryptodev",
		"if_epair",
	}

	for _, module := range requiredModules {
		if _, err := utils.RunCommand("kldload", "-n", module); err != nil {
			return fmt.Errorf("failed to load kernel module %s: %w", module, err)
		}
	}

	return nil
}

func (s *Service) CheckSyslogConfig() error {
	const syslogConfPath = "/etc/syslog.conf"
	const sylveLine = "LOCAL7.* /var/log/samba4/audit.log"

	exists, err := utils.FileExists(syslogConfPath)
	if err != nil {
		return fmt.Errorf("failed to check syslog config file: %w", err)
	}

	if !exists {
		if err := os.WriteFile(syslogConfPath, []byte(sylveLine+"\n"), 0644); err != nil {
			return fmt.Errorf("failed to create syslog config file: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(syslogConfPath)
	if err != nil {
		return fmt.Errorf("failed to read syslog config file: %w", err)
	}

	if !strings.Contains(string(data), sylveLine) {
		f, err := os.OpenFile(syslogConfPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open syslog config for appending: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString("\n" + sylveLine + "\n"); err != nil {
			return fmt.Errorf("failed to append to syslog config: %w", err)
		}
	}

	return nil
}

func (s *Service) DevfsSync() error {
	const devfsRulesPath = "/etc/devfs.rules"

	requiredBlock := `[devfsrules_jails=8181]
add include $devfsrules_hide_all
add include $devfsrules_unhide_basic
add include $devfsrules_unhide_login
add path 'bpf*' unhide
`

	var existing string
	if data, err := os.ReadFile(devfsRulesPath); err == nil {
		existing = string(data)

		if strings.Contains(existing, "[devfsrules_jails=8181]") &&
			strings.Contains(existing, "add path 'bpf*' unhide") {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("devfssync: failed to check devfs.rules: %w", err)
	}

	var newContent string
	if existing != "" {
		newContent = existing + "\n\n" + requiredBlock
	} else {
		newContent = requiredBlock
	}

	if err := os.WriteFile(devfsRulesPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("devfssync: failed to write to /etc/devfs.rules: %w", err)
	}

	if _, err := utils.RunCommand("service", "devfs", "restart"); err != nil {
		return fmt.Errorf("devfssync: failed to restart devfs service: %w", err)
	}

	return nil
}

func ensureServiceRunning(service string) error {
	_, err := utils.RunCommand("service", service, "status")
	if err == nil {
		return nil
	}

	_, startErr := utils.RunCommand("service", service, "start")
	if startErr != nil {
		return fmt.Errorf("could not start service %s: %w", service, startErr)
	}

	return nil
}
