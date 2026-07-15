// SPDX-License-Identifier: BSD-2-Clause

package clusterServiceInterfaces

import "context"

// GuestIdentityAvailabilityChecker verifies that a numeric VM/jail identifier
// is unused before a guest creation path starts provisioning resources.
type GuestIdentityAvailabilityChecker interface {
	RequireGuestIDAvailable(ctx context.Context, guestID uint) error
	RequireGuestIDsAvailable(ctx context.Context, guestIDs []uint) error
}
