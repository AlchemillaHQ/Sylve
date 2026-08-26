// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"errors"
	"testing"
)

func TestParseZpoolIOStatLine(t *testing.T) {
	name, stat, err := parseZpoolIOStatLine("tank\t1000\t2000\t12\t34\t5678\t9012\t11000\t22000\t9000\t18000\t0\t0\t0\t0\t0\t0\t0")
	if err != nil {
		t.Fatalf("parse zpool iostat line: %v", err)
	}
	if name != "tank" {
		t.Fatalf("pool name: got %q, want tank", name)
	}
	if stat.ReadIOPS != 12 || stat.WriteIOPS != 34 {
		t.Fatalf("operations mismatch: read=%d write=%d", stat.ReadIOPS, stat.WriteIOPS)
	}
	if stat.ReadBytesPerSecond != 5678 || stat.WriteBytesPerSecond != 9012 {
		t.Fatalf("bandwidth mismatch: read=%d write=%d", stat.ReadBytesPerSecond, stat.WriteBytesPerSecond)
	}
	if stat.ReadLatencyNanos != 11000 || stat.WriteLatencyNanos != 22000 {
		t.Fatalf("latency mismatch: read=%d write=%d", stat.ReadLatencyNanos, stat.WriteLatencyNanos)
	}
	if !stat.ReadLatencyAvailable || !stat.WriteLatencyAvailable {
		t.Fatal("expected latency capability to be detected")
	}
}

func TestParseZpoolIOStatLineWithoutLatency(t *testing.T) {
	_, stat, err := parseZpoolIOStatLine("tank 1000 2000 1 2 3 4")
	if err != nil {
		t.Fatalf("parse zpool iostat line: %v", err)
	}
	if stat.ReadLatencyNanos != 0 || stat.WriteLatencyNanos != 0 {
		t.Fatalf("expected unavailable latency to be zero, got read=%d write=%d", stat.ReadLatencyNanos, stat.WriteLatencyNanos)
	}
	if stat.ReadLatencyAvailable || stat.WriteLatencyAvailable {
		t.Fatal("latency unexpectedly marked available")
	}
}

func TestParseZpoolIOStatLineWithOneSidedLatency(t *testing.T) {
	_, stat, err := parseZpoolIOStatLine("tank 1000 2000 0 2 0 4096 - 134266880")
	if err != nil {
		t.Fatalf("parse one-sided latency sample: %v", err)
	}
	if stat.ReadLatencyAvailable {
		t.Fatal("read latency unexpectedly marked available")
	}
	if !stat.WriteLatencyAvailable || stat.WriteLatencyNanos != 134266880 {
		t.Fatalf("write latency mismatch: available=%v value=%d", stat.WriteLatencyAvailable, stat.WriteLatencyNanos)
	}
}

func TestParseZpoolIOStatLineRejectsMalformedInput(t *testing.T) {
	if _, _, err := parseZpoolIOStatLine("tank 1 2 nope 4 5 6"); err == nil {
		t.Fatal("expected malformed operations value to fail")
	}
	if _, _, err := parseZpoolIOStatLine("tank 1 2"); err == nil {
		t.Fatal("expected short input to fail")
	}
}

func TestZpoolIOStatLatencyFallbackOnlyForUnsupportedOption(t *testing.T) {
	if !isZpoolIOStatLatencyUnsupported(errors.New("zpool iostat: invalid option 'l'")) {
		t.Fatal("expected invalid latency option to trigger fallback")
	}
	if isZpoolIOStatLatencyUnsupported(errors.New("zpool iostat: process exited unexpectedly")) {
		t.Fatal("transient monitor failure unexpectedly disabled latency collection")
	}
}
