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
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mackerelio/go-osstat/loadavg"
)

func resetHostnameCacheForTest() {
	hostnameOnce = sync.Once{}
	cachedHostname = ""
	hostnameErr = nil
}

func TestGetSystemUUID_Success(t *testing.T) {
	original := getSysctlString
	defer func() { getSysctlString = original }()

	getSysctlString = func(key string) (string, error) {
		if key == "kern.hostuuid" {
			return "mocked-uuid-1234", nil
		}
		return "", errors.New("unexpected key")
	}

	uuid, err := GetSystemUUID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "mocked-uuid-1234" {
		t.Errorf("expected 'mocked-uuid-1234', got %s", uuid)
	}
}

func TestGetSystemUUID_Error(t *testing.T) {
	original := getSysctlString
	defer func() { getSysctlString = original }()

	getSysctlString = func(key string) (string, error) {
		return "", errors.New("sysctl failed")
	}

	_, err := GetSystemUUID()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetSystemHostname_Success(t *testing.T) {
	original := getHostname
	resetHostnameCacheForTest()
	defer resetHostnameCacheForTest()
	defer func() { getHostname = original }()

	getHostname = func() (string, error) {
		return "mocked-hostname", nil
	}

	hostname, err := GetSystemHostname()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hostname != "mocked-hostname" {
		t.Errorf("expected 'mocked-hostname', got %s", hostname)
	}
}

func TestGetSystemHostname_Error(t *testing.T) {
	original := getHostname
	resetHostnameCacheForTest()
	defer resetHostnameCacheForTest()
	defer func() { getHostname = original }()

	getHostname = func() (string, error) {
		return "", errors.New("hostname failed")
	}

	_, err := GetSystemHostname()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetUptime_Success(t *testing.T) {
	original := getUptime
	defer func() { getUptime = original }()

	getUptime = func() (time.Duration, error) {
		return time.Hour, nil
	}

	seconds, err := GetUptime()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seconds != 3600 {
		t.Errorf("expected 3600 seconds, got %d", seconds)
	}
}

func TestGetUptime_Error(t *testing.T) {
	original := getUptime
	defer func() { getUptime = original }()

	getUptime = func() (time.Duration, error) {
		return 0, errors.New("uptime failed")
	}

	_, err := GetUptime()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetLoadAvg_Success(t *testing.T) {
	original := getLoadAvg
	defer func() { getLoadAvg = original }()

	getLoadAvg = func() (*loadavg.Stats, error) {
		return &loadavg.Stats{
			Loadavg1:  0.55,
			Loadavg5:  0.76,
			Loadavg15: 1.23,
		}, nil
	}

	result, err := GetLoadAvg()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "0.55 0.76 1.23"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestGetLoadAvg_Error(t *testing.T) {
	original := getLoadAvg
	defer func() { getLoadAvg = original }()

	getLoadAvg = func() (*loadavg.Stats, error) {
		return nil, errors.New("load average error")
	}

	_, err := GetLoadAvg()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBootMode(t *testing.T) {
	original := getSysctlString
	defer func() { getSysctlString = original }()

	tests := []struct {
		name         string
		mockReturn   string
		mockError    error
		expectedMode string
	}{
		{
			name:         "BIOS mode",
			mockReturn:   "BIOS",
			mockError:    nil,
			expectedMode: "BIOS",
		},
		{
			name:         "UEFI mode",
			mockReturn:   "UEFI Firmware",
			mockError:    nil,
			expectedMode: "UEFI",
		},
		{
			name:         "Unknown mode string",
			mockReturn:   "SomeOtherBoot",
			mockError:    nil,
			expectedMode: "Unknown",
		},
		{
			name:         "Error from sysctl",
			mockReturn:   "",
			mockError:    errors.New("sysctl error"),
			expectedMode: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getSysctlString = func(key string) (string, error) {
				return tt.mockReturn, tt.mockError
			}

			result := BootMode()
			if result != tt.expectedMode {
				t.Errorf("BootMode() = %q; expected %q", result, tt.expectedMode)
			}
		})
	}
}

func TestReadDiskSector(t *testing.T) {
	tmp, err := os.CreateTemp("", "diskmock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	data := make([]byte, 1024)
	copy(data[512:], []byte("SECTOR1DATA"))
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}

	buf, err := ReadDiskSector(tmp.Name(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(buf[:11]) != "SECTOR1DATA" {
		t.Errorf("expected 'SECTOR1DATA', got %q", string(buf[:11]))
	}
}

func TestIsGPT(t *testing.T) {
	tests := []struct {
		name     string
		sector   []byte
		expected bool
	}{
		{
			name:     "Valid GPT Signature",
			sector:   []byte{0x45, 0x46, 0x49, 0x20, 0x50, 0x41, 0x52, 0x54},
			expected: true,
		},
		{
			name:     "Invalid Signature",
			sector:   []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77},
			expected: false,
		},
		{
			name:     "Partially Correct Signature",
			sector:   []byte{0x45, 0x46, 0x49, 0x20, 0x50, 0x00, 0x00, 0x00},
			expected: false,
		},
		{
			name:     "Too Short Input",
			sector:   []byte{0x45, 0x46},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsGPT(tt.sector)
			if result != tt.expected {
				t.Errorf("IsGPT(%v) = %v; want %v", tt.sector, result, tt.expected)
			}
		})
	}
}
