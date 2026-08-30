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
	"testing"
	"time"
)

func TestMutationGateStartsClosed(t *testing.T) {
	gate := NewMutationGate()
	_, release, err := gate.Enter(context.Background())
	if !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("expected fenced error, got %v", err)
	}
	if release != nil {
		t.Fatal("unexpected release function")
	}
}

func TestMutationGateDrainWaitsAndRejectsNewEntries(t *testing.T) {
	gate := NewMutationGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	admittedCtx, release, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	drained := make(chan error, 1)
	go func() {
		drained <- gate.Drain(context.Background())
	}()

	deadline := time.Now().Add(time.Second)
	for !gate.IsFenced() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !gate.IsFenced() {
		t.Fatal("gate did not enter draining state")
	}
	if _, _, err := gate.Enter(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("expected fenced error, got %v", err)
	}
	if _, nestedRelease, err := gate.Enter(admittedCtx); err != nil {
		t.Fatalf("admitted operation could not enter nested work: %v", err)
	} else {
		nestedRelease()
	}
	select {
	case err := <-drained:
		t.Fatalf("drain returned before release: %v", err)
	default:
	}

	release()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("drain did not finish")
	}
}

func TestMutationGateNestedPermit(t *testing.T) {
	gate := NewMutationGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nested, nestedRelease, err := gate.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nested != ctx {
		t.Fatal("nested entry replaced context")
	}
	nestedRelease()

	drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.Drain(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected drain timeout, got %v", err)
	}
	release()
	if err := gate.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMutationGateReopensOnlyWhenIdle(t *testing.T) {
	gate := NewMutationGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	_, release, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.Close()
	if err := gate.Open(); err == nil {
		t.Fatal("expected active gate error")
	}
	release()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	if _, release, err := gate.Enter(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

func TestMutationGateCanceledDrainReopensAfterActiveWork(t *testing.T) {
	gate := NewMutationGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	_, release, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := gate.Drain(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error=%v", err)
	}
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gate.Enter(context.Background()); !errors.Is(err, ErrNodeLeaveFenced) {
		t.Fatalf("gate reopened before active work finished: %v", err)
	}
	release()
	_, secondRelease, err := gate.Enter(context.Background())
	if err != nil {
		t.Fatalf("gate did not reopen after drain: %v", err)
	}
	secondRelease()
}
