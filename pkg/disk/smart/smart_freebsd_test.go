// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package smart

import "testing"

func TestBytesToUint64(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		bigEndian bool
		want      uint64
	}{
		{
			name:      "little endian short payload",
			input:     []byte{0x34, 0x12},
			bigEndian: false,
			want:      0x1234,
		},
		{
			name:      "big endian short payload",
			input:     []byte{0x12, 0x34},
			bigEndian: true,
			want:      0x1234,
		},
		{
			name:      "little endian ATA payload uses bytes 5..10",
			input:     []byte{0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 0},
			bigEndian: false,
			want:      0x060504030201,
		},
		{
			name:      "big endian ATA payload uses bytes 5..10",
			input:     []byte{0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 0},
			bigEndian: true,
			want:      0x010203040506,
		},
		{
			name:      "empty payload",
			input:     []byte{},
			bigEndian: false,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesToUint64(tt.input, tt.bigEndian)
			if got != tt.want {
				t.Fatalf("unexpected value: got 0x%x, want 0x%x", got, tt.want)
			}
		})
	}
}