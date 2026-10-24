// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package hostupdates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	releaseCacheTTL    = 5 * time.Minute
	releaseReadTimeout = 20 * time.Second
)

type ReleaseView struct {
	Sylve            sylveupdate.Installed  `json:"sylve"`
	ReleaseChannel   string                 `json:"releaseChannel"`
	Release          *sylveupdate.Candidate `json:"release"`
	ReleaseCheckedAt *time.Time             `json:"releaseCheckedAt"`
	ReleaseError     string                 `json:"releaseError"`
	ReleaseAvailable bool                   `json:"releaseAvailable"`
}

func releaseAvailable(state models.HostUpdateState, installed sylveupdate.Installed) bool {
	return state.Release != nil && state.ReleaseCheckedAt != nil && state.ReleaseError == "" &&
		state.Release.Channel == state.ReleaseChannel && sylveupdate.Allowed(installed, *state.Release) == nil
}

func (s *Service) GetRelease(ctx context.Context, refresh bool) (ReleaseView, error) {
	ctx, cancel := context.WithTimeout(ctx, releaseReadTimeout)
	defer cancel()
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if err := ctx.Err(); err != nil {
		return ReleaseView{}, err
	}
	state, err := s.state()
	if err != nil {
		return ReleaseView{}, err
	}
	current := s.installed(ctx, state)
	metadataOnly := current.Mode != "standalone" && current.Mode != "package" && current.Path != "" && current.Path != "/usr/local/sbin/sylve"
	if current.Mode != "standalone" && !metadataOnly {
		return ReleaseView{}, errors.New(current.Reason)
	}
	checkedAt := s.releaseAttemptAt
	if state.ReleaseCheckedAt != nil && state.ReleaseCheckedAt.After(checkedAt) {
		checkedAt = *state.ReleaseCheckedAt
	}
	age := time.Since(checkedAt)
	fresh := !checkedAt.IsZero() && age >= 0 && age < releaseCacheTTL &&
		(state.ReleaseCheckedAt != nil || state.ReleaseError != "") &&
		(state.Release == nil || state.Release.Channel == state.ReleaseChannel)
	if refresh || !fresh {
		if err := s.refreshRelease(ctx, state, current, metadataOnly); err != nil {
			return ReleaseView{}, err
		}
		state, err = s.state()
		if err != nil {
			return ReleaseView{}, err
		}
	}
	return ReleaseView{
		Sylve:            current,
		ReleaseChannel:   state.ReleaseChannel,
		Release:          state.Release,
		ReleaseCheckedAt: state.ReleaseCheckedAt,
		ReleaseError:     state.ReleaseError,
		ReleaseAvailable: releaseAvailable(state, current),
	}, nil
}

func (s *Service) checkRelease(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, releaseReadTimeout)
	defer cancel()
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.state()
	if err != nil {
		return err
	}
	return s.refreshRelease(ctx, state, s.installed(ctx, state), false)
}

func (s *Service) refreshRelease(ctx context.Context, state models.HostUpdateState, current sylveupdate.Installed, metadataOnly bool) (err error) {
	defer func() {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.releaseAttemptAt = time.Now().UTC()
		if err != nil {
			_ = s.DB.Model(&state).Update("release_error", pkgmanager.Redact(err.Error())).Error
		}
	}()
	if current.Mode == "package" || (current.Reason == "sylve_standalone_path_required" && !metadataOnly) {
		return s.DB.Model(&state).Select("Release", "ReleaseCheckedAt", "ReleaseError").Updates(&models.HostUpdateState{}).Error
	}
	if current.Mode != "standalone" && !metadataOnly {
		return errors.New(current.Reason)
	}
	if !metadataOnly && current.BuiltAt != "" && (state.InstalledDigest != current.SHA256 || state.InstalledBuiltAt != current.BuiltAt) {
		if err = s.DB.Model(&state).Updates(map[string]any{"installed_channel": current.Channel, "installed_digest": current.SHA256, "installed_built_at": current.BuiltAt}).Error; err != nil {
			return err
		}
	}
	currentVersion := current.Version
	if metadataOnly {
		currentVersion = ""
	}
	target, err := s.releases.Resolve(ctx, state.ReleaseChannel, currentVersion)
	if err != nil {
		if code(err) == "sylve_downgrade_forbidden" {
			now := time.Now().UTC()
			return s.DB.Model(&state).Select("Release", "ReleaseCheckedAt", "ReleaseError").Updates(&models.HostUpdateState{ReleaseCheckedAt: &now}).Error
		}
		return err
	}
	releaseError := ""
	var eligibilityErr error
	if !metadataOnly {
		userland, err := s.command(ctx, "/bin/freebsd-version", "-u")
		if err != nil {
			return err
		}
		eligibilityErr = sylveupdate.Allowed(current, target)
		if !sylveupdate.Compatible(userland, target.FreeBSD) {
			releaseError = "sylve_release_requires_newer_freebsd"
		} else if eligibilityErr != nil && code(eligibilityErr) != "sylve_already_current" && code(eligibilityErr) != "sylve_downgrade_forbidden" {
			releaseError = code(eligibilityErr)
		}
	}
	now := time.Now().UTC()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var previous models.HostUpdateState
		if err := tx.First(&previous, 1).Error; err != nil {
			return err
		}
		previousDigest := ""
		if previous.Release != nil {
			previousDigest = previous.Release.SHA256
		}
		if err := tx.Model(&previous).Select("Release", "ReleaseCheckedAt", "ReleaseError").Updates(&models.HostUpdateState{Release: &target, ReleaseCheckedAt: &now, ReleaseError: releaseError}).Error; err != nil {
			return err
		}
		if !metadataOnly && releaseError == "" && eligibilityErr == nil && previousDigest != target.SHA256 {
			return s.event(tx, "available", "Sylve update available", fmt.Sprintf("%s %s (%s) is available. Review the update before installation.", target.Channel, target.Version, target.Commit), "info", 0, "sylve")
		}
		return nil
	})
}

func (s *Service) releasePreview(ctx context.Context, op *models.HostUpdateOperation, state models.HostUpdateState) error {
	if err := s.phase(op, "checking_release"); err != nil {
		return err
	}
	current := s.installed(ctx, state)
	if current.Mode != "standalone" {
		return errors.New(current.Reason)
	}
	target, err := s.releases.Resolve(ctx, state.ReleaseChannel, current.Version)
	if err != nil {
		return err
	}
	if err = sylveupdate.Allowed(current, target); err != nil {
		return err
	}
	userland, err := s.command(ctx, "/bin/freebsd-version", "-u")
	if err != nil || !sylveupdate.Compatible(userland, target.FreeBSD) {
		return fmt.Errorf("sylve_release_requires_newer_freebsd")
	}
	plan := models.HostUpdatePlan{
		ID:             uuid.NewString(),
		Kind:           "sylve",
		Release:        &target,
		ExecutableHash: current.SHA256,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	if err = s.DB.Create(&plan).Error; err != nil {
		return err
	}
	op.PlanID = plan.ID
	return s.DB.Model(op).Update("plan_id", plan.ID).Error
}
