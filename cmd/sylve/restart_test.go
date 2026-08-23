// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

func TestRequestSelfRestartIsNonBlocking(t *testing.T) {
	requests := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		requestSelfRestart(requests)
		requestSelfRestart(requests)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("duplicate restart request blocked")
	}

	if got := len(requests); got != 1 {
		t.Fatalf("queued restart requests = %d, want 1", got)
	}
}

func TestRestartSentinelSurvivesCommandRun(t *testing.T) {
	command := &cli.Command{
		Name: "sylve-test",
		Action: func(context.Context, *cli.Command) error {
			return errSelfRestartRequested
		},
	}

	err := command.Run(context.Background(), []string{"sylve-test"})
	if !errors.Is(err, errSelfRestartRequested) {
		t.Fatalf("command error = %v, want restart sentinel", err)
	}
}

func TestReexecProcessPreservesProcessState(t *testing.T) {
	wantArgs := []string{"/usr/local/sbin/sylve", "-config", "/usr/local/etc/sylve/config.json"}
	wantEnvironment := []string{"PATH=/usr/local/bin:/usr/local/sbin", "TEST=value"}

	var gotExecutable string
	var gotArgs []string
	var gotEnvironment []string
	err := reexecProcess(
		func() (string, error) { return "/usr/local/sbin/sylve", nil },
		func(executable string, args, environment []string) error {
			gotExecutable = executable
			gotArgs = append([]string(nil), args...)
			gotEnvironment = append([]string(nil), environment...)
			return nil
		},
		wantArgs,
		wantEnvironment,
	)
	if err != nil {
		t.Fatalf("reexecProcess returned error: %v", err)
	}
	if gotExecutable != "/usr/local/sbin/sylve" {
		t.Fatalf("executable = %q, want %q", gotExecutable, "/usr/local/sbin/sylve")
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
	if !reflect.DeepEqual(gotEnvironment, wantEnvironment) {
		t.Fatalf("environment = %#v, want %#v", gotEnvironment, wantEnvironment)
	}
}

func TestReexecProcessReportsFailures(t *testing.T) {
	resolveErr := errors.New("resolve failed")
	err := reexecProcess(
		func() (string, error) { return "", resolveErr },
		func(string, []string, []string) error {
			t.Fatal("exec should not be called when executable resolution fails")
			return nil
		},
		nil,
		nil,
	)
	if !errors.Is(err, resolveErr) {
		t.Fatalf("resolution error = %v, want wrapped %v", err, resolveErr)
	}

	execErr := errors.New("exec failed")
	err = reexecProcess(
		func() (string, error) { return "/usr/local/sbin/sylve", nil },
		func(string, []string, []string) error { return execErr },
		nil,
		nil,
	)
	if !errors.Is(err, execErr) {
		t.Fatalf("exec error = %v, want wrapped %v", err, execErr)
	}
}
