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

	"github.com/alchemillahq/sylve/internal/db"
)

const (
	zfsHistoryEventType = "sysevent.fs.zfs.history_event"
	zfsStateChangeType  = "resource.fs.zfs.statechange"
)

type zfsEvent struct {
	System    string
	Subsystem string
	Type      string
	Attrs     map[string]string
}

func shouldProcessNetlinkEvent(ev *zfsEvent) bool {
	if ev == nil {
		return false
	}

	eventType := strings.TrimSpace(strings.ToLower(ev.Type))
	return eventType == zfsHistoryEventType || eventType == zfsStateChangeType
}

func zfsCacheInvalidationKind(ev *zfsEvent) (string, bool) {
	if ev == nil || strings.TrimSpace(strings.ToLower(ev.Type)) != zfsHistoryEventType {
		return "", false
	}

	if strings.Contains(ev.Attrs["history_dsname"], "@") {
		return db.ZFSCacheKindSnapshot, true
	}

	return db.ZFSCacheKindGenericDataset, true
}

func zfsEventPool(ev *zfsEvent) (string, bool) {
	if ev == nil {
		return "", false
	}

	if pool := strings.TrimSpace(ev.Attrs["pool"]); pool != "" {
		return pool, true
	}

	dataset := strings.TrimSpace(ev.Attrs["history_dsname"])
	if dataset == "" {
		return "", false
	}
	if separator := strings.IndexAny(dataset, "/@"); separator > 0 {
		return dataset[:separator], true
	}

	return dataset, true
}

func shouldLogNetlinkEvent(ev *zfsEvent) bool {
	if ev == nil {
		return false
	}

	eventType := strings.TrimSpace(strings.ToLower(ev.Type))
	return eventType != zfsHistoryEventType
}
