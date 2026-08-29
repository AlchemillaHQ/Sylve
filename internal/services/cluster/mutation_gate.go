// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
)

var ErrNodeLeaveFenced = errors.New("node_leave_fenced")

type nodeReaddressFencedError struct{}

func (nodeReaddressFencedError) Error() string {
	return "node_readdress_fenced"
}

func (nodeReaddressFencedError) Is(target error) bool {
	return target == ErrNodeLeaveFenced
}

var ErrNodeReaddressFenced error = nodeReaddressFencedError{}

type mutationGateState uint8

const (
	mutationGateClosed mutationGateState = iota
	mutationGateOpen
	mutationGateDraining
	mutationGateReopening
)

type mutationPermitKey struct{}

type mutationPermit struct {
	gate   *MutationGate
	active atomic.Bool
}

type MutationGate struct {
	mu     sync.Mutex
	state  mutationGateState
	active int
	idle   chan struct{}
}

func NewMutationGate() *MutationGate {
	idle := make(chan struct{})
	close(idle)
	return &MutationGate{state: mutationGateClosed, idle: idle}
}

func (g *MutationGate) Enter(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if permit, ok := ctx.Value(mutationPermitKey{}).(*mutationPermit); ok && permit.gate == g && permit.active.Load() {
		return ctx, func() {}, nil
	}

	g.mu.Lock()
	if g.state != mutationGateOpen {
		g.mu.Unlock()
		return ctx, nil, ErrNodeLeaveFenced
	}
	if g.active == 0 {
		g.idle = make(chan struct{})
	}
	g.active++
	g.mu.Unlock()

	permit := &mutationPermit{gate: g}
	permit.active.Store(true)
	var once sync.Once
	release := func() {
		once.Do(func() {
			permit.active.Store(false)
			g.mu.Lock()
			g.active--
			if g.active == 0 {
				close(g.idle)
				if g.state == mutationGateReopening {
					g.state = mutationGateOpen
				}
			}
			g.mu.Unlock()
		})
	}
	return context.WithValue(ctx, mutationPermitKey{}, permit), release, nil
}

func (g *MutationGate) Drain(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	g.state = mutationGateDraining
	idle := g.idle
	g.mu.Unlock()

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *MutationGate) Close() {
	g.mu.Lock()
	g.state = mutationGateClosed
	g.mu.Unlock()
}

func (g *MutationGate) Open() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active != 0 {
		if g.state == mutationGateDraining || g.state == mutationGateReopening {
			g.state = mutationGateReopening
			return nil
		}
		return errors.New("mutation_gate_not_idle")
	}
	g.state = mutationGateOpen
	return nil
}

func (g *MutationGate) IsFenced() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state != mutationGateOpen
}

func (s *Service) EnterMutation(ctx context.Context) (context.Context, func(), error) {
	if s == nil || s.mutationGate == nil {
		return ctx, nil, ErrNodeLeaveFenced
	}
	admittedCtx, release, err := s.mutationGate.Enter(ctx)
	if err == nil || s.DB == nil {
		return admittedCtx, release, err
	}
	var record struct {
		ReaddressPhase string
	}
	if s.DB.Model(&clusterModels.Cluster{}).Select("readdress_phase").First(&record).Error == nil &&
		strings.TrimSpace(record.ReaddressPhase) != "" {
		return admittedCtx, release, ErrNodeReaddressFenced
	}
	return admittedCtx, release, err
}

func (s *Service) DrainMutations(ctx context.Context) error {
	if s == nil || s.mutationGate == nil {
		return ErrNodeLeaveFenced
	}
	return s.mutationGate.Drain(ctx)
}

func (s *Service) ReopenMutations() error {
	if s == nil || s.mutationGate == nil {
		return ErrNodeLeaveFenced
	}
	return s.mutationGate.Open()
}

func (s *Service) IsMutationFenced() bool {
	return s == nil || s.mutationGate == nil || s.mutationGate.IsFenced()
}
