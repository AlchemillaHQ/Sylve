// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alchemillahq/gzfs"
	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/disk/smart"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/rs/zerolog"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

var _ diskServiceInterfaces.DiskServiceInterface = (*Service)(nil)

const smartFailRetryAfter = 1 * time.Hour
const physicalDiskResolveCacheTTL = 30 * time.Second

type physicalDiskResolveCacheEntry struct {
	disk      diskServiceInterfaces.DiskInfo
	expiresAt time.Time
}

type smartFailureCacheEntry struct {
	failedAt time.Time
	identity string
}

type diskIdentity struct {
	uuid   string
	stable bool
}

type Service struct {
	DB                        *gorm.DB
	DiskOperationMutex        sync.Mutex
	ZFS                       zfsServiceInterfaces.ZfsServiceInterface
	GZFS                      *gzfs.Client
	smartFailCache            map[string]smartFailureCacheEntry
	smartFailMu               sync.Mutex
	selfTestDriver            selfTestBackend
	selfTestCache             map[string]selfTestCacheEntry
	selfTestCacheMu           sync.Mutex
	selfTestCacheTTL          time.Duration
	selfTestReadGroup         singleflight.Group
	selfTestDeviceLock        sync.Map
	selfTestActiveKinds       sync.Map
	selfTestScheduleMu        sync.Mutex
	selfTestJobEnqueue        func(context.Context, smartSelfTestSchedulerJob) error
	selfTestEventEnqueue      func(context.Context, smartSelfTestEventJob) error
	selfTestJobsReady         atomic.Bool
	selfTestTrackingActive    atomic.Bool
	selfTestEventRelayActive  atomic.Bool
	selfTestEventRelayVersion atomic.Uint64
	physicalDiskCache         map[string]physicalDiskResolveCacheEntry
	physicalDiskCacheMu       sync.Mutex
	physicalDiskSource        func() ([]diskServiceInterfaces.DiskInfo, error)
	smartDataSource           func(diskServiceInterfaces.DiskInfo) (any, *diskServiceInterfaces.DiskSelfTestLog, error)
	ataPowerModeSource        func(string) (smart.ATAPowerMode, error)
	scsiPowerModeSource       func(string) (smart.SCSIPowerMode, error)
	diskGPTSource             func(string) bool
}

func NewDiskService(db *gorm.DB, zfsService zfsServiceInterfaces.ZfsServiceInterface, gzfs *gzfs.Client) diskServiceInterfaces.DiskServiceInterface {
	if db != nil {
		db = db.Session(&gorm.Session{NowFunc: func() time.Time { return time.Now().UTC() }})
	}
	return &Service{
		DB:                db,
		ZFS:               zfsService,
		GZFS:              gzfs,
		smartFailCache:    make(map[string]smartFailureCacheEntry),
		selfTestDriver:    librarySelfTestBackend{},
		selfTestCache:     make(map[string]selfTestCacheEntry),
		selfTestCacheTTL:  defaultSelfTestCacheTTL,
		physicalDiskCache: make(map[string]physicalDiskResolveCacheEntry),
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
		diskType := "SSD"

		if strings.Contains(strings.ToLower(provider.Config.Descr), "virtual") ||
			strings.Contains(strings.ToLower(provider.Config.Descr), "iscsi") {
			diskType = "Virtual"
		} else if provider.Config.RotationRate != "0" && provider.Config.RotationRate != "" {
			diskType = "HDD"
		} else if strings.HasPrefix(provider.Name, "nvme") ||
			strings.HasPrefix(provider.Name, "nda") ||
			strings.HasPrefix(provider.Name, "nvd") ||
			strings.HasPrefix(provider.Alias, "nv") {
			diskType = "NVMe"
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

func physicalDiskIdentities(disks []diskServiceInterfaces.DiskInfo) []diskIdentity {
	seeds := make([]string, len(disks))
	counts := make(map[string]int, len(disks))
	for i, disk := range disks {
		if strings.TrimSpace(disk.LunID) == "" && strings.TrimSpace(disk.Serial) == "" {
			continue
		}
		seed := fmt.Sprintf("%s-%s", disk.LunID, disk.Serial)
		seeds[i] = seed
		counts[seed]++
	}
	identities := make([]diskIdentity, len(disks))
	for i, disk := range disks {
		seed := seeds[i]
		stable := seed != "" && counts[seed] == 1
		if !stable {
			seed = disk.Name
		}
		identities[i] = diskIdentity{uuid: utils.GenerateDeterministicUUID(seed), stable: stable}
	}
	return identities
}

func (s *Service) physicalDisks() ([]diskServiceInterfaces.DiskInfo, error) {
	if s.physicalDiskSource != nil {
		return s.physicalDiskSource()
	}

	mesh, err := s.ParseGeomOutput()
	if err != nil {
		return nil, err
	}

	return ExtractDiskInfo(&mesh)
}

func (s *Service) diskIsGPT(device string) bool {
	if s.diskGPTSource != nil {
		return s.diskGPTSource(device)
	}
	return s.IsDiskGPT(device)
}

func (s *Service) readSmartData(disk diskServiceInterfaces.DiskInfo, includeSelfTestLog bool) (any, *diskServiceInterfaces.DiskSelfTestLog, error) {
	if s.smartDataSource != nil {
		return s.smartDataSource(disk)
	}
	return s.getSmartData(disk, includeSelfTestLog)
}

func (s *Service) GetDiskDevices(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return s.getDiskDevices(ctx, true, false)
}

func (s *Service) GetDiskDevicesWithoutSMART(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return s.getDiskDevices(ctx, false, false)
}

func (s *Service) GetDiskDevicesForSMARTMonitor(ctx context.Context) ([]diskServiceInterfaces.Disk, error) {
	return s.getDiskDevices(ctx, true, true)
}

func (s *Service) GetDiskDevicesInventory(_ context.Context) ([]diskServiceInterfaces.Disk, error) {
	disks, err := s.physicalDisks()
	if err != nil {
		return nil, err
	}
	result := make([]diskServiceInterfaces.Disk, 0, len(disks))
	identities := physicalDiskIdentities(disks)
	for i, item := range disks {
		result = append(result, diskServiceInterfaces.Disk{
			UUID:           identities[i].uuid,
			IdentityStable: identities[i].stable,
			Device:         item.Name,
			Type:           item.Type,
			Size:           uint64(item.MediaSize),
			Model:          item.Description,
			Serial:         item.Serial,
			Partitions:     []diskServiceInterfaces.Partition{},
		})
	}
	return result, nil
}

func (s *Service) getDiskDevices(ctx context.Context, includeSMART, avoidWake bool) ([]diskServiceInterfaces.Disk, error) {
	var disks []diskServiceInterfaces.Disk

	dinfo, err := s.physicalDisks()
	if err != nil {
		return nil, err
	}

	identities := physicalDiskIdentities(dinfo)
	s.pruneSmartFailureCache(dinfo)
	skipRemainingSMART := false
	for i, d := range dinfo {
		var disk diskServiceInterfaces.Disk
		disk.UUID = identities[i].uuid
		disk.IdentityStable = identities[i].stable
		disk.Device = d.Name
		disk.Type = d.Type
		disk.Size = uint64(d.MediaSize)
		disk.Serial = d.Serial

		if !avoidWake {
			disk.GPT = s.diskIsGPT("/dev/" + d.Name)
		}

		if includeSMART && !skipRemainingSMART {
			failed := s.smartReadSuppressed(d, time.Now())
			if !failed && avoidWake && d.Type != "NVMe" {
				probe := s.ataPowerModeSource
				if probe == nil {
					probe = smart.CheckATAPowerMode
				}
				mode, probeErr := probe(d.Name)
				if probeErr == nil && mode.IsStandbyOrSleeping() {
					disk.SmartReadPowerSkipped = true
					disk.SmartData = nil
					goto smartDone
				}
				if errors.Is(probeErr, smart.ErrUnsupportedFeature) {
					scsiProbe := s.scsiPowerModeSource
					if scsiProbe == nil {
						scsiProbe = smart.CheckSCSIPowerMode
					}
					scsiMode, scsiProbeErr := scsiProbe(d.Name)
					probeErr = scsiProbeErr
					if scsiProbeErr == nil && scsiMode.IsStandbyOrSleeping() {
						disk.SmartReadPowerSkipped = true
						disk.SmartData = nil
						goto smartDone
					}
				}
				if probeErr != nil && !errors.Is(probeErr, smart.ErrUnsupportedFeature) {
					s.recordSmartFailure(d, time.Now())
					logger.LogWithDeduplication(zerolog.DebugLevel, fmt.Sprintf("Failed to check disk power state %v", probeErr))
					disk.SmartData = nil
					if smart.IsControllerError(probeErr) {
						logger.L.Warn().Str("device", d.Name).Msg("controller_level_error_skipping_remaining_smart_reads")
						skipRemainingSMART = true
					}
					goto smartDone
				}
			}

			if !failed {
				smartData, selfTestLog, err := s.readSmartData(d, false)
				if err != nil {
					s.recordSmartFailure(d, time.Now())
					logger.LogWithDeduplication(zerolog.DebugLevel, fmt.Sprintf("Failed to retrieve S.M.A.R.T data %v", err))

					if smart.IsControllerError(err) {
						logger.L.Warn().
							Str("device", d.Name).
							Msg("controller_level_error_skipping_remaining_smart_reads")
						disk.SmartData = nil
						skipRemainingSMART = true
					}

					disk.SmartData = nil
				} else if err == nil && smartData != nil {
					disk.SmartData = smartData
					disk.SelfTestLog = selfTestLog
				}
			}
		} else {
			disk.SmartData = nil
		}

	smartDone:
		disk.WearOut = s.formatWearOut(d.Type, disk.SmartData)
		disk.Model = d.Description
		if avoidWake {
			disks = append(disks, disk)
			continue
		}

		disk.Partitions = []diskServiceInterfaces.Partition{}
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

			in, _, err := s.GZFS.Zpool.IsDeviceInZpool(ctx, devPath)
			if err == nil && in {
				disk.Usage = "ZFS"
				found = true
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

func smartFailureIdentity(disk diskServiceInterfaces.DiskInfo) string {
	return strings.Join([]string{
		strings.TrimSpace(disk.LunID),
		strings.TrimSpace(disk.Serial),
		strings.TrimSpace(disk.Description),
		strconv.FormatInt(disk.MediaSize, 10),
	}, "\x00")
}

func (s *Service) smartReadSuppressed(disk diskServiceInterfaces.DiskInfo, now time.Time) bool {
	identity := smartFailureIdentity(disk)
	s.smartFailMu.Lock()
	defer s.smartFailMu.Unlock()
	entry, ok := s.smartFailCache[disk.Name]
	if !ok {
		return false
	}
	if entry.identity != identity || now.Sub(entry.failedAt) >= smartFailRetryAfter {
		delete(s.smartFailCache, disk.Name)
		return false
	}
	return true
}

func (s *Service) recordSmartFailure(disk diskServiceInterfaces.DiskInfo, now time.Time) {
	s.smartFailMu.Lock()
	if s.smartFailCache == nil {
		s.smartFailCache = make(map[string]smartFailureCacheEntry)
	}
	s.smartFailCache[disk.Name] = smartFailureCacheEntry{failedAt: now, identity: smartFailureIdentity(disk)}
	s.smartFailMu.Unlock()
}

func (s *Service) pruneSmartFailureCache(disks []diskServiceInterfaces.DiskInfo) {
	present := make(map[string]string, len(disks))
	for _, disk := range disks {
		present[disk.Name] = smartFailureIdentity(disk)
	}
	s.smartFailMu.Lock()
	for name, entry := range s.smartFailCache {
		if identity, ok := present[name]; !ok || identity != entry.identity {
			delete(s.smartFailCache, name)
		}
	}
	s.smartFailMu.Unlock()
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

	output, err := utils.RunCommand("/sbin/gpart", "create", "-s", "gpt", device)
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
