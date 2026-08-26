// SPDX-License-Identifier: BSD-2-Clause

package zelta

import (
	"fmt"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/remoteexec"
)

func canonicalizeBackupTarget(
	target *clusterModels.BackupTarget,
) (remoteexec.SSHDestination, remoteexec.ZFSDataset, error) {
	if target == nil {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("backup_target_required")
	}
	destination, err := remoteexec.ParseSSHDestination(target.SSHHost)
	if err != nil {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_ssh_host: %w", err)
	}
	root, err := remoteexec.ParseZFSDataset(target.BackupRoot)
	if err != nil {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_backup_root: %w", err)
	}
	if target.SSHPort < 0 || target.SSHPort > 65535 {
		return remoteexec.SSHDestination{}, remoteexec.ZFSDataset{}, fmt.Errorf("invalid_ssh_port")
	}
	target.SSHHost = destination.String()
	target.BackupRoot = root.String()
	return destination, root, nil
}

func canonicalZeltaEndpoint(target *clusterModels.BackupTarget, suffix string) (string, error) {
	destination, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return "", err
	}
	dataset, err := remoteexec.JoinZFSDataset(root, suffix)
	if err != nil {
		return "", fmt.Errorf("invalid_target_dataset: %w", err)
	}
	return destination.ZeltaString() + ":" + dataset.String(), nil
}

func canonicalZeltaSnapshotEndpoint(
	target *clusterModels.BackupTarget,
	datasetRaw, snapshotRaw string,
) (string, error) {
	destination, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return "", err
	}
	dataset, err := remoteexec.ParseZFSDataset(datasetRaw)
	if err != nil || !dataset.Within(root) {
		return "", fmt.Errorf("remote_dataset_invalid")
	}
	snapshot, err := remoteexec.ParseZFSSnapshotName(snapshotRaw)
	if err != nil {
		return "", fmt.Errorf("snapshot_invalid: %w", err)
	}
	return destination.ZeltaString() + ":" + dataset.String() + snapshot.WithAt(), nil
}

func canonicalReplicationTransferValues(
	target *clusterModels.BackupTarget,
	sourceRaw, suffix string,
) (remoteexec.ZFSDataset, remoteexec.ZFSDataset, error) {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return remoteexec.ZFSDataset{}, remoteexec.ZFSDataset{}, err
	}
	source, err := remoteexec.ParseZFSDataset(sourceRaw)
	if err != nil {
		return remoteexec.ZFSDataset{}, remoteexec.ZFSDataset{}, fmt.Errorf("source_dataset_invalid: %w", err)
	}
	destination, err := remoteexec.JoinZFSDataset(root, suffix)
	if err != nil || destination.String() == root.String() {
		return remoteexec.ZFSDataset{}, remoteexec.ZFSDataset{}, fmt.Errorf("replication_target_dataset_invalid")
	}
	return source, destination, nil
}

func canonicalTargetDataset(
	target *clusterModels.BackupTarget,
	raw string,
) (remoteexec.ZFSDataset, error) {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return remoteexec.ZFSDataset{}, err
	}
	dataset, err := remoteexec.ParseZFSDataset(raw)
	if err != nil || !dataset.Within(root) {
		return remoteexec.ZFSDataset{}, fmt.Errorf("dataset_outside_backup_root")
	}
	return dataset, nil
}

func canonicalZFSProperties(properties map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(properties))
	for rawName, rawValue := range properties {
		name, err := remoteexec.ParseZFSPropertyName(rawName)
		if err != nil {
			return nil, err
		}
		value, err := remoteexec.ParseZFSPropertyValue(rawValue)
		if err != nil {
			return nil, err
		}
		if _, exists := result[name.String()]; exists {
			return nil, fmt.Errorf("duplicate_zfs_property: property=%s", name.String())
		}
		result[name.String()] = value.String()
	}
	return result, nil
}

func canonicalZFSPropertyAssignments(assignments []string) ([]string, error) {
	result := make([]string, 0, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for _, assignment := range assignments {
		rawName, rawValue, ok := strings.Cut(assignment, "=")
		if !ok {
			return nil, fmt.Errorf("invalid_zfs_property_assignment")
		}
		name, err := remoteexec.ParseZFSPropertyName(rawName)
		if err != nil {
			return nil, err
		}
		value, err := remoteexec.ParseZFSPropertyValue(rawValue)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[name.String()]; exists {
			return nil, fmt.Errorf("duplicate_zfs_property: property=%s", name.String())
		}
		seen[name.String()] = struct{}{}
		result = append(result, name.String()+"="+value.String())
	}
	return result, nil
}
