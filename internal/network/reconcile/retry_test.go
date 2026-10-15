// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package reconcile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestRetryBackoffAndExhaustion(t *testing.T) {
	for _, succeedsAt := range []int{1, 2, 4, 0} {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			calls := 0
			failure := errors.New("network not ready")
			err := Retry(t.Context(), Backoff(), func(_ context.Context, attempt int) error {
				calls++
				if attempt == succeedsAt {
					return nil
				}
				return failure
			})
			want := succeedsAt
			if want == 0 {
				want = 4
				if !errors.Is(err, failure) {
					t.Fatalf("error = %v, want exhausted failure", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls != want {
				t.Fatalf("attempts = %d, want %d", calls, want)
			}
			elapsed := time.Since(start)
			if elapsed > 7*time.Second || (want == 4 && elapsed != 7*time.Second) {
				t.Fatalf("backoff elapsed = %s", elapsed)
			}
		})
	}
}

func TestRetryWindowIncludesAttemptTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		err := Retry(t.Context(), Backoff(), func(ctx context.Context, _ int) error {
			calls++
			select {
			case <-time.After(6 * time.Second):
				return errors.New("still settling")
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if !errors.Is(err, context.DeadlineExceeded) || calls != 2 || time.Since(start) != RetryWindow {
			t.Fatalf("error=%v attempts=%d elapsed=%s", err, calls, time.Since(start))
		}
	})
}

func TestRetryCancellationPreservesLastFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	failure := errors.New("port missing")
	err := Retry(ctx, Backoff(), func(context.Context, int) error {
		calls++
		cancel()
		return failure
	})
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestLockCancellationDoesNotAcquireLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mutex sync.Mutex
		mutex.Lock()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := Lock(ctx, &mutex); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lock error = %v", err)
		}
		mutex.Unlock()
		if !mutex.TryLock() {
			t.Fatal("canceled lock attempt acquired the lock later")
		}
		mutex.Unlock()
	})
}

func TestStatusCloneOwnsItsData(t *testing.T) {
	now := time.Now()
	status := Status{State: "degraded", LastAttemptAt: &now, Failures: []Failure{{Name: "broken"}}}
	copy := status.Clone()
	copy.Failures[0].Name = "modified"
	*copy.LastAttemptAt = time.Time{}
	if status.Failures[0].Name != "broken" || status.LastAttemptAt.IsZero() {
		t.Fatal("status clone shares mutable data")
	}
}
