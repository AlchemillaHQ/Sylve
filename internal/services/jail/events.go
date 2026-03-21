// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"strings"
	"time"

	hub "github.com/alchemillahq/sylve/internal/events"
)

func (s *Service) SetLeftPanelRefreshEmitter(emitter func(reason string)) {
	s.leftPanelRefreshEmitterMu.Lock()
	s.leftPanelRefreshEmitter = emitter
	s.leftPanelRefreshEmitterMu.Unlock()
}

func (s *Service) emitLeftPanelRefresh(reason string) {
	s.leftPanelRefreshEmitterMu.RLock()
	emitter := s.leftPanelRefreshEmitter
	s.leftPanelRefreshEmitterMu.RUnlock()

	reason = strings.TrimSpace(reason)
	if emitter != nil {
		emitter(reason)
		return
	}

	hub.SSE.Publish(hub.Event{
		Type:      "left-panel-refresh",
		Timestamp: time.Now(),
	})
}
