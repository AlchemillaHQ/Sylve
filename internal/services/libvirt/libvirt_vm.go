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
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/digitalocean/go-libvirt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *Service) CreateVmXML(vm vmModels.VM, vmPath string) (string, error) {
	var memoryBacking *libvirtServiceInterfaces.MemoryBacking

	if vm.PCIDevices != nil && len(vm.PCIDevices) > 0 {
		fmt.Println(len(vm.PCIDevices), "-> This many PCI DEVICES!")
		memoryBacking = &libvirtServiceInterfaces.MemoryBacking{
			Locked: struct{}{},
		}
	} else {
		memoryBacking = nil
	}

	var devices libvirtServiceInterfaces.Devices

	devices.Controllers = []libvirtServiceInterfaces.Controller{
		{
			Type:  "usb",
			Model: "nec-xhci",
		},
	}

	devices.Inputs = []libvirtServiceInterfaces.Input{
		{
			Type: "tablet",
			Bus:  "usb",
		},
	}

	if vm.Serial {
		devices.Serials = []libvirtServiceInterfaces.Serial{
			{
				Type: "nmdm",
				Source: libvirtServiceInterfaces.SerialSource{
					Master: fmt.Sprintf("/dev/nmdm%dA", vm.RID),
					Slave:  fmt.Sprintf("/dev/nmdm%dB", vm.RID),
				},
			},
		}
	}

	sIndex := 10
	uefi := fmt.Sprintf("%s,%s/%d_vars.fd", "/usr/local/share/uefi-firmware/BHYVE_UEFI.fd", vmPath, vm.RID)

	var bhyveArgs [][]libvirtServiceInterfaces.BhyveArg

	/* Why does this fail with:
	bhyve: invalid lpc device configuration ' tpm,swtpm,/root/Projects/Sylve/data/vms/100/100_tpm.socket'
	when I have a space between "-l" and "tpm"
	*/

	if vm.TPMEmulation {
		tpmArg := fmt.Sprintf("-ltpm,swtpm,%s", filepath.Join(vmPath, fmt.Sprintf("%d_tpm.socket", vm.RID)))
		bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
			{
				Value: tpmArg,
			},
		})
	}

	if vm.Storages != nil && len(vm.Storages) > 0 {
		sort.Slice(vm.Storages, func(i, j int) bool {
			return vm.Storages[i].BootOrder < vm.Storages[j].BootOrder
		})

		for _, storage := range vm.Storages {
			var disk string

			if storage.Type == vmModels.VMStorageTypeRaw {
				disk = fmt.Sprintf("/%s/sylve/virtual-machines/%d/raw-%d/%d.img", storage.Pool, vm.RID, storage.ID, storage.ID)
			} else if storage.Type == vmModels.VMStorageTypeZVol {
				disk = fmt.Sprintf("/dev/zvol/%s/sylve/virtual-machines/%d/zvol-%d", storage.Pool, vm.RID, storage.ID)
			} else if storage.Type == vmModels.VMStorageTypeDiskImage {
				var err error
				disk, err = s.FindISOByUUID(storage.DownloadUUID, true)
				if err != nil {
					return "", fmt.Errorf("failed_to_find_iso: %w", err)
				}
			}

			bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
				{
					Value: fmt.Sprintf("-s %d:%d,%s,%s",
						sIndex,
						0,
						storage.Emulation,
						disk,
					),
				},
			})

			sIndex++
		}
	}

	if vm.CloudInitData != "" || vm.CloudInitMetaData != "" {
		cloudInitISOPath, err := s.GetCloudInitISOPath(vm.RID)
		if err != nil {
			return "", fmt.Errorf("failed_to_get_cloud_init_iso_path: %w", err)
		}

		bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
			{
				Value: fmt.Sprintf("-s %d:0,ahci-cd,%s,ro",
					sIndex,
					cloudInitISOPath,
				),
			},
		})

		sIndex++
	}

	if vm.QemuGuestAgent {
		qgaArg := fmt.Sprintf("-s %d,virtio-console,org.qemu.guest_agent.0=%s",
			sIndex,
			filepath.Join(vmPath, "qga.sock"),
		)
		bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
			{
				Value: qgaArg,
			},
		})
		sIndex++
	}

	var interfaces []libvirtServiceInterfaces.Interface

	if vm.Networks != nil && len(vm.Networks) > 0 {
		for _, network := range vm.Networks {
			if network.SwitchID != 0 {
				nType := "bridge"
				emulation := network.Emulation

				var mac *libvirtServiceInterfaces.MACAddress
				if network.MacID != nil && *network.MacID != 0 {
					var macObj networkModels.Object
					err := s.DB.Preload("Entries").
						Where("id = ? and type = ?", *network.MacID, "Mac").
						First(&macObj).Error
					if errors.Is(err, gorm.ErrRecordNotFound) {
						logger.L.Warn().
							Uint("rid", vm.RID).
							Uint("mac_id", *network.MacID).
							Msg("vm_network_mac_object_missing_skipping_mac_assignment")
					} else if err != nil {
						return "", fmt.Errorf("failed_to_find_mac_object: %w", err)
					} else if len(macObj.Entries) == 0 {
						logger.L.Warn().
							Uint("rid", vm.RID).
							Uint("mac_id", macObj.ID).
							Msg("vm_network_mac_object_has_no_entries_skipping_mac_assignment")
					} else {
						entry := strings.TrimSpace(macObj.Entries[0].Value)
						if entry != "" {
							mac = &libvirtServiceInterfaces.MACAddress{Address: entry}
						}
					}
				}

				if network.SwitchType == "manual" {
					var sw networkModels.ManualSwitch
					if err := s.DB.Where("id = ?", network.SwitchID).First(&sw).Error; err != nil {
						return "", fmt.Errorf("failed_to_find_manual_switch: %w", err)
					}

					interfaces = append(interfaces, libvirtServiceInterfaces.Interface{
						Type:   nType,
						MAC:    mac,
						Source: libvirtServiceInterfaces.BridgeSource{Bridge: sw.Bridge},
						Model:  libvirtServiceInterfaces.Model{Type: emulation},
					})
				} else if network.SwitchType == "standard" {
					var sw networkModels.StandardSwitch
					if err := s.DB.Where("id = ?", network.SwitchID).First(&sw).Error; err != nil {
						return "", fmt.Errorf("failed_to_find_standard_switch: %w", err)
					}

					interfaces = append(interfaces, libvirtServiceInterfaces.Interface{
						Type:   nType,
						MAC:    mac,
						Source: libvirtServiceInterfaces.BridgeSource{Bridge: sw.BridgeName},
						Model:  libvirtServiceInterfaces.Model{Type: emulation},
					})
				}
			}
		}
	}

	devices.Interfaces = interfaces

	var features libvirtServiceInterfaces.Features
	if vm.APIC {
		features.APIC = struct{}{}
	}

	if vm.ACPI {
		features.ACPI = struct{}{}
	}

	domain := libvirtServiceInterfaces.Domain{
		Type:       "bhyve",
		XMLNSBhyve: "http://libvirt.org/schemas/domain/bhyve/1.0",
		Name:       strconv.Itoa(int(vm.RID)),
		Memory: libvirtServiceInterfaces.Memory{
			Unit: "B",
			Text: strconv.Itoa(vm.RAM),
		},
		MemoryBacking: memoryBacking,
		CPU: libvirtServiceInterfaces.CPU{
			Topology: libvirtServiceInterfaces.Topology{
				Sockets: strconv.Itoa(vm.CPUSockets),
				Cores:   strconv.Itoa(vm.CPUCores),
				Threads: strconv.Itoa(vm.CPUThreads),
			},
		},
		VCPU: (vm.CPUSockets * vm.CPUCores * vm.CPUThreads),
		OS: libvirtServiceInterfaces.OS{
			Type: "hvm",
			Loader: libvirtServiceInterfaces.Loader{
				ReadOnly: "yes",
				Type:     "pflash",
				Path:     uefi,
			},
		},
		Features: features,
		Clock: libvirtServiceInterfaces.Clock{
			Offset: string(vm.TimeOffset),
		},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
		Devices:    devices,
	}

	if vm.PCIDevices != nil && len(vm.PCIDevices) > 0 {
		for _, pci := range vm.PCIDevices {
			var pciDevice models.PassedThroughIDs
			if err := s.DB.First(&pciDevice, pci).Error; err != nil {
				return "", fmt.Errorf("failed_to_find_pci_device: %w", err)
			}

			bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
				{
					Value: fmt.Sprintf("-s %d:0,passthru,%s",
						sIndex,
						pciDevice.DeviceID,
					),
				},
			})

			sIndex++
		}
	}

	if len(vm.CPUPinning) > 0 {
		pinArgs := s.GeneratePinArgs(vm.CPUPinning)
		for _, arg := range pinArgs {
			bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
				{
					Value: arg,
				},
			})
		}
	}

	width, height, f := strings.Cut(vm.VNCResolution, "x")
	if f != true {
		return "", fmt.Errorf("invalid_vnc_resolution")
	}

	vncWait := ""
	if vm.VNCWait {
		vncWait = ",wait"
	}

	/* Libvirt doesn't allow wait yet, so we're going to resort to using bhyve args for now
	domain.Devices.Graphics = &libvirtServiceInterfaces.Graphics{
		Type:     "vnc",
		Port:     fmt.Sprintf("%d", vm.VNCPort),
		Password: vm.VNCPassword,
		Listen: libvirtServiceInterfaces.GraphicsListen{
			Type:    "address",
			Address: "127.0.0.1",
		},
	}

	domain.Devices.Video = &libvirtServiceInterfaces.Video{
		Model: libvirtServiceInterfaces.VideoModel{
			Type:    "gop",
			Heads:   "1",
			Primary: "yes",
			Res: &libvirtServiceInterfaces.VideoResolution{
				X: width,
				Y: height,
			},
		},
	}
	*/

	vncArg := fmt.Sprintf("-s %d:0,fbuf,tcp=0.0.0.0:%d,w=%s,h=%s,password=%s%s",
		sIndex,
		vm.VNCPort,
		width,
		height,
		vm.VNCPassword,
		vncWait,
	)

	bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
		{
			Value: vncArg,
		},
	})

	if vm.IgnoreUMSR {
		bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
			{
				Value: "-w",
			},
		})
	}

	var flatBhyveArgs []libvirtServiceInterfaces.BhyveArg
	for _, args := range bhyveArgs {
		flatBhyveArgs = append(flatBhyveArgs, args...)
	}

	domain.BhyveCommandline = &libvirtServiceInterfaces.BhyveCommandline{
		Args: flatBhyveArgs,
	}

	out, err := xml.Marshal(domain)
	if err != nil {
		return "", fmt.Errorf("failed_to_marshal_vm_xml: %w", err)
	}

	return string(out), nil
}

func (s *Service) CreateLvVm(id int, ctx context.Context) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	vm, err := s.GetVM(id)
	if err != nil {
		return err
	}

	vmPath, err := s.CreateVMDirectory(vm.RID)
	if err != nil {
		return err
	}

	err = s.ResetUEFIVars(vm.RID)
	if err != nil {
		return err
	}

	if len(vm.Storages) > 0 {
		for _, storage := range vm.Storages {
			if storage.Type == vmModels.VMStorageTypeRaw ||
				storage.Type == vmModels.VMStorageTypeZVol {
				err = s.CreateStorageParent(vm.RID, storage.Pool, ctx)
				if err != nil {
					return fmt.Errorf("failed_to_create_storage_parent: %w", err)
				}

				err = s.CreateVMDisk(vm.RID, storage, ctx)

				if err != nil {
					return err
				}
			}
		}
	}

	vm, err = s.GetVM(id)
	if err != nil {
		return err
	}

	if vm.CloudInitData != "" && vm.CloudInitMetaData != "" {
		err = s.CreateCloudInitISO(vm)
		if err != nil {
			return fmt.Errorf("failed_to_create_cloud_init_iso: %w", err)
		}

		err = s.FlashCloudInitMediaToDisk(vm)
		if err != nil {
			return fmt.Errorf("failed_to_flash_cloud_init_to_disk: %w", err)
		}

		err := s.DB.
			Where("vm_id = ? AND type = ?", vm.ID, vmModels.VMStorageTypeDiskImage).
			Delete(&vmModels.Storage{}).Error

		if err != nil {
			return fmt.Errorf("failed_to_remove_cloud_init_storage_entry: %w", err)
		}

		vm, err = s.GetVM(id)
		if err != nil {
			return err
		}
	}

	generated, err := s.CreateVmXML(vm, vmPath)
	if err != nil {
		return fmt.Errorf("failed to generate VM XML: %w", err)
	}

	_, err = s.Conn.DomainDefineXML(generated)

	if err != nil {
		return fmt.Errorf("failed to define VM domain: %w", err)
	}

	err = s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after creation")
	}

	return nil
}

func (s *Service) RemoveLvVm(rid uint) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	domainGone := false
	if err != nil {
		logger.L.Warn().Err(err).Msgf("Domain for VM RID %d not found, assuming already removed", rid)
		domainGone = true
	}

	if !domainGone {
		if err := s.Conn.DomainDestroy(domain); err != nil {
			if !strings.Contains(err.Error(), "is not running") {
				return fmt.Errorf("failed_to_destroy_domain: %w", err)
			}
		}

		if err := s.Conn.DomainUndefine(domain); err != nil {
			return fmt.Errorf("failed_to_undefine_domain: %w", err)
		}
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	err = s.StopTPM(rid)
	if err != nil {
		logger.L.Error().Err(err).Msgf("Failed to stop TPM for VM RID %d", rid)
	}

	vmPath := filepath.Join(vmDir, strconv.Itoa(int(rid)))
	if _, err := os.Stat(vmPath); err == nil {
		if err := os.RemoveAll(vmPath); err != nil {
			return fmt.Errorf("failed to remove VM directory: %w", err)
		}
	}

	return nil
}

func (s *Service) GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error) {
	if err := s.requireConnection(); err != nil {
		return nil, err
	}

	var dom libvirtServiceInterfaces.LvDomain

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return nil, fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	stateMap := map[int32]string{
		0: "No State",
		1: "Running",
		2: "Blocked",
		3: "Paused",
		4: "Shutdown",
		5: "Shutoff",
		6: "Crashed",
		7: "PMSuspended",
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	dom.ID = domain.ID
	dom.UUID = uuid.UUID(domain.UUID).String()
	dom.Name = domain.Name
	dom.Status = stateMap[state]

	return &dom, nil
}

func (s *Service) StartTPM() error {
	vms, err := s.ListVMs()
	if err != nil {
		return fmt.Errorf("failed_to_list_vms: %w", err)
	}

	vmDir, err := config.GetVMsPath()

	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	rids := make([]uint, 0, len(vms))
	for _, vm := range vms {
		if vm.TPMEmulation {
			rids = append(rids, vm.RID)
		}
	}

	psOut, err := utils.RunCommand("/bin/ps", "--libxo", "json", "-aux")
	if err != nil {
		return fmt.Errorf("failed_to_run_ps_command: %w", err)
	}

	var top struct {
		ProcessInformation systemServiceInterfaces.ProcessInformation `json:"process-information"`
	}

	if err := json.Unmarshal([]byte(psOut), &top); err != nil {
		return fmt.Errorf("failed_to_unmarshal_ps_output: %w", err)
	}

	swtpmRunning := make(map[uint]bool)

	for _, proc := range top.ProcessInformation.Process {
		for _, rid := range rids {
			if strings.Contains(proc.Command, fmt.Sprintf("%d_tpm.socket", rid)) {
				swtpmRunning[rid] = true
			}
		}
	}

	for _, rid := range rids {
		if !swtpmRunning[rid] {
			vmPath := fmt.Sprintf("%s/%d", vmDir, rid)
			tpmSocket := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.socket", rid))
			tpmState := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.state", rid))
			tpmLog := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.log", rid))

			args := []string{
				"socket",
				"--tpmstate",
				fmt.Sprintf("backend-uri=file://%s", tpmState),
				"--tpm2",
				"--server",
				fmt.Sprintf("type=unixio,path=%s", tpmSocket),
				"--log",
				fmt.Sprintf("file=%s", tpmLog),
				"--flags",
				"not-need-init",
				"--daemon",
			}

			_, err = utils.RunCommand("/usr/local/bin/swtpm", args...)
			if err != nil {
				return fmt.Errorf("failed_to_start_swtpm_for_vm: %d, error: %w", rid, err)
			}
		}
	}

	return nil
}

func (s *Service) StopTPM(rid uint) error {
	var vm vmModels.VM

	err := s.DB.Find(&vm, "rid = ?", rid).Error
	if err != nil {
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	if vm.ID == 0 {
		return fmt.Errorf("vm_not_found: %d", rid)
	}

	if !vm.TPMEmulation {
		return nil
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	tpmSocket := filepath.Join(vmDir, strconv.Itoa(int(rid)), fmt.Sprintf("%d_tpm.socket", rid))
	if _, err := os.Stat(tpmSocket); os.IsNotExist(err) {
		return fmt.Errorf("tpm_socket_not_found: %s", tpmSocket)
	}

	psOut, err := utils.RunCommand("/bin/ps", "--libxo", "json", "-aux")
	if err != nil {
		return fmt.Errorf("failed_to_run_ps_command: %w", err)
	}

	var top struct {
		ProcessInformation systemServiceInterfaces.ProcessInformation `json:"process-information"`
	}

	if err := json.Unmarshal([]byte(psOut), &top); err != nil {
		return fmt.Errorf("failed_to_unmarshal_ps_output: %w", err)
	}

	for _, proc := range top.ProcessInformation.Process {
		if strings.Contains(proc.Command, tpmSocket) {
			pid, err := strconv.Atoi(proc.PID)
			if err != nil {
				return fmt.Errorf("failed_to_parse_pid: %s, error: %w", proc.PID, err)
			}

			if pid > 0 {
				if err := utils.KillProcess(pid); err != nil {
					return fmt.Errorf("failed_to_kill_swtpm_process: %d, error: %w", pid, err)
				}
				logger.L.Info().Msgf("Stopped swtpm process for VM RID %d", rid)
			}
		}
	}

	return nil
}

func (s *Service) CheckPCIDevicesInUse(vm vmModels.VM) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	if vm.PCIDevices == nil || len(vm.PCIDevices) == 0 {
		return nil
	}

	vms, err := s.ListVMs()
	if err != nil {
		return fmt.Errorf("failed_to_list_vms: %w", err)
	}

	for _, other := range vms {
		if other.RID == vm.RID {
			continue
		}

		domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(other.RID)))
		if err != nil {
			continue
		}

		state, _, _ := s.Conn.DomainGetState(domain, 0)
		if state != 1 {
			continue
		}

		for _, pci := range vm.PCIDevices {
			for _, o := range other.PCIDevices {
				if pci == o {
					return fmt.Errorf("pci_device_%d_in_use_by_vm_%d", pci, other.RID)
				}
			}
		}
	}

	return nil
}

func (s *Service) LvVMAction(vm vmModels.VM, action string) error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	if action == "start" {
		allowed, err := s.canStartProtectedVM(vm.RID)
		if err != nil {
			return fmt.Errorf("replication_lease_check_failed: %w", err)
		}
		if !allowed {
			return fmt.Errorf("replication_lease_not_owned")
		}
	}

	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(vm.RID)))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	if action == "start" || action == "reboot" {
		if err := s.CheckPCIDevicesInUse(vm); err != nil {
			return err
		}
	}

	switch action {
	case "start":
		err = s.startVM(&domain, vm)
	case "shutdown":
		err = s.shutdownVM(&domain, vm)
	case "stop":
		err = s.stopVM(&domain, vm)
	case "reboot":
		err = s.rebootVM(&domain, vm)
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	if err != nil {
		return err
	}

	if err := s.SetActionDate(vm, action); err != nil {
		logger.L.Error().Err(err).Msgf("Failed to set %s action date for VM ID %d", action, vm.RID)
	}

	return nil
}

func (s *Service) canStartProtectedVM(rid uint) (bool, error) {
	if rid == 0 {
		return true, nil
	}

	var policy clusterModels.ReplicationPolicy
	err := s.DB.Where("guest_type = ? AND guest_id = ? AND enabled = ?", clusterModels.ReplicationGuestTypeVM, rid, true).
		First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		return false, err
	}

	var lease clusterModels.ReplicationLease
	if err := s.DB.Where("policy_id = ?", policy.ID).First(&lease).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}

	if time.Now().UTC().After(lease.ExpiresAt) {
		return false, nil
	}

	return strings.TrimSpace(lease.OwnerNodeID) == strings.TrimSpace(nodeID), nil
}

func (s *Service) startVM(domain *libvirt.Domain, vm vmModels.VM) error {
	if err := s.RemoveQGASocket(vm); err != nil {
		logger.L.Warn().Err(err).Msg("Non-fatal error removing socket before start")
	}

	state, _, err := s.Conn.DomainGetState(*domain, 0)
	if err != nil {
		return fmt.Errorf("could_not_get_state: %w", err)
	}

	if state == 1 {
		return nil
	}

	if err := s.StartTPM(); err != nil {
		return fmt.Errorf("failed_to_start_tpm: %w", err)
	}

	if err := s.Conn.DomainCreate(*domain); err != nil {
		return fmt.Errorf("failed_to_start_domain: %w", err)
	}

	newState, _, err := s.Conn.DomainGetState(*domain, 0)
	if err != nil {
		return fmt.Errorf("could_not_verify_run: %w", err)
	}
	if newState != 1 {
		return fmt.Errorf("unexpected_state_after_start: %d", newState)
	}

	return nil
}

func (s *Service) stopVM(domain *libvirt.Domain, vm vmModels.VM) error {
	if err := s.Conn.DomainDestroy(*domain); err != nil {
		return fmt.Errorf("failed_to_force_stop_domain: %w", err)
	}

	return s.cleanupResources(vm)
}

func (s *Service) shutdownVM(domain *libvirt.Domain, vm vmModels.VM) error {
	if err := s.Conn.DomainShutdown(*domain); err != nil {
		logger.L.Warn().Err(err).Msg("Graceful shutdown signal failed, will wait and force stop if needed")
	}

	waitTime := vm.ShutdownWaitTime
	if waitTime <= 0 {
		waitTime = 30
	}

	timeout := time.After(time.Duration(waitTime) * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			logger.L.Warn().Msgf("Shutdown timed out after %ds, forcing destroy", waitTime)
			return s.forceDestroy(domain, vm)

		case <-ticker.C:
			state, _, err := s.Conn.DomainGetState(*domain, 0)
			if err != nil {
				return fmt.Errorf("failed_to_get_state: %w", err)
			}

			if state == 5 {
				return s.cleanupResources(vm)
			}
		}
	}
}

func (s *Service) forceDestroy(domain *libvirt.Domain, vm vmModels.VM) error {
	if err := s.Conn.DomainDestroy(*domain); err != nil {
		state, _, _ := s.Conn.DomainGetState(*domain, 0)
		if state != 5 {
			return fmt.Errorf("failed_to_force_destroy: %w", err)
		}
	}

	state, _, err := s.Conn.DomainGetState(*domain, 0)
	if err != nil {
		return fmt.Errorf("failed_to_verify_stop: %w", err)
	}

	if state != 5 {
		return fmt.Errorf("vm_still_running_after_destroy_state_%d", state)
	}

	return s.cleanupResources(vm)
}

func (s *Service) rebootVM(domain *libvirt.Domain, vm vmModels.VM) error {
	state, _, err := s.Conn.DomainGetState(*domain, 0)
	if err != nil {
		return fmt.Errorf("could_not_get_state: %w", err)
	}
	if state != 1 {
		return fmt.Errorf("domain_not_running_for_reboot")
	}

	if err := s.shutdownVM(domain, vm); err != nil {
		return fmt.Errorf("reboot_failed_during_shutdown: %w", err)
	}

	if err := s.startVM(domain, vm); err != nil {
		return fmt.Errorf("reboot_failed_during_start: %w", err)
	}

	return nil
}

func (s *Service) cleanupResources(vm vmModels.VM) error {
	user, err := utils.GetPortUserPID("tcp", vm.VNCPort)
	if err != nil {
		if !strings.HasPrefix(err.Error(), "no process found using tcp port") {
			logger.L.Error().Err(err).Msg("Error checking VNC port usage")
		}
	} else if user > 0 {
		if err := utils.KillProcess(user); err != nil {
			logger.L.Error().Err(err).Msg("Failed to kill process using VNC port")
		}
	}

	if err := s.RemoveQGASocket(vm); err != nil {
		logger.L.Error().Err(err).Msg("Error cleaning up qemu-ga socket")
		return err
	}

	return nil
}

func (s *Service) RemoveQGASocket(vm vmModels.VM) error {
	if vm.QemuGuestAgent {
		dataPath, err := s.GetVMConfigDirectory(vm.RID)
		if err == nil {
			qgaSocketPath := filepath.Join(dataPath, "qga.sock")
			err := utils.DeleteFileIfExists(qgaSocketPath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) SetActionDate(vm vmModels.VM, action string) error {
	now := time.Now().UTC()

	switch action {
	case "start":
		vm.StartedAt = &now
	case "reboot":
		vm.StartedAt = &now
	case "stop":
		vm.StoppedAt = &now
	case "shutdown":
		vm.StoppedAt = &now
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_save_vm_action_date: %w", err)
	}

	err := s.WriteVMJson(vm.RID)
	if err != nil {
		logger.L.Error().Err(err).Msg("Failed to write VM JSON after setting action date")
	}

	return nil
}

func (s *Service) GetVMXML(rid uint) (string, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return "", fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	xmlDesc, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	return xmlDesc, nil
}

func (s *Service) IsDomainInactive(rid uint) (bool, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return false, fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)

	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state != 5 {
		return false, nil
	}

	return true, nil
}

func (s *Service) GetDomainState(rid int) (libvirt.DomainState, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(rid))
	if err != nil {
		return libvirt.DomainState(libvirt.DomainNostate), err
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)
	if err != nil {
		return libvirt.DomainState(libvirt.DomainNostate), err
	}

	return libvirt.DomainState(state), nil
}
