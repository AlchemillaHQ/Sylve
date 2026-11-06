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

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
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

func TestEnterMutationWhenReadyWaits(t *testing.T) {
	service := &Service{DB: newClusterServiceTestDB(t), mutationGate: NewMutationGate()}
	if err := service.DB.Create(&clusterModels.Cluster{Enabled: true, RaftIP: "192.0.2.1"}).Error; err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	go func() {
		_, release, err := service.EnterMutationWhenReady(context.Background())
		if err == nil {
			release()
		}
		admitted <- err
	}()
	select {
	case err := <-admitted:
		t.Fatalf("admitted through a closed gate: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := service.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-admitted:
		t.Fatalf("admitted while clustered without Raft: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if err := service.DB.Model(&clusterModels.Cluster{}).Where("1 = 1").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("after the gate opened: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("still waiting after the gate opened")
	}

	service.mutationGate.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := service.EnterMutationWhenReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fenced until the context ended: %v", err)
	}

	voter := newStandaloneTestService(t)
	voter.NodeID = "node-a"
	startSoleVoter(t, voter, "node-a")
	if _, release, err := voter.EnterReadyMutation(context.Background()); err != nil {
		t.Fatalf("a voter: %v", err)
	} else {
		release()
	}
	voter.NodeID = "node-b"
	if _, _, err := voter.EnterReadyMutation(context.Background()); err == nil {
		t.Fatal("ready on a node that doesn't vote")
	}
}
