// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type setfaclCall struct {
	action string
	entry  string
	paths  []string
}

func captureSetfacl(t *testing.T, during func(call setfaclCall)) *[]setfaclCall {
	t.Helper()
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	var mu sync.Mutex
	calls := []setfaclCall{}
	sambaRunCommand = func(command string, args ...string) (string, error) {
		if command != "/bin/setfacl" || len(args) < 4 || args[0] != "-h" {
			t.Errorf("unexpected command %s %v", command, args)
			return "", nil
		}
		call := setfaclCall{action: args[1], entry: args[2], paths: append([]string(nil), args[3:]...)}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		if during != nil {
			during(call)
		}
		return "", nil
	}
	return &calls
}

func waitForJob(t *testing.T, s *Service, id uint) SharePermissionJob {
	t.Helper()
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	job := registry.jobs[id]
	registry.mu.Unlock()
	if job == nil {
		t.Fatal("no job was started")
	}
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the job did not finish")
	}
	return job.snapshot()
}

func TestPermissionJobAppliesAccessToTheDatasetsOwnTree(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"docs", "docs/deep", "mounted"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"top.txt", "docs/a.txt", "docs/deep/b.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounted := false
	if os.Geteuid() == 0 {
		if err := exec.Command("mount", "-t", "tmpfs", "tmpfs", filepath.Join(root, "mounted")).Run(); err == nil {
			mounted = true
			t.Cleanup(func() { _ = exec.Command("umount", "-f", filepath.Join(root, "mounted")).Run() })
			if err := os.WriteFile(filepath.Join(root, "mounted", "other-fs.txt"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	calls := captureSetfacl(t, nil)
	s := &Service{}
	plan := newPermissionPlan(root,
		sambaPrincipalNames{WriteUsers: []string{"alice", "bob"}, ReadGroups: []string{"staff"}},
		sambaPrincipalNames{ReadUsers: []string{"alice"}, WriteGroups: []string{"editors"}},
		false, false)
	s.startPermissionJob(7, plan)
	status := waitForJob(t, s, 7)

	wantDirs := []string{filepath.Join(root, "docs"), filepath.Join(root, "docs/deep")}
	wantFiles := []string{filepath.Join(root, "docs/a.txt"), filepath.Join(root, "docs/deep/b.txt"), filepath.Join(root, "top.txt")}
	if !mounted {
		wantDirs = append(wantDirs, filepath.Join(root, "mounted"))
	}
	var modifiedDirs, modifiedFiles []string
	removed := map[string]bool{}
	for _, call := range *calls {
		switch call.action {
		case "-m":
			if strings.Contains(call.entry, ":fd:allow") {
				if call.entry != "u:alice:read_set/execute:fd:allow,g:editors:modify_set:fd:allow" {
					t.Errorf("directory entries = %q", call.entry)
				}
				modifiedDirs = append(modifiedDirs, call.paths...)
			} else {
				if call.entry != "u:alice:read_set/execute::allow,g:editors:modify_set::allow" {
					t.Errorf("file entries = %q", call.entry)
				}
				modifiedFiles = append(modifiedFiles, call.paths...)
			}
		case "-x":
			removed[call.entry] = true
		}
	}
	slices.Sort(modifiedDirs)
	slices.Sort(modifiedFiles)
	slices.Sort(wantDirs)
	if !slices.Equal(modifiedDirs, wantDirs) || !slices.Equal(modifiedFiles, wantFiles) {
		t.Fatalf("changed dirs %v files %v; want dirs %v files %v (no root, link, pipe or other file system)", modifiedDirs, modifiedFiles, wantDirs, wantFiles)
	}
	if !removed["u:bob:modify_set::allow"] || !removed["g:staff:modify_set::allow"] || removed["u:alice:modify_set::allow"] {
		t.Fatalf("removed entries = %v; want bob and staff only", removed)
	}
	if status.State != PermissionJobCompleted || status.Processed != int64(len(wantDirs)+len(wantFiles)) || status.Failed != 0 {
		t.Fatalf("job status = %+v", status)
	}
}

func TestPermissionJobAppliesGuestAccessWithoutRemovingEveryone(t *testing.T) {
	plan := newPermissionPlan("/mnt/public", sambaPrincipalNames{ReadUsers: []string{"alice"}}, sambaPrincipalNames{}, true, true)
	if !slices.Equal(plan.dirEntries, []string{"everyone@:modify_set:fd:allow"}) || !slices.Equal(plan.fileEntries, []string{"everyone@:modify_set::allow"}) {
		t.Fatalf("guest entries = %v %v", plan.dirEntries, plan.fileEntries)
	}
	if !slices.Equal(plan.remove, []string{"u:alice:modify_set::allow"}) {
		t.Fatalf("removals = %v; want only the user that lost access", plan.remove)
	}
}

func TestPermissionJobCanBeCancelled(t *testing.T) {
	root := t.TempDir()
	for i := range 600 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	captureSetfacl(t, func(setfaclCall) {
		once.Do(func() {
			close(started)
			<-release
		})
	})
	s := &Service{}
	s.startPermissionJob(3, newPermissionPlan(root, sambaPrincipalNames{}, sambaPrincipalNames{WriteUsers: []string{"alice"}}, false, false))
	<-started
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	job := registry.jobs[3]
	registry.mu.Unlock()
	job.cancel()
	close(release)
	status := waitForJob(t, s, 3)
	if status.State != PermissionJobCancelled || status.Processed >= 600 {
		t.Fatalf("cancelled job status = %+v; want cancelled before finishing", status)
	}
	if s.CancelPermissionJob(3) {
		t.Fatal("cancelling a finished job reported success")
	}
}

func TestApplyShareAccessRefusesProtectedDatasets(t *testing.T) {
	svc, runner := newSambaServiceWithMockRunner(t)
	addDatasetLookupMocks(t, runner, []mockDataset{
		{Name: "tank", GUID: "guid-tank", Mountpoint: "/tank"},
		{Name: "tank/sylve", GUID: "guid-sylve", Mountpoint: "/tank/sylve"},
	})
	share := sharesWithDataset(t, svc, "guid-tank")
	captureSetfacl(t, func(call setfaclCall) { t.Errorf("a refused job ran setfacl: %+v", call) })

	err := svc.ApplyShareAccessToExisting(context.Background(), uint(share), nil)
	if err == nil || !strings.Contains(err.Error(), ShareDatasetContainsProtected) {
		t.Fatalf("ApplyShareAccessToExisting returned %v; want a refusal", err)
	}
	if job := svc.PermissionJob(uint(share)); job == nil || job.State != PermissionJobFailed || !strings.Contains(job.Error, ShareDatasetContainsProtected) {
		t.Fatalf("job = %+v; want a failed job explaining the refusal", job)
	}
}

func TestPermissionJobFailsWhenEveryChangeFails(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	original := sambaRunCommand
	t.Cleanup(func() { sambaRunCommand = original })
	sambaRunCommand = func(string, ...string) (string, error) { return "", errors.New("operation not permitted") }
	s := &Service{}
	s.startPermissionJob(4, newPermissionPlan(root, sambaPrincipalNames{}, sambaPrincipalNames{ReadUsers: []string{"alice"}}, false, false))
	status := waitForJob(t, s, 4)
	if status.State != PermissionJobFailed || status.Failed != 2 || status.Processed != 2 || status.Error == "" {
		t.Fatalf("job status = %+v; want a failed job with both files counted as failed", status)
	}
}

func TestFailingAJobRequestStopsTheRunningJob(t *testing.T) {
	root := t.TempDir()
	for i := range 600 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	captureSetfacl(t, func(setfaclCall) {
		once.Do(func() {
			close(started)
			<-release
		})
	})
	s := &Service{}
	running := s.startPermissionJob(5, newPermissionPlan(root, sambaPrincipalNames{}, sambaPrincipalNames{WriteUsers: []string{"alice"}}, false, false))
	<-started
	cancelled := make(chan struct{})
	cancel := running.cancel
	running.cancel = func() {
		cancel()
		close(cancelled)
	}
	failed := make(chan struct{})
	go func() {
		s.failPermissionJob(5, errors.New("dataset_not_mounted"))
		close(failed)
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("recording the failure did not cancel the running job")
	}
	close(release)
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("recording the failure did not return")
	}
	if status := running.snapshot(); status.State != PermissionJobCancelled {
		t.Fatalf("the replaced job = %+v; want it cancelled, not left running", status)
	}
	if job := s.PermissionJob(5); job == nil || job.State != PermissionJobFailed || job.Error != "dataset_not_mounted" {
		t.Fatalf("current job = %+v; want the failure", job)
	}
}
