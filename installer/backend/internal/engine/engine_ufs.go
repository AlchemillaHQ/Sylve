// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	ufsRootLabel = "rootfs"
	ufsSwapLabel = "swap0"
)

var sizeStringRe = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*([a-z]*)\s*$`)

func wipeDeviceStart(dev string, size uint64) error {
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dev, err)
	}
	defer f.Close()

	n := uint64(1 << 20)
	if size > 0 && size < n {
		n = size
	}
	zeros := make([]byte, n)
	if _, err := f.WriteAt(zeros, 0); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("wipe %s: %w", dev, err)
	}
	if size > n {
		if _, err := f.WriteAt(zeros, int64(size-n)); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("wipe %s tail: %w", dev, err)
		}
	}
	return f.Sync()
}

func (e *Engine) nukePartitionTable(dev string) error {
	_ = e.cmd("/sbin/gpart", "destroy", "-F", dev)
	if err := e.cmd("/sbin/gpart", "create", "-s", "gpt", dev); err != nil {
		return err
	}
	return e.cmd("/sbin/gpart", "destroy", "-F", dev)
}

func (e *Engine) addPartitionWiped(dev string, args ...string) error {
	gpartArgs := append([]string{"add"}, args...)
	gpartArgs = append(gpartArgs, dev)
	if err := e.cmd("/sbin/gpart", gpartArgs...); err != nil {
		return err
	}
	label := ""
	for i, a := range args {
		if a == "-l" && i+1 < len(args) {
			label = args[i+1]
		}
	}
	if label == "" {
		return nil
	}
	disk := strings.TrimPrefix(dev, "/dev/")
	if p, ok := ufsPartitionByLabel(disk, label); ok {
		return wipeDeviceStart(fmt.Sprintf("%sp%d", dev, p.Index), p.SizeBytes)
	}
	return nil
}

func parseSizeString(s string) (uint64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	m := sizeStringRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	var mult uint64
	switch strings.ToLower(m[2]) {
	case "", "b", "byte", "bytes":
		mult = 1
	case "k", "kb", "ki", "kib":
		mult = 1 << 10
	case "m", "mb", "mi", "mib":
		mult = 1 << 20
	case "g", "gb", "gi", "gib":
		mult = 1 << 30
	case "t", "tb", "ti", "tib":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("invalid size %q", s)
	}
	bytes := math.Round(value * float64(mult))
	if math.IsInf(bytes, 0) || bytes >= math.Exp2(63) {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return uint64(bytes), nil
}

func ufsPartitionByLabel(disk, label string) (Partition, bool) {
	info, ok := DiskByName(disk)
	if !ok {
		return Partition{}, false
	}
	for _, p := range info.Partitions {
		if p.Label == label {
			return p, true
		}
	}
	return Partition{}, false
}

func ufsPartitionByType(disk, ptype string) (Partition, bool) {
	info, ok := DiskByName(disk)
	if !ok {
		return Partition{}, false
	}
	for _, p := range info.Partitions {
		if p.Type == ptype {
			return p, true
		}
	}
	return Partition{}, false
}

func (e *Engine) stepPartitionUFS() error {
	disk := e.storage.UFS.Disk
	if err := e.validateCurrentLayout(); err != nil {
		return err
	}
	swapBytes, err := parseSwapSize(e.storage.UFS.Swap)
	if err != nil {
		return err
	}
	dev := "/dev/" + disk

	if e.storage.UFS.Target == "free" {
		return e.partitionUFSFree(disk, dev, swapBytes)
	}

	if err := e.nukePartitionTable(dev); err != nil {
		return err
	}
	if err := e.cmd("/sbin/gpart", "create", "-s", "gpt", dev); err != nil {
		return err
	}

	if err := e.addPartitionWiped(dev, "-t", "freebsd-boot", "-s", fmt.Sprintf("%dB", biosBootBytes), "-l", "boot0"); err != nil {
		return err
	}
	if err := e.addPartitionWiped(dev, "-t", "efi", "-s", fmt.Sprintf("%dB", efiBytes), "-l", "efi0"); err != nil {
		return err
	}
	if err := e.cmd("/sbin/newfs_msdos", "-F", "16", "-L", "EFI", dev+"p2"); err != nil {
		return err
	}

	if swapBytes > 0 {
		if err := e.addPartitionWiped(dev, "-t", "freebsd-swap", "-s", fmt.Sprintf("%dB", swapBytes), "-l", ufsSwapLabel); err != nil {
			return err
		}
	}
	return e.addPartitionWiped(dev, "-t", "freebsd-ufs", "-l", ufsRootLabel)
}

func (e *Engine) partitionUFSFree(disk, dev string, swapBytes uint64) error {
	info, ok := DiskByName(disk)
	if !ok {
		return fmt.Errorf("disk %s not found", disk)
	}
	parts, _, err := ufsFreeLayout(info, swapBytes, e.storage.UFS.Encrypt)
	if err != nil {
		return err
	}
	for _, part := range parts {
		if err := e.addPartitionWiped(dev, "-t", part.Type, "-b", fmt.Sprintf("%dB", part.Start),
			"-s", fmt.Sprintf("%dB", part.Size), "-l", part.Label); err != nil {
			return err
		}
		if part.Type == "efi" {
			p, found := ufsPartitionByLabel(disk, part.Label)
			if !found {
				return fmt.Errorf("cannot locate the newly created EFI partition on %s", disk)
			}
			if err := e.cmd("/sbin/newfs_msdos", "-F", "16", "-L", "EFI", fmt.Sprintf("%sp%d", dev, p.Index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) ufsRootDevice() (string, error) {
	p, ok := ufsPartitionByLabel(e.storage.UFS.Disk, ufsRootLabel)
	if !ok {
		return "", fmt.Errorf("root filesystem partition not found on %s", e.storage.UFS.Disk)
	}
	dev := fmt.Sprintf("/dev/%sp%d", e.storage.UFS.Disk, p.Index)
	if e.storage.UFS.Encrypt {
		dev += ".eli"
	}
	return dev, nil
}

func (e *Engine) stepEncryptUFS() error {
	p, ok := ufsPartitionByLabel(e.storage.UFS.Disk, ufsRootLabel)
	if !ok {
		return fmt.Errorf("root filesystem partition not found on %s", e.storage.UFS.Disk)
	}
	if err := os.WriteFile(passFile, []byte(e.storage.UFS.Passphrase), 0o600); err != nil {
		return err
	}
	dev := fmt.Sprintf("/dev/%sp%d", e.storage.UFS.Disk, p.Index)
	if err := e.cmd("/sbin/geli", "init", "-g", "-s", "4096", "-e", "AES-XTS", "-l", "256", "-J", passFile, dev); err != nil {
		return err
	}
	return e.cmd("/sbin/geli", "attach", "-j", passFile, dev)
}

func (e *Engine) stepRootfsUFS() error {
	rootDev, err := e.ufsRootDevice()
	if err != nil {
		return err
	}
	if err := e.cmd("/sbin/newfs", "-U", "-L", ufsRootLabel, rootDev); err != nil {
		return err
	}
	if err := os.MkdirAll(mountRoot, 0o755); err != nil {
		return err
	}
	return e.cmd("/sbin/mount", rootDev, mountRoot)
}

func (e *Engine) ufsLoaderConf() string {
	loader := "autoboot_delay=\"3\"\n"
	loader += "vfs.root.mountfrom=\"ufs:/dev/ufs/" + ufsRootLabel + "\"\n"
	if e.storage.UFS.Encrypt {
		loader += "geom_eli_load=\"YES\"\n"
	}
	return loader
}

func (e *Engine) ufsFstab() string {
	fstab := fstabHeader
	fstab += "/dev/ufs/" + ufsRootLabel + "\t/\tufs\trw\t1\t1\n"
	if bytes, err := parseSizeString(e.storage.UFS.Swap); err == nil && bytes > 0 {
		fstab += swapFstabEntry(ufsSwapLabel, e.storage.UFS.Encrypt)
	}
	return fstab
}

func (e *Engine) stepBootUFS() error {
	disk := e.storage.UFS.Disk
	dev := "/dev/" + disk

	bootIdx := 1
	if p, ok := ufsPartitionByType(disk, "freebsd-boot"); ok {
		bootIdx = p.Index
	}
	if err := e.cmd("/sbin/gpart", "bootcode", "-b", mountRoot+"/boot/pmbr",
		"-p", mountRoot+"/boot/gptboot", "-i", fmt.Sprintf("%d", bootIdx), dev); err != nil {
		return err
	}

	efiIdx := 2
	if p, ok := ufsPartitionByType(disk, "efi"); ok {
		efiIdx = p.Index
	}
	mp := "/mnt/.efi"
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return err
	}
	if err := e.cmd("/sbin/mount", "-t", "msdosfs", fmt.Sprintf("%sp%d", dev, efiIdx), mp); err != nil {
		return err
	}
	if err := os.MkdirAll(mp+"/EFI/BOOT", 0o755); err != nil {
		return err
	}
	if err := e.cmd("/bin/cp", mountRoot+"/boot/loader.efi", mp+"/EFI/BOOT/BOOTX64.EFI"); err != nil {
		return err
	}
	return e.cmd("/sbin/umount", mp)
}
