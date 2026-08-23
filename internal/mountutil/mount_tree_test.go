// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mountutil

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var (
	errFakeBusy = errors.New("busy")
	errFakeIO   = errors.New("io failure")
	errFakeGone = errors.New("gone")
)

type fakeUnmountCall struct {
	target string
	force  bool
}

type fakeMountTreeOps struct {
	mounts       []mountInfo
	normalErrors map[string]error
	forceErrors  map[string]error
	calls        []fakeUnmountCall
}

func (f *fakeMountTreeOps) list(context.Context) ([]mountInfo, error) {
	return append([]mountInfo(nil), f.mounts...), nil
}

func (f *fakeMountTreeOps) unmount(target string, force bool) error {
	f.calls = append(f.calls, fakeUnmountCall{target: target, force: force})
	if force {
		if err := f.forceErrors[target]; err != nil {
			return err
		}
	} else if err := f.normalErrors[target]; err != nil {
		return err
	}

	for index := len(f.mounts) - 1; index >= 0; index-- {
		if f.mounts[index].Target != target {
			continue
		}
		f.mounts = append(f.mounts[:index], f.mounts[index+1:]...)
		return nil
	}
	return errFakeGone
}

func (*fakeMountTreeOps) isBusy(err error) bool {
	return errors.Is(err, errFakeBusy)
}

func (*fakeMountTreeOps) isGone(err error) bool {
	return errors.Is(err, errFakeGone)
}

func testMounts(entries ...mountInfo) []mountInfo {
	return append([]mountInfo{{
		Source: "tank/sylve/jails/10",
		Target: "/jails/10",
		FSType: "zfs",
	}}, entries...)
}

func TestUnmountDescendantsDeepestFirstAndStrictlyBounded(t *testing.T) {
	ops := &fakeMountTreeOps{mounts: testMounts(
		mountInfo{Source: "devfs", Target: "/jails/10/dev", FSType: "devfs"},
		mountInfo{Source: "fdescfs", Target: "/jails/10/dev/fd", FSType: "fdescfs"},
		mountInfo{Source: "tmpfs", Target: "/jails/10/dev/shm", FSType: "tmpfs"},
		mountInfo{Source: "outside", Target: "/jails/100/dev", FSType: "devfs"},
	)}

	if err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	); err != nil {
		t.Fatalf("unmount descendants: %v", err)
	}

	got := make([]string, 0, len(ops.calls))
	for _, call := range ops.calls {
		got = append(got, fmt.Sprintf("%s:%t", call.target, call.force))
	}
	want := []string{
		"/jails/10/dev/fd:false",
		"/jails/10/dev/shm:false",
		"/jails/10/dev:false",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unmount calls = %v, want %v", got, want)
	}
	if len(ops.mounts) != 2 || ops.mounts[1].Target != "/jails/100/dev" {
		t.Fatalf("outside mount was changed: %+v", ops.mounts)
	}
}

func TestUnmountDescendantsForcesOnlyBusyMounts(t *testing.T) {
	busyTarget := "/jails/10/dev/fd"
	ops := &fakeMountTreeOps{
		mounts: testMounts(mountInfo{
			Source: "fdescfs", Target: busyTarget, FSType: "fdescfs",
		}),
		normalErrors: map[string]error{busyTarget: errFakeBusy},
	}

	if err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	); err != nil {
		t.Fatalf("force busy descendant: %v", err)
	}
	want := []fakeUnmountCall{{target: busyTarget}, {target: busyTarget, force: true}}
	if fmt.Sprint(ops.calls) != fmt.Sprint(want) {
		t.Fatalf("unmount calls = %+v, want %+v", ops.calls, want)
	}
}

func TestUnmountDescendantsClearsStackedTargetByRescanning(t *testing.T) {
	target := "/jails/10/dev"
	ops := &fakeMountTreeOps{mounts: testMounts(
		mountInfo{Source: "devfs-lower", Target: target, FSType: "devfs"},
		mountInfo{Source: "devfs-upper", Target: target, FSType: "devfs"},
	)}

	if err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	); err != nil {
		t.Fatalf("unmount stacked target: %v", err)
	}
	if len(ops.calls) != 2 || ops.calls[0].target != target || ops.calls[1].target != target {
		t.Fatalf("stacked unmount calls = %+v", ops.calls)
	}
}

func TestUnmountDescendantsRequiresAnchorWhenResidualExists(t *testing.T) {
	ops := &fakeMountTreeOps{mounts: []mountInfo{{
		Source: "devfs", Target: "/jails/10/dev", FSType: "devfs",
	}}}

	err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	)
	if err == nil || !strings.Contains(err.Error(), "mount_tree_zfs_anchor_missing") {
		t.Fatalf("missing anchor error = %v", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("cleanup mutated mounts without an anchor: %+v", ops.calls)
	}
}

func TestUnmountDescendantsAllowsUnmountedTreeWithoutResiduals(t *testing.T) {
	ops := &fakeMountTreeOps{}
	if err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	); err != nil {
		t.Fatalf("empty unmounted tree: %v", err)
	}
}

func TestUnmountDescendantsReportsNonBusyFailure(t *testing.T) {
	target := "/jails/10/dev"
	ops := &fakeMountTreeOps{
		mounts: testMounts(mountInfo{
			Source: "devfs", Target: target, FSType: "devfs",
		}),
		normalErrors: map[string]error{target: errFakeIO},
	}

	err := unmountDescendants(
		t.Context(), "tank/sylve/jails/10", "/jails/10", ops,
	)
	if err == nil || !strings.Contains(err.Error(), errFakeIO.Error()) {
		t.Fatalf("non-busy failure = %v", err)
	}
	if len(ops.calls) != 1 || ops.calls[0].force {
		t.Fatalf("non-busy failure was forcibly retried: %+v", ops.calls)
	}
}
