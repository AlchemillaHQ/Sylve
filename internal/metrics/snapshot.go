// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package metrics

import (
	"slices"
	"sync"
	"time"
)

// OptionalValue distinguishes an unavailable measurement from a measured zero.
type OptionalValue struct {
	Value     float64
	Available bool
}

type Jail struct {
	CTID           uint
	Name           string
	Hostname       string
	Running        bool
	CPUUsageRatio  float64
	CPUCores       float64
	MemoryRSS      int64
	ResourceLimits bool
	MemoryLimit    int
}

type VM struct {
	RID              uint
	Name             string
	State            string
	CPUSeconds       OptionalValue
	MemoryRSS        OptionalValue
	MemoryConfigured int
	VCPUs            int
	Success          bool
}

type Container struct {
	OCID            uint
	Name            string
	Image           string
	Pool            string
	Stack           string
	Running         bool
	CPUSeconds      OptionalValue
	MemoryRSS       OptionalValue
	CPUCores        float64
	MemoryLimit     uint64
	UpdateAvailable bool
}

// Snapshot contains the latest collection, including any partial results.
// Items must contain values only, so a shallow copy isolates readers and writers.
type Snapshot[T any] struct {
	Items         []T
	CollectedAt   time.Time
	LastSuccessAt time.Time
	Success       bool
}

// Cache is populated by the existing service monitors. Scrapes never perform I/O.
type Cache[T any] struct {
	mu       sync.RWMutex
	snapshot Snapshot[T]
}

func (c *Cache[T]) Store(items []T, success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.Items = slices.Clone(items)
	c.snapshot.CollectedAt = time.Now()
	c.snapshot.Success = success
	if success {
		c.snapshot.LastSuccessAt = c.snapshot.CollectedAt
	}
}

// Fail keeps the previous items exportable until they expire, so a transient
// error does not leave a gap in every series.
func (c *Cache[T]) Fail() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.Success = false
}

func (c *Cache[T]) CollectedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot.CollectedAt
}

func (c *Cache[T]) Snapshot() Snapshot[T] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := c.snapshot
	snapshot.Items = slices.Clone(snapshot.Items)
	return snapshot
}
