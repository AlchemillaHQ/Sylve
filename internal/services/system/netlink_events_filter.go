// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"strings"

	"github.com/alchemillahq/sylve/internal/db/models"
)

const (
	zfsHistoryEventType = "sysevent.fs.zfs.history_event"
	zfsStateChangeType  = "resource.fs.zfs.statechange"
)

func shouldPersistNetlinkEvent(ev *models.NetlinkEvent) bool {
	if ev == nil {
		return false
	}

	eventType := strings.TrimSpace(strings.ToLower(ev.Type))
	return eventType == zfsHistoryEventType || eventType == zfsStateChangeType
}
