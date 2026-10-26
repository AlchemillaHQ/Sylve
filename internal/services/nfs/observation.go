// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"encoding/json"
	"time"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

const monitorInterval = 15 * time.Second
const observationTTL = 3 * monitorInterval

type runtimeObservation struct {
	identity  string
	checkedAt time.Time
	running   *bool
}

func runtimeIdentity(state nfsModels.NFSRuntime) string {
	state.UpdatedAt = time.Time{}
	data, _ := json.Marshal(state)
	return exportsHash(string(data))
}

func (s *Service) observe(state nfsModels.NFSRuntime, running *bool) {
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	s.observation = runtimeObservation{identity: runtimeIdentity(state), checkedAt: time.Now(), running: running}
}

func (s *Service) observationFor(state nfsModels.NFSRuntime) runtimeObservation {
	s.observationMu.RLock()
	defer s.observationMu.RUnlock()
	if s.observation.identity != runtimeIdentity(state) || time.Since(s.observation.checkedAt) > observationTTL {
		return runtimeObservation{}
	}
	return s.observation
}

func (s *Service) beginRuntimeChange() {
	s.revision++ // Protected by mutationMu; stale monitor results cannot revoke a newer apply.
	s.observationMu.Lock()
	s.observation = runtimeObservation{}
	s.observationMu.Unlock()
}
