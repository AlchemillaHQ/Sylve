// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import "testing"

func buildMagicPacket(mac [6]byte) []byte {
	packet := make([]byte, 0, 102)
	packet = append(packet, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}...)
	for i := 0; i < 16; i++ {
		packet = append(packet, mac[:]...)
	}
	return packet
}

func TestIsWOLPacket(t *testing.T) {
	mac := [6]byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	valid := buildMagicPacket(mac)

	tests := []struct {
		name    string
		payload []byte
		want    bool
	}{
		{
			name:    "valid magic packet exact size",
			payload: append([]byte(nil), valid...),
			want:    true,
		},
		{
			name:    "valid magic packet with trailing bytes",
			payload: append(append([]byte(nil), valid...), []byte{0x01, 0x02}...),
			want:    true,
		},
		{
			name:    "too short packet",
			payload: valid[:101],
			want:    false,
		},
		{
			name: "invalid sync stream",
			payload: func() []byte {
				p := append([]byte(nil), valid...)
				p[0] = 0x00
				return p
			}(),
			want: false,
		},
		{
			name: "mismatched repeated mac",
			payload: func() []byte {
				p := append([]byte(nil), valid...)
				p[6+5*6] ^= 0xff
				return p
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsWOLPacket(tt.payload)
			if got != tt.want {
				t.Fatalf("IsWOLPacket() = %v, want %v", got, tt.want)
			}
		})
	}
}
