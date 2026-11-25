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
	"fmt"
	"regexp"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

var _ diskServiceInterfaces.PoolDeviceInspector = (*Service)(nil)

var geomIdleModePattern = regexp.MustCompile(`^r0+w0+e0+$`)

func (s *Service) LockPoolDeviceMutation() func() {
	s.DiskOperationMutex.Lock()
	return s.DiskOperationMutex.Unlock
}

func (s *Service) ReadPoolDeviceInventory(ctx context.Context) ([]diskServiceInterfaces.PoolDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readMesh := s.ParseGeomOutput
	if s.poolDeviceMeshSource != nil {
		readMesh = s.poolDeviceMeshSource
	}
	mesh, err := readMesh()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return poolDeviceInventoryFromMesh(&mesh)
}

func geomModeIdle(mode string) bool {
	return geomIdleModePattern.MatchString(strings.ToLower(strings.TrimSpace(mode)))
}

func poolDeviceInventoryFromMesh(mesh *diskServiceInterfaces.Mesh) ([]diskServiceInterfaces.PoolDevice, error) {
	disks, err := ExtractDiskInfo(mesh)
	if err != nil {
		return nil, err
	}
	type providerState struct {
		name             string
		busy             bool
		children, labels []string
	}
	providers := make(map[string]*providerState)
	byName := make(map[string]string)
	partitioned := make(map[string]bool)
	for _, class := range mesh.Classes {
		for _, geom := range class.Geoms {
			if class.Name == "PART" {
				partitioned[geom.Name] = true
			}
			for _, provider := range geom.Providers {
				if provider.ID == "" || provider.Name == "" || providers[provider.ID] != nil || byName[provider.Name] != "" {
					return nil, fmt.Errorf("GEOM provider identity is unavailable or ambiguous: %q", provider.Name)
				}
				providers[provider.ID] = &providerState{name: provider.Name, busy: !geomModeIdle(provider.Mode)}
				byName[provider.Name] = provider.ID
			}
		}
	}
	for _, class := range mesh.Classes {
		for _, geom := range class.Geoms {
			for _, consumer := range geom.Consumers {
				parent := consumer.ProviderLink.Ref
				if parent == "" {
					parent = consumer.ProviderRef
				} else if consumer.ProviderRef != "" && consumer.ProviderRef != parent {
					return nil, fmt.Errorf("inconsistent GEOM consumer reference")
				}
				state := providers[parent]
				if state == nil {
					return nil, fmt.Errorf("GEOM consumer provider is unavailable")
				}
				state.busy = state.busy || !geomModeIdle(consumer.Mode)
				for _, provider := range geom.Providers {
					state.children = append(state.children, provider.ID)
					if class.Name == "LABEL" {
						state.labels = append(state.labels, provider.ID)
					}
				}
			}
		}
	}
	var busy func(string, map[string]bool) bool
	busy = func(id string, seen map[string]bool) bool {
		provider := providers[id]
		if seen[id] || provider == nil || provider.busy {
			return true
		}
		seen[id] = true
		for _, child := range provider.children {
			if busy(child, seen) {
				return true
			}
		}
		delete(seen, id)
		return false
	}
	aliases := func(name string, paths []string) []string {
		result := append([]string(nil), paths...)
		seen := make(map[string]bool)
		var walk func(string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			for _, child := range providers[id].labels {
				result = append(result, canonicalDevicePath(providers[child].name))
				walk(child)
			}
		}
		walk(byName[name])
		return result
	}
	identities := physicalDiskIdentities(disks)
	partitionIDs := make(map[string]int)
	for _, disk := range disks {
		for _, partition := range disk.Partitions {
			partitionIDs[partition.UUID]++
		}
	}
	var result []diskServiceInterfaces.PoolDevice
	for index, disk := range disks {
		id := byName[disk.Name]
		if disk.MediaSize <= 0 {
			continue
		}
		identity := identities[index]
		hasSwap := false
		boot := disk.IsBootDevice
		for _, partition := range disk.Partitions {
			kind := strings.ToLower(partition.Type + " " + partition.Filesystem)
			hasSwap = hasSwap || strings.Contains(kind, "swap")
			boot = boot || strings.Contains(kind, "boot") || strings.Contains(kind, "efi")
		}
		inUse := busy(id, make(map[string]bool))
		result = append(result, diskServiceInterfaces.PoolDevice{
			Path: canonicalDevicePath(disk.Name), Aliases: aliases(disk.Name, diskDevicePaths(disk, false)),
			Disk: disk.Name, Identity: fmt.Sprintf("%s|%s|%d", identity.uuid, disk.Serial, disk.MediaSize),
			IdentityStable: identity.stable, Size: uint64(disk.MediaSize), Boot: boot,
			Swap: hasSwap, Busy: inUse, Partitioned: partitioned[disk.Name],
		})
		for _, partition := range disk.Partitions {
			if partition.Size <= 0 {
				return nil, fmt.Errorf("partition provider is unavailable")
			}
			paths := partitionDevicePaths(partition)
			if partition.UUID != "" {
				paths = append(paths, "/dev/gptid/"+partition.UUID)
			}
			result = append(result, diskServiceInterfaces.PoolDevice{
				Path: canonicalDevicePath(partition.Name), Aliases: aliases(partition.Name, paths),
				Disk: disk.Name, Identity: fmt.Sprintf("%s|%s|%s|%d", partition.UUID, identity.uuid, disk.Serial, partition.Size),
				IdentityStable: identity.stable || (partition.UUID != "" && partitionIDs[partition.UUID] == 1),
				Size:           uint64(partition.Size), Boot: boot, Swap: hasSwap, Busy: inUse,
				Partition: true, PartitionType: partition.Filesystem,
			})
		}
	}
	return result, nil
}
