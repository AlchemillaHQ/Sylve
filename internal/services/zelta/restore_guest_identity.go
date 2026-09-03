// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
)

type restoreGuestIdentityReservationKey struct{}

type restoreGuestIdentityReservation struct {
	GuestKind     string
	GuestID       uint
	Reservation   clusterServiceInterfaces.GuestIdentityReservation
	OwnedByCaller bool
}

func withRestoreGuestIdentityReservation(
	ctx context.Context,
	guestKind string,
	guestID uint,
	reservation clusterServiceInterfaces.GuestIdentityReservation,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, restoreGuestIdentityReservationKey{}, restoreGuestIdentityReservation{
		GuestKind:   strings.TrimSpace(guestKind),
		GuestID:     guestID,
		Reservation: reservation,
	})
}

func restoreGuestIdentityReservationFromContext(
	ctx context.Context,
	guestKind string,
	guestID uint,
) (clusterServiceInterfaces.GuestIdentityReservation, bool) {
	if ctx == nil {
		return clusterServiceInterfaces.GuestIdentityReservation{}, false
	}
	value, ok := ctx.Value(restoreGuestIdentityReservationKey{}).(restoreGuestIdentityReservation)
	if !ok || value.GuestKind != strings.TrimSpace(guestKind) || value.GuestID != guestID {
		return clusterServiceInterfaces.GuestIdentityReservation{}, false
	}
	for _, entry := range value.Reservation.Entries {
		if entry.GuestKind == value.GuestKind && entry.GuestID == value.GuestID {
			return value.Reservation, true
		}
	}
	return clusterServiceInterfaces.GuestIdentityReservation{}, false
}

func (s *Service) canonicalGuestIdentityExists(guestKind string, guestID uint) (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("guest_identity_database_not_initialized")
	}
	var count int64
	switch strings.TrimSpace(guestKind) {
	case clusterModels.ReplicationGuestTypeVM:
		if err := s.DB.Model(&vmModels.VM{}).Where("rid = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false, fmt.Errorf("lookup_vm_guest_identity_failed: %w", err)
		}
	case clusterModels.ReplicationGuestTypeJail:
		if err := s.DB.Model(&jailModels.Jail{}).Where("ct_id = ?", guestID).Limit(1).Count(&count).Error; err != nil {
			return false, fmt.Errorf("lookup_jail_guest_identity_failed: %w", err)
		}
	default:
		return false, fmt.Errorf("guest_identity_kind_invalid")
	}
	return count > 0, nil
}

func (s *Service) reserveGuestIdentityForRestore(
	ctx context.Context,
	guestKind string,
	guestID uint,
) (context.Context, *restoreGuestIdentityReservation, error) {
	if reservation, inherited := restoreGuestIdentityReservationFromContext(ctx, guestKind, guestID); inherited {
		return ctx, &restoreGuestIdentityReservation{
			GuestKind:   guestKind,
			GuestID:     guestID,
			Reservation: reservation,
		}, nil
	}
	if s == nil || s.Cluster == nil {
		return ctx, nil, fmt.Errorf("guest_identity_inventory_scan_failed: cluster_service_not_initialized")
	}
	reservation, err := s.Cluster.ReserveGuestIdentities(ctx, guestKind, []uint{guestID})
	if err != nil {
		return ctx, nil, err
	}
	state := &restoreGuestIdentityReservation{
		GuestKind:     guestKind,
		GuestID:       guestID,
		Reservation:   reservation,
		OwnedByCaller: true,
	}
	return withRestoreGuestIdentityReservation(ctx, guestKind, guestID, reservation), state, nil
}

func (s *Service) releaseGuestIdentityReservationAfterError(
	ctx context.Context,
	state *restoreGuestIdentityReservation,
	retErr *error,
) {
	if state == nil || !state.OwnedByCaller || retErr == nil || *retErr == nil {
		return
	}
	exists, err := s.canonicalGuestIdentityExists(state.GuestKind, state.GuestID)
	if err != nil {
		*retErr = errors.Join(*retErr, fmt.Errorf("guest_identity_release_deferred: %w", err))
		return
	}
	if exists && state.Reservation.Clustered {
		return
	}
	if err := s.Cluster.ReleaseGuestIdentities(ctx, state.Reservation); err != nil {
		*retErr = errors.Join(*retErr, fmt.Errorf("guest_identity_release_failed: %w", err))
		return
	}
	state.OwnedByCaller = false
}

func (s *Service) finalizeGuestIdentityRestore(
	ctx context.Context,
	state *restoreGuestIdentityReservation,
) error {
	if state == nil || !state.OwnedByCaller {
		return nil
	}
	if err := s.Cluster.FinalizeGuestIdentities(ctx, state.Reservation); err != nil {
		return fmt.Errorf("guest_identity_finalize_failed: %w", err)
	}
	state.OwnedByCaller = false
	return nil
}
