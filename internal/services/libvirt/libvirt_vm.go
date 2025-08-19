// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sylve/internal/config"
	"sylve/internal/db/models"
	networkModels "sylve/internal/db/models/network"
	vmModels "sylve/internal/db/models/vm"
	libvirtServiceInterfaces "sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "sylve/internal/interfaces/services/system"
	"sylve/internal/logger"
	"sylve/pkg/utils"
	"sylve/pkg/zfs"
	"time"

	"github.com/google/uuid"
)

func (s *Service) CreateVmXML(vm vmModels.VM, vmPath string) (string, error) {
	var memoryBacking *libvirtServiceInterfaces.MemoryBacking

	if vm.PCIDevices != nil && len(vm.PCIDevices) > 0 {
		memoryBacking = &libvirtServiceInterfaces.MemoryBacking{
			Locked: struct{}{},
		}
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

	sIndex := 10
	uefi := fmt.Sprintf("%s,%s/%d_vars.fd", "/usr/local/share/uefi-firmware/BHYVE_UEFI.fd", vmPath, vm.VmID)

	var bhyveArgs [][]libvirtServiceInterfaces.BhyveArg

	/* Why does this fail with:
	bhyve: invalid lpc device configuration ' tpm,swtpm,/root/Projects/Sylve/data/vms/100/100_tpm.socket'
	when I have a space between "-l" and "tpm"
	*/

	if vm.TPMEmulation {
		tpmArg := fmt.Sprintf("-ltpm,swtpm,%s", filepath.Join(vmPath, fmt.Sprintf("%d_tpm.socket", vm.VmID)))
		bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
			{
				Value: tpmArg,
			},
		})
	}

	if vm.Storages != nil && len(vm.Storages) > 0 {
		for _, storage := range vm.Storages {
			datasets, err := zfs.Datasets("")
			if err != nil {
				return "", fmt.Errorf("failed_to_get_datasets: %w", err)
			}

			var dataset *zfs.Dataset

			if storage.Dataset != "" && storage.Type != "iso" {
				for _, d := range datasets {
					guid, err := d.GetProperty("guid")
					if err != nil {
						return "", fmt.Errorf("failed_to_get_dataset_properties: %w", err)
					}

					if guid == storage.Dataset {
						dataset = d
						break
					}
				}
			}

			if dataset == nil && storage.Type != "iso" {
				return "", fmt.Errorf("dataset_not_found: %s", storage.Dataset)
			}

			pool := ""

			if dataset != nil {
				pool = strings.SplitN(dataset.Name, "/", 2)[0]
			}

			if storage.Type == "iso" {
				isoPath, err := s.FindISOByUUID(storage.Dataset, false)
				if err != nil {
					return "", fmt.Errorf("failed to find ISO: %w", err)
				}

				if isoPath == "" {
					return "", fmt.Errorf("iso_file_not_found: %s", storage.Dataset)
				}

				bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
					{
						Value: fmt.Sprintf("-s %d:%d,%s,%s",
							sIndex,
							0,
							"ahci-cd",
							isoPath),
					},
				})

				sIndex++
			} else if storage.Type == "zvol" {
				volume := dataset.Name

				if idx := strings.LastIndex(volume, "/"); idx != -1 {
					volume = volume[idx+1:]
				}

				bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
					{
						Value: fmt.Sprintf("-s %d:%d,%s,%s",
							sIndex,
							0,
							storage.Emulation,
							filepath.Join("/dev/zvol", pool, volume)),
					},
				})

				sIndex++
			} else if storage.Type == "raw" {
				imagePath := filepath.Join(dataset.Mountpoint, "sylve-vm-images", strconv.Itoa(vm.VmID), fmt.Sprintf("%d.img", vm.VmID))

				if _, err := os.Stat(imagePath); os.IsNotExist(err) {
					return "", fmt.Errorf("image_file_not_found: %s", imagePath)
				}

				bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
					{
						Value: fmt.Sprintf("-s %d:%d,%s,%s",
							sIndex,
							0,
							storage.Emulation,
							imagePath),
					},
				})

				sIndex++
			} else {
				return "", fmt.Errorf("invalid_storage_type: %s", storage.Type)
			}
		}
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
					if err := s.DB.Preload("Entries").Find(&macObj).Where("id = ?", *network.MacID).Error; err != nil {
						return "", fmt.Errorf("failed_to_find_mac_object: %w", err)
					}

					entry := macObj.Entries[0]
					mac = &libvirtServiceInterfaces.MACAddress{Address: entry.Value}
				}

				interfaces = append(interfaces, libvirtServiceInterfaces.Interface{
					Type:   nType,
					MAC:    mac,
					Source: libvirtServiceInterfaces.BridgeSource{Bridge: network.Switch.BridgeName},
					Model:  libvirtServiceInterfaces.Model{Type: emulation},
				})
			}
		}
	}

	devices.Interfaces = interfaces

	domain := libvirtServiceInterfaces.Domain{
		Type:       "bhyve",
		XMLNSBhyve: "http://libvirt.org/schemas/domain/bhyve/1.0",
		Name:       strconv.Itoa(vm.VmID),
		Memory: libvirtServiceInterfaces.Memory{
			Unit: "B",
			Text: strconv.Itoa(vm.RAM),
		},
		MemoryBacking: memoryBacking,
		CPU: libvirtServiceInterfaces.CPU{
			Topology: libvirtServiceInterfaces.Topology{
				Sockets: strconv.Itoa(vm.CPUSockets),
				Cores:   strconv.Itoa(vm.CPUCores),
				Threads: strconv.Itoa(vm.CPUsThreads),
			},
		},
		VCPU: (vm.CPUSockets * vm.CPUCores * vm.CPUsThreads),
		OS: libvirtServiceInterfaces.OS{
			Type: "hvm",
			Loader: libvirtServiceInterfaces.Loader{
				ReadOnly: "yes",
				Type:     "pflash",
				Path:     uefi,
			},
		},
		Features: libvirtServiceInterfaces.Features{
			ACPI: struct{}{},
			APIC: struct{}{},
		},
		Clock: libvirtServiceInterfaces.Clock{
			Offset: "utc",
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

	if vm.CPUPinning != nil && len(vm.CPUPinning) > 0 {
		for i, cpu := range vm.CPUPinning {
			if cpu < 0 {
				return "", fmt.Errorf("invalid_cpu_pinning_value: %d", cpu)
			}

			bhyveArgs = append(bhyveArgs, []libvirtServiceInterfaces.BhyveArg{
				{
					Value: fmt.Sprintf("-p %d:%d", i, cpu),
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

func (s *Service) CreateLvVm(id int) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	var vm vmModels.VM
	if err := s.DB.Preload("Storages").Preload("Networks.Switch").First(&vm, id).Error; err != nil {
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	vmDir, err := config.GetVMsPath()

	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	vmPath := fmt.Sprintf("%s/%d", vmDir, vm.VmID)

	if _, err := os.Stat(vmPath); err == nil {
		if err := os.RemoveAll(vmPath); err != nil {
			return fmt.Errorf("failed to clear VM directory: %w", err)
		}
	}

	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("failed to create VM directory: %w", err)
	}

	uefiVarsBase := "/usr/local/share/uefi-firmware/BHYVE_UEFI_VARS.fd"
	uefiVarsPath := filepath.Join(vmPath, fmt.Sprintf("%d_vars.fd", vm.VmID))

	err = utils.CopyFile(uefiVarsBase, uefiVarsPath)

	if err != nil {
		return fmt.Errorf("failed to copy UEFI vars file: %w", err)
	}

	if vm.Storages != nil && len(vm.Storages) > 0 {
		for _, storage := range vm.Storages {
			if storage.Type == "raw" {
				err = s.CreateDiskImage(vm.VmID, storage.Dataset, storage.Size, "")
				if err != nil {
					return fmt.Errorf("failed to create disk image: %w", err)
				}
			}
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

	return nil
}

func (s *Service) RemoveLvVm(vmId int) error {
	s.crudMutex.Lock()
	defer s.crudMutex.Unlock()

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	if err := s.Conn.DomainDestroy(domain); err != nil {
		if !strings.Contains(err.Error(), "is not running") {
			return fmt.Errorf("failed_to_destroy_domain: %w", err)
		}
	}

	if err := s.Conn.DomainUndefine(domain); err != nil {
		return fmt.Errorf("failed_to_undefine_domain: %w", err)
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	err = s.StopTPM(vmId)
	if err != nil {
		return fmt.Errorf("failed to stop TPM for VM %d: %w", vmId, err)
	}

	vmPath := filepath.Join(vmDir, strconv.Itoa(vmId))
	if _, err := os.Stat(vmPath); err == nil {
		if err := os.RemoveAll(vmPath); err != nil {
			return fmt.Errorf("failed to remove VM directory: %w", err)
		}
	}

	return nil
}

func (s *Service) GetLvDomain(vmId int) (*libvirtServiceInterfaces.LvDomain, error) {
	var dom libvirtServiceInterfaces.LvDomain

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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

	vmIds := make([]int, 0, len(vms))
	for _, vm := range vms {
		if vm.TPMEmulation {
			vmIds = append(vmIds, vm.VmID)
		}
	}

	psOut, err := utils.RunCommand("ps", "--libxo", "json", "-aux")
	if err != nil {
		return fmt.Errorf("failed_to_run_ps_command: %w", err)
	}

	var top struct {
		ProcessInformation systemServiceInterfaces.ProcessInformation `json:"process-information"`
	}

	if err := json.Unmarshal([]byte(psOut), &top); err != nil {
		return fmt.Errorf("failed_to_unmarshal_ps_output: %w", err)
	}

	swtpmRunning := make(map[int]bool)

	for _, proc := range top.ProcessInformation.Process {
		for _, vmId := range vmIds {
			if strings.Contains(proc.Command, fmt.Sprintf("%d_tpm.socket", vmId)) {
				swtpmRunning[vmId] = true
			}
		}
	}

	for _, vmId := range vmIds {
		if !swtpmRunning[vmId] {
			vmPath := fmt.Sprintf("%s/%d", vmDir, vmId)
			tpmSocket := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.socket", vmId))
			tpmState := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.state", vmId))
			tpmLog := filepath.Join(vmPath, fmt.Sprintf("%d_tpm.log", vmId))

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

			_, err = utils.RunCommand("swtpm", args...)
			if err != nil {
				return fmt.Errorf("failed_to_start_swtpm_for_vm: %d, error: %w", vmId, err)
			}
		}
	}

	return nil
}

func (s *Service) StopTPM(vmId int) error {
	var vm vmModels.VM

	err := s.DB.Find(&vm, "vm_id = ?", vmId).Error
	if err != nil {
		return fmt.Errorf("failed_to_find_vm: %w", err)
	}

	if vm.ID == 0 {
		return fmt.Errorf("vm_not_found: %d", vmId)
	}

	if !vm.TPMEmulation {
		return nil
	}

	vmDir, err := config.GetVMsPath()
	if err != nil {
		return fmt.Errorf("failed to get VMs path: %w", err)
	}

	tpmSocket := filepath.Join(vmDir, strconv.Itoa(vmId), fmt.Sprintf("%d_tpm.socket", vmId))
	if _, err := os.Stat(tpmSocket); os.IsNotExist(err) {
		return fmt.Errorf("tpm_socket_not_found: %s", tpmSocket)
	}

	psOut, err := utils.RunCommand("ps", "--libxo", "json", "-aux")
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
				logger.L.Info().Msgf("Stopped swtpm process for VM ID %d", vmId)
			}
		}
	}

	return nil
}

func (s *Service) CheckPCIDevicesInUse(vm vmModels.VM) error {
	if vm.PCIDevices == nil || len(vm.PCIDevices) == 0 {
		return nil
	}

	vms, err := s.ListVMs()
	if err != nil {
		return fmt.Errorf("failed_to_list_vms: %w", err)
	}

	for _, other := range vms {
		if other.VmID == vm.VmID {
			continue
		}

		domain, err := s.Conn.DomainLookupByName(strconv.Itoa(other.VmID))
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
					return fmt.Errorf("pci_device_%d_in_use_by_vm_%d", pci, other.VmID)
				}
			}
		}
	}

	return nil
}

func (s *Service) LvVMAction(vm vmModels.VM, action string) error {
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vm.VmID))
	if err != nil {
		return fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	err = s.CheckPCIDevicesInUse(vm)
	if err != nil {
		return err
	}

	switch action {
	case "start":
		state, _, err := s.Conn.DomainGetState(domain, 0)
		if err != nil {
			return fmt.Errorf("could_not_get_state: %w", err)
		}

		if state == 1 {
			return nil
		}

		err = s.StartTPM()

		if err != nil {
			return fmt.Errorf("failed_to_start_tpm: %w", err)
		}

		if err := s.Conn.DomainCreate(domain); err != nil {
			return fmt.Errorf("failed_to_start_domain: %w", err)
		}

		newState, _, err := s.Conn.DomainGetState(domain, 0)

		if err != nil {
			return fmt.Errorf("could_not_verify_run: %w", err)
		}

		if newState != 1 {
			return fmt.Errorf("unexpected_state_after_start: %d", newState)
		}

		err = s.SetActionDate(vm, "start")

		if err != nil {
			return fmt.Errorf("failed_to_set_start_date: %w", err)
		}

	case "stop":
		shutdown := false
		if err := s.Conn.DomainShutdown(domain); err == nil {
			shutdown = true
		}

		time.Sleep(10 * time.Second)

		stateAfterShutdown, _, err := s.Conn.DomainGetState(domain, 0)

		if !shutdown || stateAfterShutdown != 5 {
			if err := s.Conn.DomainDestroy(domain); err != nil {
				return fmt.Errorf("failed_to_stop_domain: %w", err)
			}
		}

		newState, _, err := s.Conn.DomainGetState(domain, 0)

		if err != nil {
			return fmt.Errorf("could_not_verify_stop: %w", err)
		}

		if newState != 5 {
			return fmt.Errorf("unexpected_state_after_stop: %d", newState)
		}

		/* This is an ugly hack because sometimes bhyve does not really stop?
		And this causes issues with the next start. So we find the user of the VNC port and kill that PID */
		user, err := utils.GetPortUserPID("tcp", vm.VNCPort)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "no process found using tcp port") {
				return err
			}
		}

		if user > 0 {
			if err := utils.KillProcess(user); err != nil {
				return fmt.Errorf("failed_to_kill_process_using_vnc_port: %w", err)
			}
		}

		err = s.SetActionDate(vm, "stop")

		if err != nil {
			return fmt.Errorf("failed_to_set_stop_date: %w", err)
		}
	case "reboot":
		if err := s.Conn.DomainReboot(domain, 0); err != nil {
			return fmt.Errorf("failed_to_reboot_domain: %w", err)
		}
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	return nil
}

func (s *Service) SetActionDate(vm vmModels.VM, action string) error {
	now := time.Now().UTC()

	switch action {
	case "start":
		vm.StartedAt = &now
	case "stop":
		vm.StoppedAt = &now
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	if err := s.DB.Save(&vm).Error; err != nil {
		return fmt.Errorf("failed_to_save_vm_action_date: %w", err)
	}

	return nil
}

func (s *Service) GetVMXML(vmId int) (string, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return "", fmt.Errorf("failed_to_lookup_domain: %w", err)
	}

	xmlDesc, err := s.Conn.DomainGetXMLDesc(domain, 0)
	if err != nil {
		return "", fmt.Errorf("failed_to_get_domain_xml_desc: %w", err)
	}

	return xmlDesc, nil
}

func (s *Service) IsDomainInactive(vmId int) (bool, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
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

func (s *Service) GetVmByVmId(vmId int) (vmModels.VM, error) {
	var vm vmModels.VM

	if err := s.DB.Preload("Storages").Preload("Networks").First(&vm, "vm_id = ?", vmId).Error; err != nil {
		return vmModels.VM{}, fmt.Errorf("failed_to_get_vm_by_id: %w", err)
	}

	return vm, nil
}

func (s *Service) IsDomainShutOff(vmId int) (bool, error) {
	domain, err := s.Conn.DomainLookupByName(strconv.Itoa(vmId))
	if err != nil {
		return false, fmt.Errorf("failed_to_lookup_domain_by_name: %w", err)
	}

	state, _, err := s.Conn.DomainGetState(domain, 0)

	if err != nil {
		return false, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}

	if state == 5 {
		return true, nil
	}

	return false, nil
}
