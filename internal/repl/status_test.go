// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusProviderCachesAndPreservesLastGoodValues(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	cpuCalls := 0
	cpuErr := false
	provider := newStatusProvider(statusSources{
		hostname: func() (string, error) { return "node-a", nil },
		cpuUsage: func() (float64, error) {
			cpuCalls++
			if cpuErr {
				return 0, errors.New("sample failed")
			}
			return 12.4, nil
		},
		ramUsage:    func() (uint64, uint64, error) { return 4 << 30, 16 << 30, nil },
		uptime:      func() (int64, error) { return 90061, nil },
		zfsHealth:   func(context.Context) (string, error) { return "ONLINE", nil },
		vmCounts:    func() (int, int, error) { return 2, 5, nil },
		jailCounts:  func() (int, int, error) { return 4, 7, nil },
		activeTasks: func(context.Context) (int64, error) { return 1, nil },
		now:         func() time.Time { return now },
	}, time.Minute)

	first, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if first.CPUUsage == nil || *first.CPUUsage != 12.4 || first.VMRunning == nil || *first.VMRunning != 2 {
		t.Fatalf("unexpected first snapshot: %#v", first)
	}
	if _, err := provider.Snapshot(context.Background()); err != nil {
		t.Fatalf("cached snapshot: %v", err)
	}
	if cpuCalls != 1 {
		t.Fatalf("CPU source calls = %d, want 1", cpuCalls)
	}

	cpuErr = true
	now = now.Add(2 * time.Minute)
	stale, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("stale snapshot: %v", err)
	}
	if !stale.Stale || stale.CPUUsage == nil || *stale.CPUUsage != 12.4 {
		t.Fatalf("last-good CPU value was not preserved: %#v", stale)
	}
}

func TestStatusProviderCoalescesConcurrentRefreshes(t *testing.T) {
	var calls atomic.Int32
	provider := newStatusProvider(statusSources{
		cpuUsage: func() (float64, error) {
			calls.Add(1)
			time.Sleep(10 * time.Millisecond)
			return 25, nil
		},
	}, time.Minute)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := provider.Snapshot(context.Background()); err != nil {
				t.Errorf("snapshot: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("CPU source calls = %d, want 1", got)
	}
}

func TestStatusProviderWaitHonorsContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := newStatusProvider(statusSources{
		cpuUsage: func() (float64, error) {
			close(started)
			<-release
			return 25, nil
		},
	}, time.Minute)

	firstDone := make(chan error, 1)
	go func() {
		_, err := provider.Snapshot(context.Background())
		firstDone <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := provider.Snapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting snapshot error = %v, want deadline exceeded", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
}

func TestStatusProviderExpiryStartsAfterCollection(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	calls := 0
	provider := newStatusProvider(statusSources{
		cpuUsage: func() (float64, error) {
			calls++
			now = now.Add(2 * time.Minute)
			return 25, nil
		},
		now: func() time.Time { return now },
	}, time.Minute)

	if _, err := provider.Snapshot(context.Background()); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if _, err := provider.Snapshot(context.Background()); err != nil {
		t.Fatalf("cached snapshot: %v", err)
	}
	if calls != 1 {
		t.Fatalf("CPU source calls = %d, want 1", calls)
	}
}
