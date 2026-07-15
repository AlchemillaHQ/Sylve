// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"sync"
	"testing"
	"time"
)

type fakeReplicationRuntimeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeReplicationRuntimeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeReplicationRuntimeClock) Sleep(duration time.Duration) {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, duration)
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func (c *fakeReplicationRuntimeClock) sleepDurations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func TestReplicationRuntimeClockInjection(t *testing.T) {
	start := time.Date(2035, time.March, 4, 5, 6, 7, 0, time.UTC)
	clock := &fakeReplicationRuntimeClock{now: start}
	service := &Service{}
	service.setReplicationRuntimeClock(clock)

	if got := service.now(); !got.Equal(start) {
		t.Fatalf("now() = %s, want %s", got, start)
	}

	service.sleep(3 * time.Second)
	if got := service.now(); !got.Equal(start.Add(3 * time.Second)) {
		t.Fatalf("now() after sleep = %s, want %s", got, start.Add(3*time.Second))
	}
	if got := clock.sleepDurations(); len(got) != 1 || got[0] != 3*time.Second {
		t.Fatalf("sleep durations = %v, want [3s]", got)
	}
}

func TestReplicationRuntimeZeroValueAndDefaults(t *testing.T) {
	service := &Service{}
	before := time.Now()
	got := service.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("zero-value now() = %s, expected between %s and %s", got, before, after)
	}
	service.sleep(0)

	constructed := NewService(nil, nil, nil, nil, nil, nil, nil)
	if constructed.runtimeClock == nil {
		t.Fatal("NewService did not initialize the runtime clock")
	}
}

func TestReplicationRuntimeConcurrentAccess(t *testing.T) {
	const (
		workers    = 32
		iterations = 200
	)

	start := time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeReplicationRuntimeClock{now: start}
	service := &Service{}
	service.setReplicationRuntimeClock(clock)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range iterations {
				_ = service.now()
				service.sleep(time.Nanosecond)
			}
		}()
	}
	wg.Wait()

	if got := len(clock.sleepDurations()); got != workers*iterations {
		t.Fatalf("sleep calls = %d, want %d", got, workers*iterations)
	}
}

func TestReplicationRuntimeClockConcurrentConfiguration(t *testing.T) {
	const iterations = 500

	service := &Service{}
	clockA := &fakeReplicationRuntimeClock{now: time.Date(2041, time.January, 1, 0, 0, 0, 0, time.UTC)}
	clockB := &fakeReplicationRuntimeClock{now: time.Date(2042, time.January, 1, 0, 0, 0, 0, time.UTC)}

	service.setReplicationRuntimeClock(clockA)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				service.setReplicationRuntimeClock(clockA)
				continue
			}
			service.setReplicationRuntimeClock(clockB)
		}
	}()
	for range 2 {
		go func() {
			defer wg.Done()
			for range iterations {
				_ = service.now()
				service.sleep(0)
			}
		}()
	}
	wg.Wait()

	got := service.now()
	if !got.Equal(clockA.Now()) && !got.Equal(clockB.Now()) {
		t.Fatalf("runtime clock returned %s, want one configured clock", got)
	}
}
