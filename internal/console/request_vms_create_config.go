// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/dustin/go-humanize"
)

const (
	defaultVMCreateRAM                 = 1024 * 1024 * 1024
	defaultVMCreateVNCResolution       = "800x600"
	maximumVMConfigTextBytes     int64 = 1024 * 1024
	maximumVMPasswordBytes       int64 = 4096
)

type VMCreateOverrides struct {
	RID         *uint
	Name        *string
	Description *string

	CPUSockets *int
	CPUCores   *int
	CPUThreads *int
	RAM        *string

	StoragePool      *string
	StorageType      *string
	StorageSize      *string
	StorageEmulation *string

	ISO                        *string
	CloudInitImage             *string
	CloudInitDataFile          *string
	CloudInitMetadataFile      *string
	CloudInitNetworkConfigFile *string

	Switch           *string
	NetworkEmulation *string

	BootROM *string

	VNCEnabled      *bool
	VNCPort         *int
	VNCBind         *string
	VNCResolution   *string
	VNCPasswordFile *string
	VNCWait         *bool

	StartAtBoot *bool
	StartOrder  *int
	TimeOffset  *string
}

type VMVNCChangeInput struct {
	Enabled       *bool
	Port          *int
	Bind          *string
	Resolution    *string
	Wait          *bool
	PasswordFile  string
	ClearPassword bool
}

type VMCloudInitReplacement struct {
	Data          string
	Metadata      string
	NetworkConfig string
}

func BuildVMVNCChanges(input VMVNCChangeInput) (VMVNCChanges, error) {
	if strings.TrimSpace(input.PasswordFile) != "" && input.ClearPassword {
		return VMVNCChanges{}, fmt.Errorf("password-file and clear-password cannot be used together")
	}
	changes := VMVNCChanges{
		Enabled: input.Enabled, Port: input.Port, Bind: input.Bind,
		Resolution: input.Resolution, Wait: input.Wait,
	}
	if input.Port != nil && (*input.Port < 1 || *input.Port > 65535) {
		return VMVNCChanges{}, fmt.Errorf("port must be between 1 and 65535")
	}
	if input.Bind != nil {
		value := strings.TrimSpace(*input.Bind)
		if value == "" {
			return VMVNCChanges{}, fmt.Errorf("bind cannot be empty")
		}
		changes.Bind = &value
	}
	if input.Resolution != nil {
		value := strings.TrimSpace(*input.Resolution)
		if value == "" {
			return VMVNCChanges{}, fmt.Errorf("resolution cannot be empty")
		}
		changes.Resolution = &value
	}
	if input.Enabled != nil && !*input.Enabled && input.Port != nil {
		return VMVNCChanges{}, fmt.Errorf("port is incompatible with enabled=false")
	}
	if path := strings.TrimSpace(input.PasswordFile); path != "" {
		password, err := loadVMTextFile(path, "VNC password", maximumVMPasswordBytes)
		if err != nil {
			return VMVNCChanges{}, err
		}
		password = strings.TrimRight(password, "\r\n")
		changes.Password = &password
	} else if input.ClearPassword {
		password := ""
		changes.Password = &password
	}
	if changes.Enabled == nil && changes.Port == nil && changes.Bind == nil && changes.Resolution == nil &&
		changes.Wait == nil && changes.Password == nil {
		return VMVNCChanges{}, fmt.Errorf("specify at least one VNC change")
	}
	return changes, nil
}

func BuildVMCloudInitReplacement(
	dataFile, metadataFile, networkConfigFile string,
	clear, noNetworkConfig bool,
) (VMCloudInitReplacement, error) {
	dataFile = strings.TrimSpace(dataFile)
	metadataFile = strings.TrimSpace(metadataFile)
	networkConfigFile = strings.TrimSpace(networkConfigFile)
	if clear {
		if dataFile != "" || metadataFile != "" || networkConfigFile != "" || noNetworkConfig {
			return VMCloudInitReplacement{}, fmt.Errorf("clear cannot be combined with cloud-init file options")
		}
		return VMCloudInitReplacement{}, nil
	}
	if dataFile == "" || metadataFile == "" {
		return VMCloudInitReplacement{}, fmt.Errorf("data-file and metadata-file are both required")
	}
	hasNetworkConfig := networkConfigFile != ""
	if hasNetworkConfig == noNetworkConfig {
		return VMCloudInitReplacement{}, fmt.Errorf("specify exactly one of network-config-file or no-network-config")
	}

	data, err := loadVMTextFile(dataFile, "cloud-init data", maximumVMConfigTextBytes)
	if err != nil {
		return VMCloudInitReplacement{}, err
	}
	metadata, err := loadVMTextFile(metadataFile, "cloud-init metadata", maximumVMConfigTextBytes)
	if err != nil {
		return VMCloudInitReplacement{}, err
	}
	networkConfig := ""
	if networkConfigFile != "" {
		networkConfig, err = loadVMTextFile(networkConfigFile, "cloud-init network configuration", maximumVMConfigTextBytes)
		if err != nil {
			return VMCloudInitReplacement{}, err
		}
	}
	if int64(len(data)+len(metadata)+len(networkConfig)) > maximumVMConfigTextBytes {
		return VMCloudInitReplacement{}, fmt.Errorf("combined cloud-init configuration exceeds 1 MiB")
	}
	return VMCloudInitReplacement{Data: data, Metadata: metadata, NetworkConfig: networkConfig}, nil
}

func ParseVMCPUPinning(values []string, clear bool) ([]libvirtServiceInterfaces.CPUPinning, error) {
	if clear && len(values) > 0 {
		return nil, fmt.Errorf("pin and clear-pinning cannot be used together")
	}
	if clear {
		return []libvirtServiceInterfaces.CPUPinning{}, nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("specify one or more pin values or clear-pinning")
	}
	pins := make([]libvirtServiceInterfaces.CPUPinning, 0, len(values))
	seenSockets := make(map[int]struct{}, len(values))
	for _, raw := range values {
		socketText, coresText, found := strings.Cut(strings.TrimSpace(raw), ":")
		if !found || strings.TrimSpace(socketText) == "" || strings.TrimSpace(coresText) == "" {
			return nil, fmt.Errorf("pin %q must use socket:core,core syntax", raw)
		}
		socket, err := strconv.Atoi(strings.TrimSpace(socketText))
		if err != nil || socket < 0 {
			return nil, fmt.Errorf("pin %q has an invalid socket", raw)
		}
		if _, duplicate := seenSockets[socket]; duplicate {
			return nil, fmt.Errorf("socket %d is specified more than once", socket)
		}
		seenSockets[socket] = struct{}{}
		coreValues := strings.Split(coresText, ",")
		cores := make([]int, 0, len(coreValues))
		seenCores := make(map[int]struct{}, len(coreValues))
		for _, coreText := range coreValues {
			core, err := strconv.Atoi(strings.TrimSpace(coreText))
			if err != nil || core < 0 {
				return nil, fmt.Errorf("pin %q has an invalid core", raw)
			}
			if _, duplicate := seenCores[core]; duplicate {
				return nil, fmt.Errorf("pin %q repeats core %d", raw, core)
			}
			seenCores[core] = struct{}{}
			cores = append(cores, core)
		}
		pins = append(pins, libvirtServiceInterfaces.CPUPinning{Socket: socket, Cores: cores})
	}
	return pins, nil
}

func ParseVMPositiveIntList(values []string, clear bool, field string) ([]int, error) {
	if clear && len(values) > 0 {
		return nil, fmt.Errorf("%s and clear cannot be used together", field)
	}
	if clear {
		return []int{}, nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("specify one or more %s values or clear", field)
	}
	result := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 {
			return nil, fmt.Errorf("%s must contain positive integers", field)
		}
		if _, duplicate := seen[parsed]; duplicate {
			return nil, fmt.Errorf("%s %d was specified more than once", field, parsed)
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

func BuildVMCreateRequest(path string, overrides VMCreateOverrides) (libvirtServiceInterfaces.CreateVMRequest, error) {
	path = strings.TrimSpace(path)
	request := defaultVMCreateRequest()
	if path != "" {
		loaded, err := LoadVMCreateRequest(path)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		request = loaded
	}

	if overrides.RID != nil {
		value := *overrides.RID
		request.RID = &value
	}
	if overrides.Name != nil {
		request.Name = strings.TrimSpace(*overrides.Name)
	}
	if overrides.Description != nil {
		request.Description = *overrides.Description
	}
	if overrides.CPUSockets != nil {
		request.CPUSockets = *overrides.CPUSockets
	}
	if overrides.CPUCores != nil {
		request.CPUCores = *overrides.CPUCores
	}
	if overrides.CPUThreads != nil {
		request.CPUThreads = *overrides.CPUThreads
	}
	if overrides.RAM != nil {
		ram, err := ParseVMMemorySize(*overrides.RAM)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		request.RAM = ram
	}

	if overrides.StoragePool != nil {
		request.StoragePool = strings.TrimSpace(*overrides.StoragePool)
	}
	if overrides.StorageType != nil {
		request.StorageType = libvirtServiceInterfaces.StorageType(strings.ToLower(strings.TrimSpace(*overrides.StorageType)))
	}
	if overrides.StorageSize != nil {
		size, err := parseVMCreateStorageSize(*overrides.StorageSize)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		request.StorageSize = &size
	}
	if overrides.StorageEmulation != nil {
		request.StorageEmulationType = libvirtServiceInterfaces.StorageEmulationType(strings.ToLower(strings.TrimSpace(*overrides.StorageEmulation)))
	}

	if overrides.ISO != nil && overrides.CloudInitImage != nil {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("iso and cloud-init-image cannot be used together")
	}
	if overrides.ISO != nil {
		request.ISO = strings.TrimSpace(*overrides.ISO)
		cloudInit := false
		request.CloudInit = &cloudInit
		request.CloudInitData = ""
		request.CloudInitMetaData = ""
		request.CloudInitNetworkConfig = ""
	}
	if overrides.CloudInitImage != nil {
		request.ISO = strings.TrimSpace(*overrides.CloudInitImage)
		cloudInit := true
		request.CloudInit = &cloudInit
	}

	cloudFilesSet := overrides.CloudInitDataFile != nil || overrides.CloudInitMetadataFile != nil ||
		overrides.CloudInitNetworkConfigFile != nil
	if cloudFilesSet {
		if overrides.CloudInitDataFile == nil || overrides.CloudInitMetadataFile == nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cloud-init-data-file and cloud-init-metadata-file are both required")
		}
		data, err := loadVMTextFile(*overrides.CloudInitDataFile, "cloud-init data", maximumVMConfigTextBytes)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		metadata, err := loadVMTextFile(*overrides.CloudInitMetadataFile, "cloud-init metadata", maximumVMConfigTextBytes)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		networkConfig := ""
		if overrides.CloudInitNetworkConfigFile != nil {
			networkConfig, err = loadVMTextFile(*overrides.CloudInitNetworkConfigFile, "cloud-init network configuration", maximumVMConfigTextBytes)
			if err != nil {
				return libvirtServiceInterfaces.CreateVMRequest{}, err
			}
		}
		if int64(len(data)+len(metadata)+len(networkConfig)) > maximumVMConfigTextBytes {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("combined cloud-init configuration exceeds 1 MiB")
		}
		request.CloudInitData = data
		request.CloudInitMetaData = metadata
		request.CloudInitNetworkConfig = networkConfig
	}

	if overrides.Switch != nil {
		request.SwitchName = strings.TrimSpace(*overrides.Switch)
	}
	if overrides.NetworkEmulation != nil {
		request.SwitchEmulationType = strings.ToLower(strings.TrimSpace(*overrides.NetworkEmulation))
	}
	if overrides.BootROM != nil {
		request.BootROM = strings.ToLower(strings.TrimSpace(*overrides.BootROM))
	}

	if overrides.VNCEnabled != nil {
		value := *overrides.VNCEnabled
		request.VNCEnabled = &value
	}
	if overrides.VNCPort != nil {
		request.VNCPort = *overrides.VNCPort
	}
	if overrides.VNCBind != nil {
		request.VNCBind = strings.TrimSpace(*overrides.VNCBind)
	}
	if overrides.VNCResolution != nil {
		request.VNCResolution = strings.TrimSpace(*overrides.VNCResolution)
	}
	if overrides.VNCPasswordFile != nil {
		password, err := loadVMTextFile(*overrides.VNCPasswordFile, "VNC password", maximumVMPasswordBytes)
		if err != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, err
		}
		request.VNCPassword = strings.TrimRight(password, "\r\n")
	}
	if overrides.VNCWait != nil {
		value := *overrides.VNCWait
		request.VNCWait = &value
	}
	if overrides.StartAtBoot != nil {
		value := *overrides.StartAtBoot
		request.StartAtBoot = &value
	}
	if overrides.StartOrder != nil {
		request.StartOrder = *overrides.StartOrder
	}
	if overrides.TimeOffset != nil {
		request.TimeOffset = libvirtServiceInterfaces.TimeOffset(strings.ToLower(strings.TrimSpace(*overrides.TimeOffset)))
	}

	return validateVMCreateCoreRequest(request, overrides)
}

func defaultVMCreateRequest() libvirtServiceInterfaces.CreateVMRequest {
	vncEnabled := false
	vncWait := false
	startAtBoot := false
	cloudInit := false
	return libvirtServiceInterfaces.CreateVMRequest{
		StorageType:          libvirtServiceInterfaces.StorageTypeNone,
		StorageEmulationType: libvirtServiceInterfaces.VirtIOStorageEmulation,
		SwitchName:           "none",
		CPUSockets:           1,
		CPUCores:             1,
		CPUThreads:           1,
		RAM:                  defaultVMCreateRAM,
		VNCEnabled:           &vncEnabled,
		VNCBind:              "127.0.0.1",
		VNCResolution:        defaultVMCreateVNCResolution,
		VNCWait:              &vncWait,
		CloudInit:            &cloudInit,
		StartAtBoot:          &startAtBoot,
		TimeOffset:           libvirtServiceInterfaces.TimeOffsetUTC,
	}
}

func validateVMCreateCoreRequest(
	request libvirtServiceInterfaces.CreateVMRequest,
	overrides VMCreateOverrides,
) (libvirtServiceInterfaces.CreateVMRequest, error) {
	if request.RID == nil || *request.RID == 0 || *request.RID > 9999 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("rid must be between 1 and 9999")
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("name is required")
	}
	if request.CPUSockets < 1 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cpu-sockets must be greater than zero")
	}
	if request.CPUCores < 1 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cpu-cores must be greater than zero")
	}
	if request.CPUThreads < 1 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cpu-threads must be greater than zero")
	}
	if request.RAM <= 0 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("ram must be a positive byte size")
	}

	request.StorageType = libvirtServiceInterfaces.StorageType(strings.ToLower(strings.TrimSpace(string(request.StorageType))))
	switch request.StorageType {
	case libvirtServiceInterfaces.StorageTypeNone:
		if overrides.StoragePool != nil || overrides.StorageSize != nil || overrides.StorageEmulation != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage pool, size, and emulation require storage-type raw or zvol")
		}
		storageExplicitlyCleared := overrides.StorageType != nil
		if !storageExplicitlyCleared && (strings.TrimSpace(request.StoragePool) != "" || request.StorageSize != nil) {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage-pool and storage-size are incompatible with storage-type none")
		}
		request.StoragePool = ""
		request.StorageSize = nil
		request.StorageEmulationType = libvirtServiceInterfaces.VirtIOStorageEmulation
	case libvirtServiceInterfaces.StorageTypeRaw, libvirtServiceInterfaces.StorageTypeZVOL:
		request.StoragePool = strings.TrimSpace(request.StoragePool)
		if request.StoragePool == "" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage-pool is required for %s storage", request.StorageType)
		}
		if request.StorageSize == nil || *request.StorageSize == 0 {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage-size is required for %s storage", request.StorageType)
		}
		if request.StorageEmulationType == "" {
			request.StorageEmulationType = libvirtServiceInterfaces.VirtIOStorageEmulation
		}
		switch request.StorageEmulationType {
		case libvirtServiceInterfaces.VirtIOStorageEmulation,
			libvirtServiceInterfaces.AHCIHDStorageEmulation,
			libvirtServiceInterfaces.NVMEStorageEmulation:
		default:
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage-emulation must be virtio-blk, ahci-hd, or nvme")
		}
	default:
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("storage-type must be none, raw, or zvol")
	}

	if overrides.Switch != nil && strings.TrimSpace(*overrides.Switch) == "" {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("switch cannot be empty; use none for no network")
	}
	switchExplicitlyCleared := overrides.Switch != nil && strings.EqualFold(strings.TrimSpace(*overrides.Switch), "none")
	request.SwitchName = strings.TrimSpace(request.SwitchName)
	if request.SwitchName == "" {
		request.SwitchName = "none"
	}
	if strings.EqualFold(request.SwitchName, "none") {
		if overrides.NetworkEmulation != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("network-emulation requires a switch")
		}
		if !switchExplicitlyCleared && (strings.TrimSpace(request.SwitchEmulationType) != "" || request.MacId != nil) {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("network-emulation and mac-id are incompatible with switch none")
		}
		request.SwitchName = "none"
		request.SwitchEmulationType = ""
		request.MacId = nil
	} else {
		request.SwitchEmulationType = strings.ToLower(strings.TrimSpace(request.SwitchEmulationType))
		if request.SwitchEmulationType == "" {
			request.SwitchEmulationType = "virtio"
		}
		if request.SwitchEmulationType != "virtio" && request.SwitchEmulationType != "e1000" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("network-emulation must be virtio or e1000")
		}
		if request.MacId != nil && *request.MacId == 0 {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("mac-id must be greater than zero")
		}
	}

	vncEnabled := true
	if request.VNCEnabled != nil {
		vncEnabled = *request.VNCEnabled
	}
	if !vncEnabled {
		if overrides.VNCPort != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("vnc-port is incompatible with VNC disabled")
		}
		request.VNCPort = 0
	} else {
		if request.VNCPort < 1 || request.VNCPort > 65535 {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("vnc-port must be between 1 and 65535 when VNC is enabled")
		}
		if strings.TrimSpace(request.VNCResolution) == "" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("vnc-resolution is required when VNC is enabled")
		}
	}
	if request.StartOrder < 0 {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("start-order must be zero or greater")
	}
	request.TimeOffset = libvirtServiceInterfaces.TimeOffset(strings.ToLower(strings.TrimSpace(string(request.TimeOffset))))
	if request.TimeOffset != libvirtServiceInterfaces.TimeOffsetUTC && request.TimeOffset != libvirtServiceInterfaces.TimeOffsetLocal {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("time-offset must be utc or localtime")
	}

	cloudInit := request.CloudInit != nil && *request.CloudInit
	if cloudInit {
		if strings.TrimSpace(request.ISO) == "" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cloud-init-image is required when cloud-init is enabled")
		}
		if strings.TrimSpace(request.CloudInitData) == "" || strings.TrimSpace(request.CloudInitMetaData) == "" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cloud-init data and metadata are required")
		}
	} else {
		if overrides.CloudInitDataFile != nil || overrides.CloudInitMetadataFile != nil ||
			overrides.CloudInitNetworkConfigFile != nil {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cloud-init files require cloud-init-image")
		}
		if strings.TrimSpace(request.CloudInitData) != "" || strings.TrimSpace(request.CloudInitMetaData) != "" ||
			strings.TrimSpace(request.CloudInitNetworkConfig) != "" {
			return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("cloud-init data, metadata, and network configuration require cloud-init to be enabled")
		}
	}
	return request, nil
}

func ParseVMMemorySize(value string) (int, error) {
	bytes, err := humanize.ParseBytes(strings.TrimSpace(value))
	if err != nil || bytes == 0 || bytes > uint64(math.MaxInt) {
		return 0, fmt.Errorf("ram must be a positive byte size such as 1GiB")
	}
	return int(bytes), nil
}

func parseVMCreateStorageSize(value string) (uint64, error) {
	bytes, err := humanize.ParseBytes(strings.TrimSpace(value))
	if err != nil || bytes == 0 {
		return 0, fmt.Errorf("storage-size must be a positive byte size such as 20GiB")
	}
	return bytes, nil
}

func loadVMTextFile(path, kind string, maxBytes int64) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s file is required", kind)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s file %q: %w", kind, path, err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s file %q: %w", kind, path, err)
	}
	if int64(len(contents)) > maxBytes {
		return "", fmt.Errorf("%s file %q exceeds %s", kind, path, humanize.IBytes(uint64(maxBytes)))
	}
	return string(contents), nil
}
