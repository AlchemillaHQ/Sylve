// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

const poolDeviceTestSize = 256 * 1024 * 1024

func poolDeviceTestMesh(partition bool) diskServiceInterfaces.Mesh {
	mesh := diskServiceInterfaces.Mesh{Classes: []diskServiceInterfaces.Class{
		{Name: "DISK", Geoms: []diskServiceInterfaces.Geom{{Name: "da7", Providers: []diskServiceInterfaces.Provider{{
			ID: "disk7", Name: "da7", Mode: "r0w0e0", MediaSize: poolDeviceTestSize, SectorSize: 512,
			Config: diskServiceInterfaces.Config{Ident: "serial7", LunID: "lun7"},
		}}}}},
		{Name: "PART"},
	}}
	if partition {
		mesh.Classes[1].Geoms = []diskServiceInterfaces.Geom{{
			Name: "da7", Config: diskServiceInterfaces.Config{Scheme: "GPT"},
			Consumers: []diskServiceInterfaces.Consumer{{
				ProviderLink: diskServiceInterfaces.Reference{Ref: "disk7"}, Mode: "r0w0e0",
			}},
			Providers: []diskServiceInterfaces.Provider{{
				ID: "part7", Name: "da7p1", Mode: "r0w0e0", MediaSize: poolDeviceTestSize / 2,
				Config: diskServiceInterfaces.Config{
					Type: "freebsd-zfs", RawType: "516E7CBA-6ECF-11D6-8FF8-00022D09712B",
					RawUUID: "partition-uuid", Length: poolDeviceTestSize / 2,
				},
			}},
		}}
	}
	return mesh
}

func TestPoolDeviceInventoryResolvesFreshDiskAndPartitionIdentities(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		mesh := poolDeviceTestMesh(partitioned)
		devices, err := poolDeviceInventoryFromMesh(&mesh)
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1+len(mesh.Classes[1].Geoms) {
			t.Fatalf("unexpected device count: %+v", devices)
		}
		disk := devices[0]
		if disk.Path != "/dev/da7" || disk.Size != poolDeviceTestSize || !disk.IdentityStable || disk.Busy || disk.Partitioned != partitioned {
			t.Fatalf("unexpected disk facts: %+v", disk)
		}
		if partitioned {
			part := devices[1]
			if part.Path != "/dev/da7p1" || !part.Partition || part.Partitioned || !part.IdentityStable || part.Size != poolDeviceTestSize/2 || !slices.Contains(part.Aliases, "/dev/gptid/partition-uuid") {
				t.Fatalf("unexpected partition facts: %+v", part)
			}
		}
	}
}

func TestPoolDeviceInventoryFollowsGEOMConsumerXMLAndAliases(t *testing.T) {
	var consumer diskServiceInterfaces.Consumer
	if err := xml.Unmarshal([]byte(`<consumer id="consumer"><provider ref="part7"/><mode>r0w0e0</mode></consumer>`), &consumer); err != nil {
		t.Fatal(err)
	}
	if consumer.ProviderLink.Ref != "part7" {
		t.Fatalf("lost nested GEOM provider reference: %+v", consumer)
	}
	mesh := poolDeviceTestMesh(true)
	mesh.Classes = append(mesh.Classes, diskServiceInterfaces.Class{
		Name: "LABEL", Geoms: []diskServiceInterfaces.Geom{{
			Consumers: []diskServiceInterfaces.Consumer{consumer},
			Providers: []diskServiceInterfaces.Provider{{
				ID: "label7", Name: "gpt/replacement", Mode: "r0w0e0", MediaSize: poolDeviceTestSize / 2,
			}},
		}},
	})
	devices, err := poolDeviceInventoryFromMesh(&mesh)
	if err != nil {
		t.Fatal(err)
	}
	if devices[1].Busy || !slices.Contains(devices[1].Aliases, "/dev/gpt/replacement") {
		t.Fatalf("idle alias not resolved: %+v", devices[1])
	}
	mesh.Classes = append(mesh.Classes, diskServiceInterfaces.Class{
		Name: "DEV", Geoms: []diskServiceInterfaces.Geom{{Consumers: []diskServiceInterfaces.Consumer{{
			ProviderLink: diskServiceInterfaces.Reference{Ref: "label7"}, Mode: "r1w1e1",
		}}}},
	})
	devices, err = poolDeviceInventoryFromMesh(&mesh)
	if err != nil {
		t.Fatal(err)
	}
	if !devices[0].Busy || !devices[1].Busy {
		t.Fatalf("an open alias did not protect its physical disk: %+v", devices)
	}
}

func TestPoolDeviceInventoryFailsClosedOnUnknownOwnership(t *testing.T) {
	for _, mode := range []string{"", "unknown", "r0w0", "r1w0e0", "r0w1e0", "r0w0e1", "r18446744073709551616w0e0"} {
		mesh := poolDeviceTestMesh(false)
		mesh.Classes[0].Geoms[0].Providers[0].Mode = mode
		devices, err := poolDeviceInventoryFromMesh(&mesh)
		if err != nil || len(devices) != 1 || !devices[0].Busy {
			t.Fatalf("mode %q was considered available: %+v %v", mode, devices, err)
		}
	}
	for _, modify := range []func(*diskServiceInterfaces.Mesh){
		func(mesh *diskServiceInterfaces.Mesh) { mesh.Classes[0].Geoms[0].Providers[0].ID = "" },
		func(mesh *diskServiceInterfaces.Mesh) {
			mesh.Classes[1].Geoms[0].Consumers[0].ProviderLink.Ref = "missing"
		},
		func(mesh *diskServiceInterfaces.Mesh) {
			mesh.Classes[1].Geoms[0].Providers[0].Name = "da7"
		},
		func(mesh *diskServiceInterfaces.Mesh) {
			mesh.Classes[1].Geoms[0].Providers[0].ID = "disk7"
		},
		func(mesh *diskServiceInterfaces.Mesh) {
			mesh.Classes[1].Geoms[0].Consumers[0].ProviderRef = "part7"
		},
	} {
		mesh := poolDeviceTestMesh(true)
		modify(&mesh)
		if _, err := poolDeviceInventoryFromMesh(&mesh); err == nil {
			t.Fatal("incomplete GEOM topology was accepted")
		}
	}
}

func TestPoolDeviceInventoryTreatsCyclicConsumersAsBusy(t *testing.T) {
	mesh := poolDeviceTestMesh(false)
	mesh.Classes = append(mesh.Classes, diskServiceInterfaces.Class{
		Name: "LABEL", Geoms: []diskServiceInterfaces.Geom{{
			Consumers: []diskServiceInterfaces.Consumer{
				{ProviderLink: diskServiceInterfaces.Reference{Ref: "disk7"}, Mode: "r0w0e0"},
				{ProviderLink: diskServiceInterfaces.Reference{Ref: "label7"}, Mode: "r0w0e0"},
			},
			Providers: []diskServiceInterfaces.Provider{{ID: "label7", Name: "gpt/cycle", Mode: "r0w0e0"}},
		}},
	})
	devices, err := poolDeviceInventoryFromMesh(&mesh)
	if err != nil || len(devices) != 1 || !devices[0].Busy || !slices.Contains(devices[0].Aliases, "/dev/gpt/cycle") {
		t.Fatalf("cyclic GEOM ownership did not fail closed: %+v %v", devices, err)
	}
}

func TestPoolDeviceInventoryProtectsBootSwapAndUnstableDisks(t *testing.T) {
	for _, kind := range []string{"efi", "freebsd-boot", "freebsd-swap"} {
		mesh := poolDeviceTestMesh(true)
		provider := &mesh.Classes[1].Geoms[0].Providers[0]
		provider.Config.Type, provider.Config.RawType = kind, ""
		devices, err := poolDeviceInventoryFromMesh(&mesh)
		if err != nil {
			t.Fatal(err)
		}
		for _, device := range devices {
			if kind == "freebsd-swap" && !device.Swap || kind != "freebsd-swap" && !device.Boot {
				t.Fatalf("protected partition type %s lost: %+v", kind, device)
			}
		}
	}
	mesh := poolDeviceTestMesh(false)
	mesh.Classes[0].Geoms[0].Providers[0].Config.Ident = ""
	mesh.Classes[0].Geoms[0].Providers[0].Config.LunID = ""
	devices, err := poolDeviceInventoryFromMesh(&mesh)
	if err != nil || devices[0].IdentityStable {
		t.Fatalf("an unidentifiable disk was considered stable: %+v %v", devices, err)
	}
}

func TestPoolDeviceInspectionSharesDiskLockAndDoesNotUseSMARTOrCaches(t *testing.T) {
	reads := 0
	service := &Service{
		poolDeviceMeshSource: func() (diskServiceInterfaces.Mesh, error) {
			reads++
			mesh := poolDeviceTestMesh(false)
			if reads == 2 {
				mesh.Classes[0].Geoms[0].Providers[0].Config.Ident = "different-disk"
			}
			return mesh, nil
		},
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			t.Fatal("pool mutations must not use cached/enriched physical inventory")
			return nil, nil
		},
		smartDataSource: func(diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
			t.Fatal("pool mutation probed SMART")
			return nil, nil, nil
		},
	}
	release := service.LockPoolDeviceMutation()
	if service.DiskOperationMutex.TryLock() {
		service.DiskOperationMutex.Unlock()
		t.Fatal("pool mutation did not acquire the disk operation lock")
	}
	first, err := service.ReadPoolDeviceInventory(t.Context())
	if err != nil {
		release()
		t.Fatal(err)
	}
	second, err := service.ReadPoolDeviceInventory(t.Context())
	release()
	if err != nil || reads != 2 || first[0].Identity == second[0].Identity {
		t.Fatalf("did not re-read physical identity: reads=%d %v", reads, err)
	}
	if !service.DiskOperationMutex.TryLock() {
		t.Fatal("disk operation lock was not released")
	}
	service.DiskOperationMutex.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ReadPoolDeviceInventory(ctx); !errors.Is(err, context.Canceled) || reads != 2 {
		t.Fatalf("canceled inspection reached GEOM: %v reads=%d", err, reads)
	}
}
