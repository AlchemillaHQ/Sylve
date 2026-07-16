// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	"github.com/alchemillahq/sylve/internal/logger"
	clusterService "github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) JailAction(ctId int, action string) error {
	return s.jailAction(ctId, action, "")
}

// JailActionForReplication is the transition engine's narrowly scoped action
// path. The persisted transition run ID is checked by the cluster ownership
// guard; callers cannot bypass the durable lock with node identity alone.
func (s *Service) JailActionForReplication(ctId int, action, transitionRunID string) error {
	transitionRunID = strings.TrimSpace(transitionRunID)
	if transitionRunID == "" {
		return fmt.Errorf("replication_transition_run_id_required")
	}
	return s.jailAction(ctId, action, transitionRunID)
}

func (s *Service) jailAction(ctId int, action, transitionRunID string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("invalid_action: %s", action)
	}

	allowed, err := s.canMutateProtectedJailForAction(uint(ctId), transitionRunID, action)
	if err != nil {
		return fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("replication_lease_not_owned")
	}

	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()
	// A transition may be committed while an action waits for serialization.
	// Re-check under the action mutex so no pre-authorized operation can race
	// the transition's persisted runtime-state capture.
	allowed, err = s.canMutateProtectedJailForAction(uint(ctId), transitionRunID, action)
	if err != nil {
		return fmt.Errorf("replication_lease_recheck_failed: %w", err)
	}
	if !allowed {
		return fmt.Errorf("replication_lease_not_owned")
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed to get jails path: %w", err)
	}

	jailConf := fmt.Sprintf("%s/%d/%d.conf", jailsPath, ctId, ctId)

	var jail jailModels.Jail
	if err := s.DB.First(&jail, "ct_id = ?", ctId).Error; err != nil {
		return fmt.Errorf("failed to find jail with ct_id %d: %w", ctId, err)
	}

	jailName := s.GetCTIDHash(jail.CTID)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("jail", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	now := time.Now().UTC()

	ensureNetworkReady := func() error {
		var jailWithNetworks jailModels.Jail
		if err := s.DB.Preload("Networks").Where("ct_id = ?", ctId).First(&jailWithNetworks).Error; err != nil {
			return fmt.Errorf("failed to load jail networks before start: %w", err)
		}

		if err := s.SyncNetwork(uint(ctId), jailWithNetworks); err != nil {
			return fmt.Errorf("failed to sync jail network before start: %w", err)
		}

		return nil
	}

	emitWithFreshState := func(reason string) {
		if _, refreshErr := s.refreshLiveStates(); refreshErr != nil {
			logger.L.Warn().
				Err(refreshErr).
				Int("ct_id", ctId).
				Str("action", action).
				Msg("failed_to_refresh_jail_live_states_after_action")
		}

		s.emitLeftPanelRefresh(reason)
	}

	switch action {
	case "start":
		active, err := s.IsJailActive(uint(ctId))
		if err != nil {
			return fmt.Errorf("failed to check if jail is active: %w", err)
		}

		if active {
			return nil
		}

		if err := ensureNetworkReady(); err != nil {
			return err
		}

		if out, err := run("-v", "-f", jailConf, "-c", jailName); err != nil {
			return fmt.Errorf("failed to start jail %s: %v\n%s", jailName, err, out)
		}
		jail.StartedAt = &now
		jail.StoppedAt = nil
		jail.IntentionallyStopped = false
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		emitWithFreshState(fmt.Sprintf("jail_start_%d", ctId))
		return nil

	case "stop":
		if out, err := run("-f", jailConf, "-r", jailName); err != nil {
			if !strings.Contains(out, "not found") && !strings.Contains(out, "No such process") {
				return fmt.Errorf("failed to stop jail %s: %v\n%s", jailName, err, out)
			}
		}
		jail.StoppedAt = &now
		jail.IntentionallyStopped = true
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		emitWithFreshState(fmt.Sprintf("jail_stop_%d", ctId))
		return nil

	case "restart":
		if out, err := run("-f", jailConf, "-r", jailName); err != nil {
			if !strings.Contains(out, "not found") && !strings.Contains(out, "No such process") {
				return fmt.Errorf("failed to stop jail %s: %v\n%s", jailName, err, out)
			}
		}

		if err := ensureNetworkReady(); err != nil {
			return err
		}

		if out, err := run("-f", jailConf, "-c", jailName); err != nil {
			return fmt.Errorf("failed to start jail %s: %v\n%s", jailName, err, out)
		}
		jail.StartedAt = &now
		jail.StoppedAt = nil
		jail.IntentionallyStopped = false
		if err := s.DB.Save(&jail).Error; err != nil {
			return fmt.Errorf("failed to update jail status: %w", err)
		}
		emitWithFreshState(fmt.Sprintf("jail_restart_%d", ctId))
		return nil
	}

	return nil
}

// ReplicationJailRuntimeStateForTransition drains admitted jail actions and
// samples state only after revalidating the exact persisted transition run.
func (s *Service) ReplicationJailRuntimeStateForTransition(ctID uint, transitionRunID string) (bool, error) {
	transitionRunID = strings.TrimSpace(transitionRunID)
	if ctID == 0 || transitionRunID == "" {
		return false, fmt.Errorf("replication_transition_runtime_state_input_invalid")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()
	allowed, err := s.canMutateProtectedJailForTransition(ctID, transitionRunID)
	if err != nil {
		return false, fmt.Errorf("replication_lease_check_failed: %w", err)
	}
	if !allowed {
		return false, fmt.Errorf("replication_lease_not_owned")
	}
	return s.IsJailRunning(ctID)
}

func (s *Service) ForceStopJail(ctID uint) error {
	ctIDInt := int(ctID)
	if ctIDInt <= 0 {
		return fmt.Errorf("invalid_ct_id")
	}
	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed to get jails path: %w", err)
	}

	jailConf := fmt.Sprintf("%s/%d/%d.conf", jailsPath, ctIDInt, ctIDInt)

	var jail jailModels.Jail
	if err := s.DB.First(&jail, "ct_id = ?", ctIDInt).Error; err != nil {
		if strings.Contains(err.Error(), "record not found") {
			return nil
		}
		return fmt.Errorf("failed to find jail: %w", err)
	}

	jailName := s.GetCTIDHash(jail.CTID)

	run := func(args ...string) (string, error) {
		cmd := exec.Command("jail", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run("-f", jailConf, "-r", jailName); err != nil {
		if !strings.Contains(out, "not found") && !strings.Contains(out, "No such process") {
			return fmt.Errorf("failed to force stop jail %s: %v\n%s", jailName, err, out)
		}
	}

	now := time.Now().UTC()
	jail.StoppedAt = &now
	jail.IntentionallyStopped = true
	if err := s.DB.Save(&jail).Error; err != nil {
		return fmt.Errorf("failed to update jail status: %w", err)
	}

	s.emitLeftPanelRefresh(fmt.Sprintf("jail_stop_%d", ctIDInt))

	return nil
}

const emergencyJailRuntimeFenceMaxPasses = 3

type emergencyJailRuntimeOps interface {
	List(context.Context) (map[string]int, error)
	Stop(context.Context, string) error
}

type hostEmergencyJailRuntimeOps struct {
	service *Service
}

func (o hostEmergencyJailRuntimeOps) List(ctx context.Context) (map[string]int, error) {
	if o.service == nil {
		return nil, fmt.Errorf("jail_service_unavailable")
	}
	return o.service.readJIDsByNameContext(ctx)
}

func (o hostEmergencyJailRuntimeOps) Stop(ctx context.Context, jailName string) error {
	cmd := exec.CommandContext(ctx, "/usr/sbin/jail", "-r", jailName)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return fmt.Errorf(
		"jail_remove_%s_failed: %w: %s",
		jailName,
		err,
		strings.TrimSpace(string(output)),
	)
}

func managedJailRuntimeCTID(jailName string) (uint, bool) {
	rawName := jailName
	jailName = strings.TrimSpace(jailName)
	if rawName != jailName {
		return 0, false
	}
	if len(jailName) != 5 {
		return 0, false
	}
	value := 0
	for i := 0; i < len(jailName); i++ {
		if jailName[i] < 'a' || jailName[i] > 'z' {
			return 0, false
		}
		value = value*26 + int(jailName[i]-'a')
	}
	if value < 1 || value > 9999 {
		return 0, false
	}
	if utils.HashIntToNLetters(value, 5) != jailName {
		return 0, false
	}
	return uint(value), true
}

func matchingJailRuntimeNames(
	runtimes map[string]int,
	matches func(string) bool,
) []string {
	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		if matches(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func emergencyStopJailRuntimesWithOps(
	ctx context.Context,
	ops emergencyJailRuntimeOps,
	matches func(string) bool,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ops == nil || matches == nil {
		return fmt.Errorf("emergency_jail_runtime_ops_unavailable")
	}

	var stopErrs []error
	for pass := 0; pass < emergencyJailRuntimeFenceMaxPasses; pass++ {
		runtimes, err := ops.List(ctx)
		if err != nil {
			return errors.Join(
				errors.Join(stopErrs...),
				fmt.Errorf("list_managed_jail_runtimes_failed: %w", err),
			)
		}
		names := matchingJailRuntimeNames(runtimes, matches)
		if len(names) == 0 {
			// The verified runtime state is authoritative. A remove command may
			// have raced an independent stop and returned an error after exit.
			return nil
		}
		for _, name := range names {
			if err := ops.Stop(ctx, name); err != nil {
				stopErrs = append(stopErrs, fmt.Errorf("stop_managed_jail_runtime_%s_failed: %w", name, err))
			}
		}
	}

	runtimes, err := ops.List(ctx)
	if err != nil {
		return errors.Join(
			errors.Join(stopErrs...),
			fmt.Errorf("verify_managed_jail_runtimes_stopped_failed: %w", err),
		)
	}
	remaining := matchingJailRuntimeNames(runtimes, matches)
	if len(remaining) == 0 {
		return nil
	}
	return errors.Join(
		errors.Join(stopErrs...),
		fmt.Errorf("managed_jail_runtimes_still_active: %s", strings.Join(remaining, ",")),
	)
}

// EmergencyStopJailRuntime is deliberately independent of the application
// database. It serializes with normal jail actions and verifies the exact jail
// is absent instead of trusting command error text.
func (s *Service) EmergencyStopJailRuntime(ctID uint) error {
	if s == nil || ctID == 0 || ctID > 9999 {
		return fmt.Errorf("invalid_ct_id")
	}
	jailName := s.GetCTIDHash(ctID)

	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	return emergencyStopJailRuntimesWithOps(
		context.Background(),
		hostEmergencyJailRuntimeOps{service: s},
		func(name string) bool { return name == jailName },
	)
}

// EmergencyStopAllManagedJails is the database-independent host-runtime
// fail-stop used when replication policy state cannot be read. Sylve jail names
// are a reversible encoding of the supported CTID range, so discovery requires
// no application metadata.
func (s *Service) EmergencyStopAllManagedJails(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("jail_service_unavailable")
	}

	s.actionMutex.Lock()
	defer s.actionMutex.Unlock()

	return emergencyStopJailRuntimesWithOps(
		ctx,
		hostEmergencyJailRuntimeOps{service: s},
		func(name string) bool {
			_, ok := managedJailRuntimeCTID(name)
			return ok
		},
	)
}

func (s *Service) canMutateProtectedJail(ctID uint) (bool, error) {
	return s.canMutateProtectedJailForTransition(ctID, "")
}

func (s *Service) jailRestoreInProgress(ctID uint) (bool, error) {
	if s == nil || s.DB == nil || !replicationguard.GuestOperationSchemaReady(s.DB) {
		return false, nil
	}
	var operation clusterModels.ReplicationGuestOperation
	result := s.DB.Where("guest_type = ? AND guest_id = ?", clusterModels.ReplicationGuestTypeJail, ctID).
		Limit(1).
		Find(&operation)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected != 0 && operation.Operation == clusterModels.ReplicationGuestOperationRestore, nil
}

func (s *Service) canMutateProtectedJailForTransition(ctID uint, transitionRunID string) (bool, error) {
	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(transitionRunID) != "" {
		return clusterService.CanNodeMutateProtectedGuestForTransition(
			s.DB,
			clusterModels.ReplicationGuestTypeJail,
			ctID,
			strings.TrimSpace(nodeID),
			transitionRunID,
		)
	}
	return clusterService.CanNodeMutateProtectedGuest(
		s.DB,
		clusterModels.ReplicationGuestTypeJail,
		ctID,
		strings.TrimSpace(nodeID),
	)
}

func (s *Service) canMutateProtectedJailForAction(ctID uint, transitionRunID, action string) (bool, error) {
	if strings.TrimSpace(transitionRunID) != "" {
		return s.canMutateProtectedJailForTransition(ctID, transitionRunID)
	}
	nodeID, err := utils.GetSystemUUID()
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(action) {
	case "stop":
		return clusterService.CanNodeStopGuestForMigration(
			s.DB,
			clusterModels.ReplicationGuestTypeJail,
			ctID,
			strings.TrimSpace(nodeID),
		)
	case "start":
		return clusterService.CanNodeStartProtectedGuest(
			s.DB,
			clusterModels.ReplicationGuestTypeJail,
			ctID,
			strings.TrimSpace(nodeID),
		)
	default:
		return s.canMutateProtectedJailForTransition(ctID, "")
	}
}

func (s *Service) CanMutateProtectedJail(ctID uint) (bool, error) {
	return s.canMutateProtectedJail(ctID)
}
