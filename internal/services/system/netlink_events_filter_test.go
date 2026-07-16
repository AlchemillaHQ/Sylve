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

	"github.com/alchemillahq/sylve/internal/db"
)

func TestShouldProcessNetlinkEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *zfsEvent
		expected bool
	}{
		{
			name:     "nil event",
			event:    nil,
			expected: false,
		},
		{
			name:     "history event allowed",
			event:    &zfsEvent{Type: "sysevent.fs.zfs.history_event"},
			expected: true,
		},
		{
			name:     "state change event allowed",
			event:    &zfsEvent{Type: "resource.fs.zfs.statechange"},
			expected: true,
		},
		{
			name:     "event type is case-insensitive and trimmed",
			event:    &zfsEvent{Type: "  RESOURCE.FS.ZFS.STATECHANGE  "},
			expected: true,
		},
		{
			name:     "other event denied",
			event:    &zfsEvent{Type: "ereport.fs.zfs.data"},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldProcessNetlinkEvent(tc.event); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestShouldLogNetlinkEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    *zfsEvent
		expected bool
	}{
		{
			name:     "nil event",
			event:    nil,
			expected: false,
		},
		{
			name:     "history event suppressed",
			event:    &zfsEvent{Type: "sysevent.fs.zfs.history_event"},
			expected: false,
		},
		{
			name:     "history event suppression is case-insensitive and trimmed",
			event:    &zfsEvent{Type: "  SYSEVENT.FS.ZFS.HISTORY_EVENT  "},
			expected: false,
		},
		{
			name:     "state change remains visible",
			event:    &zfsEvent{Type: "resource.fs.zfs.statechange"},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldLogNetlinkEvent(tc.event); got != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestZFSCacheInvalidationKind(t *testing.T) {
	tests := []struct {
		name     string
		event    *zfsEvent
		wantKind string
		wantOK   bool
	}{
		{name: "nil event"},
		{name: "state change", event: &zfsEvent{Type: zfsStateChangeType}},
		{
			name:     "snapshot history",
			event:    &zfsEvent{Type: zfsHistoryEventType, Attrs: map[string]string{"history_dsname": "zroot/data@snap"}},
			wantKind: db.ZFSCacheKindSnapshot,
			wantOK:   true,
		},
		{
			name:     "dataset history",
			event:    &zfsEvent{Type: zfsHistoryEventType, Attrs: map[string]string{"history_dsname": "zroot/data"}},
			wantKind: db.ZFSCacheKindGenericDataset,
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, ok := zfsCacheInvalidationKind(tc.event)
			if kind != tc.wantKind || ok != tc.wantOK {
				t.Fatalf("got (%q, %v), want (%q, %v)", kind, ok, tc.wantKind, tc.wantOK)
			}
		})
	}
}

func TestZFSEventPool(t *testing.T) {
	tests := []struct {
		name     string
		event    *zfsEvent
		wantPool string
		wantOK   bool
	}{
		{name: "nil event"},
		{name: "missing pool", event: &zfsEvent{Attrs: map[string]string{}}},
		{name: "explicit pool", event: &zfsEvent{Attrs: map[string]string{"pool": "zroot"}}, wantPool: "zroot", wantOK: true},
		{name: "dataset pool", event: &zfsEvent{Attrs: map[string]string{"history_dsname": "tank/data"}}, wantPool: "tank", wantOK: true},
		{name: "root snapshot pool", event: &zfsEvent{Attrs: map[string]string{"history_dsname": "backup@snap"}}, wantPool: "backup", wantOK: true},
		{name: "root dataset pool", event: &zfsEvent{Attrs: map[string]string{"history_dsname": "zroot"}}, wantPool: "zroot", wantOK: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, ok := zfsEventPool(tc.event)
			if pool != tc.wantPool || ok != tc.wantOK {
				t.Fatalf("got (%q, %v), want (%q, %v)", pool, ok, tc.wantPool, tc.wantOK)
			}
		})
	}
}
