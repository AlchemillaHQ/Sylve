// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sysinfo

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Interface struct {
	Name string   `json:"name"`
	IPv4 []string `json:"ipv4"`
}

type Info struct {
	Hostname      string      `json:"hostname"`
	Kernel        string      `json:"kernel"`
	KernelVersion string      `json:"kernelVersion"`
	BootMethod    string      `json:"bootMethod"`
	Mode          string      `json:"mode"`
	CPUs          int         `json:"cpus"`
	MemoryBytes   uint64      `json:"memoryBytes"`
	Interfaces    []Interface `json:"interfaces"`
}

func Collect() Info {
	info := Info{Hostname: hostname(), Mode: kioskMode()}

	vals := sysctl("kern.osrelease", "kern.version", "machdep.bootmethod", "hw.ncpu", "hw.physmem")
	info.Kernel = vals["kern.osrelease"]
	if v := vals["kern.version"]; v != "" {
		info.KernelVersion = strings.SplitN(v, "\n", 2)[0]
	}
	info.BootMethod = vals["machdep.bootmethod"]
	if n, err := strconv.Atoi(vals["hw.ncpu"]); err == nil {
		info.CPUs = n
	}
	if n, err := strconv.ParseUint(vals["hw.physmem"], 10, 64); err == nil {
		info.MemoryBytes = n
	}
	info.Interfaces = interfaces()
	return info
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "installer"
	}
	return h
}

func kioskMode() string {
	if b, err := os.ReadFile("/var/run/freebsd-sylve/kiosk-mode"); err == nil {
		if m := strings.TrimSpace(string(b)); m != "" {
			return m
		}
	}
	if _, err := os.Stat("/dev/dri/card0"); err == nil {
		return "drm"
	}
	if _, err := os.Stat("/dev/fb0"); err == nil {
		return "x11"
	}
	return "headless"
}

func sysctl(names ...string) map[string]string {
	out := map[string]string{}
	sysctlBin := "/sbin/sysctl"
	if _, err := os.Stat(sysctlBin); err != nil {
		sysctlBin = "/usr/sbin/sysctl"
	}
	for _, name := range names {
		b, err := exec.Command(sysctlBin, "-n", name).Output()
		if err != nil {
			continue
		}
		out[name] = strings.TrimSpace(string(b))
	}
	return out
}

func interfaces() []Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := []Interface{}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		entry := Interface{Name: iface.Name}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			entry.IPv4 = append(entry.IPv4, ipnet.IP.String())
		}
		if len(entry.IPv4) > 0 {
			result = append(result, entry)
		}
	}
	return result
}
