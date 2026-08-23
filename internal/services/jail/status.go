// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"strings"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

// GetManagedJailCounts returns active and total Sylve-managed jails.
func (s *Service) GetManagedJailCounts() (int, int, error) {
	var ctids []uint
	if err := s.DB.Model(&jailModels.Jail{}).Pluck("ct_id", &ctids).Error; err != nil {
		return 0, 0, err
	}
	if len(ctids) == 0 {
		return 0, 0, nil
	}

	s.liveStateMutex.RLock()
	updatedAt := s.liveStateUpdatedAt
	s.liveStateMutex.RUnlock()

	states := s.getCachedStates()
	if len(states) == 0 || updatedAt.IsZero() || time.Since(updatedAt) > 2*jailLiveStateInterval {
		var err error
		states, err = s.refreshLiveStates()
		if err != nil {
			return 0, len(ctids), err
		}
	}

	return countActiveManagedJails(ctids, states), len(ctids), nil
}

func countActiveManagedJails(ctids []uint, states []jailServiceInterfaces.State) int {
	managed := make(map[uint]struct{}, len(ctids))
	for _, ctid := range ctids {
		managed[ctid] = struct{}{}
	}

	running := 0
	for _, state := range states {
		if _, ok := managed[state.CTID]; ok && strings.EqualFold(state.State, "ACTIVE") {
			running++
		}
	}
	return running
}
