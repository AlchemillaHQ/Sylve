// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package main

import (
	"testing"
	"time"
)

func TestWaitForQueueShutdownReturnsWhenQueueStops(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if !waitForQueueShutdown(done, time.Second) {
		t.Fatal("expected completed queue shutdown")
	}
}

func TestWaitForQueueShutdownTimesOut(t *testing.T) {
	done := make(chan struct{})
	started := time.Now()
	if waitForQueueShutdown(done, 10*time.Millisecond) {
		t.Fatal("expected queue shutdown timeout")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("timeout was not bounded: %s", elapsed)
	}
}
