// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"strings"
	"time"

	hub "github.com/alchemillahq/sylve/internal/events"
)

func (s *Service) emitLeftPanelRefresh(reason string) {
	reason = strings.TrimSpace(reason)

	if s != nil && s.Cluster != nil {
		s.Cluster.EmitLeftPanelRefreshClusterWide(reason)
		return
	}

	hub.SSE.Publish(hub.Event{
		Type:      "left-panel-refresh",
		Timestamp: time.Now(),
	})
}
