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
	"testing"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

func TestShouldHandleZFSStateChangeEvent(t *testing.T) {
	if !shouldHandleZFSStateChangeEvent(&models.NetlinkEvent{Type: "resource.fs.zfs.statechange"}) {
		t.Fatalf("expected_statechange_event_to_be_handled")
	}

	if shouldHandleZFSStateChangeEvent(&models.NetlinkEvent{Type: "sysevent.fs.zfs.vdev_online"}) {
		t.Fatalf("expected_non_statechange_event_to_be_ignored")
	}
}

func TestShouldEmitNotificationForPoolState(t *testing.T) {
	for _, state := range []string{"DEGRADED", "OFFLINE", "FAULTED", "UNAVAIL", "REMOVED", "SUSPENDED", "ONLINE"} {
		if !shouldEmitNotificationForPoolState(state) {
			t.Fatalf("expected_state_to_emit: %s", state)
		}
	}

	if shouldEmitNotificationForPoolState("SCRUBBING") {
		t.Fatalf("expected_scrubbing_to_be_ignored")
	}
}

func TestResolvePoolStateFromStatus(t *testing.T) {
	status := &gzfs.ZPoolStatusPool{
		State: "DEGRADED",
		Vdevs: map[string]*gzfs.ZPoolStatusVDEV{
			"root": {
				GUID:  "4347464003291029671",
				Path:  "/dev/nda1p3",
				State: "OFFLINE",
			},
		},
	}

	state := resolvePoolStateFromStatus(status, &gzfs.ZPool{State: gzfs.ZPoolStateOnline}, map[string]string{
		"vdev_guid": "4347464003291029671",
		"vdev_path": "/dev/nda1p3",
	})
	if state != "OFFLINE" {
		t.Fatalf("expected_offline_state got: %s", state)
	}

	fallback := resolvePoolStateFromStatus(status, &gzfs.ZPool{State: gzfs.ZPoolStateOnline}, map[string]string{})
	if fallback != "DEGRADED" {
		t.Fatalf("expected_status_state_fallback got: %s", fallback)
	}

	poolFallback := resolvePoolStateFromStatus(nil, &gzfs.ZPool{State: gzfs.ZPoolStateOnline}, map[string]string{})
	if poolFallback != "ONLINE" {
		t.Fatalf("expected_pool_state_fallback got: %s", poolFallback)
	}
}

func TestBuildPoolStateNotificationInput(t *testing.T) {
	attrs := map[string]string{
		"vdev_guid": "4347464003291029671",
		"vdev_path": "/dev/nda1p3",
	}

	input := buildPoolStateNotificationInput("zroot", "DEGRADED", attrs)
	if input.Kind != notifier.KindForZFSPoolState("zroot") {
		t.Fatalf("unexpected_kind: %s", input.Kind)
	}
	if input.Severity != "warning" {
		t.Fatalf("expected_warning_severity got: %s", input.Severity)
	}
	if input.Fingerprint != "zroot|4347464003291029671|degraded" {
		t.Fatalf("unexpected_fingerprint: %s", input.Fingerprint)
	}
	if input.Metadata["state"] != "DEGRADED" {
		t.Fatalf("expected_state_metadata got: %s", input.Metadata["state"])
	}

	recovery := buildPoolStateNotificationInput("zroot", "ONLINE", attrs)
	if recovery.Severity != "info" {
		t.Fatalf("expected_info_severity_for_recovery got: %s", recovery.Severity)
	}
	if !strings.Contains(strings.ToLower(recovery.Title), "recovered") {
		t.Fatalf("expected_recovery_title got: %s", recovery.Title)
	}
}
