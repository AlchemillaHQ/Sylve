// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"strings"
	"testing"
)

func panicRecoveredAsOperationError() (resultErr error) {
	defer recoverOperationPanic("test_operation", &resultErr)
	panic("boom")
}

func panicSeenByFinalizer() (resultErr error, finalizedErr error) {
	defer func() {
		finalizedErr = resultErr
	}()
	defer recoverOperationPanic("finalized_operation", &resultErr)
	panic("boom")
}

func TestRecoverOperationPanicSetsNamedError(t *testing.T) {
	err := panicRecoveredAsOperationError()
	if err == nil {
		t.Fatal("expected recovered panic to return an error")
	}
	if !strings.Contains(err.Error(), "test_operation_panicked: boom") {
		t.Fatalf("unexpected recovered panic error: %v", err)
	}
}

func TestRecoverOperationPanicRunsBeforeFinalizer(t *testing.T) {
	returnedErr, finalizedErr := panicSeenByFinalizer()
	if returnedErr == nil || finalizedErr == nil {
		t.Fatalf("panic error must reach both caller and finalizer: returned=%v finalized=%v", returnedErr, finalizedErr)
	}
	if returnedErr.Error() != finalizedErr.Error() {
		t.Fatalf("caller/finalizer mismatch: returned=%v finalized=%v", returnedErr, finalizedErr)
	}
}
