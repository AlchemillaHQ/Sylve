// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"strings"
	"testing"
)

func TestValidateLibvirtVersion(t *testing.T) {
	tests := []struct {
		name    string
		version uint64
		wantErr bool
	}{
		{name: "older minor", version: 12_004_000, wantErr: true},
		{name: "minimum", version: 12_005_000},
		{name: "newer release", version: 12_005_001},
		{name: "newer major", version: 13_000_000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateLibvirtVersion(test.version)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateLibvirtVersion(%d) error = %v, wantErr %t", test.version, err, test.wantErr)
			}
		})
	}
}

func TestValidateLibvirtVersionReportsRequiredAndActualVersions(t *testing.T) {
	err := validateLibvirtVersion(12_004_000)
	if err == nil {
		t.Fatal("expected unsupported version error")
	}
	if !strings.Contains(err.Error(), "12.5.0") || !strings.Contains(err.Error(), "12.4.0") {
		t.Fatalf("expected required and actual versions in error, got: %v", err)
	}
}
