// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"strings"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var jailEpairRuntimeState = func(s *Service, ctID uint) (bool, error) {
	return s.IsJailRunning(ctID)
}

func (s *Service) jailEpairNames(jail jailModels.Jail) ([]string, error) {
	if s == nil || s.DB == nil || jail.ID == 0 || jail.CTID == 0 {
		return nil, fmt.Errorf("jail_epair_configuration_unavailable")
	}

	var networkIDs []uint
	if err := s.DB.
		Table((jailModels.Network{}).TableName()).
		Where("jid = ?", jail.ID).
		Order("id ASC").
		Pluck("id", &networkIDs).Error; err != nil {
		return nil, fmt.Errorf("load_jail_epair_network_ids: %w", err)
	}

	hash := utils.HashIntToNLetters(int(jail.CTID), 5)
	names := make([]string, 0, len(networkIDs))
	for _, networkID := range networkIDs {
		names = append(names, fmt.Sprintf("%s_net%d", hash, networkID))
	}
	return names, nil
}

func (s *Service) ensureJailEpairs(jail jailModels.Jail) error {
	if s == nil || s.NetworkService == nil {
		return fmt.Errorf("jail_epair_network_service_unavailable")
	}
	names, err := s.jailEpairNames(jail)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := s.NetworkService.EnsureEpair(name); err != nil {
			return fmt.Errorf("ensure_jail_epair_%s: %w", name, err)
		}
	}
	return nil
}

func (s *Service) verifyJailInactive(ctID uint) error {
	running, err := jailEpairRuntimeState(s, ctID)
	if err != nil {
		return fmt.Errorf("verify_jail_inactive: %w", err)
	}
	if running {
		return fmt.Errorf("jail_runtime_still_active")
	}
	return nil
}

func (s *Service) cleanupJailEpairsIfStopped(jail jailModels.Jail) error {
	if err := s.verifyJailInactive(jail.CTID); err != nil {
		return err
	}
	return s.cleanupJailEpairs(jail)
}

// cleanupJailEpairs attempts every configured pair. Missing pairs already
// satisfy the stopped-jail invariant; ownership and command failures remain
// visible to the caller without preventing cleanup of later pairs.
func (s *Service) cleanupJailEpairs(jail jailModels.Jail) error {
	if s == nil || s.NetworkService == nil {
		return fmt.Errorf("jail_epair_network_service_unavailable")
	}
	names, err := s.jailEpairNames(jail)
	if err != nil {
		return err
	}

	var cleanupErrs []error
	for _, name := range names {
		if err := s.NetworkService.DeleteEpair(name); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "not found") {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete_jail_epair_%s: %w", name, err))
		}
	}
	return errors.Join(cleanupErrs...)
}
