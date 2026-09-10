// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package bridgevlan

import (
	"testing"
	"time"
)

func TestStandardSwitchLifecycleGuardSerializesCallers(t *testing.T) {
	releaseFirst := LockStandardSwitchLifecycle("bridge0")

	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		releaseSecond := LockStandardSwitchLifecycle("bridge0")
		close(acquired)
		releaseSecond()
		close(done)
	}()

	select {
	case <-acquired:
		releaseFirst()
		t.Fatal("second caller acquired the lifecycle guard before the first released it")
	case <-time.After(25 * time.Millisecond):
	}

	releaseFirst()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second caller did not acquire the lifecycle guard after release")
	}
}

func TestStandardSwitchLifecycleGuardDoesNotSerializeUnrelatedBridges(t *testing.T) {
	releaseFirst := LockStandardSwitchLifecycle("bridge1")
	defer releaseFirst()

	acquired := make(chan struct{})
	go func() {
		releaseSecond := LockStandardSwitchLifecycle("bridge2")
		releaseSecond()
		close(acquired)
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("unrelated bridge was serialized behind bridge1")
	}
}
