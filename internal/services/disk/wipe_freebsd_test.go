// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package disk

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	diskServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/disk"
)

const mdIntegrationDiskSize = "64m"

var (
	mdIntegrationDevicePattern   = regexp.MustCompile(`^md[0-9]+$`)
	mdIntegrationProviderPattern = regexp.MustCompile(`^[[:alnum:]_.-]+$`)
)

type mdIntegrationDisk struct {
	name       string
	device     string
	mediaSize  uint64
	sectorSize int
}

func requireMDIntegrationHost(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping md(4) disk wipe integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("run md(4) disk wipe integration tests as root")
	}

	for _, command := range []string{
		"/sbin/gpart",
		"/sbin/mdconfig",
		"/sbin/mount",
		"/sbin/newfs",
		"/sbin/umount",
		"/usr/sbin/diskinfo",
	} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("required command %q is unavailable: %v", command, err)
		}
	}
}

func runMDIntegrationCommand(t *testing.T, command string, args ...string) string {
	t.Helper()

	output, err := exec.Command(command, args...).CombinedOutput()
	if err != nil {
		t.Fatalf(
			"%s %s failed: %v\noutput: %s",
			command,
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}

	return strings.TrimSpace(string(output))
}

func readMDIntegrationGeometry(t *testing.T, device string) (uint64, int) {
	t.Helper()

	output := runMDIntegrationCommand(t, "/usr/sbin/diskinfo", "-v", device)
	var (
		mediaSize       uint64
		sectorSize      uint64
		foundMediaSize  bool
		foundSectorSize bool
	)

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch {
		case strings.Contains(line, "mediasize in bytes"):
			value, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				t.Fatalf("parse media size from diskinfo line %q: %v", line, err)
			}
			mediaSize = value
			foundMediaSize = true
		case strings.Contains(line, "sectorsize"):
			value, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				t.Fatalf("parse sector size from diskinfo line %q: %v", line, err)
			}
			sectorSize = value
			foundSectorSize = true
		}
	}

	if !foundMediaSize || mediaSize == 0 {
		t.Fatalf("diskinfo did not report a positive media size for %s:\n%s", device, output)
	}
	if !foundSectorSize || sectorSize == 0 || sectorSize > uint64(^uint(0)>>1) {
		t.Fatalf("diskinfo reported an invalid sector size for %s: %d", device, sectorSize)
	}

	return mediaSize, int(sectorSize)
}

func createMDIntegrationDisk(t *testing.T, sectorSize int) mdIntegrationDisk {
	t.Helper()

	output := runMDIntegrationCommand(
		t,
		"/sbin/mdconfig",
		"-a",
		"-t",
		"swap",
		"-s",
		mdIntegrationDiskSize,
		"-S",
		strconv.Itoa(sectorSize),
	)
	name := strings.TrimSpace(output)
	if !mdIntegrationDevicePattern.MatchString(name) {
		t.Fatalf("mdconfig returned unsafe device name %q", name)
	}

	t.Cleanup(func() {
		// The table may already be gone. This is only a best-effort cleanup
		// before detaching the automatically allocated memory disk.
		_, _ = exec.Command("/sbin/gpart", "destroy", "-F", name).CombinedOutput()

		output, err := exec.Command("/sbin/mdconfig", "-d", "-u", name).CombinedOutput()
		if err == nil {
			return
		}

		forceOutput, forceErr := exec.Command(
			"/sbin/mdconfig",
			"-d",
			"-u",
			name,
			"-o",
			"force",
		).CombinedOutput()
		if forceErr != nil {
			t.Errorf(
				"detach memory disk %s: %v: %s; forced detach: %v: %s",
				name,
				err,
				strings.TrimSpace(string(output)),
				forceErr,
				strings.TrimSpace(string(forceOutput)),
			)
		}
	})

	device := "/dev/" + name
	mediaSize, actualSectorSize := readMDIntegrationGeometry(t, device)
	if actualSectorSize != sectorSize {
		t.Fatalf(
			"memory disk %s sector size = %d; want %d",
			name,
			actualSectorSize,
			sectorSize,
		)
	}

	return mdIntegrationDisk{
		name:       name,
		device:     device,
		mediaSize:  mediaSize,
		sectorSize: actualSectorSize,
	}
}

func (disk mdIntegrationDisk) service() *Service {
	return &Service{
		physicalDiskSource: func() ([]diskServiceInterfaces.DiskInfo, error) {
			return []diskServiceInterfaces.DiskInfo{
				{
					Name:       disk.name,
					MediaSize:  int64(disk.mediaSize),
					SectorSize: disk.sectorSize,
				},
			}, nil
		},
	}
}

func createMDIntegrationGPT(t *testing.T, disk mdIntegrationDisk, partitionType string) string {
	t.Helper()

	runMDIntegrationCommand(t, "/sbin/gpart", "create", "-s", "GPT", disk.name)
	if partitionType == "" {
		return ""
	}

	output := runMDIntegrationCommand(
		t,
		"/sbin/gpart",
		"add",
		"-t",
		partitionType,
		disk.name,
	)
	const addedSuffix = " added"
	if !strings.HasSuffix(output, addedSuffix) {
		t.Fatalf("unexpected gpart add output %q", output)
	}

	provider := strings.TrimSuffix(output, addedSuffix)
	if provider == disk.name ||
		!strings.HasPrefix(provider, disk.name) ||
		!mdIntegrationProviderPattern.MatchString(provider) {
		t.Fatalf("gpart returned unsafe partition provider %q", provider)
	}

	return "/dev/" + provider
}

func assertMDIntegrationGpartTable(t *testing.T, disk mdIntegrationDisk, wantTable bool) {
	t.Helper()

	output, err := exec.Command("/sbin/gpart", "show", disk.name).CombinedOutput()
	if wantTable && err != nil {
		t.Fatalf(
			"gpart table for %s is unavailable: %v: %s",
			disk.name,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	if !wantTable && err == nil {
		t.Fatalf("gpart still recognizes a table on %s:\n%s", disk.name, output)
	}
}

func writeMDIntegrationStaleGPTSignatures(t *testing.T, disk mdIntegrationDisk) {
	t.Helper()

	if disk.mediaSize < uint64(2*disk.sectorSize) {
		t.Fatalf(
			"memory disk %s is too small for GPT signatures: size=%d sector=%d",
			disk.name,
			disk.mediaSize,
			disk.sectorSize,
		)
	}

	sector := make([]byte, disk.sectorSize)
	copy(sector, []byte("EFI PART"))
	offsets := []int64{
		int64(disk.sectorSize),
		int64(disk.mediaSize) - int64(disk.sectorSize),
	}

	file, err := os.OpenFile(disk.device, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open %s for stale-signature setup: %v", disk.device, err)
	}

	for _, offset := range offsets {
		written, writeErr := file.WriteAt(sector, offset)
		if writeErr != nil {
			_ = file.Close()
			t.Fatalf("write stale GPT signature at offset %d: %v", offset, writeErr)
		}
		if written != len(sector) {
			_ = file.Close()
			t.Fatalf(
				"write stale GPT signature at offset %d: wrote %d of %d bytes",
				offset,
				written,
				len(sector),
			)
		}
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync stale GPT signatures on %s: %v", disk.device, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s after stale-signature setup: %v", disk.device, err)
	}

	for _, offset := range offsets {
		sector := make([]byte, disk.sectorSize)
		file, err := os.Open(disk.device)
		if err != nil {
			t.Fatalf("open %s to verify stale GPT signature: %v", disk.device, err)
		}
		read, readErr := file.ReadAt(sector, offset)
		closeErr := file.Close()
		if readErr != nil {
			t.Fatalf("read stale GPT signature at offset %d: %v", offset, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %s after signature verification: %v", disk.device, closeErr)
		}
		if read != len(sector) || !bytes.Equal(sector[:len("EFI PART")], []byte("EFI PART")) {
			t.Fatalf("stale GPT signature was not written at offset %d", offset)
		}
	}
}

func assertMDIntegrationEdgesZero(t *testing.T, disk mdIntegrationDisk) {
	t.Helper()

	file, err := os.Open(disk.device)
	if err != nil {
		t.Fatalf("open %s for edge verification: %v", disk.device, err)
	}
	defer file.Close()

	offsets := []int64{
		0,
		int64(disk.mediaSize - diskEdgeWipeSize),
	}
	for _, offset := range offsets {
		edge := make([]byte, diskEdgeWipeSize)
		read, err := file.ReadAt(edge, offset)
		if err != nil {
			t.Fatalf("read disk edge at offset %d: %v", offset, err)
		}
		if read != len(edge) {
			t.Fatalf("read disk edge at offset %d: read %d of %d bytes", offset, read, len(edge))
		}

		for index, value := range edge {
			if value != 0 {
				t.Fatalf(
					"disk edge at offset %d is nonzero at byte %d (0x%02x)",
					offset,
					index,
					value,
				)
			}
		}
	}
}

func TestDestroyPartitionTableMDIntegration(t *testing.T) {
	requireMDIntegrationHost(t)

	t.Run("recognized GPT", func(t *testing.T) {
		disk := createMDIntegrationDisk(t, 512)
		createMDIntegrationGPT(t, disk, "freebsd-zfs")
		assertMDIntegrationGpartTable(t, disk, true)

		service := disk.service()
		if !service.IsDiskGPT(disk.device, disk.sectorSize) {
			t.Fatal("raw GPT detector did not see the recognized GPT table")
		}
		if err := service.DestroyPartitionTable(disk.device); err != nil {
			t.Fatalf("wipe recognized GPT table: %v", err)
		}

		assertMDIntegrationGpartTable(t, disk, false)
		if service.IsDiskGPT(disk.device, disk.sectorSize) {
			t.Fatal("raw GPT detector still sees the wiped GPT table")
		}
		assertMDIntegrationEdgesZero(t, disk)
	})

	for _, sectorSize := range []int{512, 4096} {
		t.Run(fmt.Sprintf("stale signatures %d-byte sectors", sectorSize), func(t *testing.T) {
			disk := createMDIntegrationDisk(t, sectorSize)
			writeMDIntegrationStaleGPTSignatures(t, disk)
			assertMDIntegrationGpartTable(t, disk, false)

			service := disk.service()
			if !service.IsDiskGPT(disk.device, disk.sectorSize) {
				t.Fatal("raw GPT detector did not see the stale primary signature")
			}
			if err := service.DestroyPartitionTable(disk.device); err != nil {
				t.Fatalf("wipe stale GPT signatures: %v", err)
			}

			assertMDIntegrationGpartTable(t, disk, false)
			if service.IsDiskGPT(disk.device, disk.sectorSize) {
				t.Fatal("raw GPT detector still sees the stale primary signature")
			}
			assertMDIntegrationEdgesZero(t, disk)
		})
	}

	t.Run("mounted partition remains busy", func(t *testing.T) {
		disk := createMDIntegrationDisk(t, 512)
		partition := createMDIntegrationGPT(t, disk, "freebsd-ufs")
		runMDIntegrationCommand(t, "/sbin/newfs", "-U", partition)

		mountPoint := t.TempDir()
		runMDIntegrationCommand(t, "/sbin/mount", partition, mountPoint)
		mounted := true
		t.Cleanup(func() {
			if !mounted {
				return
			}
			output, err := exec.Command("/sbin/umount", mountPoint).CombinedOutput()
			if err != nil {
				t.Errorf(
					"unmount %s: %v: %s",
					mountPoint,
					err,
					strings.TrimSpace(string(output)),
				)
			}
		})

		service := disk.service()
		err := service.DestroyPartitionTable(disk.device)
		if err == nil || !strings.Contains(err.Error(), "Device busy") {
			t.Fatalf("wipe mounted GPT error = %v; want Device busy", err)
		}

		assertMDIntegrationGpartTable(t, disk, true)
		if !service.IsDiskGPT(disk.device, disk.sectorSize) {
			t.Fatal("busy wipe cleared the primary GPT signature")
		}

		runMDIntegrationCommand(t, "/sbin/umount", mountPoint)
		mounted = false

		if err := service.DestroyPartitionTable(disk.device); err != nil {
			t.Fatalf("wipe GPT after unmount: %v", err)
		}
		assertMDIntegrationGpartTable(t, disk, false)
		if service.IsDiskGPT(disk.device, disk.sectorSize) {
			t.Fatal("raw GPT detector still sees GPT after unmounted wipe")
		}
		assertMDIntegrationEdgesZero(t, disk)
	})
}
