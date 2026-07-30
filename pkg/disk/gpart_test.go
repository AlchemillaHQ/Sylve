// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDestroyDiskClassifiesGpartResult(t *testing.T) {
	commandErr := errors.New("exit status 1")

	tests := []struct {
		name              string
		output            string
		commandErr        error
		wantNil           bool
		wantNoTable       bool
		wantCommandCause  bool
		wantErrorContains string
	}{
		{
			name:       "success",
			output:     "nda0 destroyed\n",
			commandErr: nil,
			wantNil:    true,
		},
		{
			name:              "no such geom",
			output:            "gpart: No such geom: nda0.\n",
			commandErr:        commandErr,
			wantNoTable:       true,
			wantErrorContains: "No such geom",
		},
		{
			name:              "invalid argument for missing PART geom",
			output:            "gpart: arg0 'nda0': Invalid argument\n",
			commandErr:        commandErr,
			wantNoTable:       true,
			wantErrorContains: "Invalid argument",
		},
		{
			name:              "device busy",
			output:            "gpart: Device busy\n",
			commandErr:        commandErr,
			wantCommandCause:  true,
			wantErrorContains: "Device busy",
		},
		{
			name:              "unexpected failure",
			output:            "gpart: Operation not permitted\n",
			commandErr:        commandErr,
			wantCommandCause:  true,
			wantErrorContains: "Operation not permitted",
		},
		{
			name:              "unrecognized invalid argument",
			output:            "gpart: permission check: Invalid argument\n",
			commandErr:        commandErr,
			wantCommandCause:  true,
			wantErrorContains: "Invalid argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCommand string
			var gotArgs []string

			err := destroyDisk(
				"/dev/nda0",
				func(device string) error {
					if device != "/dev/nda0" {
						t.Fatalf("unexpected device passed to validation: %q", device)
					}
					return nil
				},
				func(command string, args ...string) (string, error) {
					gotCommand = command
					gotArgs = append([]string(nil), args...)
					return tt.output, tt.commandErr
				},
			)

			if gotCommand != "/sbin/gpart" {
				t.Fatalf("command = %q; want /sbin/gpart", gotCommand)
			}
			if want := []string{"destroy", "-F", "/dev/nda0"}; !reflect.DeepEqual(gotArgs, want) {
				t.Fatalf("args = %#v; want %#v", gotArgs, want)
			}

			if tt.wantNil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrNoPartitionTable); got != tt.wantNoTable {
				t.Fatalf("errors.Is(err, ErrNoPartitionTable) = %t; want %t; err = %v", got, tt.wantNoTable, err)
			}
			if got := errors.Is(err, commandErr); got != tt.wantCommandCause {
				t.Fatalf("errors.Is(err, commandErr) = %t; want %t; err = %v", got, tt.wantCommandCause, err)
			}
			if !strings.Contains(err.Error(), tt.wantErrorContains) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErrorContains)
			}
		})
	}
}

func TestDestroyDiskStopsAfterValidationFailure(t *testing.T) {
	validationErr := errors.New("not a disk device")
	commandCalled := false

	err := destroyDisk(
		"/tmp/not-a-disk",
		func(string) error {
			return validationErr
		},
		func(string, ...string) (string, error) {
			commandCalled = true
			return "", nil
		},
	)

	if !errors.Is(err, validationErr) {
		t.Fatalf("error = %v; want validation error", err)
	}
	if commandCalled {
		t.Fatal("gpart command ran after validation failed")
	}
}
