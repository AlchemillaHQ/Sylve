// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

const (
	restorePropertyRole        = "sylve:restore-role"
	restorePropertyOwner       = "sylve:restore-owner"
	restorePropertyDestination = "sylve:restore-destination"
	restorePropertyAttempt     = "sylve:restore-attempt"
	restoreRoleStaging         = "staging"
)

var restoreStagingPropertyNames = []string{
	restorePropertyRole,
	restorePropertyOwner,
	restorePropertyDestination,
	restorePropertyAttempt,
}

type restoreStagingIdentity struct {
	Owner       string
	Destination string
	Attempt     string
}

func newRestoreStagingIdentity(jobID *uint, targetID uint, destination string) restoreStagingIdentity {
	owner := ""
	if jobID != nil && *jobID > 0 {
		owner = fmt.Sprintf("job-%d", *jobID)
	} else {
		owner = fmt.Sprintf("target-%d", targetID)
	}

	return restoreStagingIdentity{
		Owner:       owner,
		Destination: normalizeRestoreDestinationDataset(destination),
		Attempt:     compactNowToken(),
	}
}

func (identity restoreStagingIdentity) receiveTopOptions() (string, error) {
	properties := identity.expectedProperties(true)
	if len(properties) != len(restoreStagingPropertyNames) {
		return "", fmt.Errorf("restore_staging_identity_incomplete")
	}

	parts := make([]string, 0, len(restoreStagingPropertyNames)*2)
	for _, property := range restoreStagingPropertyNames {
		value := strings.TrimSpace(properties[property])
		if value == "" || strings.ContainsAny(value, " \t\r\n") {
			return "", fmt.Errorf("restore_staging_property_value_invalid: property=%s", property)
		}
		parts = append(parts, "-o", property+"="+value)
	}
	return strings.Join(parts, " "), nil
}

func (identity restoreStagingIdentity) expectedProperties(requireAttempt bool) map[string]string {
	properties := map[string]string{
		restorePropertyRole:        restoreRoleStaging,
		restorePropertyOwner:       strings.TrimSpace(identity.Owner),
		restorePropertyDestination: normalizeRestoreDestinationDataset(identity.Destination),
	}
	if requireAttempt {
		properties[restorePropertyAttempt] = strings.TrimSpace(identity.Attempt)
	}
	return properties
}

type restoreZFSProperty struct {
	Value  string
	Source string
}

func (s *Service) readLocalRestoreProperties(
	ctx context.Context,
	dataset string,
) (map[string]restoreZFSProperty, error) {
	dataset = normalizeRestoreDestinationDataset(dataset)
	if dataset == "" {
		return nil, fmt.Errorf("restore_staging_dataset_required")
	}

	output, err := utils.RunCommandWithContext(
		ctx,
		"zfs", "get", "-H", "-o", "property,value,source",
		strings.Join(restoreStagingPropertyNames, ","),
		dataset,
	)
	if err != nil {
		return nil, fmt.Errorf("read_restore_staging_provenance_failed: %w", err)
	}

	properties := make(map[string]restoreZFSProperty, len(restoreStagingPropertyNames))
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("parse_restore_staging_provenance_failed: line=%q", line)
		}
		properties[strings.TrimSpace(fields[0])] = restoreZFSProperty{
			Value:  strings.TrimSpace(fields[1]),
			Source: strings.TrimSpace(fields[2]),
		}
	}
	return properties, nil
}

func restoreStagingPropertiesMatch(
	actual map[string]restoreZFSProperty,
	identity restoreStagingIdentity,
	requireExactAttempt bool,
) bool {
	expected := identity.expectedProperties(requireExactAttempt)
	for property, expectedValue := range expected {
		value, ok := actual[property]
		if !ok || value.Source != "local" || value.Value != expectedValue {
			return false
		}
	}

	attempt, ok := actual[restorePropertyAttempt]
	if !ok || attempt.Source != "local" || strings.TrimSpace(attempt.Value) == "" || attempt.Value == "-" {
		return false
	}
	return true
}

// prepareRestoreStagingDataset permits retry cleanup only when the existing
// dataset carries exact, locally-set Sylve restore provenance. A suffix or an
// inherited property is never ownership proof.
func (s *Service) prepareRestoreStagingDataset(
	ctx context.Context,
	dataset string,
	identity restoreStagingIdentity,
) error {
	dataset = normalizeRestoreDestinationDataset(dataset)
	exists, err := s.localDatasetExists(ctx, dataset)
	if err != nil {
		return fmt.Errorf("restore_staging_dataset_check_failed: %w", err)
	}
	if !exists {
		return nil
	}

	properties, err := s.readLocalRestoreProperties(ctx, dataset)
	if err != nil || !restoreStagingPropertiesMatch(properties, identity, false) {
		return fmt.Errorf(
			"restore_staging_dataset_exists_requires_manual_cleanup: dataset=%s",
			dataset,
		)
	}

	if err := s.destroyLocalDatasetWithRetry(ctx, dataset, true, 5, 250*time.Millisecond); err != nil {
		return fmt.Errorf("restore_owned_staging_cleanup_failed: dataset=%s: %w", dataset, err)
	}
	return nil
}

func (s *Service) cleanupOwnedRestoreStaging(
	ctx context.Context,
	dataset string,
	identity restoreStagingIdentity,
) error {
	dataset = normalizeRestoreDestinationDataset(dataset)
	exists, err := s.localDatasetExists(ctx, dataset)
	if err != nil {
		return fmt.Errorf("restore_staging_dataset_check_failed: %w", err)
	}
	if !exists {
		return nil
	}

	properties, err := s.readLocalRestoreProperties(ctx, dataset)
	if err != nil {
		return err
	}
	if !restoreStagingPropertiesMatch(properties, identity, true) {
		return fmt.Errorf("restore_staging_provenance_mismatch: dataset=%s", dataset)
	}
	if err := s.destroyLocalDatasetWithRetry(ctx, dataset, true, 5, 250*time.Millisecond); err != nil {
		return fmt.Errorf("restore_owned_staging_cleanup_failed: dataset=%s: %w", dataset, err)
	}
	return nil
}

func (s *Service) clearRestoreStagingProperties(
	ctx context.Context,
	dataset string,
	identity restoreStagingIdentity,
) error {
	properties, err := s.readLocalRestoreProperties(ctx, dataset)
	if err != nil {
		return err
	}
	if !restoreStagingPropertiesMatch(properties, identity, true) {
		return fmt.Errorf("restore_staging_provenance_mismatch_after_promotion: dataset=%s", dataset)
	}

	for _, property := range restoreStagingPropertyNames {
		if _, err := utils.RunCommandWithContext(ctx, "zfs", "inherit", property, dataset); err != nil {
			return fmt.Errorf("clear_restore_staging_property_failed: property=%s: %w", property, err)
		}
	}
	return nil
}

func restoreErrorWithCleanup(primary, cleanup error) error {
	if primary == nil {
		return cleanup
	}
	if cleanup == nil {
		return primary
	}
	return fmt.Errorf("%w; restore_cleanup_failed: %v", primary, cleanup)
}

func restoreRecoveryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func (s *Service) cleanupOwnedRestoreStagingAfterError(
	dataset string,
	identity restoreStagingIdentity,
	primary error,
) error {
	ctx, cancel := restoreRecoveryContext()
	defer cancel()
	return restoreErrorWithCleanup(primary, s.cleanupOwnedRestoreStaging(ctx, dataset, identity))
}

func (s *Service) rollbackRestorePromotionAfterError(
	destination string,
	backupDataset string,
	destinationExisted bool,
	primary error,
) error {
	if primary == nil {
		return nil
	}

	ctx, cancel := restoreRecoveryContext()
	defer cancel()

	var rollbackErr error
	if strings.TrimSpace(backupDataset) != "" {
		rollbackErr = s.rollbackPromotedDataset(ctx, destination, backupDataset)
	} else if !destinationExisted {
		rollbackErr = s.destroyLocalDatasetWithRetry(ctx, destination, true, 20, 250*time.Millisecond)
	} else {
		rollbackErr = fmt.Errorf("restore_backup_dataset_missing_after_promotion")
	}
	if rollbackErr != nil {
		return fmt.Errorf("%w; restore_rollback_failed: %v", primary, rollbackErr)
	}
	return primary
}

type restoredDatasetBackup struct {
	destination string
	backup      string
}

func (s *Service) rollbackRestoredDatasetBackups(entries []restoredDatasetBackup) error {
	ctx, cancel := restoreRecoveryContext()
	defer cancel()

	var rollbackErrors []error
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		var err error
		if strings.TrimSpace(entry.backup) == "" {
			// No archive means the root did not exist before its successful
			// promotion. Remove it on rollback in every restore mode.
			err = s.destroyLocalDatasetWithRetry(ctx, entry.destination, true, 20, 500*time.Millisecond)
		} else {
			err = s.rollbackPromotedDataset(ctx, entry.destination, entry.backup)
		}
		if err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf(
				"restore_dataset_rollback_failed: destination=%s backup=%s: %w",
				entry.destination,
				entry.backup,
				err,
			))
		}
	}
	return errors.Join(rollbackErrors...)
}

type restoreTargetGenerationSelection struct {
	ActiveDataset   string
	SelectedDataset string
}

type restoreTargetGenerationActivation struct {
	ActiveDataset   string
	SelectedDataset string
	ArchivedDataset string
	ActiveExisted   bool
	Activated       bool
}

func (activation restoreTargetGenerationActivation) changed() bool {
	return activation.Activated &&
		normalizeDatasetPath(activation.ActiveDataset) != "" &&
		normalizeDatasetPath(activation.SelectedDataset) != ""
}

func (s *Service) rollbackTargetGenerationForRestore(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	activation restoreTargetGenerationActivation,
) error {
	if !activation.changed() {
		return nil
	}

	active := normalizeDatasetPath(activation.ActiveDataset)
	selected := normalizeDatasetPath(activation.SelectedDataset)
	archived := normalizeDatasetPath(activation.ArchivedDataset)
	if active == "" || selected == "" || active == selected {
		return fmt.Errorf("restore_generation_rollback_paths_invalid")
	}
	if activation.ActiveExisted && archived == "" {
		return fmt.Errorf("restore_generation_rollback_archive_required")
	}

	exists := func(dataset string) (bool, error) {
		value, err := s.targetDatasetExists(ctx, target, dataset)
		if err != nil {
			return false, fmt.Errorf("restore_generation_rollback_topology_check_failed: dataset=%s: %w", dataset, err)
		}
		return value, nil
	}

	activeExists, err := exists(active)
	if err != nil {
		return err
	}
	selectedExists, err := exists(selected)
	if err != nil {
		return err
	}
	if !activation.ActiveExisted {
		switch {
		case !activeExists && selectedExists:
			// Already rolled back.
			return nil
		case activeExists && !selectedExists:
			if err := s.renameTargetDataset(ctx, target, active, selected); err != nil {
				return fmt.Errorf("restore_generation_rollback_selected_failed: %w", err)
			}
			return nil
		default:
			return fmt.Errorf(
				"restore_generation_rollback_topology_unexpected: active_exists=%t selected_exists=%t",
				activeExists,
				selectedExists,
			)
		}
	}

	archivedExists, err := exists(archived)
	if err != nil {
		return err
	}
	switch {
	case activeExists && selectedExists && !archivedExists:
		// Already rolled back.
		return nil
	case activeExists && !selectedExists && archivedExists:
		if err := s.renameTargetDataset(ctx, target, active, selected); err != nil {
			return fmt.Errorf("restore_generation_rollback_selected_failed: %w", err)
		}
		activeExists = false
		selectedExists = true
	case !activeExists && selectedExists && archivedExists:
		// Resume after active -> selected succeeded but archive -> active
		// failed (or its SSH result was interrupted).
	default:
		return fmt.Errorf(
			"restore_generation_rollback_topology_unexpected: active_exists=%t selected_exists=%t archive_exists=%t",
			activeExists,
			selectedExists,
			archivedExists,
		)
	}

	if err := s.renameTargetDataset(ctx, target, archived, active); err != nil {
		return fmt.Errorf("restore_generation_rollback_active_failed: %w", err)
	}
	return nil
}

func (s *Service) rollbackTargetGenerationActivations(
	target *clusterModels.BackupTarget,
	activations []restoreTargetGenerationActivation,
) error {
	ctx, cancel := restoreRecoveryContext()
	defer cancel()

	var rollbackErrors []error
	for idx := len(activations) - 1; idx >= 0; idx-- {
		if err := s.rollbackTargetGenerationForRestore(ctx, target, activations[idx]); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func normalizeRestoreGenerationSelections(
	selections []restoreTargetGenerationSelection,
) []restoreTargetGenerationSelection {
	seen := make(map[string]struct{}, len(selections))
	out := make([]restoreTargetGenerationSelection, 0, len(selections))
	for _, selection := range selections {
		selection.ActiveDataset = normalizeDatasetPath(selection.ActiveDataset)
		selection.SelectedDataset = normalizeDatasetPath(selection.SelectedDataset)
		if selection.ActiveDataset == "" || selection.SelectedDataset == "" {
			continue
		}
		key := selection.ActiveDataset + "\x00" + selection.SelectedDataset
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, selection)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ActiveDataset < out[j].ActiveDataset
	})
	return out
}

func (s *Service) activateTargetGenerationsForRestore(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	selections []restoreTargetGenerationSelection,
) ([]restoreTargetGenerationActivation, error) {
	selections = normalizeRestoreGenerationSelections(selections)
	activations := make([]restoreTargetGenerationActivation, 0, len(selections))
	for _, selection := range selections {
		activation, err := s.activateTargetGenerationForRestore(
			ctx,
			target,
			selection.ActiveDataset,
			selection.SelectedDataset,
		)
		if err != nil {
			rollbackActivations := activations
			if activation.changed() {
				rollbackActivations = append(append(
					[]restoreTargetGenerationActivation(nil),
					activations...,
				), activation)
			}
			rollbackErr := s.rollbackTargetGenerationActivations(target, rollbackActivations)
			if rollbackErr != nil {
				return nil, fmt.Errorf("activate_restore_generation_failed: %w; remote_rollback_failed: %v", err, rollbackErr)
			}
			return nil, fmt.Errorf("activate_restore_generation_failed: %w", err)
		}
		if activation.changed() {
			activations = append(activations, activation)
		}
	}
	return activations, nil
}
