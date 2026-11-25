// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
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
var stat = os.Stat

// ErrSystemHostnameNotConfigured indicates that the operating system returned no usable hostname.
var ErrSystemHostnameNotConfigured = errors.New("system_hostname_not_configured")

var cachedHostname string
var hostnameOnce sync.Once
var hostnameErr error

func GetSystemUUID() (string, error) {
	const kenvKey = "kern.hostuuid"

	uuid, err := getSysctlString(kenvKey)
	if err != nil {
		return "", err
	}
	return uuid, nil
}

func GetSystemHostname() (string, error) {
	hostnameOnce.Do(func() {
		cachedHostname, hostnameErr = getHostname()
		if hostnameErr != nil {
			return
		}

		cachedHostname = strings.TrimSpace(cachedHostname)
		if cachedHostname == "" {
			hostnameErr = ErrSystemHostnameNotConfigured
		}
	})

	return cachedHostname, hostnameErr
}

func GetOS() string {
	v, err := getSysctlString("kern.version")
	if err != nil {
		return "FreeBSD"
	}
	return v
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
	if err == nil {
		if strings.Contains(v, "BIOS") {
			return "BIOS"
		}
		if strings.Contains(v, "UEFI") {
			return "UEFI"
		}
	}

	if _, err := getSysctlString("machdep.efi_rt_handle_faults"); err == nil {
		return "UEFI"
	}
	if _, err := stat("/dev/efi"); err == nil {
		return "UEFI"
	}

	return "Unknown"
}

func ReadDiskSector(disk string, lba, sectorSize int64) ([]byte, error) {
	if lba < 0 {
		return nil, fmt.Errorf("logical block address must be non-negative: %d", lba)
	}
	if sectorSize <= 0 {
		return nil, fmt.Errorf("logical sector size must be positive: %d", sectorSize)
	}
	if sectorSize > int64(math.MaxInt) {
		return nil, fmt.Errorf("logical sector size exceeds supported range: %d", sectorSize)
	}
	if lba > math.MaxInt64/sectorSize {
		return nil, fmt.Errorf("logical block offset exceeds supported range")
	}

	file, err := os.Open(disk)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, sectorSize)

	_, err = file.ReadAt(buf, lba*sectorSize)
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

func GetCPUModel() string {
	model, err := getSysctlString("hw.model")
	if err != nil {
		return ""
	}
	return model
}

func hottestCelsius(deciKelvin []int64) float64 {
	if len(deciKelvin) == 0 {
		return 0
	}

	return math.Round(float64(slices.Max(deciKelvin))-2731.5) / 10
}

func GetCPUTemperature() float64 {
	readings := []int64{}
	for core := range GetLogicalCores() {
		if value, err := getSysctlInt64(fmt.Sprintf("dev.cpu.%d.temperature", core)); err == nil {
			readings = append(readings, value)
		}
	}

	return hottestCelsius(readings)
}

var smbiosPlaceholders = []string{
	"To Be Filled By O.E.M.",
	"Default string",
	"System manufacturer",
	"System Product Name",
	"System Serial Number",
	"Not Specified",
	"None",
}

func smbiosValue(raw string) string {
	value := strings.TrimSpace(raw)
	if slices.ContainsFunc(smbiosPlaceholders, func(placeholder string) bool {
		return strings.EqualFold(value, placeholder)
	}) {
		return ""
	}

	return value
}

var cachedSMBIOS [3]string
var smbiosOnce sync.Once

func GetSystemSMBIOS() (vendor, product, serial string) {
	smbiosOnce.Do(func() {
		for i, key := range []string{"maker", "product", "serial"} {
			out, _ := RunCommand("/bin/kenv", "-q", "smbios.system."+key)
			cachedSMBIOS[i] = smbiosValue(out)
		}
	})

	return cachedSMBIOS[0], cachedSMBIOS[1], cachedSMBIOS[2]
}

func GetLogicalCores() int {
	ncpu, err := getSysctlInt64("hw.ncpu")
	if err != nil || ncpu <= 0 {
		return 0
	}

	return int(ncpu)
}

func GetVMMMaxCPUs() (int64, error) {
	maxCPUs, err := getSysctlInt64("hw.vmm.maxcpu")
	if err != nil {
		return 0, err
	}
	if maxCPUs < 1 {
		return 0, fmt.Errorf("hw.vmm.maxcpu must be positive: %d", maxCPUs)
	}

	return maxCPUs, nil
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
