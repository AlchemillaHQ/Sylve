// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	networkAttachment "github.com/alchemillahq/sylve/internal/network/attachment"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	jailService "github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
)

func sortReplicationVMTargetProbe(probe *replicationVMTargetProbe) {
	if probe == nil {
		return
	}
	sort.Slice(probe.Switches, func(i, j int) bool {
		left := probe.Switches[i]
		right := probe.Switches[j]
		leftKey := left.Attachment.SwitchType + "\x00" + left.Attachment.SwitchName
		rightKey := right.Attachment.SwitchType + "\x00" + right.Attachment.SwitchName
		if leftKey == rightKey {
			return !left.IdentityOnly && right.IdentityOnly
		}
		return leftKey < rightKey
	})
}

func sortReplicationJailTargetProbe(probe *replicationJailTargetProbe) {
	if probe == nil {
		return
	}
	sort.Slice(probe.Networks, func(i, j int) bool {
		left := probe.Networks[i]
		right := probe.Networks[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		leftKey := left.Attachment.SwitchType + "\x00" + left.Attachment.SwitchName
		rightKey := right.Attachment.SwitchType + "\x00" + right.Attachment.SwitchName
		return leftKey < rightKey
	})
}

func replicationVMTargetNetworkCheckFromMetadata(
	raw []byte,
	expectedRID uint,
) (replicationTargetNetworkCheck, error) {
	if expectedRID == 0 {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_vm_guest_id")
	}

	var metadata vmModels.VM
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_replication_vm_metadata_json: %w", err)
	}
	if metadata.RID != expectedRID {
		return replicationTargetNetworkCheck{}, fmt.Errorf(
			"replication_vm_metadata_identity_mismatch: expected=%d actual=%d",
			expectedRID,
			metadata.RID,
		)
	}

	probe := replicationVMTargetProbe{RID: expectedRID}
	seen := make(map[string]int, len(metadata.Networks))
	for index, network := range metadata.Networks {
		contract, err := libvirt.VMNetworkAttachmentFromMetadata(network)
		if err != nil {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_vm_snapshot_network_%d_invalid: %w",
				index+1,
				err,
			)
		}
		identityOnly := !network.Enable
		entry := networkAttachment.NamedContract{
			Name:         contract.SwitchName,
			Attachment:   contract,
			IdentityOnly: identityOnly,
		}
		key := contract.SwitchType + "\x00" + contract.SwitchName
		if existingIndex, exists := seen[key]; exists {
			existing := probe.Switches[existingIndex]
			switch {
			case existing.IdentityOnly && !identityOnly:
				probe.Switches[existingIndex] = entry
			case !existing.IdentityOnly && identityOnly:
			default:
				if err := networkAttachment.Compare(existing.Attachment, contract); err != nil {
					return replicationTargetNetworkCheck{}, fmt.Errorf(
						"replication_vm_snapshot_switch_contract_conflict: %s: %w",
						contract.SwitchName,
						err,
					)
				}
			}
			continue
		}
		seen[key] = len(probe.Switches)
		probe.Switches = append(probe.Switches, entry)
	}
	sortReplicationVMTargetProbe(&probe)

	if len(probe.Switches) == 0 {
		return replicationTargetNetworkCheck{}, nil
	}
	return replicationTargetNetworkCheck{
		action:  "migration/check-vm-target",
		payload: probe,
	}, nil
}

func replicationJailTargetNetworkCheckFromMetadata(
	raw []byte,
	expectedCTID uint,
) (replicationTargetNetworkCheck, error) {
	if expectedCTID == 0 {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_jail_guest_id")
	}

	var metadata jailModels.Jail
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_replication_jail_metadata_json: %w", err)
	}
	if metadata.CTID != expectedCTID {
		return replicationTargetNetworkCheck{}, fmt.Errorf(
			"replication_jail_metadata_identity_mismatch: expected=%d actual=%d",
			expectedCTID,
			metadata.CTID,
		)
	}

	probe := replicationJailTargetProbe{
		Networks: make([]networkAttachment.NamedContract, 0, len(metadata.Networks)),
	}
	for index, network := range metadata.Networks {
		contract, err := jailService.NetworkAttachmentFromMetadata(network)
		if err != nil {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_jail_snapshot_network_%d_invalid: %w",
				index+1,
				err,
			)
		}
		probe.Networks = append(probe.Networks, networkAttachment.NamedContract{
			Name:       strings.TrimSpace(network.Name),
			Attachment: contract,
		})
	}
	sortReplicationJailTargetProbe(&probe)

	if len(probe.Networks) == 0 {
		return replicationTargetNetworkCheck{}, nil
	}
	return replicationTargetNetworkCheck{
		action:  "migration/check-jail-target",
		payload: probe,
	}, nil
}

func replicationTargetNetworkCheckFromMetadata(
	guestType string,
	guestID uint,
	raw []byte,
) (replicationTargetNetworkCheck, error) {
	switch strings.ToLower(strings.TrimSpace(guestType)) {
	case clusterModels.ReplicationGuestTypeVM:
		return replicationVMTargetNetworkCheckFromMetadata(raw, guestID)
	case clusterModels.ReplicationGuestTypeJail:
		return replicationJailTargetNetworkCheckFromMetadata(raw, guestID)
	default:
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_replication_guest_type")
	}
}

func replicationNetworkMetadataPath(guestType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(guestType)) {
	case clusterModels.ReplicationGuestTypeVM:
		return ".sylve/vm.json", nil
	case clusterModels.ReplicationGuestTypeJail:
		return ".sylve/jail.json", nil
	default:
		return "", fmt.Errorf("invalid_replication_guest_type")
	}
}

func replicationTargetNetworkCheckPayload(check replicationTargetNetworkCheck) ([]byte, error) {
	payload, err := json.Marshal(check.payload)
	if err != nil {
		return nil, fmt.Errorf("marshal_replication_snapshot_network_check: %w", err)
	}
	return payload, nil
}

func replicationTargetNetworkChecksEqual(
	left replicationTargetNetworkCheck,
	right replicationTargetNetworkCheck,
) (bool, error) {
	if left.action != right.action {
		return false, nil
	}
	leftPayload, err := replicationTargetNetworkCheckPayload(left)
	if err != nil {
		return false, err
	}
	rightPayload, err := replicationTargetNetworkCheckPayload(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftPayload, rightPayload), nil
}

func (s *Service) readReplicationSnapshotMetadata(
	ctx context.Context,
	dataset string,
	snapshotName string,
	relativePath string,
) (raw []byte, found bool, retErr error) {
	if s != nil && s.replicationSnapshotMetadataReader != nil {
		return s.replicationSnapshotMetadataReader(ctx, dataset, snapshotName, relativePath)
	}
	if s == nil {
		return nil, false, fmt.Errorf("zelta_service_unavailable")
	}
	dataset = normalizeDatasetPath(dataset)
	if dataset == "" {
		return nil, false, fmt.Errorf("source_dataset_required")
	}
	parsedSnapshot, err := remoteexec.ParseZFSSnapshotName(snapshotName)
	if err != nil {
		return nil, false, fmt.Errorf("invalid_snapshot_name: %w", err)
	}
	snapshotName = parsedSnapshot.String()

	mountedOut, mountpointOut, err := s.replicationSnapshotDatasetMountState(ctx, dataset)
	if err != nil {
		return nil, false, err
	}
	mountpoint := strings.TrimSpace(mountpointOut)
	if mountpoint == "" || mountpoint == "-" || mountpoint == "none" || mountpoint == "legacy" {
		return nil, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(mountedOut), "yes") {
		if err := s.mountLocalDataset(ctx, dataset); err != nil {
			return nil, false, fmt.Errorf("mount_replication_source_dataset: %w", err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				replicationControlDefaultTimeout,
			)
			defer cancel()
			if err := s.unmountLocalDatasetNormally(cleanupCtx, dataset); err != nil && !isLocalDatasetNotMountedError(err) {
				retErr = errors.Join(retErr, fmt.Errorf("unmount_replication_source_dataset: %w", err))
			}
		}()
	}

	metadataPath := filepath.Join(
		strings.TrimSuffix(mountpoint, "/"),
		".zfs",
		"snapshot",
		snapshotName,
		filepath.FromSlash(strings.TrimLeft(relativePath, "/")),
	)
	raw, err = os.ReadFile(metadataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read_replication_snapshot_metadata: %w", err)
	}
	return raw, true, nil
}

func (s *Service) replicationSnapshotDatasetMountState(
	ctx context.Context,
	dataset string,
) (string, string, error) {
	if s != nil && s.replicationSnapshotDatasetState != nil {
		return s.replicationSnapshotDatasetState(ctx, dataset)
	}
	mounted, err := s.runLocalZFSGet(ctx, "mounted", dataset)
	if err != nil {
		return "", "", fmt.Errorf("read_replication_snapshot_mounted_property: %w", err)
	}
	mountpoint, err := s.runLocalZFSGet(ctx, "mountpoint", dataset)
	if err != nil {
		return "", "", fmt.Errorf("read_replication_snapshot_mountpoint_property: %w", err)
	}
	return mounted, mountpoint, nil
}

func (s *Service) buildGuestTargetNetworkCheckFromSnapshot(
	ctx context.Context,
	guestType string,
	guestID uint,
	sourceDatasets []string,
	snapshotName string,
) (replicationTargetNetworkCheck, error) {
	if guestID == 0 {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_guest_id")
	}
	relativePath, err := replicationNetworkMetadataPath(guestType)
	if err != nil {
		return replicationTargetNetworkCheck{}, err
	}

	datasets := append([]string(nil), sourceDatasets...)
	sort.Strings(datasets)
	seenDatasets := make(map[string]struct{}, len(datasets))
	var (
		capturedCheck   replicationTargetNetworkCheck
		capturedPayload []byte
		metadataFound   bool
	)
	for _, dataset := range datasets {
		dataset = normalizeDatasetPath(dataset)
		if dataset == "" {
			return replicationTargetNetworkCheck{}, fmt.Errorf("source_dataset_required")
		}
		if _, seen := seenDatasets[dataset]; seen {
			continue
		}
		seenDatasets[dataset] = struct{}{}

		raw, found, err := s.readReplicationSnapshotMetadata(
			ctx,
			dataset,
			snapshotName,
			relativePath,
		)
		if err != nil {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_snapshot_metadata_read_failed: dataset=%s: %w",
				dataset,
				err,
			)
		}
		if !found {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_snapshot_network_metadata_not_found: dataset=%s path=%s",
				dataset,
				relativePath,
			)
		}

		check, err := replicationTargetNetworkCheckFromMetadata(
			guestType,
			guestID,
			raw,
		)
		if err != nil {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_snapshot_network_metadata_invalid: dataset=%s: %w",
				dataset,
				err,
			)
		}
		payload, err := replicationTargetNetworkCheckPayload(check)
		if err != nil {
			return replicationTargetNetworkCheck{}, err
		}
		if !metadataFound {
			capturedCheck = check
			capturedPayload = payload
			metadataFound = true
			continue
		}
		if capturedCheck.action != check.action || !bytes.Equal(capturedPayload, payload) {
			return replicationTargetNetworkCheck{}, fmt.Errorf(
				"replication_snapshot_network_metadata_mismatch: dataset=%s",
				dataset,
			)
		}
	}
	if !metadataFound {
		return replicationTargetNetworkCheck{}, fmt.Errorf(
			"replication_snapshot_network_metadata_not_found: %s",
			relativePath,
		)
	}
	return capturedCheck, nil
}

func (s *Service) buildReplicationTargetNetworkCheckFromSnapshot(
	ctx context.Context,
	policy *clusterModels.ReplicationPolicy,
	sourceDatasets []string,
	snapshotName string,
) (replicationTargetNetworkCheck, error) {
	if policy == nil || policy.ID == 0 {
		return replicationTargetNetworkCheck{}, fmt.Errorf("invalid_policy")
	}
	return s.buildGuestTargetNetworkCheckFromSnapshot(
		ctx,
		policy.GuestType,
		policy.GuestID,
		sourceDatasets,
		snapshotName,
	)
}

func (s *Service) validateBackupSnapshotNetworkMetadata(
	ctx context.Context,
	guestType string,
	guestID uint,
	sourceDatasets []string,
	snapshotName string,
) error {
	captured, err := s.buildGuestTargetNetworkCheckFromSnapshot(
		ctx, guestType, guestID, sourceDatasets, snapshotName,
	)
	if err != nil {
		return fmt.Errorf("backup_snapshot_network_metadata_invalid: %w", err)
	}
	current, err := s.buildGuestTargetNetworkCheck(guestType, guestID)
	if err != nil {
		return fmt.Errorf("backup_source_network_compatibility_recheck_failed: %w", err)
	}
	matches, err := replicationTargetNetworkChecksEqual(captured, current)
	if err != nil {
		return fmt.Errorf("backup_snapshot_network_metadata_compare_failed: %w", err)
	}
	if !matches {
		return fmt.Errorf("backup_snapshot_network_metadata_stale")
	}
	return nil
}

func (s *Service) cleanupRejectedBackupSnapshot(
	ctx context.Context,
	job *clusterModels.BackupJob,
	snapshotName string,
	scopes []backupScope,
) error {
	if job == nil || job.ID == 0 {
		return fmt.Errorf("backup_job_required")
	}
	parsedName, err := normalizeBackupSnapshotName(snapshotName)
	if err != nil || !backupSnapshotRequiresCommit(job.ID, parsedName) {
		return fmt.Errorf("rejected_backup_snapshot_name_invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		replicationControlDefaultTimeout,
	)
	defer cancel()

	var cleanupErr error
	for _, scope := range scopes {
		sourceDataset := normalizeDatasetPath(scope.sourceDataset)
		targetDataset := remoteActiveDatasetForSuffix(job.Target.BackupRoot, scope.destSuffix)
		if sourceDataset == "" || targetDataset == "" {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rejected_backup_snapshot_scope_invalid"))
			continue
		}

		if err := s.destroyRemoteSnapshotBestEffort(
			cleanupCtx,
			&job.Target,
			targetDataset,
			parsedName,
		); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
				"cleanup_rejected_backup_target_snapshot: source=%s: %w",
				sourceDataset,
				err,
			))
			continue
		}

		if err := s.destroyLocalDataset(
			cleanupCtx,
			sourceDataset+"@"+parsedName,
			true,
		); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
				"cleanup_rejected_backup_source_snapshot: source=%s: %w",
				sourceDataset,
				err,
			))
		}
	}
	return cleanupErr
}
