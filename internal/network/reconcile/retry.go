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
	"time"
)

const RetryWindow = 15 * time.Second

// Backoff makes four attempts, with at most seven seconds spent waiting.
func Backoff() []time.Duration {
	return []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
}

// Retry never overlaps attempts or abandons an in-flight mutation. The context
// bounds waits and admission of new attempts; an attempt must honor cancellation
// itself to bound its execution time.
func Retry(ctx context.Context, delays []time.Duration, attempt func(context.Context, int) error) error {
	ctx, cancel := context.WithTimeout(ctx, RetryWindow)
	defer cancel()
	deadline, _ := ctx.Deadline()

	var lastErr error
	for index := 0; index <= len(delays); index++ {
		if index > 0 {
			timer := time.NewTimer(delays[index-1])
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(lastErr, err)
		}
		if !time.Now().Before(deadline) {
			return errors.Join(lastErr, context.DeadlineExceeded)
		}
		lastErr = attempt(ctx, index+1)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// Lock permits cancellation while waiting without leaving a goroutine that may
// acquire the mutation lock after its caller has returned.
func Lock(ctx context.Context, mutex *sync.Mutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mutex.TryLock() {
			return nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type Failure struct {
	SwitchID uint   `json:"switchId,omitempty"`
	Name     string `json:"name,omitempty"`
	Bridge   string `json:"bridge,omitempty"`
	Phase    string `json:"phase"`
	Error    string `json:"error"`
}

// Status describes the last reconciliation, not continuous live-state health.
// It remains available until the next run or daemon restart.
type Status struct {
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
	Failures      []Failure  `json:"failures"`
}

func (s Status) Clone() Status {
	s.Failures = append([]Failure{}, s.Failures...)
	if s.LastAttemptAt != nil {
		value := *s.LastAttemptAt
		s.LastAttemptAt = &value
	}
	if s.CompletedAt != nil {
		value := *s.CompletedAt
		s.CompletedAt = &value
	}
	if s.State == "" {
		s.State = "not_attempted"
	}
	return s
}

type SwitchStatus struct {
	Runtime   Status `json:"runtime"`
	Bootstrap Status `json:"bootstrap"`
}
