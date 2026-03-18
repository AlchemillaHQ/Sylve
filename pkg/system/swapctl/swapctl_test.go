// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package swapctl

import (
	"reflect"
	"testing"
)

func TestParseSwapctlOutput_ValidSingleDevice(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity
/dev/ada0p3        2097152   524288  1572864    25%`

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []SwapDevice{
		{
			Device:     "/dev/ada0p3",
			Blocks1024: 2097152,
			Used:       524288,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseSwapctlOutput_ValidMultipleDevices(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity
/dev/ada0p3        2097152   524288  1572864    25%
/dev/ada1p2        1048576   262144   786432    25%`

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []SwapDevice{
		{
			Device:     "/dev/ada0p3",
			Blocks1024: 2097152,
			Used:       524288,
		},
		{
			Device:     "/dev/ada1p2",
			Blocks1024: 1048576,
			Used:       262144,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseSwapctlOutput_EmptyOutput(t *testing.T) {
	input := ``

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %#v", got)
	}
}

func TestParseSwapctlOutput_HeaderOnly(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity`

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %#v", got)
	}
}

func TestParseSwapctlOutput_SkipsBlankLines(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity

/dev/ada0p3        2097152   524288  1572864    25%

/dev/ada1p2        1048576   262144   786432    25%
`

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 devices, got %#v", got)
	}
}

func TestParseSwapctlOutput_SkipsShortLines(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity
garbage
/dev/ada0p3        2097152   524288  1572864    25%`

	got, err := parseSwapctlOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []SwapDevice{
		{
			Device:     "/dev/ada0p3",
			Blocks1024: 2097152,
			Used:       524288,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseSwapctlOutput_InvalidBlocks(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity
/dev/ada0p3        notanumber   524288  1572864    25%`

	_, err := parseSwapctlOutput(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseSwapctlOutput_InvalidUsed(t *testing.T) {
	input := `Device          1K-blocks     Used    Avail Capacity
/dev/ada0p3        2097152   notanumber  1572864    25%`

	_, err := parseSwapctlOutput(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
