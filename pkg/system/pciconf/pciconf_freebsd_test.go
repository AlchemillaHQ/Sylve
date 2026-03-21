// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package pciconf

import (
	"os"
	"sync"
	"testing"
)

func resetPCIDBState() {
	pciVendors = map[uint16]string{}
	pciDevices = map[uint32]string{}
	parseOnce = sync.Once{}
	parseErr = nil
}

func TestParsePCIDatabaseParsesVendorsAndDevices(t *testing.T) {
	resetPCIDBState()

	f, err := os.CreateTemp(t.TempDir(), "pci_vendors")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer f.Close()

	content := "# sample database\n" +
		"8086 Intel Corporation\n" +
		"\t100e 82540EM Gigabit Ethernet Controller\n" +
		"10de NVIDIA Corporation\n" +
		"\t1eb8 TU104 [GeForce RTX]\n"

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write sample database: %v", err)
	}

	if err := parsePCIDatabase(f.Name()); err != nil {
		t.Fatalf("parsePCIDatabase failed: %v", err)
	}

	if got := pciVendors[0x8086]; got != "Intel Corporation" {
		t.Fatalf("vendor 0x8086: got %q, want %q", got, "Intel Corporation")
	}

	if got := pciDevices[0x8086<<16|0x100e]; got != "82540EM Gigabit Ethernet Controller" {
		t.Fatalf("device 0x8086:0x100e: got %q, want %q", got, "82540EM Gigabit Ethernet Controller")
	}

	if got := pciVendors[0x10de]; got != "NVIDIA Corporation" {
		t.Fatalf("vendor 0x10de: got %q, want %q", got, "NVIDIA Corporation")
	}

	if got := pciDevices[0x10de<<16|0x1eb8]; got != "TU104 [GeForce RTX]" {
		t.Fatalf("device 0x10de:0x1eb8: got %q, want %q", got, "TU104 [GeForce RTX]")
	}
}

func TestParsePCIDatabaseSkipsMalformedLines(t *testing.T) {
	resetPCIDBState()

	f, err := os.CreateTemp(t.TempDir(), "pci_vendors")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer f.Close()

	content := "zzzz not-hex\n" +
		"8086 Intel Corporation\n" +
		"\tbadhex Broken Device\n" +
		"\t1234 Valid Device\n"

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write sample database: %v", err)
	}

	if err := parsePCIDatabase(f.Name()); err != nil {
		t.Fatalf("parsePCIDatabase failed: %v", err)
	}

	if got := pciVendors[0x8086]; got != "Intel Corporation" {
		t.Fatalf("vendor 0x8086: got %q, want %q", got, "Intel Corporation")
	}

	if _, ok := pciDevices[0x8086<<16|0x0bad]; ok {
		t.Fatal("unexpected malformed device entry present")
	}

	if got := pciDevices[0x8086<<16|0x1234]; got != "Valid Device" {
		t.Fatalf("device 0x8086:0x1234: got %q, want %q", got, "Valid Device")
	}
}

func TestGuessClassAndSubclass(t *testing.T) {
	if len(pciNomatchTab) == 0 {
		t.Fatal("pciNomatchTab is empty")
	}

	var classEntry struct {
		class    uint8
		subclass int
		desc     string
	}
	var subclassEntry struct {
		class    uint8
		subclass int
		desc     string
	}

	for _, entry := range pciNomatchTab {
		if classEntry.desc == "" && entry.subclass == -1 {
			classEntry = entry
		}
		if subclassEntry.desc == "" && entry.subclass >= 0 {
			subclassEntry = entry
		}
	}

	if classEntry.desc == "" {
		t.Fatal("no class-level entry found in pciNomatchTab")
	}
	if subclassEntry.desc == "" {
		t.Fatal("no subclass-level entry found in pciNomatchTab")
	}

	if got := guessClass(classEntry.class); got != classEntry.desc {
		t.Fatalf("guessClass(%d): got %q, want %q", classEntry.class, got, classEntry.desc)
	}

	if got := guessSubclass(subclassEntry.class, uint8(subclassEntry.subclass)); got != subclassEntry.desc {
		t.Fatalf("guessSubclass(%d, %d): got %q, want %q", subclassEntry.class, subclassEntry.subclass, got, subclassEntry.desc)
	}

	unknownClass := uint8(255)
	for i := 255; i >= 0; i-- {
		candidate := uint8(i)
		if guessClass(candidate) == "" {
			unknownClass = candidate
			break
		}
	}

	if got := guessClass(unknownClass); got != "" {
		t.Fatalf("guessClass(%d): got %q, want empty", unknownClass, got)
	}

	unknownSubclass := uint8(255)
	for i := 255; i >= 0; i-- {
		candidate := uint8(i)
		if guessSubclass(subclassEntry.class, candidate) == "" {
			unknownSubclass = candidate
			break
		}
	}

	if got := guessSubclass(subclassEntry.class, unknownSubclass); got != "" {
		t.Fatalf("guessSubclass(%d, %d): got %q, want empty", subclassEntry.class, unknownSubclass, got)
	}
}
