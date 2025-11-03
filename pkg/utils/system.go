// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"

	"github.com/alchemillahq/sylve/pkg/utils/sysctl"

	"github.com/mackerelio/go-osstat/loadavg"
	"github.com/mackerelio/go-osstat/uptime"
)

var getSysctlInt64 = sysctl.GetInt64
var getSysctlString = sysctl.GetString
var getHostname = os.Hostname
var getUptime = uptime.Get
var getLoadAvg = loadavg.Get

func GetSystemUUID() (string, error) {
	const kenvKey = "kern.hostuuid"

	uuid, err := getSysctlString(kenvKey)
	if err != nil {
		return "", err
	}
	return uuid, nil
}

func GetSystemHostname() (string, error) {
	return getHostname()
}

func GetOS() string {
	switch runtime.GOOS {
	case "freebsd":
		v, err := getSysctlString("kern.version")
		if err != nil {
			return "FreeBSD"
		}
		return v
	default:
		return "Linux"
	}
}

func GetUptime() (int64, error) {
	u, err := getUptime()
	if err != nil {
		return 0, err
	}
	return int64(u.Seconds()), nil
}

func GetLoadAvg() (string, error) {
	l, err := getLoadAvg()
	if err != nil {
		return "", err
	}

	avg1 := float64(l.Loadavg1)
	avg2 := float64(l.Loadavg5)
	avg3 := float64(l.Loadavg15)

	return fmt.Sprintf("%.2f %.2f %.2f", avg1, avg2, avg3), nil
}

func BootMode() string {
	v, err := getSysctlString("machdep.bootmethod")
	if err != nil {
		return "Unknown"
	}

	if strings.Contains(v, "BIOS") {
		return "BIOS"
	} else if strings.Contains(v, "UEFI") {
		return "UEFI"
	} else {
		return "Unknown"
	}
}

func ReadDiskSector(disk string, sector int64) ([]byte, error) {
	file, err := os.Open(disk)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sectorSize := int64(512)
	buf := make([]byte, sectorSize)

	_, err = file.ReadAt(buf, sector*sectorSize)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func IsGPT(sector []byte) bool {
	if len(sector) < 8 {
		return false
	}

	gptSignature := []byte{0x45, 0x46, 0x49, 0x20, 0x50, 0x41, 0x52, 0x54}
	for i := 0; i < 8; i++ {
		if sector[i] != gptSignature[i] {
			return false
		}
	}
	return true
}

func KillProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}

func GetSupportedCharsets() []string {
	output, err := RunCommand("iconv", "-l")
	if err != nil {
		return nil
	}

	lines := strings.Split(output, "\n")
	charsets := make([]string, 0)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		tokens := strings.Fields(line)
		charsets = append(charsets, tokens...)
	}

	return charsets
}

func GetSystemMemoryBytes() (int64, error) {
	const kenvKey = "hw.physmem"

	memBytes, err := getSysctlInt64(kenvKey)
	if err != nil {
		return 0, err
	}

	return memBytes, nil
}

func GetLogicalCores() int {
	ncpu, err := getSysctlInt64("hw.ncpu")
	if err != nil || ncpu <= 0 {
		return 0
	}

	return int(ncpu)
}

func GetSocketCount(coresPerSocket, threadsPerCore int) int {
	ncpu := GetLogicalCores()

	den := coresPerSocket * threadsPerCore
	if den <= 0 {
		return int(ncpu)
	}

	sockets := int(ncpu) / den
	if sockets <= 0 {
		sockets = 1
	}

	return sockets
}
