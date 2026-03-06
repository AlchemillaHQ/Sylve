// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import "fmt"

func (s *Service) requireVMMutationOwnership(rid uint) error {
	if rid == 0 {
		return fmt.Errorf("invalid_vm_rid")
	}

	allowed, leaseErr := s.canMutateProtectedVM(rid)
	if leaseErr != nil {
		return fmt.Errorf("replication_lease_check_failed: %w", leaseErr)
	}
	if !allowed {
		return fmt.Errorf("replication_lease_not_owned")
	}

	return nil
}
