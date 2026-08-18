// SPDX-License-Identifier: BSD-2-Clause

package zfs

import (
	"errors"
	"testing"
)

func TestNormalizePoolMountpoint(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		properties map[string]string
		want       string
		wantErr    bool
	}{
		{name: "omitted"},
		{name: "blank", raw: "  \t "},
		{name: "absolute", raw: " /mnt/tank ", want: "/mnt/tank"},
		{name: "cleaned", raw: "/mnt/./storage/../tank/", want: "/mnt/tank"},
		{name: "relative", raw: "mnt/tank", wantErr: true},
		{name: "root", raw: "/", wantErr: true},
		{name: "cleaned root", raw: "/mnt/..", wantErr: true},
		{name: "none", raw: "none", wantErr: true},
		{name: "legacy", raw: "LEGACY", wantErr: true},
		{name: "control character", raw: "/mnt/ta\x00nk", wantErr: true},
		{name: "trailing control character", raw: "/mnt/tank\n", wantErr: true},
		{name: "whitespace", raw: "/mnt/tank data", wantErr: true},
		{name: "quote", raw: `/mnt/tank"data`, wantErr: true},
		{name: "backslash", raw: `/mnt/tank\data`, wantErr: true},
		{name: "dollar", raw: "/mnt/$tank", wantErr: true},
		{name: "unicode", raw: "/mnt/tänk", wantErr: true},
		{
			name:       "altroot with mountpoint",
			raw:        "/mnt/tank",
			properties: map[string]string{" altroot ": "/tmp/import"},
			wantErr:    true,
		},
		{
			name:       "altroot without mountpoint",
			properties: map[string]string{"altroot": "/tmp/import"},
			wantErr:    true,
		},
		{
			name:       "default altroot marker",
			raw:        "/mnt/tank",
			properties: map[string]string{"ALTROOT": "-"},
			want:       "/mnt/tank",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizePoolMountpoint(test.raw, test.properties)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v, want ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePoolMountpoint returned an error: %v", err)
			}
			if got != test.want {
				t.Fatalf("mountpoint = %q, want %q", got, test.want)
			}
		})
	}
}
