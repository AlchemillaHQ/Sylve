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
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var SSHKeyDirectory string

func GetSSHKeyDir() (string, error) {
	if SSHKeyDirectory != "" {
		return SSHKeyDirectory, nil
	}

	data, err := config.GetDataPath()
	if err != nil {
		return "", fmt.Errorf("get_data_path_failed: %w", err)
	}

	if data != "" {
		SSHKeyDirectory = filepath.Join(data, "ssh")
	}

	if err := os.MkdirAll(SSHKeyDirectory, 0700); err != nil {
		return "", fmt.Errorf("create_ssh_key_dir: %w", err)
	}

	return SSHKeyDirectory, nil
}

func SaveSSHKey(targetID uint, keyData string) (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", err
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	content := strings.TrimSpace(keyData) + "\n"
	if err := os.WriteFile(keyPath, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write_ssh_key: %w", err)
	}

	return keyPath, nil
}

// SaveTemporarySSHKey writes key material for pre-create target validation.
// The filename intentionally does not match the managed target-<id>_id pattern,
// so reconciliation cannot mistake it for an orphaned persisted target key.
func SaveTemporarySSHKey(keyData string) (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", err
	}

	keyFile, err := os.CreateTemp(sshDir, ".target-validation-*")
	if err != nil {
		return "", fmt.Errorf("create_temporary_ssh_key: %w", err)
	}
	keyPath := keyFile.Name()
	cleanup := func() {
		_ = keyFile.Close()
		_ = os.Remove(keyPath)
	}

	if err := keyFile.Chmod(0600); err != nil {
		cleanup()
		return "", fmt.Errorf("chmod_temporary_ssh_key: %w", err)
	}
	if _, err := keyFile.WriteString(strings.TrimSpace(keyData) + "\n"); err != nil {
		cleanup()
		return "", fmt.Errorf("write_temporary_ssh_key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		_ = os.Remove(keyPath)
		return "", fmt.Errorf("close_temporary_ssh_key: %w", err)
	}

	return keyPath, nil
}

func RemoveTemporarySSHKey(keyPath string) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return
	}

	cleanedPath := filepath.Clean(strings.TrimSpace(keyPath))
	if !pathWithinDir(cleanedPath, sshDir) || !strings.HasPrefix(filepath.Base(cleanedPath), ".target-validation-") {
		return
	}
	_ = os.Remove(cleanedPath)
}

func ensureSSHKeyFileAtPath(keyPath, keyData string) error {
	trimmed := strings.TrimSpace(keyData)
	if keyPath == "" || trimmed == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return fmt.Errorf("create_ssh_key_parent_dir: %w", err)
	}

	if err := os.WriteFile(keyPath, []byte(trimmed+"\n"), 0600); err != nil {
		return fmt.Errorf("write_ssh_key: %w", err)
	}

	return nil
}

func (s *Service) RemoveSSHKey(targetID uint) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		logger.L.Warn().Err(err).Uint("target_id", targetID).Msg("failed_to_get_ssh_key_dir_for_removal")
		return
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	_ = os.Remove(keyPath)
}

func isManagedSSHKeyName(name string) bool {
	return strings.HasPrefix(name, "target-") && strings.HasSuffix(name, "_id")
}

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func (s *Service) targetSSHKeyPath(target *clusterModels.BackupTarget) (string, error) {
	if target == nil {
		return "", fmt.Errorf("backup_target_required")
	}

	stored := strings.TrimSpace(target.SSHKeyPath)

	if target.ID == 0 {
		return stored, nil
	}

	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", err
	}
	canonical := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", target.ID))

	if stored == "" {
		return canonical, nil
	}

	if isManagedSSHKeyName(filepath.Base(stored)) && pathWithinDir(stored, sshDir) {
		return canonical, nil
	}

	return stored, nil
}

func (s *Service) resolvedSSHKeyPath(target *clusterModels.BackupTarget) string {
	path, err := s.targetSSHKeyPath(target)
	if err != nil {
		return strings.TrimSpace(target.SSHKeyPath)
	}
	return path
}

func (s *Service) ensureBackupTargetSSHKeyMaterialized(target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}

	target.SSHKeyPath = strings.TrimSpace(target.SSHKeyPath)
	keyData := strings.TrimSpace(target.SSHKey)

	if keyData == "" {
		return nil
	}

	keyPath, err := s.targetSSHKeyPath(target)
	if err != nil {
		return fmt.Errorf("resolve_target_ssh_key_path id=%d: %w", target.ID, err)
	}
	if keyPath == "" {
		return nil
	}

	if err := ensureSSHKeyFileAtPath(keyPath, keyData); err != nil {
		return fmt.Errorf("materialize_target_ssh_key id=%d: %w", target.ID, err)
	}

	target.SSHKeyPath = keyPath
	return nil
}

func (s *Service) ReconcileBackupTargetSSHKeys() error {
	if s.Cluster == nil {
		return nil
	}

	targets, err := s.Cluster.ListBackupTargetsForSync()
	if err != nil {
		return err
	}

	for i := range targets {
		if err := s.ensureBackupTargetSSHKeyMaterialized(&targets[i]); err != nil {
			return err
		}
	}

	if err := s.cleanupOrphanTargetSSHKeys(targets); err != nil {
		logger.L.Warn().Err(err).Msg("cleanup_orphan_ssh_keys_failed")
	}

	return nil
}

func (s *Service) cleanupOrphanTargetSSHKeys(targets []clusterModels.BackupTarget) error {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return fmt.Errorf("read_ssh_key_dir: %w", err)
	}

	knownIDs := make(map[uint]struct{}, len(targets))
	for _, t := range targets {
		knownIDs[t.ID] = struct{}{}
	}

	var cleaned int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "target-") || !strings.HasSuffix(name, "_id") {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(name, "target-"), "_id")
		id, parseErr := strconv.ParseUint(idStr, 10, 64)
		if parseErr != nil {
			continue
		}
		if _, exists := knownIDs[uint(id)]; exists {
			continue
		}
		keyPath := filepath.Join(sshDir, name)
		if err := os.Remove(keyPath); err != nil {
			logger.L.Warn().Err(err).Str("path", keyPath).Msg("failed_to_remove_orphan_ssh_key")
			continue
		}
		cleaned++
	}

	if cleaned > 0 {
		logger.L.Info().Int("count", cleaned).Msg("removed_orphan_ssh_keys")
	}

	return nil
}

func (s *Service) ValidateTarget(ctx context.Context, target *clusterModels.BackupTarget) error {
	backupRoot := strings.TrimSpace(target.BackupRoot)
	if backupRoot == "" {
		return fmt.Errorf("backup_root_required")
	}

	if err := s.ensureBackupTargetSSHKeyMaterialized(target); err != nil {
		return fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}

	if err := s.ensureSSHConnectivity(ctx, target); err != nil {
		return err
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	rootExists, _, err := s.remoteDatasetExists(ctx, target, backupRoot)
	if err == nil && rootExists {
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	pool := parseZFSPoolNameFromDataset(backupRoot)
	if pool == "" {
		return fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		return fmt.Errorf("backup_pool_check_failed: %s", poolOutput)
	}

	if !poolExists {
		return fmt.Errorf("backup_pool_not_found: pool '%s' does not exist on target", pool)
	}

	if err := s.remoteCreateDataset(ctx, target, backupRoot); err != nil {
		return err
	}

	created, verifyOutput, verifyErr := s.remoteDatasetExists(ctx, target, backupRoot)
	if verifyErr != nil || !created {
		if verifyErr != nil {
			return fmt.Errorf("backup_root_create_verify_failed: %s", verifyOutput)
		}
		return fmt.Errorf("backup_root_create_verify_failed: dataset '%s' still not visible on target", backupRoot)
	}

	return nil
}

func parseZFSPoolNameFromDataset(dataset string) string {
	trimmed := strings.TrimSpace(dataset)
	if trimmed == "" {
		return ""
	}

	idx := strings.Index(trimmed, "/")
	if idx <= 0 {
		return trimmed
	}

	return strings.TrimSpace(trimmed[:idx])
}

func (s *Service) remoteDatasetExists(ctx context.Context, target *clusterModels.BackupTarget, dataset string) (bool, string, error) {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "list", "-H", "-o", "name", "-t", "filesystem", "-d", "0", dataset)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return false, output, fmt.Errorf("%w (output: %q)", err, output)
	}

	return strings.TrimSpace(output) != "", output, nil
}

func (s *Service) remoteZFSPoolExists(ctx context.Context, target *clusterModels.BackupTarget, pool string) (bool, string, error) {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zpool", "list", "-H", "-o", "name", pool)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		combined := strings.ToLower(strings.TrimSpace(output + " " + err.Error()))
		if strings.Contains(combined, "no such pool") {
			return false, output, nil
		}
		return false, output, fmt.Errorf("%w (output: %q)", err, output)
	}

	return strings.TrimSpace(output) == pool, output, nil
}

func (s *Service) remoteCreateDataset(ctx context.Context, target *clusterModels.BackupTarget, dataset string) error {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "create", "-p", dataset)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return fmt.Errorf("backup_root_create_failed: failed to create dataset '%s': %w (output: %q)", dataset, err, output)
	}

	return nil
}

// isRemoteSubcommandBlocked returns true when the remote ZFS shell rejected a
// subcommand that it does not permit (e.g. recv-only PBS endpoints).
func isRemoteSubcommandBlocked(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	return strings.Contains(lower, "subcommand not allowed") ||
		strings.Contains(lower, "not permitted")
}

func (s *Service) ensureSSHConnectivity(ctx context.Context, target *clusterModels.BackupTarget) error {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "version")

	_, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return fmt.Errorf("ssh_connection_failed: %w", err)
	}

	return nil
}

func sshControlPath(target *clusterModels.BackupTarget, keyPath string) string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s:%d:%s", target.SSHHost, target.SSHPort, keyPath)
	return filepath.Join(os.TempDir(), fmt.Sprintf("sylve-ssh-%x.sock", h.Sum32()))
}

func (s *Service) buildSSHArgs(target *clusterModels.BackupTarget) []string {
	keyPath := s.resolvedSSHKeyPath(target)

	args := []string{
		"-n",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=3",
		"-o", "ConnectionAttempts=1",
		"-o", "UpdateHostKeys=no",
		"-o", "ControlMaster=auto",
		"-o", fmt.Sprintf("ControlPath=%s", sshControlPath(target, keyPath)),
		"-o", "ControlPersist=60",
	}

	if target.SSHPort != 0 && target.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", target.SSHPort))
	}

	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}

	return args
}
