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

func TestParseSwapctlOutput(t *testing.T) {
	const header = "Device          1K-blocks     Used    Avail Capacity\n"
	ada0 := SwapDevice{Device: "/dev/ada0p3", Blocks1024: 2097152, Used: 524288}
	ada1 := SwapDevice{Device: "/dev/ada1p2", Blocks1024: 1048576, Used: 262144}
	tests := []struct {
		name    string
		input   string
		want    []SwapDevice
		wantErr bool
	}{
		{name: "single device", input: header + "/dev/ada0p3        2097152   524288  1572864    25%", want: []SwapDevice{ada0}},
		{
			name:  "multiple devices",
			input: header + "/dev/ada0p3        2097152   524288  1572864    25%\n/dev/ada1p2        1048576   262144   786432    25%",
			want:  []SwapDevice{ada0, ada1},
		},
		{name: "empty output", input: ""},
		{name: "header only", input: header},
		{
			name:  "skips blank lines",
			input: header + "\n/dev/ada0p3        2097152   524288  1572864    25%\n\n/dev/ada1p2        1048576   262144   786432    25%\n",
			want:  []SwapDevice{ada0, ada1},
		},
		{name: "skips short lines", input: header + "garbage\n/dev/ada0p3        2097152   524288  1572864    25%", want: []SwapDevice{ada0}},
		{name: "invalid blocks", input: header + "/dev/ada0p3        notanumber   524288  1572864    25%", wantErr: true},
		{name: "invalid used", input: header + "/dev/ada0p3        2097152   notanumber  1572864    25%", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSwapctlOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) || (len(tt.want) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
