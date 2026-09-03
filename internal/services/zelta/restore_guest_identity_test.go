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
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newStandaloneRestoreIdentityService(t *testing.T) *Service {
	t.Helper()
	database := testutil.NewSQLiteTestDB(
		t,
		&clusterModels.Cluster{},
		&vmModels.VM{},
		&jailModels.Jail{},
	)
	return &Service{
		DB: database,
		Cluster: &clusterService.Service{
			DB: database, NodeID: "standalone-node",
		},
	}
}

func TestRestoreGuestIdentityReservationLifecycleStandalone(t *testing.T) {
	service := newStandaloneRestoreIdentityService(t)
	ctx, state, err := service.reserveGuestIdentityForRestore(
		t.Context(), clusterModels.ReplicationGuestTypeJail, 800,
	)
	if err != nil {
		t.Fatalf("reserve restore destination: %v", err)
	}
	if state == nil || !state.OwnedByCaller {
		t.Fatalf("reservation state = %+v", state)
	}

	if inherited, ok := restoreGuestIdentityReservationFromContext(
		ctx, clusterModels.ReplicationGuestTypeJail, 800,
	); !ok || inherited.Token != state.Reservation.Token {
		t.Fatalf("matching child restore did not inherit reservation: %+v, ok=%t", inherited, ok)
	}
	if _, ok := restoreGuestIdentityReservationFromContext(
		ctx, clusterModels.ReplicationGuestTypeVM, 800,
	); ok {
		t.Fatal("different guest kind inherited reservation")
	}
	if _, ok := restoreGuestIdentityReservationFromContext(
		ctx, clusterModels.ReplicationGuestTypeJail, 801,
	); ok {
		t.Fatal("different guest ID inherited reservation")
	}

	if _, err := service.Cluster.ReserveGuestIdentities(
		t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{800},
	); err == nil || !strings.Contains(err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
		t.Fatalf("cross-kind reservation while restore is pending = %v", err)
	}

	if err := service.DB.Create(&jailModels.Jail{CTID: 800, Name: "restored-jail"}).Error; err != nil {
		t.Fatalf("create canonical restored jail: %v", err)
	}
	if err := service.finalizeGuestIdentityRestore(ctx, state); err != nil {
		t.Fatalf("finalize restore reservation: %v", err)
	}
	if state.OwnedByCaller {
		t.Fatal("committed restore reservation remains caller-owned")
	}
	if _, err := service.Cluster.ReserveGuestIdentities(
		t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{800},
	); err == nil || !strings.Contains(err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
		t.Fatalf("canonical restored jail did not keep shared ID occupied: %v", err)
	}
}

func TestRestoreGuestIdentityFailureReleaseBoundaryStandalone(t *testing.T) {
	service := newStandaloneRestoreIdentityService(t)

	_, releasedState, err := service.reserveGuestIdentityForRestore(
		t.Context(), clusterModels.ReplicationGuestTypeJail, 801,
	)
	if err != nil {
		t.Fatalf("reserve failed restore destination: %v", err)
	}
	retErr := errors.New("restore_provisioning_failed")
	service.releaseGuestIdentityReservationAfterError(t.Context(), releasedState, &retErr)
	if releasedState.OwnedByCaller {
		t.Fatal("rolled-back restore reservation remains caller-owned")
	}
	reused, err := service.Cluster.ReserveGuestIdentities(
		t.Context(), clusterModels.ReplicationGuestTypeVM, []uint{801},
	)
	if err != nil {
		t.Fatalf("reuse ID after complete restore rollback: %v", err)
	}
	if err := service.Cluster.ReleaseGuestIdentities(t.Context(), reused); err != nil {
		t.Fatalf("release reuse probe: %v", err)
	}

	_, retainedState, err := service.reserveGuestIdentityForRestore(
		t.Context(), clusterModels.ReplicationGuestTypeVM, 802,
	)
	if err != nil {
		t.Fatalf("reserve partially materialized restore: %v", err)
	}
	if err := service.DB.Create(&vmModels.VM{RID: 802, Name: "partial-restore"}).Error; err != nil {
		t.Fatalf("create canonical partial restore: %v", err)
	}
	retErr = errors.New("restore_runtime_finalize_failed")
	service.releaseGuestIdentityReservationAfterError(t.Context(), retainedState, &retErr)
	if retainedState.OwnedByCaller {
		t.Fatal("standalone reservation remained held after canonical identity became durable")
	}
	if _, err := service.Cluster.ReserveGuestIdentities(
		t.Context(), clusterModels.ReplicationGuestTypeJail, []uint{802},
	); err == nil || !strings.Contains(err.Error(), clusterModels.ErrGuestIdentityAlreadyInUse.Error()) {
		t.Fatalf("partially materialized restore ID became reusable: %v", err)
	}

	clusteredState := *retainedState
	clusteredState.OwnedByCaller = true
	clusteredState.Reservation.Clustered = true
	retErr = errors.New("restore_runtime_finalize_failed")
	service.releaseGuestIdentityReservationAfterError(t.Context(), &clusteredState, &retErr)
	if !clusteredState.OwnedByCaller {
		t.Fatal("clustered reservation was released after canonical identity became durable")
	}
}
