// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var ErrApplyFailed = errors.New("nfs_configuration_saved_apply_pending")

type runtimeDriver interface {
	Available() bool
	Publish(context.Context, string) error
	Stop(context.Context, *nfsModels.NFSRuntime) error
	Start(context.Context, nfsModels.NFSConfig, *nfsModels.NFSRuntime, func() error) error
	Check(context.Context, nfsModels.NFSConfig, *nfsModels.NFSRuntime) error
	Running(context.Context, *nfsModels.NFSRuntime) (bool, error)
}

func exportsHash(exports string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(exports))) }

func configurationHash(config nfsModels.NFSConfig) string {
	data, _ := json.Marshal(config)
	return exportsHash(string(data))
}

func (s *Service) runtimeState(ctx context.Context) (nfsModels.NFSRuntime, error) {
	state := nfsModels.NFSRuntime{ID: 1, ApplyStatus: "stopped"}
	err := s.DB.WithContext(ctx).First(&state, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return state, err
}

func (s *Service) enabled(ctx context.Context) (bool, error) {
	var basic models.BasicSettings
	err := s.DB.WithContext(ctx).First(&basic).Error
	return slices.Contains(basic.Services, models.NFS), err
}

func (s *Service) Apply(ctx context.Context) error {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return s.apply(ctx)
}

// Reconcile applies changed saved intent, without restarting a healthy server
// for a policy-neutral edit. Explicit Apply and startup always force a fresh apply.
func (s *Service) Reconcile(ctx context.Context) error {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return s.reconcile(ctx)
}

func (s *Service) reconcile(ctx context.Context) error {
	// Intent may already be committed when the requesting client disconnects.
	// Read ownership independently so cancellation cannot bypass withdrawal.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	state, err := s.runtimeState(readCtx)
	if err != nil {
		return err
	}
	enabled, err := s.enabled(readCtx)
	if err != nil || !enabled && !state.Managed {
		return err
	}
	if !enabled {
		return s.applyPolicy(ctx, savedPolicy{}, nil, nil, state)
	}
	policy, err := s.readPolicy(readCtx)
	if err != nil {
		return err
	}
	return s.reconcilePolicy(ctx, policy, nil)
}

func (s *Service) reconcilePolicy(ctx context.Context, policy savedPolicy, known []*gzfs.Dataset) error {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	state, err := s.runtimeState(readCtx)
	if err != nil || !policy.enabled && !state.Managed {
		return err
	}
	if policy.enabled && state.Managed && (state.ApplyStatus == "applied" || state.ApplyStatus == "idle") && state.PolicyHash == policy.hash() {
		if observation := s.observationFor(state); !observation.checkedAt.IsZero() {
			return nil
		}
		// A stale observation is not grounds to silently accept changed identities.
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := s.verifyApplied(ctx, policy, state); err != nil {
			return s.failClosed(ctx, &state, err)
		}
		running := len(state.Filesystems) > 0
		s.observe(state, &running)
		return nil
	}
	return s.applyPolicy(ctx, policy, known, state.Filesystems, state)
}

// Caller holds mutationMu and, except for post-deletion cleanup, topologyMu.
func (s *Service) apply(ctx context.Context) error {
	state, err := s.runtimeState(ctx)
	if err != nil {
		return err
	}
	enabled, err := s.enabled(ctx)
	if err != nil {
		return err
	}
	if !enabled && !state.Managed {
		return nil
	}
	policy := savedPolicy{}
	if enabled {
		policy, err = s.readPolicy(ctx)
		if err != nil {
			return s.failClosed(ctx, &state, err)
		}
	}
	return s.applyPolicy(ctx, policy, nil, nil, state)
}

func (s *Service) applyPolicy(ctx context.Context, policy savedPolicy, known []*gzfs.Dataset, hints []nfsModels.ExportedFilesystem, state nfsModels.NFSRuntime) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	s.beginRuntimeChange()
	state.Managed, state.ApplyStatus, state.LastError = true, "applying", ""
	checkpoint := func() error { return s.DB.WithContext(ctx).Save(&state).Error }
	fail := func(cause error) error { return s.failClosed(ctx, &state, cause) }
	if err := checkpoint(); err != nil {
		return fail(err)
	}
	// A full restart deliberately trades continuity for unambiguous revocation.
	// Never use a successful SIGHUP or showmount result as proof of policy.
	if err := s.driver.Stop(ctx, &state); err != nil {
		return fail(err)
	}
	if err := s.driver.Publish(ctx, exportsMarker+"\n"); err != nil {
		return fail(err)
	}
	state.Hash, state.Filesystems = exportsHash(exportsMarker+"\n"), nil
	state.BindAddresses = nil
	enabled, err := s.enabled(ctx)
	if err != nil {
		return fail(err)
	}
	if enabled != policy.enabled {
		if enabled {
			policy, err = s.readPolicy(ctx)
			if err != nil {
				return fail(err)
			}
		} else {
			policy.enabled = false
		}
	}
	if !policy.enabled {
		state.Managed, state.ApplyStatus = false, "stopped"
		if err := checkpoint(); err != nil {
			return fail(err)
		}
		running := false
		s.observe(state, &running)
		return nil
	}
	plan, err := s.buildExportPlan(ctx, policy, known, hints)
	if err != nil {
		return fail(err)
	}
	config := policy.config
	if err := s.driver.Publish(ctx, plan.Exports); err != nil {
		return fail(err)
	}
	state.Hash, state.Filesystems = exportsHash(plan.Exports), plan.filesystems
	state.ConfigHash = configurationHash(config)
	state.PolicyHash = policy.hash()
	state.KerberosHash, state.Mappings = plan.kerberosHash, plan.mappings
	state.ApplyStatus = "starting"
	if err := checkpoint(); err != nil {
		return fail(err)
	}
	if enabled, err := s.enabled(ctx); err != nil {
		return fail(err)
	} else if !enabled {
		return s.apply(ctx)
	}
	if len(plan.filesystems) > 0 {
		if err := s.driver.Start(ctx, config, &state, checkpoint); err != nil {
			return fail(err)
		}
	}
	if err := s.driver.Check(ctx, config, &state); err != nil {
		return fail(err)
	}
	if enabled, err := s.enabled(ctx); err != nil {
		return fail(err)
	} else if !enabled {
		return s.apply(ctx)
	}
	state.ApplyStatus = "applied"
	if len(plan.filesystems) == 0 {
		state.ApplyStatus = "idle"
	}
	if err := checkpoint(); err != nil {
		return fail(err)
	}
	running := len(plan.filesystems) > 0
	s.observe(state, &running)
	return nil
}

func (s *Service) failClosed(ctx context.Context, state *nfsModels.NFSRuntime, cause error) error {
	// Client cancellation must not cancel revocation or leave stale exports on disk.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	s.beginRuntimeChange()
	stopErr := s.driver.Stop(ctx, state)
	writeErr := s.driver.Publish(ctx, exportsMarker+"\n")
	if stopErr == nil {
		state.Filesystems = nil
	}
	if writeErr == nil {
		state.Hash = exportsHash(exportsMarker + "\n")
	}
	state.ApplyStatus = "pending"
	if stopErr == nil && writeErr == nil {
		if enabled, err := s.enabled(ctx); err == nil && !enabled {
			state.Managed, state.ApplyStatus = false, "stopped"
		}
	}
	state.LastError = errors.Join(cause, stopErr, writeErr).Error()
	persistErr := s.DB.WithContext(ctx).Save(state).Error
	var running *bool
	if stopErr == nil {
		stopped := false
		running = &stopped
	} else if active, err := s.driver.Running(ctx, state); err == nil {
		running = &active
	}
	s.observe(*state, running)
	return fmt.Errorf("%w: %w", ErrApplyFailed, errors.Join(errors.New(state.LastError), persistErr))
}

// GuardStorageMutation fences topology changes against apply. Initial runtime
// support requires an explicit service disable before storage topology changes;
// it does not silently interrupt clients and later republish their old grants.
// Lock order: system settings / ZFS sync -> topologyMu -> mutationMu.
func (s *Service) GuardStorageMutation(ctx context.Context) (func(), error) {
	// Storage callers may already hold a pool lease. Never wait for another
	// storage operation holding this fence and waiting for that same lease.
	if !s.topologyMu.TryLock() {
		return nil, conflict("dataset", "NFS or another storage topology change is in progress; retry after it finishes.")
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	state, err := s.runtimeState(ctx)
	if err == nil && state.Managed {
		var enabled bool
		enabled, err = s.enabled(ctx)
		var count int64
		if err == nil {
			err = s.DB.WithContext(ctx).Model(&nfsModels.NFSShare{}).Where("enabled = ?", true).Count(&count).Error
		}
		if err == nil && (enabled && count > 0 || len(state.Filesystems) > 0 || state.NFSd.PID != 0 || state.Mountd.PID != 0) {
			err = conflict("dataset", "Disable NFS and successfully withdraw its exports before changing storage topology.")
		}
	}
	if err != nil {
		s.topologyMu.Unlock()
		return nil, err
	}
	return s.topologyMu.Unlock, nil
}

// Monitor only withdraws drifted policy. Restart/retry is explicit, preventing an
// external topology change from automatically publishing unintended access.
func (s *Service) Monitor(ctx context.Context) {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.checkRuntime(ctx); err != nil {
				logger.L.Error().Err(err).Msg("nfs_runtime_reconciliation_failed")
			}
		}
	}
}

func (s *Service) checkRuntime(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !s.topologyMu.TryLock() {
		return nil
	}
	if !s.mutationMu.TryLock() {
		s.topologyMu.Unlock()
		return nil
	}
	locked := true
	defer func() {
		if locked {
			s.mutationMu.Unlock()
			s.topologyMu.Unlock()
		}
	}()
	state, err := s.runtimeState(ctx)
	if err != nil || !state.Managed {
		return err
	}
	enabled, err := s.enabled(ctx)
	if err != nil {
		return s.failClosed(ctx, &state, err)
	}
	if !enabled {
		return s.apply(ctx)
	}
	if state.ApplyStatus == "pending" && (state.NFSd.PID != 0 || state.Mountd.PID != 0 || len(state.Filesystems) > 0) {
		return s.failClosed(ctx, &state, errors.New("NFS export withdrawal remains pending"))
	}
	if state.ApplyStatus != "applied" && state.ApplyStatus != "idle" {
		return nil
	}
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return s.failClosed(ctx, &state, err)
	}
	revision := s.revision
	s.mutationMu.Unlock()
	s.topologyMu.Unlock()
	locked = false
	checkErr := s.verifyApplied(ctx, policy, state)
	// Host reads run outside the mutation fence. A concurrent save/apply wins;
	// only a result for the unchanged runtime may update health or withdraw it.
	if !s.topologyMu.TryLock() {
		return nil
	}
	if !s.mutationMu.TryLock() {
		s.topologyMu.Unlock()
		return nil
	}
	locked = true
	if s.revision != revision {
		return nil
	}
	current, err := s.runtimeState(ctx)
	if err != nil {
		return s.failClosed(ctx, &state, err)
	}
	if runtimeIdentity(current) != runtimeIdentity(state) {
		return nil
	}
	if enabled, err := s.enabled(ctx); err != nil {
		return s.failClosed(ctx, &state, err)
	} else if enabled != policy.enabled {
		return nil
	}
	if checkErr != nil {
		return s.failClosed(ctx, &state, checkErr)
	}
	running := len(state.Filesystems) > 0
	s.observe(state, &running)
	return nil
}

func (s *Service) verifyApplied(ctx context.Context, policy savedPolicy, state nfsModels.NFSRuntime) error {
	if state.PolicyHash != policy.hash() || state.ConfigHash != configurationHash(policy.config) {
		return errors.New("saved NFS policy no longer matches the applied configuration")
	}
	if err := s.checkFilesystems(ctx, state.Filesystems, policy.pools); err != nil {
		return err
	}
	_, mappings, err := s.resolveMappings(ctx, policy.shares)
	if err != nil {
		return err
	}
	if !slices.Equal(mappings, state.Mappings) {
		return errors.New("NFS mapping credentials no longer match the applied configuration")
	}
	kerberosHash, err := s.kerberosState(ctx, policy.config, policy.shares)
	if err != nil {
		return err
	}
	if kerberosHash != state.KerberosHash {
		return errors.New("NFS Kerberos credentials no longer match the applied configuration")
	}
	return s.driver.Check(ctx, policy.config, &state)
}
