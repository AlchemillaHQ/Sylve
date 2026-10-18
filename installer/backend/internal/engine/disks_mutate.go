// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
)

var (
	ErrDiskNotFound = errors.New("disk not found")
	ErrBootDisk     = errors.New("the installer media cannot be modified")
	ErrNoScheme     = errors.New("the disk has no partition table — initialize GPT first")
)

var (
	validLabelRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	partitionRe  = regexp.MustCompile(`^([a-z]+[0-9]+)p([0-9]+)$`)
)

var allowedPartitionTypes = map[string]bool{
	"efi":          true,
	"freebsd-boot": true,
	"freebsd-swap": true,
	"freebsd-ufs":  true,
	"freebsd-zfs":  true,
	"freebsd":      true,
}

func targetDisk(name string) (Disk, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/dev/")
	if name == "" {
		return Disk{}, ErrDiskNotFound
	}
	disk, ok := DiskByName(name)
	if !ok {
		return Disk{}, ErrDiskNotFound
	}
	if disk.Boot {
		return Disk{}, ErrBootDisk
	}
	return disk, nil
}

func gpart(args ...string) error {
	out, err := exec.Command("/sbin/gpart", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		switch {
		case strings.Contains(msg, "No space left on device"):
			msg = "not enough free space — leave the size empty to use the remaining space"
		case strings.Contains(msg, "already exists"):
			msg = "the disk already has a partition table — wipe it first"
		case strings.Contains(msg, "index") && strings.Contains(msg, "No such file"):
			msg = "that partition no longer exists — refresh and try again"
		case strings.Contains(msg, "No such geom") || strings.Contains(msg, "No such file"):
			msg = "no partition table found — initialize GPT first"
		case strings.Contains(msg, "Device busy"):
			msg = "the disk is busy — close anything using it and try again"
		case strings.Contains(msg, "invalid argument"):
			msg = "gpart rejected the arguments — check the size and label"
		case msg == "":
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func WipeDisk(name string) error {
	disk, err := targetDisk(name)
	if err != nil {
		return err
	}
	dev := "/dev/" + disk.Name

	_ = exec.Command("/sbin/gpart", "destroy", "-F", dev).Run()
	_ = gzfs.NewClient(gzfs.Options{ZpoolBin: zpoolBin}).Zpool.LabelClear(context.Background(), dev, true)

	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dev, err)
	}
	defer f.Close()

	zeros := make([]byte, 1<<20)
	if _, err := f.WriteAt(zeros, 0); err != nil {
		return fmt.Errorf("wipe %s (start): %w", dev, err)
	}
	if disk.SizeBytes > 2*(1<<20) {
		if _, err := f.WriteAt(zeros, int64(disk.SizeBytes)-(1<<20)); err != nil {
			return fmt.Errorf("wipe %s (end): %w", dev, err)
		}
	}
	return f.Sync()
}

func InitGPT(name string) error {
	disk, err := targetDisk(name)
	if err != nil {
		return err
	}
	if disk.Scheme != "" {
		return fmt.Errorf("%s already has a %s partition table", disk.Name, disk.Scheme)
	}
	return gpart("create", "-s", "gpt", "/dev/"+disk.Name)
}

func AddPartition(name string, sizeBytes uint64, ptype, label string) error {
	disk, err := targetDisk(name)
	if err != nil {
		return err
	}
	if disk.Scheme == "" {
		return ErrNoScheme
	}
	if !allowedPartitionTypes[ptype] {
		return fmt.Errorf("unsupported partition type %q", ptype)
	}
	if label != "" && !validLabelRe.MatchString(label) {
		return fmt.Errorf("invalid label %q — use up to 32 letters, digits, dots, dashes or underscores", label)
	}

	args := []string{"add", "-t", ptype}
	if sizeBytes > 0 {
		args = append(args, "-s", strconv.FormatUint(sizeBytes, 10)+"B")
	}
	if label != "" {
		args = append(args, "-l", label)
	}
	args = append(args, "/dev/"+disk.Name)
	return gpart(args...)
}

func DeletePartition(partition string) error {
	m := partitionRe.FindStringSubmatch(strings.TrimPrefix(strings.TrimSpace(partition), "/dev/"))
	if m == nil {
		return fmt.Errorf("invalid partition %q", partition)
	}
	diskName, index := m[1], m[2]
	if _, err := targetDisk(diskName); err != nil {
		return err
	}
	return gpart("delete", "-i", index, "/dev/"+diskName)
}
