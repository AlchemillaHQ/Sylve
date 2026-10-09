// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

func TestExtractDiskInfoTypes(t *testing.T) {
	tests := []struct {
		name         string
		description  string
		rotationRate string
		want         string
	}{
		{name: "vtbd0", rotationRate: "unknown", want: "Virtual"},
		{name: "vtbd1", rotationRate: "0", want: "Virtual"},
		{name: "da0", description: "ATA KINGSTON SEDC500", rotationRate: "0", want: "SSD"},
		{name: "da1", description: "TOSHIBA MG07SCA12TA", rotationRate: "7200", want: "HDD"},
		{name: "nda0", rotationRate: "unknown", want: "NVMe"},
		{name: "nvd0", rotationRate: "0", want: "NVMe"},
		{name: "da2", description: "Virtual Disk", rotationRate: "unknown", want: "Virtual"},
		{name: "da3", description: "iSCSI Disk", rotationRate: "0", want: "Virtual"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mesh := diskServiceInterfaces.Mesh{Classes: []diskServiceInterfaces.Class{
				{Name: "DISK", Geoms: []diskServiceInterfaces.Geom{{Providers: []diskServiceInterfaces.Provider{{
					Name: test.name,
					Config: diskServiceInterfaces.Config{
						Descr:        test.description,
						RotationRate: test.rotationRate,
					},
				}}}}},
				{Name: "PART"},
			}}
			disks, err := ExtractDiskInfo(&mesh)
			if err != nil {
				t.Fatal(err)
			}
			if len(disks) != 1 || disks[0].Type != test.want {
				t.Fatalf("disks=%+v want type=%s", disks, test.want)
			}
			if disks[0].Description != test.description || disks[0].Serial != "" {
				t.Fatalf("invented disk identity: %+v", disks[0])
			}
		})
	}
}
