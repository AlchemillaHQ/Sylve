// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package interfaceref

import (
	"errors"
	"testing"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func TestCoordinatorSerializesInterfaceMutationWithReferencePersistence(t *testing.T) {
	coordinator := NewCoordinator()
	releaseRead := coordinator.ReadLock()

	writeAttempted := make(chan struct{})
	writeAcquired := make(chan struct{})
	writeFinished := make(chan struct{})
	go func() {
		close(writeAttempted)
		releaseWrite := coordinator.WriteLock()
		close(writeAcquired)
		releaseWrite()
		close(writeFinished)
	}()

	<-writeAttempted
	select {
	case <-writeAcquired:
		t.Fatal("interface mutation acquired the coordinator while reference persistence was active")
	case <-time.After(50 * time.Millisecond):
	}

	releaseRead()
	select {
	case <-writeFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("interface mutation did not acquire the coordinator after reference persistence completed")
	}
}

func TestRejectFilteredStandardBridgeInterfaces(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &networkModels.StandardSwitch{})
	if err := db.Create(&networkModels.StandardSwitch{
		Name:          "tenant",
		BridgeName:    "vm-tenant",
		VLANFiltering: true,
	}).Error; err != nil {
		t.Fatalf("create filtered Standard Switch: %v", err)
	}
	if err := db.Create(&networkModels.StandardSwitch{
		Name:       "management",
		BridgeName: "vm-management",
	}).Error; err != nil {
		t.Fatalf("create unfiltered Standard Switch: %v", err)
	}

	err := RejectFilteredStandardBridgeInterfaces(db, "vm-management", " vm-tenant ", "vm-tenant")
	if !errors.Is(err, ErrFilteredStandardSwitchL2Only) {
		t.Fatalf("expected filtered bridge rejection, got %v", err)
	}
	if err := RejectFilteredStandardBridgeInterfaces(db, "vm-management", "vm-tenant.20"); err != nil {
		t.Fatalf("expected host-facing interfaces to remain valid, got %v", err)
	}
}

func TestRejectFilteredStandardBridgeInterfacesFailsClosedWithoutSwitchSchema(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t)
	if err := RejectFilteredStandardBridgeInterfaces(db, "vm-tenant"); err == nil {
		t.Fatal("expected missing Standard Switch schema to fail closed")
	}
}
