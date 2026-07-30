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
	"io"
	"os"
	"path/filepath"
	"strings"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
	diskUtils "github.com/alchemillahq/sylve/pkg/disk"
)

const (
	diskEdgeWipeSize    = uint64(1024 * 1024)
	minimumGPTWipeSize  = 2 * diskEdgeWipeSize
	maximumWriteAtRange = uint64(1<<63 - 1)
)

var (
	errDiskTooSmallForGPTWipe = errors.New("disk size is too small for GPT wipe")
	errDiskTooLargeForGPTWipe = errors.New("disk size exceeds supported GPT wipe range")
)

type diskEdgeWriter interface {
	WriteAt([]byte, int64) (int, error)
	Sync() error
}

type diskWriteCloser interface {
	diskEdgeWriter
	Close() error
}

func validateGPTWipeSize(diskSize uint64) error {
	if diskSize < minimumGPTWipeSize {
		return errDiskTooSmallForGPTWipe
	}
	if diskSize > maximumWriteAtRange {
		return errDiskTooLargeForGPTWipe
	}

	return nil
}

func writeDiskEdge(writer diskEdgeWriter, zeros []byte, offset int64, edge string) error {
	written, err := writer.WriteAt(zeros, offset)
	if err != nil {
		return fmt.Errorf("error wiping %s GPT: %w", edge, err)
	}
	if written != len(zeros) {
		return fmt.Errorf(
			"error wiping %s GPT: %w: wrote %d of %d bytes",
			edge,
			io.ErrShortWrite,
			written,
			len(zeros),
		)
	}

	return nil
}

func wipeDiskEdges(writer diskEdgeWriter, diskSize uint64) error {
	if err := validateGPTWipeSize(diskSize); err != nil {
		return err
	}

	zeros := make([]byte, diskEdgeWipeSize)
	if err := writeDiskEdge(writer, zeros, 0, "primary"); err != nil {
		return err
	}

	backupOffset := int64(diskSize - diskEdgeWipeSize)
	if err := writeDiskEdge(writer, zeros, backupOffset, "backup"); err != nil {
		return err
	}

	if err := writer.Sync(); err != nil {
		return fmt.Errorf("failed to sync disk: %w", err)
	}

	return nil
}

func relativeDiskDeviceName(device string) (string, error) {
	const devicePrefix = "/dev/"

	if filepath.Clean(device) != device || !strings.HasPrefix(device, devicePrefix) {
		return "", fmt.Errorf("invalid disk device path: %s", device)
	}

	name := strings.TrimPrefix(device, devicePrefix)
	if name == "" {
		return "", fmt.Errorf("invalid disk device path: %s", device)
	}

	return name, nil
}

func diskInfoMatchesDevice(disk diskServiceInterfaces.DiskInfo, deviceName string) bool {
	if strings.TrimPrefix(disk.Name, "/dev/") == deviceName {
		return true
	}

	for _, alias := range disk.Aliases {
		if strings.TrimPrefix(alias, "/dev/") == deviceName {
			return true
		}
	}

	return false
}

func resolveWholeDiskSize(
	device string,
	checkDevice func(string) error,
	listDisks func() ([]diskServiceInterfaces.DiskInfo, error),
) (uint64, error) {
	deviceName, err := relativeDiskDeviceName(device)
	if err != nil {
		return 0, err
	}

	if err := checkDevice(device); err != nil {
		return 0, err
	}

	disks, err := listDisks()
	if err != nil {
		return 0, fmt.Errorf("failed to list disk providers: %w", err)
	}

	for _, disk := range disks {
		if !diskInfoMatchesDevice(disk, deviceName) {
			continue
		}
		if disk.MediaSize <= 0 {
			return 0, fmt.Errorf("disk %s has an invalid media size: %d", device, disk.MediaSize)
		}

		return uint64(disk.MediaSize), nil
	}

	return 0, fmt.Errorf("device is not a top-level disk provider: %s", device)
}

func openDiskForWrite(device string) (diskWriteCloser, error) {
	return os.OpenFile(device, os.O_WRONLY, 0600)
}

func destroyPartitionTable(
	device string,
	resolveSize func(string) (uint64, error),
	destroyDisk func(string) error,
	openDisk func(string) (diskWriteCloser, error),
) error {
	diskSize, err := resolveSize(device)
	if err != nil {
		return err
	}
	if err := validateGPTWipeSize(diskSize); err != nil {
		return err
	}

	if err := destroyDisk(device); err != nil && !errors.Is(err, diskUtils.ErrNoPartitionTable) {
		return fmt.Errorf("failed to detach partition table from %s: %w", device, err)
	}

	writer, err := openDisk(device)
	if err != nil {
		return fmt.Errorf("failed to open disk %s for wiping: %w", device, err)
	}

	wipeErr := wipeDiskEdges(writer, diskSize)
	closeErr := writer.Close()

	if wipeErr != nil && closeErr != nil {
		return errors.Join(wipeErr, fmt.Errorf("failed to close disk %s: %w", device, closeErr))
	}
	if wipeErr != nil {
		return wipeErr
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close disk %s: %w", device, closeErr)
	}

	return nil
}
