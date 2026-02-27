// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/digitalocean/go-libvirt"
	"github.com/klauspost/cpuid/v2"
)

func domainReasonToString(state libvirt.DomainState, reason int32) libvirtServiceInterfaces.DomainStateReason {
	switch state {
	case libvirt.DomainRunning:
		switch reason {
		case 0:
			return libvirtServiceInterfaces.DomainReasonUnknown
		case 1:
			return libvirtServiceInterfaces.DomainReasonRunningBooted
		case 2:
			return libvirtServiceInterfaces.DomainReasonRunningMigrated
		case 3:
			return libvirtServiceInterfaces.DomainReasonRunningRestored
		case 4:
			return libvirtServiceInterfaces.DomainReasonRunningFromSnapshot
		case 5:
			return libvirtServiceInterfaces.DomainReasonRunningUnpaused
		case 6:
			return libvirtServiceInterfaces.DomainReasonRunningMigrationCanceled
		case 7:
			return libvirtServiceInterfaces.DomainReasonRunningSaveCanceled
		case 8:
			return libvirtServiceInterfaces.DomainReasonRunningWakeup
		case 9:
			return libvirtServiceInterfaces.DomainReasonRunningCrashed
		default:
			return libvirtServiceInterfaces.DomainReasonUnknown
		}

	case libvirt.DomainShutoff:
		switch reason {
		case 0:
			return libvirtServiceInterfaces.DomainReasonUnknown
		case 1:
			return libvirtServiceInterfaces.DomainReasonShutoffShutdown
		case 2:
			return libvirtServiceInterfaces.DomainReasonShutoffDestroyed
		case 3:
			return libvirtServiceInterfaces.DomainReasonShutoffCrashed
		case 4:
			return libvirtServiceInterfaces.DomainReasonShutoffSaved
		case 5:
			return libvirtServiceInterfaces.DomainReasonShutoffFailed
		case 6:
			return libvirtServiceInterfaces.DomainReasonShutoffFromSnapshot
		default:
			return libvirtServiceInterfaces.DomainReasonUnknown
		}

	case libvirt.DomainPaused:
		switch reason {
		case 0:
			return libvirtServiceInterfaces.DomainReasonUnknown
		case 1:
			return libvirtServiceInterfaces.DomainReasonPausedUser
		case 2:
			return libvirtServiceInterfaces.DomainReasonPausedMigration
		case 3:
			return libvirtServiceInterfaces.DomainReasonPausedSave
		case 4:
			return libvirtServiceInterfaces.DomainReasonPausedDump
		case 5:
			return libvirtServiceInterfaces.DomainReasonPausedIOError
		case 6:
			return libvirtServiceInterfaces.DomainReasonPausedWatchdog
		case 7:
			return libvirtServiceInterfaces.DomainReasonPausedFromSnapshot
		case 8:
			return libvirtServiceInterfaces.DomainReasonPausedShuttingDown
		case 9:
			return libvirtServiceInterfaces.DomainReasonPausedSnapshot
		default:
			return libvirtServiceInterfaces.DomainReasonUnknown
		}

	default:
		return libvirtServiceInterfaces.DomainReasonUnknown
	}
}

func (s *Service) FindISOByUUID(uuid string, includeImg bool) (string, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.
		Preload("Files").
		Where("uuid = ?", uuid).
		First(&download).Error; err != nil {
		return "", fmt.Errorf("failed_to_find_download: %w", err)
	}

	hasAllowedExt := func(p string) bool {
		if p == "" {
			return false
		}
		l := strings.ToLower(p)
		return strings.HasSuffix(l, ".iso") || (includeImg && (strings.HasSuffix(l, ".img") || strings.HasSuffix(l, ".raw")))
	}

	fileExists := func(p string) bool {
		if p == "" {
			return false
		}
		fi, err := os.Stat(p)
		return err == nil && !fi.IsDir()
	}

	switch download.Type {
	case "http":
		downloadsDir := config.GetDownloadsPath("http")
		isoPath := filepath.Join(downloadsDir, download.Name)

		mainExists := fileExists(isoPath)
		extractExists := fileExists(download.ExtractedPath)

		if mainExists && hasAllowedExt(isoPath) {
			return isoPath, nil
		}

		if extractExists && hasAllowedExt(download.ExtractedPath) {
			return download.ExtractedPath, nil
		}

		if download.ExtractedPath != "" {
			files, err := os.ReadDir(download.ExtractedPath)
			if err == nil {
				for _, f := range files {
					full := filepath.Join(download.ExtractedPath, f.Name())
					if fileExists(full) && hasAllowedExt(full) {
						return full, nil
					}
				}
			}
		}

		// Nothing usable; craft a helpful error (often main is compressed like .iso.bz2).
		return "", fmt.Errorf(
			"iso_or_img_not_found: main=%s (exists=%t, allowed=%t) extracted=%s (exists=%t, allowed=%t)",
			isoPath, mainExists, hasAllowedExt(isoPath),
			download.ExtractedPath, extractExists, hasAllowedExt(download.ExtractedPath),
		)

	case "torrent":
		torrentsDir := config.GetDownloadsPath("torrents")

		var isoCandidate, imgCandidate string
		for _, f := range download.Files {
			full := filepath.Join(torrentsDir, uuid, f.Name)
			if !fileExists(full) {
				continue
			}

			l := strings.ToLower(f.Name)

			if strings.HasSuffix(l, ".iso") && isoCandidate == "" {
				isoCandidate = full
			} else if includeImg && strings.HasSuffix(l, ".img") && imgCandidate == "" {
				imgCandidate = full
			}
		}

		if isoCandidate == "" && imgCandidate == "" {
			isoCandidate = filepath.Join(torrentsDir, uuid, download.Name)
			if !fileExists(isoCandidate) || !hasAllowedExt(isoCandidate) {
				isoCandidate = ""
			}
		}

		if isoCandidate != "" {
			return isoCandidate, nil
		}

		if includeImg && imgCandidate != "" {
			return imgCandidate, nil
		}

		return "", fmt.Errorf("iso_or_img_not_found_in_torrent: %s", uuid)

	case "path":
		pathDir := config.GetDownloadsPath("path")
		fullPath := filepath.Join(pathDir, download.Name)

		if fileExists(fullPath) && hasAllowedExt(fullPath) {
			return fullPath, nil
		}

		return "", fmt.Errorf("iso_or_img_not_found_in_path: %s", fullPath)

	default:
		return "", fmt.Errorf("unsupported_download_type: %s", download.Type)
	}
}

func (s *Service) GetDomainStates() ([]libvirtServiceInterfaces.DomainState, error) {
	var states []libvirtServiceInterfaces.DomainState

	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := s.Conn.ConnectListAllDomains(1, flags)
	if err != nil {
		return states, err
	}

	for _, d := range domains {
		state, reason, err := s.Conn.DomainGetState(d, 0)
		if err != nil {
			fmt.Printf("failed to get domain state: %v\n", err)
		}

		pState := libvirt.DomainState(state)
		states = append(states, libvirtServiceInterfaces.DomainState{
			Domain: d.Name,
			State:  pState,
			Reason: domainReasonToString(pState, reason),
		})
	}

	return states, nil
}

func (s *Service) IsDomainShutOff(rid uint) (bool, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return false, fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)

	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state == int32(libvirt.DomainShutoff) {
		return true, nil
	}

	return false, nil
}

func (s *Service) IsDomainShutOffByID(id uint) (bool, error) {
	var rid uint
	if err := s.DB.Model(&vmModels.VM{}).
		Where("id = ?", id).
		Select("rid").
		Scan(&rid).Error; err != nil {
		return false, fmt.Errorf("failed_to_get_vm_rid: %w", err)
	}

	return s.IsDomainShutOff(rid)
}

func (s *Service) CreateVMDirectory(rid uint) (string, error) {
	vmDir, err := config.GetVMsPath()

	if err != nil {
		return "", fmt.Errorf("failed to get VMs path: %w", err)
	}

	vmPath := fmt.Sprintf("%s/%d", vmDir, rid)

	if _, err := os.Stat(vmPath); err == nil {
		if err := os.RemoveAll(vmPath); err != nil {
			return "", fmt.Errorf("failed to clear VM directory: %w", err)
		}
	}

	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create VM directory: %w", err)
	}

	return vmPath, nil
}

func (s *Service) ResetUEFIVars(rid uint) error {
	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	vmPath := fmt.Sprintf("%s/%d", vmDir, rid)
	uefiVarsBase := "/usr/local/share/uefi-firmware/BHYVE_UEFI_VARS.fd"
	uefiVarsPath := filepath.Join(vmPath, fmt.Sprintf("%d_vars.fd", rid))

	err = utils.CopyFile(uefiVarsBase, uefiVarsPath)

	if err != nil {
		if strings.Contains("failed_to_open_source", err.Error()) {
			logger.L.Err(err).Msg("Error finding BHYVE_UEFI_VARS file, do we have bhyve-firmware?")
		} else {
			return err
		}
	}

	return nil
}

func (s *Service) ValidateCPUPins(rid uint, pins []libvirtServiceInterfaces.CPUPinning, hostLogicalPerSocket int) error {
	if len(pins) == 0 {
		return nil
	}

	hostLogicalCores := utils.GetLogicalCores()
	hostSocketCount := utils.GetSocketCount(cpuid.CPU.PhysicalCores,
		cpuid.CPU.ThreadsPerCore)

	if hostSocketCount <= 0 {
		return fmt.Errorf("invalid_host_socket_count")
	}

	if hostLogicalCores <= 0 {
		return fmt.Errorf("invalid_host_logical_cores")
	}

	seenSockets := make(map[int]struct{}, len(pins))
	for i, pin := range pins {
		if pin.Socket < 0 || pin.Socket >= hostSocketCount {
			return fmt.Errorf("socket_index_out_of_range: socket=%d max=%d", pin.Socket, hostSocketCount-1)
		}
		if _, dup := seenSockets[pin.Socket]; dup {
			return fmt.Errorf("duplicate_socket_in_request: socket=%d index=%d", pin.Socket, i)
		}
		seenSockets[pin.Socket] = struct{}{}
		if len(pin.Cores) == 0 {
			return fmt.Errorf("empty_core_list_for_socket: socket=%d", pin.Socket)
		}
	}

	seenCores := make(map[int]struct{}, 128)
	perSocketCounts := make(map[int]int, hostSocketCount)
	totalPinned := 0

	for _, pin := range pins {
		perSocketSeen := make(map[int]struct{}, len(pin.Cores))
		for j, c := range pin.Cores {
			if c < 0 || c >= hostLogicalCores {
				return fmt.Errorf("core_index_out_of_range: core=%d (max=%d) socket=%d pos=%d",
					c, hostLogicalCores-1, pin.Socket, j)
			}
			if _, dup := perSocketSeen[c]; dup {
				return fmt.Errorf("duplicate_core_within_socket: core=%d socket=%d", c, pin.Socket)
			}
			perSocketSeen[c] = struct{}{}

			if _, dup := seenCores[c]; dup {
				return fmt.Errorf("duplicate_core_across_sockets: core=%d", c)
			}
			seenCores[c] = struct{}{}
		}
		perSocketCounts[pin.Socket] += len(pin.Cores)
		totalPinned += len(pin.Cores)
	}

	if totalPinned > hostLogicalCores {
		return fmt.Errorf("cpu_pinning_exceeds_logical_cores: pinned=%d logical=%d", totalPinned, hostLogicalCores)
	}

	if hostLogicalPerSocket > 0 {
		for sock, cnt := range perSocketCounts {
			if cnt > hostLogicalPerSocket {
				return fmt.Errorf("socket_capacity_exceeded: socket=%d pinned=%d cap=%d",
					sock, cnt, hostLogicalPerSocket)
			}
		}
	}

	var vms []vmModels.VM
	if err := s.DB.Preload("CPUPinning").Find(&vms).Error; err != nil {
		return fmt.Errorf("failed_to_fetch_vms: %w", err)
	}

	occupied := make(map[int]uint, 512)
	for _, vm := range vms {
		if rid != 0 && uint(vm.RID) == rid {
			continue
		}
		for _, p := range vm.CPUPinning {
			for _, c := range p.HostCPU {
				occupied[c] = uint(vm.RID)
			}
		}
	}

	for c := range seenCores {
		if owner, taken := occupied[c]; taken {
			return fmt.Errorf("core_conflict: core=%d already_pinned_by_rid=%d", c, owner)
		}
	}

	return nil
}

func (s *Service) GeneratePinArgs(pins []vmModels.VMCPUPinning) []string {
	var args []string
	vcpu := 0

	socketCount := utils.GetSocketCount(cpuid.CPU.PhysicalCores, cpuid.CPU.ThreadsPerCore)
	if socketCount <= 0 {
		socketCount = 1
	}

	coresPerSocket := cpuid.CPU.LogicalCores / socketCount
	if coresPerSocket <= 0 {
		coresPerSocket = cpuid.CPU.LogicalCores
	}

	for _, p := range pins {
		for _, localCPU := range p.HostCPU {
			globalCPU := p.HostSocket*coresPerSocket + localCPU
			args = append(args, fmt.Sprintf("-p %d:%d", vcpu, globalCPU))
			vcpu++
		}
	}
	return args
}

func (s *Service) GetVMConfigDirectory(rid uint) (string, error) {
	vmDir, err := config.GetVMsPath()
	if err != nil {
		return "", fmt.Errorf("failed to get VMs path: %w", err)
	}

	return fmt.Sprintf("%s/%d", vmDir, rid), nil
}

func (s *Service) CreateCloudInitISO(vm vmModels.VM) error {
	if vm.CloudInitData == "" && vm.CloudInitMetaData == "" {
		return nil
	}

	vmPath, err := s.GetVMConfigDirectory(vm.RID)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_path: %w", err)
	}

	cloudInitISOPath := filepath.Join(vmPath, "cloud-init.iso")
	if _, err := os.Stat(cloudInitISOPath); err == nil {
		if err := os.Remove(cloudInitISOPath); err != nil {
			return fmt.Errorf("failed_to_remove_existing_cloud_init_iso: %w", err)
		}
	}

	cloudInitPath := filepath.Join(vmPath, "cloud-init")
	if _, err := os.Stat(cloudInitPath); err == nil {
		if err := os.RemoveAll(cloudInitPath); err != nil {
			return fmt.Errorf("failed_to_remove_existing_cloud_init_directory: %w", err)
		}
	}

	if err := os.MkdirAll(cloudInitPath, 0755); err != nil {
		return fmt.Errorf("failed_to_create_cloud_init_directory: %w", err)
	}

	userDataPath := filepath.Join(cloudInitPath, "user-data")
	metaDataPath := filepath.Join(cloudInitPath, "meta-data")
	networkConfigPath := filepath.Join(cloudInitPath, "network-config")

	err = os.WriteFile(userDataPath, []byte(vm.CloudInitData), 0644)
	if err != nil {
		return fmt.Errorf("failed_to_write_user_data: %w", err)
	}

	err = os.WriteFile(metaDataPath, []byte(vm.CloudInitMetaData), 0644)
	if err != nil {
		return fmt.Errorf("failed_to_write_meta_data: %w", err)
	}

	if vm.CloudInitNetworkConfig != "" {
		err = os.WriteFile(networkConfigPath, []byte(vm.CloudInitNetworkConfig), 0644)
		if err != nil {
			return fmt.Errorf("failed_to_write_network_config: %w", err)
		}
	}

	isoPath := filepath.Join(vmPath, "cloud-init.iso")
	_, err = utils.RunCommand("/usr/sbin/makefs", "-t", "cd9660", "-o", "rockridge", "-o", "label=cidata", isoPath, cloudInitPath)

	if err != nil {
		return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
	}

	return nil
}

func (s *Service) GetCloudInitISOPath(rid uint) (string, error) {
	vmPath, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_vm_path: %w", err)
	}

	cloudInitISOPath := filepath.Join(vmPath, "cloud-init.iso")
	if _, err := os.Stat(cloudInitISOPath); err != nil {
		return "", fmt.Errorf("cloud_init_iso_not_found: %w", err)
	}

	return cloudInitISOPath, nil
}

func (s *Service) FlashCloudInitMediaToDisk(vm vmModels.VM) error {
	if vm.Storages == nil || len(vm.Storages) == 0 {
		return fmt.Errorf("need_storage_to_flash_cloud_init_disk")
	} else if len(vm.Storages) > 2 {
		return fmt.Errorf("too_many_storages_to_flash_cloud_init_disk")
	}

	if vm.CloudInitData == "" && vm.CloudInitMetaData == "" {
		return nil
	}

	var mediaStorage *vmModels.Storage
	var diskStorage *vmModels.Storage

	for _, storage := range vm.Storages {
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			mediaStorage = &storage
		} else if storage.Type == vmModels.VMStorageTypeRaw ||
			storage.Type == vmModels.VMStorageTypeZVol {
			diskStorage = &storage
		}
	}

	if mediaStorage == nil || diskStorage == nil {
		return fmt.Errorf("media_and_disk_required")
	}

	mediaPath, err := s.FindISOByUUID(mediaStorage.DownloadUUID, true)
	if err != nil {
		return fmt.Errorf("failed_to_find_media_iso: %w", err)
	}

	mediaInfo, err := os.Stat(mediaPath)
	if err != nil {
		return fmt.Errorf("failed_to_stat_media_iso: %w", err)
	}

	mediaSize := mediaInfo.Size()

	if diskStorage.Size < mediaSize {
		return fmt.Errorf("disk_too_small_for_media: disk_size=%d media_size=%d",
			diskStorage.Size, mediaSize)
	}

	var storagePath string

	if diskStorage.Type == vmModels.VMStorageTypeRaw {
		storagePath = fmt.Sprintf(
			"/%s/sylve/virtual-machines/%d/raw-%d/%d.img",
			diskStorage.Dataset.Pool,
			vm.RID,
			diskStorage.ID,
			diskStorage.ID,
		)

		if _, err := os.Stat(storagePath); err != nil {
			return fmt.Errorf("disk_image_not_found: %w", err)
		}
	} else if diskStorage.Type == vmModels.VMStorageTypeZVol {
		storagePath = fmt.Sprintf(
			"/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d",
			diskStorage.Dataset.Pool,
			vm.RID,
			diskStorage.ID,
		)

		if _, err := os.Stat(storagePath); err != nil {
			return fmt.Errorf("zvol_not_found: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := utils.FlashImageToDiskCtx(ctx, mediaPath, storagePath); err != nil {
		return fmt.Errorf("failed_to_flash_media_to_disk: %w", err)
	}

	return nil
}
