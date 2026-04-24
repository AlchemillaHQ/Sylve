// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
)

func TestShouldPersistNetlinkEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *models.NetlinkEvent
		expected bool
	}{
		{
			name:     "nil event",
			event:    nil,
			expected: false,
		},
		{
			name:     "history event allowed",
			event:    &models.NetlinkEvent{Type: "sysevent.fs.zfs.history_event"},
			expected: true,
		},
		{
			name:     "state change event allowed",
			event:    &models.NetlinkEvent{Type: "resource.fs.zfs.statechange"},
			expected: true,
		},
		{
			name:     "event type is case-insensitive and trimmed",
			event:    &models.NetlinkEvent{Type: "  RESOURCE.FS.ZFS.STATECHANGE  "},
			expected: true,
		},
		{
			name:     "other event denied",
			event:    &models.NetlinkEvent{Type: "ereport.fs.zfs.data"},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPersistNetlinkEvent(tc.event); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}
