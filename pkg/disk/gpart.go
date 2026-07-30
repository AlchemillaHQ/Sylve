// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

var ErrNoPartitionTable = errors.New("no GEOM-recognized partition table")

var noPartitionTableDiagnostics = []*regexp.Regexp{
	regexp.MustCompile(`^gpart: No such geom(?:: [^\r\n]+)?\.?$`),
	regexp.MustCompile(`^gpart: arg0 '[^'\r\n]+': Invalid argument$`),
}

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

func isNoPartitionTableDiagnostic(output string) bool {
	diagnostic := strings.TrimSpace(output)
	for _, pattern := range noPartitionTableDiagnostics {
		if pattern.MatchString(diagnostic) {
			return true
		}
	}

	return false
}

func classifyDestroyDiskResult(device, output string, commandErr error) error {
	if commandErr == nil {
		return nil
	}

	diagnostic := strings.TrimSpace(output)
	if isNoPartitionTableDiagnostic(diagnostic) {
		return fmt.Errorf("%w for %s: %s", ErrNoPartitionTable, device, diagnostic)
	}

	if diagnostic == "" {
		return fmt.Errorf("error destroying disk %s: %w", device, commandErr)
	}

	return fmt.Errorf("error destroying disk %s: %w, output: %s", device, commandErr, diagnostic)
}

func destroyDisk(
	device string,
	checkDevice func(string) error,
	runCommand func(string, ...string) (string, error),
) error {
	err := checkDevice(device)

	if err != nil {
		return err
	}

	output, err := runCommand("/sbin/gpart", "destroy", "-F", device)
	return classifyDestroyDiskResult(device, output, err)
}

func DestroyDisk(device string) error {
	return destroyDisk(device, CheckDevice, utils.RunCommand)
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
