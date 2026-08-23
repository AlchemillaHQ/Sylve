// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"errors"
	"sync"
	"testing"
)

func newZeltaService() *Service {
	return &Service{
		runningTransitions: make(map[uint]struct{}),
		runningJobs:        make(map[uint]struct{}),
		queuedJobs:         make(map[uint]struct{}),
		runningReplication: make(map[uint]struct{}),
		poolDownMisses:     make(map[string]int),
		runningWorkloadOp:  make(map[string]string),
	}
}

func TestTransitionAcquireRelease(t *testing.T) {
	svc := newZeltaService()
	policyID := uint(100)

	if svc.IsPolicyTransitionRunning(policyID) {
		t.Fatal("transition should not be running before acquire")
	}

	if !svc.acquirePolicyTransition(policyID) {
		t.Fatal("first acquire should succeed")
	}

	if !svc.IsPolicyTransitionRunning(policyID) {
		t.Fatal("transition should be running after acquire")
	}

	svc.releasePolicyTransition(policyID)

	if svc.IsPolicyTransitionRunning(policyID) {
		t.Fatal("transition should not be running after release")
	}
}

func TestTransitionAcquireZeroPolicyID(t *testing.T) {
	svc := newZeltaService()

	if svc.acquirePolicyTransition(0) {
		t.Fatal("acquire with zero policy ID should fail")
	}
	if svc.IsPolicyTransitionRunning(0) {
		t.Fatal("zero policy ID should never be running")
	}
}

func TestTransitionDoubleAcquireRejected(t *testing.T) {
	svc := newZeltaService()
	policyID := uint(200)

	if !svc.acquirePolicyTransition(policyID) {
		t.Fatal("first acquire should succeed")
	}
	if svc.acquirePolicyTransition(policyID) {
		t.Fatal("second acquire on same policy should fail (in_progress)")
	}
}

func TestTransitionReleaseNotAcquired(t *testing.T) {
	svc := newZeltaService()

	svc.releasePolicyTransition(999)
	if svc.IsPolicyTransitionRunning(999) {
		t.Fatal("releasing non-existent transition should not leave it running")
	}
}

func TestTransitionDoubleRelease(t *testing.T) {
	svc := newZeltaService()
	policyID := uint(300)

	svc.acquirePolicyTransition(policyID)
	svc.releasePolicyTransition(policyID)
	svc.releasePolicyTransition(policyID)

	if svc.IsPolicyTransitionRunning(policyID) {
		t.Fatal("double release should not leave transition running")
	}
}

func TestTransitionActivateFromPassiveRetry(t *testing.T) {
	svc := newZeltaService()
	policyID := uint(400)

	if !svc.acquirePolicyTransition(policyID) {
		t.Fatal("initial acquire should succeed (none → activating)")
	}
	svc.releasePolicyTransition(policyID)

	if !svc.acquirePolicyTransition(policyID) {
		t.Fatal("re-acquire should succeed (passive → activating retry)")
	}
	svc.releasePolicyTransition(policyID)
}

func TestTransitionMultiplePoliciesIndependent(t *testing.T) {
	svc := newZeltaService()

	if !svc.acquirePolicyTransition(10) {
		t.Fatal("acquire policy 10 should succeed")
	}
	if !svc.acquirePolicyTransition(20) {
		t.Fatal("acquire policy 20 should succeed (independent)")
	}
	if !svc.IsPolicyTransitionRunning(10) {
		t.Fatal("policy 10 should still be running")
	}
	if !svc.IsPolicyTransitionRunning(20) {
		t.Fatal("policy 20 should still be running")
	}

	svc.releasePolicyTransition(10)
	if svc.IsPolicyTransitionRunning(10) {
		t.Fatal("policy 10 should not be running after release")
	}
	if !svc.IsPolicyTransitionRunning(20) {
		t.Fatal("policy 20 should still be running")
	}

	svc.releasePolicyTransition(20)
}

func TestTransitionConcurrentSamePolicy(t *testing.T) {
	svc := newZeltaService()
	policyID := uint(500)

	if !svc.acquirePolicyTransition(policyID) {
		t.Fatal("initial acquire should succeed")
	}

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if svc.acquirePolicyTransition(policyID) {
				mu.Lock()
				successCount++
				mu.Unlock()
				svc.releasePolicyTransition(policyID)
			}
		}()
	}

	wg.Wait()

	if successCount != 0 {
		t.Fatalf("expected 0 successful concurrent acquires on held lock, got %d", successCount)
	}

	if !svc.IsPolicyTransitionRunning(policyID) {
		t.Fatal("original holder should still have the lock")
	}
}

func TestTransitionConcurrentDifferentPolicies(t *testing.T) {
	svc := newZeltaService()

	var wg sync.WaitGroup
	for i := uint(1); i <= 10; i++ {
		wg.Add(1)
		go func(policyID uint) {
			defer wg.Done()
			if !svc.acquirePolicyTransition(policyID) {
				t.Errorf("concurrent acquire on unique policy %d should succeed", policyID)
			}
			svc.releasePolicyTransition(policyID)
		}(i)
	}
	wg.Wait()
}

func TestIsReplicationPolicyTransitionRunningErrorType(t *testing.T) {
	if isReplicationPolicyTransitionRunningError(nil) {
		t.Fatal("nil error should not be transition running error")
	}
	if !isReplicationPolicyTransitionRunningError(errReplicationPolicyTransitionAlreadyRunning) {
		t.Fatal("errReplicationPolicyTransitionAlreadyRunning should be detected")
	}
	if isReplicationPolicyTransitionRunningError(errors.New("some other error")) {
		t.Fatal("unrelated error should not be transition running error")
	}
}
