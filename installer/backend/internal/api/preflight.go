// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/installer/backend/internal/engine"
	"github.com/alchemillahq/sylve/installer/backend/internal/sysinfo"
)

type PreflightCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Fatal  bool   `json:"fatal"`
	Detail string `json:"detail"`
	Error  string `json:"error,omitempty"`
}

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func preflightChecks() []PreflightCheck {
	payload := engine.OfflinePayload()
	checks := []func() PreflightCheck{
		checkVirt,
		checkDisks,
	}
	if payload.Available {
		checks = append(checks, func() PreflightCheck { return checkPayload(payload) })
	}
	checks = append(checks,
		func() PreflightCheck { return checkNetwork(payload.Available) },
		func() PreflightCheck { return checkRepos(payload.Available) },
		func() PreflightCheck { return checkSylveSource(payload.Available) },
		checkMemory,
	)
	out := make([]PreflightCheck, len(checks))
	var wg sync.WaitGroup
	wg.Add(len(checks))
	for i, check := range checks {
		go func() {
			defer wg.Done()
			out[i] = check()
		}()
	}
	wg.Wait()
	return out
}

func checkPayload(info engine.OfflinePayloadInfo) PreflightCheck {
	c := PreflightCheck{ID: "payload", Label: "Offline Payload", Status: "ok"}
	var parts []string
	if info.TipVersion != "" {
		commit := info.TipCommit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		if commit != "" {
			parts = append(parts, fmt.Sprintf("Sylve %s (%s)", info.TipVersion, commit))
		} else {
			parts = append(parts, "Sylve "+info.TipVersion)
		}
	}
	if info.BakedAt != "" {
		parts = append(parts, "baked "+info.BakedAt)
	}
	if info.Packages > 0 {
		parts = append(parts, fmt.Sprintf("%d packages", info.Packages))
	}
	if len(parts) == 0 {
		parts = append(parts, "Bundled packages and Sylve binary are ready")
	}
	c.Detail = strings.Join(parts, " - ")
	return c
}

func checkVirt() PreflightCheck {
	c := PreflightCheck{ID: "virt", Label: "Virtualization"}

	if _, err := runCmd(5*time.Second, "/sbin/kldstat", "-q", "-m", "vmm"); err == nil {
		c.Status = "ok"
		c.Detail = "VMM is loaded - Sylve can run virtual machines"
		return c
	}
	out, err := runCmd(10*time.Second, "/sbin/kldload", "vmm")
	switch {
	case err == nil:
		c.Status = "ok"
		c.Detail = "VMM loaded - Sylve can run virtual machines"
		return c
	case strings.Contains(out, "already loaded"), strings.Contains(out, "File exists"):
		c.Status = "ok"
		c.Detail = "VMM is loaded - Sylve can run virtual machines"
		return c
	}

	cpu := cpuVirtFeature()
	c.Status = "warn"
	if cpu != "" {
		c.Detail = fmt.Sprintf(
			"CPU reports %s but VMM failed to load (%s) - Sylve VMs will not run",
			cpu, firstLine(out))
	} else {
		c.Detail = fmt.Sprintf(
			"CPU virtualization (%s) is not available - Sylve installs, but virtual machines will not run",
			virtFeatureHint())
	}
	return c
}

func virtFeatureHint() string {
	switch runtime.GOARCH {
	case "amd64", "386":
		return "Intel VT-x / AMD-V"
	case "arm64":
		return "EL2 + GICv3"
	default:
		return "hardware virtualization extensions"
	}
}

func cpuVirtFeature() string {
	out, err := runCmd(5*time.Second, "/sbin/dmesg")
	if err != nil {
		return ""
	}
	if runtime.GOARCH == "arm64" {
		return arm64VirtFeature(out)
	}
	return x86VirtFeature(out)
}

func x86VirtFeature(dmesg string) string {
	for _, line := range strings.Split(dmesg, "\n") {
		if strings.Contains(line, "Features2=") && strings.Contains(line, "VMX") {
			return "Intel VT-x"
		}
		if strings.Contains(line, "AMD Features2=") && strings.Contains(line, "SVM") {
			return "AMD-V (SVM)"
		}
	}
	return ""
}

func arm64VirtFeature(dmesg string) string {
	el2, gic := false, false
	for _, line := range strings.Split(dmesg, "\n") {
		if strings.Contains(line, "ARM Generic Interrupt Controller v3") {
			gic = true
		}
		if strings.Contains(line, "Processor Features") {
			if hasToken(line, "EL2") {
				el2 = true
			}
			if hasToken(line, "GIC") {
				gic = true
			}
		}
	}
	switch {
	case el2 && gic:
		return "ARM64 virtualization (EL2 + GICv3)"
	case el2:
		return "ARM64 EL2 (no GICv3)"
	case gic:
		return "ARM64 GICv3 (no EL2)"
	}
	return ""
}

func hasToken(line, token string) bool {
	for _, field := range strings.FieldsFunc(line, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if field == token {
			return true
		}
	}
	return false
}

func checkDisks() PreflightCheck {
	c := PreflightCheck{ID: "disks", Label: "Install Target", Fatal: true}
	disks, err := engine.Disks()
	if err != nil {
		c.Status = "fail"
		c.Detail = "Could not read the disk inventory: " + err.Error()
		return c
	}
	detail, ok := diskSummary(disks)
	if !ok {
		c.Status = "fail"
		c.Detail = "No installable disks found - attach a disk and re-run the checks"
		return c
	}
	c.Status = "ok"
	c.Detail = detail
	return c
}

func diskSummary(disks []engine.Disk) (string, bool) {
	const maxDiskNames = 3
	var names []string
	for _, d := range disks {
		if d.Boot {
			continue
		}
		names = append(names, fmt.Sprintf("%s (%s)", d.Name, humanBytes(d.SizeBytes)))
	}
	if len(names) == 0 {
		return "", false
	}
	total := len(names)
	extra := total - maxDiskNames
	if extra > 0 {
		names = names[:maxDiskNames]
	}
	plural := "s"
	if total == 1 {
		plural = ""
	}
	detail := fmt.Sprintf("%d installable disk%s: %s", total, plural, strings.Join(names, ", "))
	if extra > 0 {
		detail += fmt.Sprintf(" and %d more", extra)
	}
	return detail, true
}

func checkNetwork(payload bool) PreflightCheck {
	c := PreflightCheck{ID: "network", Label: "Live Network", Fatal: true}
	info := sysinfo.Collect()
	var ifaces []string
	addr := ""
	for _, iface := range info.Interfaces {
		if len(iface.IPv4) == 0 {
			continue
		}
		ifaces = append(ifaces, iface.Name)
		if addr == "" {
			addr = iface.IPv4[0]
		}
	}
	if addr == "" {
		c.Status = "fail"
		c.Detail = "No IPv4 address on any interface - configure the live network"
		if payload {
			c.Status = "warn"
			c.Detail = "No IPv4 address on any interface - the bundled offline payload will be used"
		}
		return c
	}
	if out, err := runCmd(3*time.Second, "/sbin/route", "-n", "get", "default"); err != nil ||
		!strings.Contains(out, "gateway:") {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("Address %s on %s, but no default route",
			addr, strings.Join(ifaces, ", "))
		if payload {
			c.Status = "warn"
			c.Detail = fmt.Sprintf("Address %s on %s, but no default route - the bundled offline payload will be used",
				addr, strings.Join(ifaces, ", "))
		}
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("%s on %s - default route up", addr, strings.Join(ifaces, ", "))
	return c
}

func checkRepos(payload bool) PreflightCheck {
	c := PreflightCheck{ID: "repos", Label: "Package Repositories", Fatal: true}
	if err := httpReachable("https://pkg.freebsd.org/"); err != nil {
		c.Status = "fail"
		c.Detail = "pkg.FreeBSD.org unreachable"
		if payload {
			c.Status = "warn"
			c.Detail = "pkg.FreeBSD.org unreachable - the bundled offline payload will be used"
		}
		c.Error = err.Error()
		return c
	}
	c.Status = "ok"
	c.Detail = "pkg.FreeBSD.org reachable"
	return c
}

func checkSylveSource(payload bool) PreflightCheck {
	c := PreflightCheck{ID: "sylve", Label: "Sylve Repositories", Fatal: true}
	if err := httpReachable("https://github.com/"); err != nil {
		c.Status = "fail"
		c.Detail = "github.com unreachable"
		if payload {
			c.Status = "warn"
			c.Detail = "github.com unreachable - the bundled Sylve binary will be used"
		}
		c.Error = err.Error()
		return c
	}
	c.Status = "ok"
	c.Detail = "github.com reachable"
	return c
}

func checkMemory() PreflightCheck {
	c := PreflightCheck{ID: "memory", Label: "Memory"}
	out, err := runCmd(3*time.Second, "/sbin/sysctl", "-n", "hw.physmem")
	if err != nil {
		c.Status = "warn"
		c.Detail = "Could not read hw.physmem"
		return c
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	if err != nil {
		c.Status = "warn"
		c.Detail = "Could not parse hw.physmem"
		return c
	}
	const minRAM = 4 << 30
	if bytes < minRAM {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%s RAM - 4 GiB or more recommended", humanBytes(bytes))
		return c
	}
	c.Status = "ok"
	c.Detail = humanBytes(bytes) + " RAM"
	return c
}

func httpReachable(url string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sylve-installer-preflight")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(n)
	i := -1
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, units[i])
	}
	return fmt.Sprintf("%.0f %s", value, units[i])
}
