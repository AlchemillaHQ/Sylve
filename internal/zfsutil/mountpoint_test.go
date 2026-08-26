// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsutil

import (
	"testing"

	"github.com/alchemillahq/gzfs"
)

func TestFilesystemMountpoint(t *testing.T) {
	tests := []struct {
		name    string
		dataset *gzfs.Dataset
		want    string
		wantErr bool
	}{
		{name: "nil dataset", wantErr: true},
		{name: "volume", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeVolume, Mountpoint: "/mnt/tank/data"}, wantErr: true},
		{name: "empty", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem}, wantErr: true},
		{name: "dash", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "-"}, wantErr: true},
		{name: "none", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "none"}, wantErr: true},
		{name: "legacy", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "legacy"}, wantErr: true},
		{name: "relative", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "mnt/data"}, wantErr: true},
		{name: "root", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/"}, wantErr: true},
		{name: "control character", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/tank\ndata"}, wantErr: true},
		{name: "c1 control character", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/tank\u0085"}, wantErr: true},
		{name: "whitespace", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/tank data"}, wantErr: true},
		{name: "quote", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: `/mnt/tank"data`}, wantErr: true},
		{name: "backslash", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: `/mnt/tank\data`}, wantErr: true},
		{name: "dollar", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/$tank"}, wantErr: true},
		{name: "unicode", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/tänk"}, wantErr: true},
		{name: "unclean", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/tank/../data"}, wantErr: true},
		{name: "usable", dataset: &gzfs.Dataset{Name: "tank/data", Type: gzfs.DatasetTypeFilesystem, Mountpoint: "/mnt/Tank-1_data.2"}, want: "/mnt/Tank-1_data.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilesystemMountpoint(tt.dataset)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got mountpoint %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mountpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeMountpoint(t *testing.T) {
	got, err := NormalizeMountpoint("  /mnt/tank/../pool  ")
	if err != nil {
		t.Fatalf("normalize mountpoint: %v", err)
	}
	if got != "/mnt/pool" {
		t.Fatalf("mountpoint = %q, want /mnt/pool", got)
	}
}
