// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/pkg"
	"github.com/alchemillahq/sylve/pkg/utils"
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
	"gorm.io/gorm"
)

var (
	startupGetSysctlInt64        = sysctl.GetInt64
	startupSetSysctlInt32        = sysctl.SetInt32
	startupSetSysctlInt64        = sysctl.SetInt64
	startupGetSystemMemoryBytes  = utils.GetSystemMemoryBytes
	startupRunCommand            = utils.RunCommand
	sambaSyslogDropInPath        = "/etc/syslog.d/sylve-samba-audit.conf"
	sambaAuditLogPath            = "/var/log/samba4/audit.log"
	sambaAuditRotationConfigPath = "/usr/local/etc/newsyslog.conf.d/sylve-samba-audit.conf"
)

const (
	arcMaxOID                 = models.SystemTunableARCMaxOID
	jailLogRotationConfigPath = "/usr/local/etc/newsyslog.conf.d/sylve.conf"
)

// computeARCMax returns 10% of host memory, capped at 16 GiB.
func computeARCMax(memBytes int64) int64 {
	arcMax := memBytes / 10
	capBytes := int64(16) * 1024 * 1024 * 1024
	if arcMax > capBytes {
		arcMax = capBytes
	}

	return arcMax
}

func (s *Service) SysctlSync() error {
	intVals := map[string]int32{
		"net.inet.ip.forwarding":            1,
		"net.inet6.ip6.forwarding":          1,
		"net.link.bridge.inherit_mac":       1,
		"kern.geom.label.disk_ident.enable": 0,
		"kern.geom.label.gptid.enable":      0,
		"net.inet6.ip6.dad_count":           0,
		"security.jail.enforce_statfs":      1,
	}

	for k, v := range intVals {
		_, err := startupGetSysctlInt64(k)
		if err != nil {
			logger.L.Error().Msgf("Error getting sysctl %s: %v, skipping!", k, err)
			continue
		}

		err = startupSetSysctlInt32(k, v)
		if err != nil {
			logger.L.Error().Msgf("Error setting sysctl %s: %v", k, err)
		}
	}

	currentFIBs, err := startupGetSysctlInt64("net.fibs")
	if err != nil {
		logger.L.Error().Msgf("Error getting sysctl net.fibs: %v, skipping!", err)
		return nil
	}

	if currentFIBs < 8 {
		if err := startupSetSysctlInt32("net.fibs", 8); err != nil {
			logger.L.Error().Msgf("Error setting sysctl net.fibs: %v", err)
			return nil
		}

		logger.L.Info().Msg("Raised sysctl net.fibs to 8 for multi-FIB routing support")
	}

	return nil
}

// ZFSTune sets the ZFS ARC max to 10% of host memory (capped at 16 GiB) when
// zfs.tune is enabled in the config. A user-stored vfs.zfs.arc.max tunable takes
// precedence and disables the auto-tune.
func (s *Service) ZFSTune() error {
	if config.ParsedConfig == nil || !config.ParsedConfig.ZFS.Tune {
		return nil
	}

	if s.DB != nil {
		var existing models.SystemTunable
		err := s.DB.Where("name = ?", arcMaxOID).First(&existing).Error
		if err == nil {
			logger.L.Info().Msgf("%s user override present, skipping ZFS ARC auto-tune", arcMaxOID)
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}

	mem, err := startupGetSystemMemoryBytes()
	if err != nil {
		logger.L.Error().Msgf("Error getting system memory for ZFS ARC tuning: %v, skipping!", err)
		return nil
	}

	arcMax := computeARCMax(mem)
	if arcMax <= 0 {
		return nil
	}

	if err := startupSetSysctlInt64(arcMaxOID, arcMax); err != nil {
		logger.L.Error().Msgf("Error setting %s: %v", arcMaxOID, err)
		return nil
	}

	logger.L.Info().Msgf("Set %s to %d bytes (10%% of host memory, capped at 16G)", arcMaxOID, arcMax)

	return nil
}

func (s *Service) InitFirewall() error {
	return nil
}

func loadKernelModule(module string) error {
	if _, err := startupRunCommand("/sbin/kldstat", "-m", module); err == nil {
		return nil
	}

	if _, err := startupRunCommand("/sbin/kldload", "-n", module); err != nil {
		return fmt.Errorf("failed to load kernel module %s: %w", module, err)
	}

	if _, err := startupRunCommand("/sbin/kldstat", "-m", module); err != nil {
		return fmt.Errorf("failed to verify kernel module %s after loading: %w", module, err)
	}

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
			requiredPackages = append(requiredPackages, "libvirt", "swtpm")

			switch runtime.GOARCH {
			case "amd64":
				requiredPackages = append(requiredPackages, "bhyve-firmware")
			case "arm64":
				requiredPackages = append(requiredPackages, "u-boot-bhyve-arm64")
			}
		}

		if slices.Contains(basicSettings.Services, models.DHCPServer) {
			requiredPackages = append(requiredPackages, "dnsmasq")
		}

		if slices.Contains(basicSettings.Services, models.SambaServer) {
			output, err := utils.RunCommand("/usr/sbin/pkg", "info")
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

func (s *Service) EnableLinux() error {
	loadKLD := func(module string) error {
		// already loaded? -n matches by filename and works inside a jail
		if _, err := utils.RunCommand("/sbin/kldstat", "-q", "-n", module); err == nil {
			return nil
		}
		if _, err := utils.RunCommand("/sbin/kldload", "-n", module); err != nil {
			return fmt.Errorf("failed to load kernel module %s: %w", module, err)
		}
		return nil
	}

	ensureFallbackBrand := func(name string) error {
		val, err := startupGetSysctlInt64(name)
		if err != nil {
			return nil
		}

		if val == -1 {
			if err := startupSetSysctlInt32(name, 3); err != nil {
				return fmt.Errorf("failed to set %s=3: %w", name, err)
			}
		}
		return nil
	}

	linuxMount := func(fs, mountPoint, opts string) error {
		mountOut, err := utils.RunCommand("/sbin/mount")
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

		if _, err := utils.RunCommand("/sbin/mount", args...); err != nil {
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

	emulPath, err := sysctl.GetString("compat.linux.emul_path")
	if err != nil {
		emulPath = "/compat/linux"
	}
	emulPath = strings.TrimSpace(emulPath)
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
		"if_bridge",
		"zfs",
		"cryptodev",
		"if_epair",
		"nullfs",
		"netlink",
		"nlsysevent",
	}

	if slices.Contains(basicSettings.Services, models.Virtualization) {
		requiredModules = append(requiredModules, "vmm", "nmdm")
	}

	if slices.Contains(basicSettings.Services, models.Firewall) {
		requiredModules = append(requiredModules, "pf")
	}

	if slices.Contains(basicSettings.Services, models.WireGuard) {
		requiredModules = append(requiredModules, "if_wg")
	}

	for _, module := range requiredModules {
		if err := loadKernelModule(module); err != nil {
			return err
		}
	}

	if slices.Contains(basicSettings.Services, models.Firewall) {
		if err := loadKernelModule("pflog"); err != nil {
			logger.L.Warn().Err(err).Msg("pflog kernel module unavailable; packet-log capture will retry in the background")
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

	changed, err := syncSambaAuditSyslogConfig(sambaSyslogDropInPath, sambaAuditLogPath)
	if err != nil {
		return err
	}
	if err := syncSambaAuditRotationConfig(sambaAuditRotationConfigPath, sambaAuditLogPath); err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if output, err := startupRunCommand("/usr/sbin/service", "syslogd", "reload"); err != nil {
		return fmt.Errorf("failed to reload syslogd: %w (output: %s)", err, strings.TrimSpace(output))
	}
	return nil
}

func syncSambaAuditRotationConfig(configPath, auditPath string) error {
	auditPath = filepath.Clean(auditPath)
	if !filepath.IsAbs(auditPath) || strings.ContainsAny(auditPath, " \t\r\n#*?[\\") {
		return fmt.Errorf("unsupported Samba audit path for newsyslog: %q", auditPath)
	}
	content := []byte(fmt.Sprintf(
		"# Managed by Sylve; changes will be overwritten.\n%s\t0600\t7\t100M\t*\tJCE\n",
		auditPath,
	))
	if current, err := os.ReadFile(configPath); err == nil {
		if bytes.Equal(current, content) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read Samba newsyslog drop-in: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create Samba newsyslog drop-in directory: %w", err)
	}
	if err := utils.AtomicWriteFile(configPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write Samba newsyslog drop-in: %w", err)
	}
	return nil
}

func syncSambaAuditSyslogConfig(dropInPath, auditPath string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(auditPath), 0755); err != nil {
		return false, fmt.Errorf("failed to create Samba audit log directory: %w", err)
	}

	_, statErr := os.Stat(auditPath)
	fileCreated := os.IsNotExist(statErr)
	if statErr != nil && !fileCreated {
		return false, fmt.Errorf("failed to inspect Samba audit log: %w", statErr)
	}
	f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return false, fmt.Errorf("failed to create Samba audit log: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("failed to close Samba audit log: %w", err)
	}
	if err := os.Chmod(auditPath, 0600); err != nil {
		return false, fmt.Errorf("failed to secure Samba audit log: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dropInPath), 0755); err != nil {
		return false, fmt.Errorf("failed to create syslog drop-in directory: %w", err)
	}
	content := []byte("# Sylve-managed Samba full_audit log\n!smbd_audit\nlocal5.notice\t" + auditPath + "\n!*\n")
	existing, err := os.ReadFile(dropInPath)
	if err == nil && bytes.Equal(existing, content) {
		return fileCreated, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to read Samba syslog drop-in: %w", err)
	}
	if err := utils.AtomicWriteFile(dropInPath, content, 0644); err != nil {
		return false, fmt.Errorf("failed to write Samba syslog drop-in: %w", err)
	}
	return true, nil
}

func (s *Service) SyncJailLogRotation() error {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed to get jails path: %w", err)
	}

	return writeJailLogRotationConfig(jailLogRotationConfigPath, jailsPath)
}

func writeJailLogRotationConfig(configPath, jailsPath string) error {
	jailsPath = filepath.Clean(jailsPath)
	if !filepath.IsAbs(jailsPath) || strings.ContainsAny(jailsPath, " \t\r\n#*?[\\") {
		return fmt.Errorf("unsupported jails path for newsyslog: %q", jailsPath)
	}

	content := []byte(fmt.Sprintf(
		"# Managed by Sylve; changes will be overwritten.\n%s\t0644\t5\t1M\t*\tBEGNZ\n",
		filepath.Join(jailsPath, "*", "*.log"),
	))

	if current, err := os.ReadFile(configPath); err == nil {
		if bytes.Equal(current, content) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read jail log rotation config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create newsyslog config directory: %w", err)
	}

	if err := utils.AtomicWriteFile(configPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write jail log rotation config: %w", err)
	}

	return nil
}

func (s *Service) DevfsSync() error {
	if config.IsDevFSDisabled() {
		return nil
	}

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

	if _, err := utils.RunCommand("/usr/sbin/service", "devfs", "restart"); err != nil {
		return fmt.Errorf("devfssync: failed to restart devfs service: %w", err)
	}

	return nil
}

func ensureServiceStarted(service string) error {
	// Check if it's already running using onestatus
	_, statusErr := utils.RunCommand("/usr/sbin/service", service, "onestatus")
	if statusErr == nil {
		return nil // Service is already running
	}

	// Force start the service without needing it enabled in /etc/rc.conf
	output, startErr := utils.RunCommand("/usr/sbin/service", service, "onestart")
	if startErr != nil {
		// Double check in case it actually started but returned a weird exit code
		if _, finalStatusErr := utils.RunCommand("/usr/sbin/service", service, "onestatus"); finalStatusErr == nil {
			return nil
		}

		return fmt.Errorf("could not force start service %s: %w (output: %s)", service, startErr, output)
	}

	return nil
}
