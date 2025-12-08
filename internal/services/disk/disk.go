// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/zfs"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

var _ diskServiceInterfaces.DiskServiceInterface = (*Service)(nil)

type Service struct {
	DB                 *gorm.DB
	DiskOperationMutex sync.Mutex
	ZFS                zfsServiceInterfaces.ZfsServiceInterface
}

func NewDiskService(db *gorm.DB, zfsService zfsServiceInterfaces.ZfsServiceInterface) diskServiceInterfaces.DiskServiceInterface {
	return &Service{
		DB:  db,
		ZFS: zfsService,
	}
}

func findClassByName(mesh *diskServiceInterfaces.Mesh, name string) *diskServiceInterfaces.Class {
	for i := range mesh.Classes {
		if mesh.Classes[i].Name == name {
			return &mesh.Classes[i]
		}
	}
	return nil
}

func ExtractDiskInfo(mesh *diskServiceInterfaces.Mesh) ([]diskServiceInterfaces.DiskInfo, error) {
	if mesh == nil {
		return nil, fmt.Errorf("nil mesh provided")
	}

	var disks []diskServiceInterfaces.DiskInfo
	diskClass := findClassByName(mesh, "DISK")
	if diskClass == nil {
		return nil, fmt.Errorf("DISK class not found in mesh")
	}

	partClass := findClassByName(mesh, "PART")
	if partClass == nil {
		return nil, fmt.Errorf("PART class not found in mesh")
	}

	for _, geom := range diskClass.Geoms {
		if len(geom.Providers) == 0 {
			continue
		}

		provider := geom.Providers[0]
		diskType := "Unknown"

		if provider.Config.RotationRate == "0" {
			if strings.HasPrefix(provider.Name, "nvme") || strings.HasPrefix(provider.Name, "nda") || strings.HasPrefix(provider.Alias, "nv") {
				diskType = "NVMe"
			} else {
				diskType = "SSD"
			}
		} else if provider.Config.RotationRate != "unknown" && provider.Config.RotationRate != "0" {
			diskType = "HDD"
		} else {
			diskType = "Unknown"
		}

		disk := diskServiceInterfaces.DiskInfo{
			Name:         provider.Name,
			Aliases:      []string{},
			MediaSize:    provider.MediaSize,
			SectorSize:   provider.SectorSize,
			Description:  provider.Config.Descr,
			RotationRate: provider.Config.RotationRate,
			Serial:       provider.Config.Ident,
			LunID:        provider.Config.LunID,
			Type:         diskType,
			Partitions:   []diskServiceInterfaces.PartitionInfo{},
			IsBootDevice: false,
		}

		if provider.Alias != "" {
			disk.Aliases = append(disk.Aliases, provider.Alias)
		}

		for _, partGeom := range partClass.Geoms {
			if partGeom.Name == provider.Name {
				isGPT := false
				if partGeom.Config.Scheme == "GPT" {
					isGPT = true
				}

				for _, partProvider := range partGeom.Providers {
					partition := diskServiceInterfaces.PartitionInfo{
						Name:       partProvider.Name,
						Aliases:    []string{},
						Type:       partProvider.Config.Type,
						Label:      partProvider.Config.Label,
						Size:       partProvider.Config.Length,
						StartBlock: partProvider.Config.Start,
						EndBlock:   partProvider.Config.End,
						UUID:       partProvider.Config.RawUUID,
						Filesystem: utils.GetDiskTypeFromUUID(partProvider.Config.RawType, partProvider.Config.Type),
						GPT:        isGPT,
					}

					if partProvider.Alias != "" {
						partition.Aliases = append(partition.Aliases, partProvider.Alias)
					}

					if strings.Contains(partition.Type, "boot") || strings.Contains(partition.Type, "efi") {
						disk.IsBootDevice = true
					}

					disk.Partitions = append(disk.Partitions, partition)
				}
			}
		}

		disks = append(disks, disk)
	}

	return disks, nil
}

func (s *Service) GetDiskDevices() ([]diskServiceInterfaces.Disk, error) {
	var disks []diskServiceInterfaces.Disk

	mesh, err := s.ParseGeomOutput()
	if err != nil {
		return nil, err
	}

	dinfo, err := ExtractDiskInfo(&mesh)

	if err != nil {
		return nil, err
	}

	for _, d := range dinfo {
		var disk diskServiceInterfaces.Disk
		disk.UUID = utils.GenerateDeterministicUUID(fmt.Sprintf("%s-%s", d.LunID, d.Serial))
		disk.Device = d.Name
		disk.Type = d.Type
		disk.Size = uint64(d.MediaSize)
		disk.Serial = d.Serial

		if s.IsDiskGPT("/dev/" + d.Name) {
			disk.GPT = true
		} else {
			disk.GPT = false
		}

		if d.Type == "NVMe" || d.Type == "SSD" || d.Type == "HDD" {
			smartData, err := s.GetSmartData(d)
			if err != nil {
				return nil, err
			}

			if smartData != nil {
				disk.SmartData = smartData
			}
		} else {
			disk.SmartData = nil
		}

		if d.Type == "NVMe" || d.Type == "SSD" || d.Type == "HDD" {
			wearOut, err := s.GetWearOut(disk.SmartData)
			if err != nil {
				return nil, err
			}

			disk.WearOut = fmt.Sprintf("%.2f", wearOut)
		} else {
			disk.WearOut = "Unknown"
		}

		disk.Partitions = []diskServiceInterfaces.Partition{}

		disk.Model = d.Description
		for _, p := range d.Partitions {
			if strings.HasPrefix(p.Name, d.Name) {
				var partition diskServiceInterfaces.Partition
				partition.UUID = p.UUID
				partition.Name = p.Name
				partition.Usage = p.Filesystem
				partition.Size = uint64(p.Size)

				disk.Partitions = append(disk.Partitions, partition)
			}
		}

		if len(disk.Partitions) == 0 {
			found := false
			devPath := "/dev/" + d.Name

			pools, err := zfs.ListZpools()
			if err == nil {
				for _, pool := range pools {
					for _, vdev := range pool.Vdevs {
						if zfs.VdevContainsDisk(vdev, devPath) {
							disk.Usage = "ZFS"
							found = true
							break
						}
					}
					if found {
						break
					}
				}
			}

			if !found {
				disk.Usage = "Unused"
			}
		} else {
			disk.Usage = "Partitions"
		}

		disks = append(disks, disk)
	}

	return disks, nil
}

func (s *Service) GetDiskSize(device string) (uint64, error) {
	size, err := diskUtils.GetDiskSize(device)

	if err != nil {
		return 0, fmt.Errorf("failed to determine disk size: %v", err)
	}

	return size, nil
}

func (s *Service) DestroyPartitionTable(device string) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	if _, err := os.Stat(device); os.IsNotExist(err) {
		return fmt.Errorf("device does not exist: %v", err)
	}

	file, err := os.OpenFile(device, os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open disk: %v", err)
	}

	defer file.Close()

	diskSize, err := s.GetDiskSize(device)
	if err != nil {
		return fmt.Errorf("failed to get disk size: %v", err)
	}

	const wipeSize = 1024 * 1024
	buffer := make([]byte, wipeSize)

	_, err = file.WriteAt(buffer, 0)
	if err != nil {
		return fmt.Errorf("error wiping primary GPT: %v", err)
	}

	if diskSize > wipeSize {
		_, err = file.WriteAt(buffer, int64(diskSize)-int64(wipeSize))
		if err != nil {
			return fmt.Errorf("error wiping backup GPT: %v", err)
		}
	} else {
		return fmt.Errorf("disk size is too small for GPT")
	}

	err = syscall.Fsync(int(file.Fd()))
	if err != nil {
		return fmt.Errorf("failed to sync disk: %v", err)
	}

	return nil
}

func (s *Service) InitializeGPT(device string) error {
	s.DiskOperationMutex.Lock()
	defer s.DiskOperationMutex.Unlock()

	output, err := utils.RunCommand("gpart", "create", "-s", "gpt", device)
	if err != nil {
		if strings.Contains(output, "File exists") {
			return fmt.Errorf("gpt_partition_table_already_exists")
		}

		return fmt.Errorf("failed_to_create_gpt_partition_table %s", output)
	}

	baseDevice := strings.TrimPrefix(device, "/dev/")
	expectedOutput := fmt.Sprintf("%s created", baseDevice)

	if !strings.Contains(output, expectedOutput) {
		return fmt.Errorf("failed_to_create_gpt_partition_table %s", output)
	}

	return nil
}

func (s *Service) IsDiskGPT(device string) bool {
	gptSector, err := utils.ReadDiskSector(device, 1)
	if err != nil {
		if strings.Contains(err.Error(), "device not configured") {
			return false
		}

		logger.LogWithDeduplication(zerolog.DebugLevel, fmt.Sprintf("failed to read sector 1: %v", err))

		return false
	}

	return utils.IsGPT(gptSector)
}
