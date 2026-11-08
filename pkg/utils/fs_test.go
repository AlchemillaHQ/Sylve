// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFlashImageToDiskCtxKeepsADiskFilesSize(t *testing.T) {
	dir := t.TempDir()
	image := bytes.Repeat([]byte("disk image data\n"), 1<<12)
	source := filepath.Join(dir, "image.img")
	if err := os.WriteFile(source, image, 0o644); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(disk, bytes.Repeat([]byte{0xff}, 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FlashImageToDiskCtx(t.Context(), source, disk); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4<<20 || !bytes.Equal(written[:len(image)], image) || written[len(image)] != 0xff {
		t.Fatalf("disk is %d bytes; image written: %v", len(written), bytes.Equal(written[:len(image)], image))
	}
}

func TestWriteFileIfChangedLeavesIdenticalFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := WriteFileIfChanged(path, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileIfChanged(path, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("identical content must not rewrite the file")
	}

	if err := WriteFileIfChanged(path, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "two" {
		t.Fatalf("changed content not written: %q", data)
	}
}
