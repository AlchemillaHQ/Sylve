// SPDX-License-Identifier: BSD-2-Clause

package clusterServiceInterfaces

import "context"

// GuestIdentityAvailabilityChecker verifies that a numeric VM/jail identifier
// is unused before a guest creation path starts provisioning resources.
type GuestIdentityAvailabilityChecker interface {
	RequireGuestIDAvailable(ctx context.Context, guestID uint) error
	RequireGuestIDsAvailable(ctx context.Context, guestIDs []uint) error
}

type GuestIdentityReference struct {
	GuestKind string `json:"guestKind"`
	GuestID   uint   `json:"guestId"`
}

type GuestIdentityReservation struct {
	OwnerNodeID string                   `json:"ownerNodeId"`
	Token       string                   `json:"token,omitempty"`
	Entries     []GuestIdentityReference `json:"entries"`
	Clustered   bool                     `json:"clustered"`
}

type GuestIdentityCoordinator interface {
	GuestIdentityAvailabilityChecker
	ReserveGuestIdentities(
		ctx context.Context,
		guestKind string,
		guestIDs []uint,
	) (GuestIdentityReservation, error)
	FinalizeGuestIdentities(
		ctx context.Context,
		reservation GuestIdentityReservation,
	) error
	ReleaseGuestIdentities(
		ctx context.Context,
		reservation GuestIdentityReservation,
	) error
	GuestIdentityClaim(
		ctx context.Context,
		guestKind string,
		guestID uint,
		expectedOwnerNodeID string,
	) (GuestIdentityReservation, error)
	MoveGuestIdentityOwner(
		ctx context.Context,
		guestKind string,
		guestID uint,
		newOwnerNodeID string,
		operationToken string,
	) error
}
