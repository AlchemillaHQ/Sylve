// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
)

const (
	PermissionJobRunning   = "running"
	PermissionJobCompleted = "completed"
	PermissionJobFailed    = "failed"
	PermissionJobCancelled = "cancelled"

	permissionJobBatchPaths = 256
	permissionJobBatchBytes = 192 << 10
)

type SharePermissionJob struct {
	State      string     `json:"state"`
	Processed  int64      `json:"processed"`
	Failed     int64      `json:"failed"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type permissionJob struct {
	mu     sync.Mutex
	status SharePermissionJob
	cancel context.CancelFunc
	done   chan struct{}
}

type permissionPlan struct {
	mountpoint  string
	dirEntries  []string
	fileEntries []string
	remove      []string
}

type permissionJobs struct {
	mu   sync.Mutex
	jobs map[uint]*permissionJob
}

func newPermissionPlan(mountpoint string, previous, desired sambaPrincipalNames, guestEnabled, guestWriteable bool) permissionPlan {
	plan := permissionPlan{mountpoint: mountpoint}
	previous = normalizeSambaPrincipalNames(previous)
	desired = normalizeSambaPrincipalNames(desired)
	add := func(principal, permission string) {
		plan.dirEntries = append(plan.dirEntries, fmt.Sprintf("%s:%s:fd:allow", principal, permission))
		plan.fileEntries = append(plan.fileEntries, fmt.Sprintf("%s:%s::allow", principal, permission))
	}
	if guestEnabled {
		add(guestACEName, sambaGuestPermission(guestWriteable))
	} else {
		for _, list := range []struct {
			tag, permission string
			names           []string
		}{
			{"u", readACLPerm, desired.ReadUsers},
			{"u", writeACLPerm, desired.WriteUsers},
			{"g", readACLPerm, desired.ReadGroups},
			{"g", writeACLPerm, desired.WriteGroups},
		} {
			for _, name := range list.names {
				add(list.tag+":"+name, list.permission)
			}
		}
	}

	kept := map[string]struct{}{}
	for _, name := range mergePrincipalNames(desired.ReadUsers, desired.WriteUsers) {
		kept["u:"+name] = struct{}{}
	}
	for _, name := range mergePrincipalNames(desired.ReadGroups, desired.WriteGroups) {
		kept["g:"+name] = struct{}{}
	}
	for tag, names := range map[string][]string{
		"u": mergePrincipalNames(previous.ReadUsers, previous.WriteUsers),
		"g": mergePrincipalNames(previous.ReadGroups, previous.WriteGroups),
	} {
		for _, name := range names {
			if _, stays := kept[tag+":"+name]; !stays {
				plan.remove = append(plan.remove, fmt.Sprintf("%s:%s:%s::allow", tag, name, writeACLPerm))
			}
		}
	}
	return plan
}

func (j *permissionJob) snapshot() SharePermissionJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *permissionJob) update(change func(*SharePermissionJob)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	change(&j.status)
}

func (s *Service) permissionJobRegistry() *permissionJobs {
	s.permissionJobsOnce.Do(func() {
		s.permissionJobs = &permissionJobs{jobs: map[uint]*permissionJob{}}
	})
	return s.permissionJobs
}

func (s *Service) PermissionJob(shareID uint) *SharePermissionJob {
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	job := registry.jobs[shareID]
	registry.mu.Unlock()
	if job == nil {
		return nil
	}
	status := job.snapshot()
	return &status
}

func (s *Service) CancelPermissionJob(shareID uint) bool {
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	job := registry.jobs[shareID]
	registry.mu.Unlock()
	if job == nil || job.snapshot().State != PermissionJobRunning {
		return false
	}
	job.cancel()
	<-job.done
	return true
}

func (s *Service) failPermissionJob(shareID uint, err error) {
	s.CancelPermissionJob(shareID)
	now := time.Now()
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.jobs[shareID] = &permissionJob{
		status: SharePermissionJob{State: PermissionJobFailed, StartedAt: now, FinishedAt: &now, Error: err.Error()},
		cancel: func() {},
		done:   closedChannel(),
	}
}

func closedChannel() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (s *Service) startPermissionJob(shareID uint, plan permissionPlan) *permissionJob {
	s.CancelPermissionJob(shareID)
	ctx, cancel := context.WithCancel(context.Background())
	job := &permissionJob{
		status: SharePermissionJob{State: PermissionJobRunning, StartedAt: time.Now()},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	registry.jobs[shareID] = job
	registry.mu.Unlock()
	go func() {
		defer cancel()
		runPermissionJob(ctx, job, plan)
	}()
	return job
}

func (s *Service) ApplyShareAccessToExisting(ctx context.Context, id uint, previous *sambaModels.SambaShare) error {
	defer s.waitForDirectoryConfiguration()()
	share, err := s.GetShare(id)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		s.failPermissionJob(id, err)
		return err
	}
	if !share.Enabled {
		return fail(fmt.Errorf("share_disabled"))
	}
	dataset, err := s.datasetByGUID(ctx, share.Dataset)
	if err != nil {
		return fail(fmt.Errorf("failed_to_fetch_dataset: %w", err))
	}
	if dataset == nil {
		return fail(fmt.Errorf("dataset_not_found"))
	}
	if !isSambaDatasetMounted(dataset) {
		return fail(fmt.Errorf("dataset_not_mounted"))
	}
	if err := s.requireShareableDataset(ctx, dataset); err != nil {
		return fail(err)
	}
	var previousNames sambaPrincipalNames
	if previous != nil && previous.Dataset == share.Dataset && !previous.GuestOk {
		previousNames = namesFromShareACLs(*previous)
	}
	var desired sambaPrincipalNames
	if !share.GuestOk {
		desired = namesFromShareACLs(share)
	}
	s.startPermissionJob(id, newPermissionPlan(dataset.Mountpoint, previousNames, desired, share.GuestOk, !share.ReadOnly))
	return nil
}

func (s *Service) forgetPermissionJob(shareID uint) {
	s.CancelPermissionJob(shareID)
	registry := s.permissionJobRegistry()
	registry.mu.Lock()
	delete(registry.jobs, shareID)
	registry.mu.Unlock()
}

func deviceOf(info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

func runPermissionJob(ctx context.Context, job *permissionJob, plan permissionPlan) {
	defer close(job.done)
	var lastErr error
	finish := func() {
		now := time.Now()
		job.update(func(status *SharePermissionJob) {
			status.FinishedAt = &now
			switch {
			case ctx.Err() != nil:
				status.State = PermissionJobCancelled
			case lastErr != nil && status.Failed >= status.Processed:
				status.State = PermissionJobFailed
				status.Error = lastErr.Error()
			default:
				status.State = PermissionJobCompleted
				if lastErr != nil {
					status.Error = lastErr.Error()
				}
			}
		})
	}
	defer finish()

	rootInfo, err := os.Lstat(plan.mountpoint)
	if err != nil {
		lastErr = err
		return
	}
	if !rootInfo.IsDir() {
		lastErr = fmt.Errorf("%s is not a directory", plan.mountpoint)
		return
	}
	rootDevice, ok := deviceOf(rootInfo)
	if !ok {
		lastErr = fmt.Errorf("could not read the device of %s", plan.mountpoint)
		return
	}

	var dirs, files []string
	batchBytes := 0
	apply := func(paths, entries []string) {
		if len(paths) == 0 {
			return
		}
		for _, entry := range plan.remove {
			_, _ = sambaRunCommand("/bin/setfacl", append([]string{"-h", "-x", entry}, paths...)...)
		}
		var failed int64
		if len(entries) > 0 {
			if _, err := sambaRunCommand("/bin/setfacl", append([]string{"-h", "-m", strings.Join(entries, ",")}, paths...)...); err != nil {
				failed = int64(len(paths))
				lastErr = err
			}
		}
		job.update(func(status *SharePermissionJob) {
			status.Processed += int64(len(paths))
			status.Failed += failed
		})
	}
	flush := func() {
		apply(dirs, plan.dirEntries)
		apply(files, plan.fileEntries)
		dirs, files, batchBytes = dirs[:0], files[:0], 0
	}

	walkErr := filepath.WalkDir(plan.mountpoint, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			job.update(func(status *SharePermissionJob) { status.Failed++ })
			lastErr = err
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == plan.mountpoint {
			return nil
		}
		switch mode := entry.Type(); {
		case mode&fs.ModeSymlink != 0:
			return nil
		case mode.IsDir():
			info, err := entry.Info()
			if err != nil {
				lastErr = err
				return fs.SkipDir
			}
			if device, ok := deviceOf(info); !ok || device != rootDevice {
				return fs.SkipDir
			}
			dirs = append(dirs, path)
		case mode.IsRegular():
			files = append(files, path)
		default:
			return nil
		}
		batchBytes += len(path) + 1
		if len(dirs)+len(files) >= permissionJobBatchPaths || batchBytes >= permissionJobBatchBytes {
			flush()
		}
		return nil
	})
	if ctx.Err() == nil {
		flush()
	}
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		lastErr = walkErr
	}
}
