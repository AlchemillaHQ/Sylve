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
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/db/models"
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
	minMajor := uint64(15)
	minMinor := uint64(0)

	rel, err := sysctl.GetString("kern.osrelease")
	if err != nil {
		return fmt.Errorf("failed to get kern.osrelease: %w", err)
	}

	rel = strings.TrimSpace(rel)
	parts := strings.Split(rel, "-")
	if len(parts) < 1 {
		return fmt.Errorf("unexpected format of kern.osrelease: %s", rel)
	}

	versionParts := strings.Split(parts[0], ".")
	if len(versionParts) < 2 {
		return fmt.Errorf("unexpected version format: %s", parts[0])
	}

	majorVersion := utils.StringToUint64(versionParts[0])
	minorVersion := utils.StringToUint64(versionParts[1])

	if majorVersion < minMajor || (majorVersion == minMajor && minorVersion < minMinor) {
		return fmt.Errorf("unsupported FreeBSD version: %s, minimum required is %d.%d", rel, minMajor, minMinor)
	}

	logger.L.Info().Msgf("FreeBSD version %s detected", rel)

	return nil
}

func (s *Service) CheckPackageDependencies(basicSettings models.BasicSettings) error {
	requiredPackages := []string{}

	if basicSettings.Services != nil {
		if slices.Contains(basicSettings.Services, models.Virtualization) {
			requiredPackages = append(requiredPackages, "libvirt", "bhyve-firmware", "jansson", "swtpm")
		}

		if slices.Contains(basicSettings.Services, models.DHCPServer) {
			requiredPackages = append(requiredPackages, "dnsmasq")
		}

		if slices.Contains(basicSettings.Services, models.SambaServer) {
			output, err := utils.RunCommand("pkg", "info")
			if err != nil {
				return fmt.Errorf("failed to run pkg info: %w", err)
			}

			lines := strings.Split(output, "\n")
			sambaInstalled := false

			for _, line := range lines {
				if strings.HasPrefix(line, "samba4") {
					sambaInstalled = true
					break
				}
			}

			if !sambaInstalled {
				requiredPackages = append(requiredPackages, "samba4XX")
			}
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(requiredPackages))

	installCmd := fmt.Sprintf("pkg install %s", strings.Join(requiredPackages, " "))

	for _, p := range requiredPackages {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !pkg.IsPackageInstalled(p) {
				errCh <- fmt.Errorf("Required package %s is not installed, run the command '%s' to install all required packages", p, installCmd)
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

func (s *Service) CheckServiceDependencies(basicSettings models.BasicSettings) error {
	const rcConfPath = "/etc/rc.conf"

	enabledServices := []string{"zfs_enable"}

	serviceMap := map[string]string{
		"virtualization": "libvirtd_enable",
		"dhcp-server":    "dnsmasq_enable",
		"samba-server":   "samba_server_enable",
	}

	for _, svc := range basicSettings.Services {
		if rc, ok := serviceMap[string(svc)]; ok {
			enabledServices = append(enabledServices, rc)
		}
	}

	serviceNames := map[string]string{
		"libvirtd_enable":     "libvirtd",
		"dnsmasq_enable":      "dnsmasq",
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

		if key == "zfs_enable" || key == "samba_server_enable" || key == "dnsmasq_enable" {
			continue
		}

		service := serviceNames[key]
		if err := ensureServiceRunning(service); err != nil {
			return fmt.Errorf("failed to ensure service %s is running: %w", service, err)
		}
	}

	return nil
}

func (s *Service) EnableLinux() error {
	loadKLD := func(module string) error {
		if _, err := utils.RunCommand("kldload", "-n", module); err != nil {
			return fmt.Errorf("failed to load kernel module %s: %w", module, err)
		}
		return nil
	}

	ensureFallbackBrand := func(name string) error {
		out, err := utils.RunCommand("sysctl", "-ni", name)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", name, err)
		}

		valStr := strings.TrimSpace(out)
		if valStr == "" {
			return nil
		}

		val, err := strconv.Atoi(valStr)
		if err != nil {
			return fmt.Errorf("failed to parse %s value %q: %w", name, valStr, err)
		}

		if val == -1 {
			if _, err := utils.RunCommand("sysctl", fmt.Sprintf("%s=3", name)); err != nil {
				return fmt.Errorf("failed to set %s=3: %w", name, err)
			}
		}
		return nil
	}

	linuxMount := func(fs, mountPoint, opts string) error {
		mountOut, err := utils.RunCommand("mount")
		if err != nil {
			return fmt.Errorf("failed to list mounts: %w", err)
		}

		pattern := fmt.Sprintf("%s on %s (", fs, mountPoint)
		if strings.Contains(mountOut, pattern) {
			return nil
		}

		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			return fmt.Errorf("failed to create mountpoint %s: %w", mountPoint, err)
		}

		args := []string{}

		if opts != "" {
			args = append(args, "-o", opts)
		}

		args = append(args, "-t", fs, fs, mountPoint)

		if _, err := utils.RunCommand("mount", args...); err != nil {
			return fmt.Errorf("failed to mount %s on %s: %w", fs, mountPoint, err)
		}
		return nil
	}

	switch runtime.GOARCH {
	case "amd64":
		if err := loadKLD("linux"); err != nil {
			return err
		}
		if err := loadKLD("linux64"); err != nil {
			return err
		}
	case "arm64":
		if err := loadKLD("linux64"); err != nil {
			return err
		}
	case "386":
		if err := loadKLD("linux"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Linux ABI not supported on GOARCH=%q", runtime.GOARCH)
	}

	if err := loadKLD("pty"); err != nil {
		return err
	}

	for _, mod := range []string{"fdescfs", "linprocfs", "linsysfs"} {
		if err := loadKLD(mod); err != nil {
			return err
		}
	}

	if err := ensureFallbackBrand("kern.elf64.fallback_brand"); err != nil {
		return err
	}
	if err := ensureFallbackBrand("kern.elf32.fallback_brand"); err != nil {
		return err
	}

	emulPathRaw, err := utils.RunCommand("sysctl", "-n", "compat.linux.emul_path")
	if err != nil {
		return fmt.Errorf("failed to get compat.linux.emul_path: %w", err)
	}
	emulPath := strings.TrimSpace(emulPathRaw)
	if emulPath == "" {
		emulPath = "/compat/linux"
	}

	if err := linuxMount("linprocfs", filepath.Join(emulPath, "proc"), "nocover"); err != nil {
		return err
	}
	if err := linuxMount("linsysfs", filepath.Join(emulPath, "sys"), "nocover"); err != nil {
		return err
	}
	if err := linuxMount("devfs", filepath.Join(emulPath, "dev"), "nocover"); err != nil {
		return err
	}
	if err := linuxMount("fdescfs", filepath.Join(emulPath, "dev", "fd"), "nocover,linrdlnk"); err != nil {
		return err
	}
	if err := linuxMount("tmpfs", filepath.Join(emulPath, "dev", "shm"), "nocover,mode=1777"); err != nil {
		return err
	}

	return nil
}

func (s *Service) CheckKernelModules(basicSettings models.BasicSettings) error {
	requiredModules := []string{
		"vmm",
		"nmdm",
		"if_bridge",
		"zfs",
		"cryptodev",
		"if_epair",
		"nullfs",
	}

	if slices.Contains(basicSettings.Services, models.Virtualization) {
		requiredModules = append(requiredModules, "vmm", "nmdm")
	}

	if slices.Contains(basicSettings.Services, models.Jails) {
		requiredModules = append(requiredModules, "if_epair", "nullfs")
	}

	for _, module := range requiredModules {
		if _, err := utils.RunCommand("kldload", "-n", module); err != nil {
			return fmt.Errorf("failed to load kernel module %s: %w", module, err)
		}
	}

	if slices.Contains(basicSettings.Services, models.Jails) {
		err := s.EnableLinux()
		if err != nil {
			return fmt.Errorf("Failed to enable Linux ABI: %w", err)
		}
	}

	return nil
}

func (s *Service) CheckSambaSyslogConfig(basicSettings models.BasicSettings) error {
	if !slices.Contains(basicSettings.Services, models.SambaServer) {
		return nil
	}

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

	requiredBlock := `[devfsrules_jails=61181]
add include $devfsrules_hide_all
add include $devfsrules_unhide_basic
add include $devfsrules_unhide_login
add path 'bpf*' unhide
`

	var existing string
	if data, err := os.ReadFile(devfsRulesPath); err == nil {
		existing = string(data)

		if strings.Contains(existing, "[devfsrules_jails=61181]") &&
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
