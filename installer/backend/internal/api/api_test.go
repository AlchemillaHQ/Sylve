// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/installer/backend/internal/engine"
)

func TestDiskMutationHoldsInstallLock(t *testing.T) {
	for _, fail := range []bool{false, true} {
		inst := &installer{en: engine.New(engine.Plan{})}
		response := httptest.NewRecorder()
		called, locked := false, true
		ok := inst.mutateDisks(response, func() error {
			called = true
			if inst.mu.TryLock() {
				locked = false
				inst.mu.Unlock()
			}
			if fail {
				return errors.New("mutation failed")
			}
			return nil
		})
		if !called || !locked || ok == fail {
			t.Fatalf("mutation result: called=%v locked=%v ok=%v fail=%v", called, locked, ok, fail)
		}
		if !inst.mu.TryLock() {
			t.Fatal("mutation did not release the install lock")
		}
		inst.mu.Unlock()
		if fail && (response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "mutation failed")) {
			t.Fatalf("mutation error response: %d %s", response.Code, response.Body.String())
		}
	}
}
