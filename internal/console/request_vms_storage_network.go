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
	"math"
	"path/filepath"
	"strings"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/dustin/go-humanize"
)

type VMStorageAttachInput struct {
	RID              uint
	Name             string
	StorageType      string
	Pool             string
	Size             string
	RawPath          string
	DatasetGUID      string
	ImageUUID        string
	Emulation        string
	FilesystemTarget string
	ReadOnly         *bool
}

type VMStorageUpdateInput struct {
	RID              uint
	StorageID        uint
	Name             *string
	Size             *string
	Emulation        *string
	BootOrder        *int
	Enabled          *bool
	FilesystemTarget *string
	ReadOnly         *bool
}

type VMNetworkUpdateInput struct {
	RID         uint
	NetworkID   uint
	SwitchName  *string
	Emulation   *string
	MacID       *uint
	GenerateMAC bool
	Enabled     *bool
}

func BuildVMStorageAttachRequest(input VMStorageAttachInput) (libvirtServiceInterfaces.StorageAttachRequest, error) {
	if err := validateVMRID(input.RID); err != nil {
		return libvirtServiceInterfaces.StorageAttachRequest{}, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("storage name is required")
	}
	if len(name) > 128 {
		return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("storage name must be at most 128 characters")
	}

	storageType, err := parseVMStorageType(input.StorageType)
	if err != nil {
		return libvirtServiceInterfaces.StorageAttachRequest{}, err
	}
	pool := strings.TrimSpace(input.Pool)
	sizeText := strings.TrimSpace(input.Size)
	rawPath := strings.TrimSpace(input.RawPath)
	datasetGUID := strings.TrimSpace(input.DatasetGUID)
	imageUUID := strings.TrimSpace(input.ImageUUID)
	filesystemTarget := strings.TrimSpace(input.FilesystemTarget)

	emulation, err := normalizeVMStorageAttachEmulation(storageType, input.Emulation)
	if err != nil {
		return libvirtServiceInterfaces.StorageAttachRequest{}, err
	}

	request := libvirtServiceInterfaces.StorageAttachRequest{
		RID:              input.RID,
		Name:             name,
		StorageType:      storageType,
		Emulation:        emulation,
		RawPath:          rawPath,
		Dataset:          datasetGUID,
		UUID:             imageUUID,
		FilesystemTarget: filesystemTarget,
		ReadOnly:         input.ReadOnly,
	}
	if pool != "" {
		request.Pool = &pool
	}

	forbid := func(condition bool, field, expected string) error {
		if !condition {
			return nil
		}
		return fmt.Errorf("%s is incompatible with %s storage", field, expected)
	}

	switch storageType {
	case libvirtServiceInterfaces.StorageTypeRaw:
		if pool == "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("pool is required for raw storage")
		}
		if err := forbid(datasetGUID != "", "dataset-guid", "raw"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if err := forbid(imageUUID != "", "image-uuid", "raw"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if err := forbid(filesystemTarget != "" || input.ReadOnly != nil, "filesystem options", "raw"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if rawPath != "" {
			if sizeText != "" {
				return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("size cannot be used when importing a raw disk")
			}
			if !filepath.IsAbs(rawPath) {
				return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("raw-path must be absolute")
			}
			request.AttachType = libvirtServiceInterfaces.StorageAttachTypeImport
		} else {
			request.AttachType = libvirtServiceInterfaces.StorageAttachTypeNew
			request.Size, err = parseVMStorageSize(sizeText)
			if err != nil {
				return libvirtServiceInterfaces.StorageAttachRequest{}, err
			}
		}

	case libvirtServiceInterfaces.StorageTypeZVOL:
		if pool == "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("pool is required for zvol storage")
		}
		if err := forbid(rawPath != "", "raw-path", "zvol"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if err := forbid(imageUUID != "", "image-uuid", "zvol"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if err := forbid(filesystemTarget != "" || input.ReadOnly != nil, "filesystem options", "zvol"); err != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, err
		}
		if datasetGUID != "" {
			if sizeText != "" {
				return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("size cannot be used when importing a zvol")
			}
			request.AttachType = libvirtServiceInterfaces.StorageAttachTypeImport
		} else {
			request.AttachType = libvirtServiceInterfaces.StorageAttachTypeNew
			request.Size, err = parseVMStorageSize(sizeText)
			if err != nil {
				return libvirtServiceInterfaces.StorageAttachRequest{}, err
			}
		}

	case libvirtServiceInterfaces.StorageTypeDiskImage:
		if imageUUID == "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("image-uuid is required for image storage")
		}
		if pool != "" || sizeText != "" || rawPath != "" || datasetGUID != "" || filesystemTarget != "" ||
			input.ReadOnly != nil {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("pool, size, raw-path, dataset-guid, and filesystem options are incompatible with image storage")
		}
		request.AttachType = libvirtServiceInterfaces.StorageAttachTypeImport

	case libvirtServiceInterfaces.StorageTypeFilesystem:
		if datasetGUID == "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("dataset-guid is required for filesystem storage")
		}
		if filesystemTarget == "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("filesystem-target is required for filesystem storage")
		}
		if pool != "" || sizeText != "" || rawPath != "" || imageUUID != "" {
			return libvirtServiceInterfaces.StorageAttachRequest{}, fmt.Errorf("pool, size, raw-path, and image-uuid are incompatible with filesystem storage")
		}
		request.AttachType = libvirtServiceInterfaces.StorageAttachTypeNew
	}

	return request, nil
}

func BuildVMStorageUpdateRequest(input VMStorageUpdateInput) (libvirtServiceInterfaces.StorageUpdateRequest, error) {
	if err := validateVMRID(input.RID); err != nil {
		return libvirtServiceInterfaces.StorageUpdateRequest{}, err
	}
	if input.StorageID == 0 {
		return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("storage-id must be greater than zero")
	}
	if input.Name == nil && input.Size == nil && input.Emulation == nil && input.BootOrder == nil &&
		input.Enabled == nil && input.FilesystemTarget == nil && input.ReadOnly == nil {
		return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("specify at least one storage change")
	}

	request := libvirtServiceInterfaces.StorageUpdateRequest{
		RID:              input.RID,
		ID:               input.StorageID,
		BootOrder:        input.BootOrder,
		Enable:           input.Enabled,
		FilesystemTarget: input.FilesystemTarget,
		ReadOnly:         input.ReadOnly,
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value == "" {
			return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("name cannot be empty")
		}
		if len(value) > 128 {
			return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("name must be at most 128 characters")
		}
		request.Name = &value
	}
	if input.Size != nil {
		size, err := parseVMStorageSize(strings.TrimSpace(*input.Size))
		if err != nil {
			return libvirtServiceInterfaces.StorageUpdateRequest{}, err
		}
		request.Size = size
	}
	if input.Emulation != nil {
		value, err := parseVMStorageEmulation(*input.Emulation)
		if err != nil {
			return libvirtServiceInterfaces.StorageUpdateRequest{}, err
		}
		request.Emulation = &value
	}
	if input.BootOrder != nil && *input.BootOrder < 0 {
		return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("boot-order must be zero or greater")
	}
	if input.FilesystemTarget != nil {
		value := strings.TrimSpace(*input.FilesystemTarget)
		if value == "" {
			return libvirtServiceInterfaces.StorageUpdateRequest{}, fmt.Errorf("filesystem-target cannot be empty")
		}
		request.FilesystemTarget = &value
	}
	return request, nil
}

func BuildVMNetworkUpdateRequest(input VMNetworkUpdateInput) (libvirtServiceInterfaces.NetworkUpdateRequest, error) {
	if err := validateVMRID(input.RID); err != nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, err
	}
	if input.NetworkID == 0 {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("network-id must be greater than zero")
	}
	if input.MacID != nil && input.GenerateMAC {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("mac-id and generate-mac cannot be used together")
	}
	if input.MacID != nil && *input.MacID == 0 {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("mac-id must be greater than zero")
	}

	request := libvirtServiceInterfaces.NetworkUpdateRequest{
		RID:       input.RID,
		NetworkID: input.NetworkID,
		Enable:    input.Enabled,
	}
	if input.SwitchName != nil {
		value := strings.TrimSpace(*input.SwitchName)
		if value == "" {
			return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("switch cannot be empty")
		}
		request.SwitchName = &value
	}
	if input.Emulation != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Emulation))
		if value != "virtio" && value != "e1000" {
			return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("emulation must be virtio or e1000")
		}
		request.Emulation = &value
	}
	if input.GenerateMAC {
		generated := uint(0)
		request.MacID = &generated
	} else {
		request.MacID = input.MacID
	}
	if request.SwitchName == nil && request.Emulation == nil && request.MacID == nil && request.Enable == nil {
		return libvirtServiceInterfaces.NetworkUpdateRequest{}, fmt.Errorf("specify at least one network change")
	}
	return request, nil
}

func validateVMRID(rid uint) error {
	if rid == 0 || rid > 9999 {
		return fmt.Errorf("rid must be between 1 and 9999")
	}
	return nil
}

func parseVMStorageType(value string) (libvirtServiceInterfaces.StorageType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(libvirtServiceInterfaces.StorageTypeRaw):
		return libvirtServiceInterfaces.StorageTypeRaw, nil
	case string(libvirtServiceInterfaces.StorageTypeZVOL):
		return libvirtServiceInterfaces.StorageTypeZVOL, nil
	case string(libvirtServiceInterfaces.StorageTypeDiskImage), "iso":
		return libvirtServiceInterfaces.StorageTypeDiskImage, nil
	case string(libvirtServiceInterfaces.StorageTypeFilesystem):
		return libvirtServiceInterfaces.StorageTypeFilesystem, nil
	default:
		return "", fmt.Errorf("type must be raw, zvol, image, iso, or filesystem")
	}
}

func normalizeVMStorageAttachEmulation(
	storageType libvirtServiceInterfaces.StorageType,
	value string,
) (libvirtServiceInterfaces.StorageEmulationType, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		switch storageType {
		case libvirtServiceInterfaces.StorageTypeDiskImage:
			return libvirtServiceInterfaces.AHCICDStorageEmulation, nil
		case libvirtServiceInterfaces.StorageTypeFilesystem:
			return libvirtServiceInterfaces.VirtIO9PStorageEmulation, nil
		default:
			return libvirtServiceInterfaces.VirtIOStorageEmulation, nil
		}
	}
	emulation, err := parseVMStorageEmulation(value)
	if err != nil {
		return "", err
	}
	if storageType == libvirtServiceInterfaces.StorageTypeFilesystem &&
		emulation != libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		return "", fmt.Errorf("filesystem storage requires virtio-9p emulation")
	}
	if storageType != libvirtServiceInterfaces.StorageTypeFilesystem &&
		emulation == libvirtServiceInterfaces.VirtIO9PStorageEmulation {
		return "", fmt.Errorf("virtio-9p emulation requires filesystem storage")
	}
	return emulation, nil
}

func parseVMStorageEmulation(value string) (libvirtServiceInterfaces.StorageEmulationType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(libvirtServiceInterfaces.VirtIOStorageEmulation):
		return libvirtServiceInterfaces.VirtIOStorageEmulation, nil
	case string(libvirtServiceInterfaces.VirtIO9PStorageEmulation):
		return libvirtServiceInterfaces.VirtIO9PStorageEmulation, nil
	case string(libvirtServiceInterfaces.AHCIHDStorageEmulation):
		return libvirtServiceInterfaces.AHCIHDStorageEmulation, nil
	case string(libvirtServiceInterfaces.AHCICDStorageEmulation):
		return libvirtServiceInterfaces.AHCICDStorageEmulation, nil
	case string(libvirtServiceInterfaces.NVMEStorageEmulation):
		return libvirtServiceInterfaces.NVMEStorageEmulation, nil
	default:
		return "", fmt.Errorf("emulation must be virtio-blk, virtio-9p, ahci-hd, ahci-cd, or nvme")
	}
}

func parseVMStorageSize(value string) (*int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("size is required")
	}
	bytes, err := humanize.ParseBytes(value)
	if err != nil || bytes == 0 {
		return nil, fmt.Errorf("size must be a positive byte size such as 10GiB")
	}
	if bytes > math.MaxInt64 {
		return nil, fmt.Errorf("size exceeds the supported maximum")
	}
	size := int64(bytes)
	return &size, nil
}
