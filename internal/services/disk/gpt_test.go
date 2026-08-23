// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDiskGPTUsesLogicalSectorSize(t *testing.T) {
	const sectorSize = 4096

	diskImage := make([]byte, 2*sectorSize)
	copy(diskImage[sectorSize:], []byte("EFI PART"))

	path := filepath.Join(t.TempDir(), "4kn-disk.img")
	if err := os.WriteFile(path, diskImage, 0600); err != nil {
		t.Fatal(err)
	}

	service := &Service{}
	if !service.IsDiskGPT(path, sectorSize) {
		t.Fatal("GPT signature at 4Kn LBA 1 was not detected")
	}
	if service.IsDiskGPT(path, 512) {
		t.Fatal("GPT was detected at the legacy 512-byte offset")
	}
}

func TestIsDiskGPTRejectsInvalidSectorSize(t *testing.T) {
	service := &Service{}
	if service.IsDiskGPT("unused", 0) {
		t.Fatal("invalid sector size was reported as GPT")
	}
}
