package qemuimg

import "testing"

func TestNormalizeFormat(t *testing.T) {
	got := normalizeFormat("  QCOW2  ")
	if got != "qcow2" {
		t.Fatalf("unexpected normalized format: %q", got)
	}
}

func TestDiskFormatHelpers(t *testing.T) {
	if !FormatQCOW2.Valid() {
		t.Fatal("expected qcow2 to be valid")
	}
	if DiskFormat("nope").Valid() {
		t.Fatal("expected unsupported format to be invalid")
	}
	if !FormatQCOW.IsQCOW() || !FormatQCOW2.IsQCOW() {
		t.Fatal("expected qcow and qcow2 to be qcow formats")
	}
	if FormatRaw.IsQCOW() {
		t.Fatal("expected raw to not be a qcow format")
	}
	if !FormatQCOW.SupportsSnapshots() || !FormatQCOW2.SupportsSnapshots() {
		t.Fatal("expected qcow formats to support snapshots")
	}
	if FormatRaw.SupportsSnapshots() {
		t.Fatal("expected raw to not support snapshots")
	}

	formats := FormatsList()
	if len(formats) != len(validFormats) {
		t.Fatalf("unexpected format count: got %d want %d", len(formats), len(validFormats))
	}
}
