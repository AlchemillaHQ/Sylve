// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build !freebsd

package smart

import "testing"

func TestReadStub(t *testing.T) {
	tests := []struct {
		name       string
		devicePath string
		wantDevice string
	}{
		{
			name:       "strips dev prefix",
			devicePath: "/dev/ada0",
			wantDevice: "ada0",
		},
		{
			name:       "keeps bare device name",
			devicePath: "nvme0",
			wantDevice: "nvme0",
		},
		{
			name:       "empty path",
			devicePath: "",
			wantDevice: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Read(tt.devicePath)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got == nil {
				t.Fatal("expected device info, got nil")
			}

			if got.Device != tt.wantDevice {
				t.Fatalf("unexpected device: got %q, want %q", got.Device, tt.wantDevice)
			}

			if got.Protocol != "Mock" {
				t.Fatalf("unexpected protocol: got %q, want %q", got.Protocol, "Mock")
			}

			if got.Temperature != 35 {
				t.Fatalf("unexpected temperature: got %d, want %d", got.Temperature, 35)
			}

			if got.PowerOnHours != 100 {
				t.Fatalf("unexpected power-on hours: got %d, want %d", got.PowerOnHours, 100)
			}

			if got.PowerCycleCount != 10 {
				t.Fatalf("unexpected power-cycle count: got %d, want %d", got.PowerCycleCount, 10)
			}

			if got.Attributes == nil {
				t.Fatal("expected non-nil attributes slice")
			}

			if len(got.Attributes) != 0 {
				t.Fatalf("unexpected attributes length: got %d, want %d", len(got.Attributes), 0)
			}
		})
	}
}
