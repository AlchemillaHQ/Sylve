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
	"fmt"
	"slices"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	guestIdentityReconcileInterval = time.Minute
	guestIdentityReconcileTimeout  = 10 * time.Second
)

func (s *Service) reconcileOwnGuestIdentityClaims(ctx context.Context) ([]uint, error) {
	ctx, cancel := context.WithTimeout(ctx, guestIdentityReconcileTimeout)
	defer cancel()
	// Hold the lock local creations and deletions take to start and finish, so
	// a guest is either in flight or fully there while this reads and acts.
	s.guestIdentityRuntimeMu.Lock()
	defer s.guestIdentityRuntimeMu.Unlock()

	inFlight, err := durableGuestIdentityInFlightIDs(s.DB.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	for guestID := range s.guestIdentityLocalReservations {
		inFlight = append(inFlight, guestID)
	}
	var operating []uint
	if err := s.DB.WithContext(ctx).Model(&clusterModels.ReplicationGuestOperation{}).Pluck("guest_id", &operating).Error; err != nil {
		return nil, err
	}
	inFlight = append(inFlight, operating...)

	var claims []clusterModels.GuestIdentityClaim
	if err := s.DB.WithContext(ctx).Order("guest_id ASC").Find(&claims).Error; err != nil {
		return nil, err
	}
	owner := s.localGuestIdentityOwner()
	report, err := ScanLocalGuestIdentityInventory(s.DB.WithContext(ctx), owner)
	if err != nil {
		return nil, err
	}

	skip := make(map[uint]bool, len(inFlight)+len(report.Conflicts))
	for _, guestID := range inFlight {
		skip[guestID] = true
	}
	for _, conflict := range report.Conflicts {
		skip[conflict.GuestID] = true
	}
	held := make(map[uint]clusterModels.GuestIdentityClaim, len(claims))
	for _, claim := range claims {
		held[claim.GuestID] = claim
	}
	local := make(map[uint]string, len(report.Entries))
	var unclaimed []clusterServiceInterfaces.GuestIdentityReference
	var heldElsewhere []uint
	for _, entry := range report.Entries {
		local[entry.GuestID] = entry.GuestType
		if skip[entry.GuestID] {
			continue
		}
		claim, exists := held[entry.GuestID]
		switch {
		case !exists:
			unclaimed = append(unclaimed, clusterServiceInterfaces.GuestIdentityReference{GuestKind: entry.GuestType, GuestID: entry.GuestID})
		case claim.OwnerNodeID != owner || claim.GuestKind != entry.GuestType:
			heldElsewhere = append(heldElsewhere, entry.GuestID)
		}
	}
	var stale []clusterServiceInterfaces.GuestIdentityReference
	for _, claim := range claims {
		if claim.OwnerNodeID != owner || skip[claim.GuestID] || local[claim.GuestID] == claim.GuestKind {
			continue
		}
		stale = append(stale, clusterServiceInterfaces.GuestIdentityReference{GuestKind: claim.GuestKind, GuestID: claim.GuestID})
	}

	var errs []error
	if len(unclaimed) != 0 {
		if err := s.Propose(ctx, "guest_identity", "claim", guestIdentityClaimSet(owner, unclaimed)); err != nil {
			errs = append(errs, fmt.Errorf("guest_identity_claim_local_guests: %w", err))
		}
	}
	if len(stale) != 0 {
		if err := s.Propose(ctx, "guest_identity", "release", guestIdentityClaimSet(owner, stale)); err != nil {
			errs = append(errs, fmt.Errorf("guest_identity_release_stale_claims: %w", err))
		}
	}
	return heldElsewhere, errors.Join(errs...)
}

// A node leaving or changing address is handing its claims off, so it skips the
// pass rather than claim them back.
func (s *Service) reconcileGuestIdentities(ctx context.Context) []uint {
	ctx, release, err := s.EnterMutation(ctx)
	if err != nil {
		return nil
	}
	defer release()
	heldElsewhere, err := s.reconcileOwnGuestIdentityClaims(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.L.Debug().Err(err).Msg("guest_identity_reconcile_deferred")
	}
	return heldElsewhere
}

func (s *Service) startGuestIdentityReconciler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastHeldElsewhere []uint
	run := func() {
		heldElsewhere := s.reconcileGuestIdentities(ctx)
		// A guest here whose ID another node holds blocks nothing else, but
		// only an operator can fix it; say so once per change.
		if !slices.Equal(heldElsewhere, lastHeldElsewhere) {
			lastHeldElsewhere = heldElsewhere
			if len(heldElsewhere) != 0 {
				logger.L.Warn().Uints("guest_ids", heldElsewhere).Msg("guest_identity_ids_held_by_another_node")
			}
		}
	}
	go func() {
		run()
		ticker := time.NewTicker(guestIdentityReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
