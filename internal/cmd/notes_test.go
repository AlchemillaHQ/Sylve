// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"context"
	"strings"
	"testing"
)

func TestNotesCommandsRejectNonPositiveIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"get zero", []string{"get", "--id", "0"}},
		{"get negative", []string{"get", "--id=-1"}},
		{"delete zero", []string{"delete", "--id", "0"}},
		{"delete negative", []string{"delete", "--id=-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newNotesCommand()
			err := command.Run(context.Background(), append([]string{"notes"}, tc.args...))
			if err == nil || !strings.Contains(err.Error(), "--id must be greater than zero") {
				t.Fatalf("command error = %v, want invalid ID error", err)
			}
		})
	}
}
