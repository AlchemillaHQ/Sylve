// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package main

import (
	"errors"
	"testing"
)

func TestShouldStartLocalSylveConsoleDisabled(t *testing.T) {
	called := false

	startLocal, err := shouldStartLocalSylve(false, func() (bool, error) {
		called = true
		return false, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !startLocal {
		t.Fatalf("expected local sylve startup when console is disabled")
	}
	if called {
		t.Fatalf("expected attach function not to be called when console is disabled")
	}
}

func TestShouldStartLocalSylveAttachSuccess(t *testing.T) {
	startLocal, err := shouldStartLocalSylve(true, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if startLocal {
		t.Fatalf("expected local startup to be skipped when attach succeeds")
	}
}

func TestShouldStartLocalSylveAttachUnavailable(t *testing.T) {
	startLocal, err := shouldStartLocalSylve(true, func() (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !startLocal {
		t.Fatalf("expected local startup when attach is unavailable")
	}
}

func TestShouldStartLocalSylveAttachError(t *testing.T) {
	attachErr := errors.New("boom")

	startLocal, err := shouldStartLocalSylve(true, func() (bool, error) {
		return false, attachErr
	})
	if !errors.Is(err, attachErr) {
		t.Fatalf("expected attach error %v, got %v", attachErr, err)
	}
	if startLocal {
		t.Fatalf("expected local startup to stop on attach error")
	}
}
