// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"time"
)

// replicationRuntimeClock is the small time surface used by replication
// orchestration. Tests can replace it without changing process-wide time.
type replicationRuntimeClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type realReplicationRuntimeClock struct{}

func (realReplicationRuntimeClock) Now() time.Time {
	return time.Now()
}

func (realReplicationRuntimeClock) Sleep(duration time.Duration) {
	time.Sleep(duration)
}

func (s *Service) now() time.Time {
	if s == nil {
		return time.Now()
	}

	s.runtimeMu.RLock()
	clock := s.runtimeClock
	s.runtimeMu.RUnlock()
	if clock == nil {
		return time.Now()
	}
	return clock.Now()
}

func (s *Service) sleep(duration time.Duration) {
	if s == nil {
		time.Sleep(duration)
		return
	}

	s.runtimeMu.RLock()
	clock := s.runtimeClock
	s.runtimeMu.RUnlock()
	if clock == nil {
		time.Sleep(duration)
		return
	}
	clock.Sleep(duration)
}

func (s *Service) setReplicationRuntimeClock(clock replicationRuntimeClock) {
	if s == nil {
		return
	}
	if clock == nil {
		clock = realReplicationRuntimeClock{}
	}

	s.runtimeMu.Lock()
	s.runtimeClock = clock
	s.runtimeMu.Unlock()
}
