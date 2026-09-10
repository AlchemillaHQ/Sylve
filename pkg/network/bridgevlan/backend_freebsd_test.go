// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package bridgevlan

import (
	"strings"
	"testing"
)

func TestValidateNativeInterfaceName(t *testing.T) {
	nameSize := nativeInterfaceNameSize()
	if nameSize < 2 {
		t.Fatalf("IFNAMSIZ = %d", nameSize)
	}

	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "ordinary", value: "bridge0"},
		{name: "maximum length", value: strings.Repeat("a", nameSize-1)},
		{name: "empty", wantError: "empty"},
		{name: "embedded NUL", value: "bridge0\x00ignored", wantError: "contains NUL"},
		{
			name:      "too long",
			value:     strings.Repeat("a", nameSize),
			wantError: "exceeds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNativeInterfaceName("bridge", test.value)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validate %q: %v", test.value, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validate %q error = %v, want %q", test.value, err, test.wantError)
			}
		})
	}
}
