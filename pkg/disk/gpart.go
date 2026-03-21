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
	"regexp"
	"strconv"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func CheckDevice(device string) error {
	info, err := os.Stat(device)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("device %s does not exist", device)
		}
		return fmt.Errorf("error accessing device %s: %v", device, err)
	}

	if info.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("%s exists but is not a block device", device)
	}

	return nil
}

func DestroyDisk(device string) error {
	err := CheckDevice(device)

	if err != nil {
		return err
	}

	output, err := utils.RunCommand("/sbin/gpart", "destroy", "-F", device)
	if err != nil {
		return fmt.Errorf("error destroying disk %s: %v, output: %s", device, err, output)
	}

	return nil
}

func CreatePartition(device string, size uint64, ptype string) error {
	err := CheckDevice(device)

	if err != nil {
		return err
	}

	mbytes := uint64(utils.BytesToSize("MB", float64(size)))
	if mbytes < 1 {
		return fmt.Errorf("size must be at least 1MB")
	}

	if ptype == "" {
		ptype = "freebsd-zfs"
	}

	output, err := utils.RunCommand("/sbin/gpart", "add", "-t", ptype, "-s", fmt.Sprintf("%dMB", mbytes), device)
	if err != nil {
		return fmt.Errorf("error creating partition on disk %s: %v, output: %s", device, err, output)
	}

	return nil
}

func CreatePartitions(device string, sizes []uint64) error {
	err := CheckDevice(device)

	if err != nil {
		return err
	}

	totalRequiredSize := uint64(0)

	for _, size := range sizes {
		totalRequiredSize += size
	}

	diskSize, err := GetDiskSize(device)

	if err != nil {
		return fmt.Errorf("failed to get disk size: %v", err)
	}

	if diskSize < totalRequiredSize {
		return fmt.Errorf("disk size is too small for partitions")
	}

	for _, size := range sizes {
		err = CreatePartition(device, size, "")

		if err != nil {
			return err
		}
	}

	return nil
}

func ParsePartition(device string) (string, int, error) {
	re := regexp.MustCompile(`^(/dev/[a-z0-9]+)[ps]([0-9]+)$`)
	matches := re.FindStringSubmatch(device)

	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid device format: %s", device)
	}

	disk := matches[1]
	partNum, err := strconv.Atoi(matches[2])
	if err != nil {
		return "", 0, fmt.Errorf("invalid partition number in %s: %v", device, err)
	}

	return disk, partNum, nil
}

func DeletePartition(device string) error {
	err := CheckDevice(device)
	if err != nil {
		return err
	}

	disk, partition, err := ParsePartition(device)
	if err != nil {
		return err
	}

	output, err := utils.RunCommand("/sbin/gpart", "delete", "-i", fmt.Sprintf("%d", partition), disk)
	if err != nil {
		return fmt.Errorf("error deleting partition %d from disk %s: %v, output: %s", partition, disk, err, output)
	}

	return nil
}
