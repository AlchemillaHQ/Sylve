// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"reflect"
	"testing"
)

func TestNormalizeDatasetOperationRootsKeepsMinimalAncestors(t *testing.T) {
	got := normalizeDatasetOperationRoots([]string{
		"tank/other",
		"tank/root/child",
		"tank/root",
		"tank/root/child/grandchild",
		"tank/other",
	})
	want := []string{"tank/other", "tank/root"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roots mismatch: got %v want %v", got, want)
	}
}

func TestDatasetOperationLockInterlocksBackupParentAndRestoreChild(t *testing.T) {
	service := &Service{runningRestoreDestination: make(map[string]struct{})}
	acquired, holder, held := service.acquireDatasetOperations([]string{"zroot/sylve"})
	if !acquired || holder != "" || len(held) != 1 {
		t.Fatalf("acquire parent: acquired=%t holder=%q held=%v", acquired, holder, held)
	}
	if acquired, holder := service.acquireRestoreDestination("zroot/sylve/jails/100"); acquired || holder != "zroot/sylve" {
		t.Fatalf("restore child should conflict: acquired=%t holder=%q", acquired, holder)
	}
	service.releaseDatasetOperations(held)
	if acquired, holder := service.acquireRestoreDestination("zroot/sylve/jails/100"); !acquired || holder != "" {
		t.Fatalf("restore child should acquire after release: acquired=%t holder=%q", acquired, holder)
	}
}

func TestDatasetOperationLockInterlocksRestoreParentAndBackupChild(t *testing.T) {
	service := &Service{runningRestoreDestination: make(map[string]struct{})}
	if acquired, holder := service.acquireRestoreDestination("zroot/sylve"); !acquired || holder != "" {
		t.Fatalf("acquire restore parent: acquired=%t holder=%q", acquired, holder)
	}
	acquired, holder, held := service.acquireDatasetOperations([]string{"zroot/sylve/jails/100"})
	if acquired || holder != "zroot/sylve" || held != nil {
		t.Fatalf("backup child should conflict: acquired=%t holder=%q held=%v", acquired, holder, held)
	}
}

func TestAcquireDatasetOperationsWhileHoldingIsAtomicAcrossVMRoots(t *testing.T) {
	s := &Service{runningRestoreDestination: make(map[string]struct{})}
	primary := "tank-a/sylve/virtual-machines/100"
	secondary := "tank-b/sylve/virtual-machines/100"

	if acquired, holder := s.acquireRestoreDestination(primary); !acquired {
		t.Fatalf("acquire primary: holder=%s", holder)
	}
	if acquired, holder := s.acquireRestoreDestination(secondary + "/disk"); !acquired {
		t.Fatalf("seed secondary conflict: holder=%s", holder)
	}

	acquired, holder, roots := s.acquireDatasetOperationsWhileHolding(
		primary,
		[]string{primary, secondary},
	)
	if acquired || holder != secondary+"/disk" || len(roots) != 0 {
		t.Fatalf("conflicting extension = acquired:%t holder:%q roots:%v", acquired, holder, roots)
	}
	if _, exists := s.runningRestoreDestination[secondary]; exists {
		t.Fatal("failed atomic extension partially acquired secondary root")
	}

	s.releaseRestoreDestination(secondary + "/disk")
	acquired, holder, roots = s.acquireDatasetOperationsWhileHolding(
		primary,
		[]string{primary, secondary},
	)
	if !acquired || holder != "" || len(roots) != 1 || roots[0] != secondary {
		t.Fatalf("successful extension = acquired:%t holder:%q roots:%v", acquired, holder, roots)
	}
	s.releaseDatasetOperations(roots)
	s.releaseRestoreDestination(primary)
}
