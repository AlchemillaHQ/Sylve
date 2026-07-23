// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package startup

import (
	"errors"
	"fmt"
	"testing"

	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

type epairStartupSyncerStub struct {
	called bool
	err    error
}

func (s *epairStartupSyncerStub) SyncEpairs(bool) error {
	s.called = true
	return s.err
}

func TestSyncEpairsOnStartupSkipsOwnershipConflict(t *testing.T) {
	syncer := &epairStartupSyncerStub{
		err: fmt.Errorf("%w: refusing to adopt unmanaged epair aaadx_net4a", networkServiceInterfaces.ErrEpairOwnershipConflict),
	}

	if err := syncEpairsOnStartup(syncer); err != nil {
		t.Fatalf("syncEpairsOnStartup: %v", err)
	}
	if !syncer.called {
		t.Fatal("SyncEpairs was not called")
	}
}

func TestSyncEpairsOnStartupReturnsUnexpectedError(t *testing.T) {
	cause := errors.New("interface list failed")
	syncer := &epairStartupSyncerStub{err: cause}

	err := syncEpairsOnStartup(syncer)
	if !errors.Is(err, cause) {
		t.Fatalf("syncEpairsOnStartup error = %v, want %v", err, cause)
	}
}
