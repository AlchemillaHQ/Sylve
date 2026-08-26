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
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/cmd"
	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

const statusCacheTTL = 9 * time.Second

type statusSources struct {
	hostname    func() (string, error)
	cpuUsage    func() (float64, error)
	ramUsage    func() (uint64, uint64, error)
	uptime      func() (int64, error)
	zfsHealth   func(context.Context) (string, error)
	vmCounts    func() (int, int, error)
	jailCounts  func() (int, int, error)
	activeTasks func(context.Context) (int64, error)
	now         func() time.Time
}

type StatusProvider struct {
	sources statusSources
	ttl     time.Duration

	mu          sync.RWMutex
	collectGate chan struct{}
	snapshot    consoleprotocol.StatusSnapshot
	expiresAt   time.Time
}

func NewStatusProvider(
	zfsService *zfs.Service,
	libvirtService *libvirt.Service,
	jailService *jail.Service,
	lifecycleService *lifecycle.Service,
) *StatusProvider {
	sources := statusSources{
		hostname: utils.GetSystemHostname,
		cpuUsage: func() (float64, error) {
			percentages, err := cpu.Percent(time.Second, false)
			if err != nil {
				return 0, err
			}
			if len(percentages) == 0 || math.IsNaN(percentages[0]) || math.IsInf(percentages[0], 0) {
				return 0, fmt.Errorf("cpu_usage_unavailable")
			}
			return percentages[0], nil
		},
		ramUsage: func() (uint64, uint64, error) {
			usage, err := mem.VirtualMemory()
			if err != nil {
				return 0, 0, err
			}
			return usage.Used, usage.Total, nil
		},
		uptime: utils.GetUptime,
		now:    time.Now,
	}

	if zfsService != nil {
		sources.zfsHealth = zfsService.GetPoolHealth
	}
	if libvirtService != nil {
		sources.vmCounts = libvirtService.GetManagedVMCounts
	}
	if jailService != nil {
		sources.jailCounts = jailService.GetManagedJailCounts
	}
	if lifecycleService != nil {
		sources.activeTasks = lifecycleService.CountActiveTasks
	}

	return newStatusProvider(sources, statusCacheTTL)
}

func newStatusProvider(sources statusSources, ttl time.Duration) *StatusProvider {
	if sources.now == nil {
		sources.now = time.Now
	}
	return &StatusProvider{
		sources:     sources,
		ttl:         ttl,
		collectGate: make(chan struct{}, 1),
	}
}

func (p *StatusProvider) Snapshot(ctx context.Context) (consoleprotocol.StatusSnapshot, error) {
	if p == nil {
		return consoleprotocol.StatusSnapshot{}, fmt.Errorf("status_provider_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return consoleprotocol.StatusSnapshot{}, err
	}

	now := p.sources.now()
	p.mu.RLock()
	if !p.expiresAt.IsZero() && now.Before(p.expiresAt) {
		snapshot := p.snapshot
		p.mu.RUnlock()
		return snapshot, nil
	}
	p.mu.RUnlock()

	select {
	case p.collectGate <- struct{}{}:
	case <-ctx.Done():
		return consoleprotocol.StatusSnapshot{}, ctx.Err()
	}

	now = p.sources.now()
	p.mu.RLock()
	if !p.expiresAt.IsZero() && now.Before(p.expiresAt) {
		snapshot := p.snapshot
		p.mu.RUnlock()
		<-p.collectGate
		return snapshot, nil
	}
	previous := p.snapshot
	p.mu.RUnlock()

	result := make(chan consoleprotocol.StatusSnapshot, 1)
	go func() {
		snapshot := p.collect(ctx, previous, now)
		completedAt := p.sources.now()
		p.mu.Lock()
		p.snapshot = snapshot
		p.expiresAt = completedAt.Add(p.ttl)
		p.mu.Unlock()
		<-p.collectGate
		result <- snapshot
	}()

	select {
	case snapshot := <-result:
		return snapshot, nil
	case <-ctx.Done():
		return consoleprotocol.StatusSnapshot{}, ctx.Err()
	}
}

func (p *StatusProvider) collect(
	ctx context.Context,
	previous consoleprotocol.StatusSnapshot,
	now time.Time,
) consoleprotocol.StatusSnapshot {
	snapshot := previous
	snapshot.Version = cmd.Version
	snapshot.CollectedAt = now
	snapshot.Stale = false

	var wg sync.WaitGroup
	failures := make(chan struct{}, 8)
	run := func(collect func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := collect(); err != nil {
				failures <- struct{}{}
			}
		}()
	}

	if p.sources.hostname != nil {
		run(func() error {
			hostname, err := p.sources.hostname()
			if err == nil {
				snapshot.Hostname = hostname
			}
			return err
		})
	}
	if p.sources.cpuUsage != nil {
		run(func() error {
			usage, err := p.sources.cpuUsage()
			if err == nil {
				snapshot.CPUUsage = pointerTo(usage)
			}
			return err
		})
	}
	if p.sources.ramUsage != nil {
		run(func() error {
			used, total, err := p.sources.ramUsage()
			if err == nil && total == 0 {
				err = fmt.Errorf("ram_usage_unavailable")
			}
			if err == nil {
				snapshot.RAMUsed = pointerTo(used)
				snapshot.RAMTotal = pointerTo(total)
			}
			return err
		})
	}
	if p.sources.uptime != nil {
		run(func() error {
			uptime, err := p.sources.uptime()
			if err == nil {
				snapshot.Uptime = pointerTo(uptime)
			}
			return err
		})
	}
	if p.sources.zfsHealth != nil {
		run(func() error {
			health, err := p.sources.zfsHealth(ctx)
			if err == nil {
				snapshot.ZFSHealth = pointerTo(health)
			}
			return err
		})
	}
	if p.sources.vmCounts != nil {
		run(func() error {
			running, total, err := p.sources.vmCounts()
			if err == nil {
				snapshot.VMRunning = pointerTo(running)
				snapshot.VMTotal = pointerTo(total)
			}
			return err
		})
	}
	if p.sources.jailCounts != nil {
		run(func() error {
			running, total, err := p.sources.jailCounts()
			if err == nil {
				snapshot.JailRunning = pointerTo(running)
				snapshot.JailTotal = pointerTo(total)
			}
			return err
		})
	}
	if p.sources.activeTasks != nil {
		run(func() error {
			count, err := p.sources.activeTasks(ctx)
			if err == nil {
				snapshot.ActiveTasks = pointerTo(count)
			}
			return err
		})
	}

	wg.Wait()
	close(failures)
	snapshot.Stale = len(failures) > 0
	return snapshot
}

func pointerTo[T any](value T) *T {
	return &value
}
