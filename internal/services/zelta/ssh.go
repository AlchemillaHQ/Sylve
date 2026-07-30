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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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

// SaveSSHKey retains the legacy canonical-path helper for replay/tests. Managed
// runtime targets use immutable content-addressed versions instead.
func SaveSSHKey(targetID uint, keyData string) (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", err
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	if err := ensureSSHKeyFileAtPath(keyPath, keyData); err != nil {
		return "", err
	}
	return keyPath, nil
}

// SaveTemporarySSHKey writes key material for pre-create target validation.
// The filename intentionally does not match any managed target identity pattern,
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
	content := []byte(trimmed + "\n")
	if existing, err := os.ReadFile(keyPath); err == nil && string(existing) == string(content) {
		if info, statErr := os.Stat(keyPath); statErr == nil && info.Mode().Perm() == 0600 {
			return nil
		}
	}

	parent := filepath.Dir(keyPath)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create_ssh_key_parent_dir: %w", err)
	}
	keyFile, err := os.CreateTemp(parent, ".target-key-materialization-*")
	if err != nil {
		return fmt.Errorf("create_temporary_ssh_key: %w", err)
	}
	temporaryPath := keyFile.Name()
	cleanup := func() {
		_ = keyFile.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := keyFile.Chmod(0600); err != nil {
		cleanup()
		return fmt.Errorf("chmod_temporary_ssh_key: %w", err)
	}
	if _, err := keyFile.Write(content); err != nil {
		cleanup()
		return fmt.Errorf("write_temporary_ssh_key: %w", err)
	}
	if err := keyFile.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync_temporary_ssh_key: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close_temporary_ssh_key: %w", err)
	}
	if err := os.Rename(temporaryPath, keyPath); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("activate_ssh_key: %w", err)
	}
	return nil
}

func BackupTargetSSHKeyFingerprint(keyData string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(keyData)))
	return hex.EncodeToString(sum[:])
}

func managedBackupTargetSSHKeyName(targetID uint, keyData string) string {
	return fmt.Sprintf("target-%d-%s_id", targetID, BackupTargetSSHKeyFingerprint(keyData))
}

func managedSSHKeyTargetID(name string) (uint, bool) {
	if !strings.HasPrefix(name, "target-") || !strings.HasSuffix(name, "_id") {
		return 0, false
	}
	identity := strings.TrimSuffix(strings.TrimPrefix(name, "target-"), "_id")
	if idx := strings.Index(identity, "-"); idx >= 0 {
		identity = identity[:idx]
	}
	id, err := strconv.ParseUint(identity, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

func (s *Service) RemoveSSHKey(targetID uint) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		logger.L.Warn().Err(err).Uint("target_id", targetID).Msg("failed_to_get_ssh_key_dir_for_removal")
		return
	}
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		logger.L.Warn().Err(err).Uint("target_id", targetID).Msg("failed_to_list_ssh_keys_for_removal")
		return
	}
	s.backupTargetKeyMu.Lock()
	defer s.backupTargetKeyMu.Unlock()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, managed := managedSSHKeyTargetID(entry.Name())
		if !managed || id != targetID {
			continue
		}
		path := filepath.Join(sshDir, entry.Name())
		if s.backupTargetKeyUsers[path] != 0 {
			continue
		}
		_ = os.Remove(path)
	}
}

func isManagedSSHKeyName(name string) bool {
	_, managed := managedSSHKeyTargetID(name)
	return managed
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

	// Replicated key material always selects one immutable, content-addressed
	// local version. Old target copies therefore retain their complete key and
	// receive a distinct SSH multiplexing control path during replacement.
	if key := strings.TrimSpace(target.SSHKey); key != "" {
		return filepath.Join(sshDir, managedBackupTargetSSHKeyName(target.ID, key)), nil
	}
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

func (s *Service) materializeBackupTargetSSHKeyLocked(target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}

	target.SSHKeyPath = strings.TrimSpace(target.SSHKeyPath)
	keyData := strings.TrimSpace(target.SSHKey)
	if keyData == "" {
		return nil
	}
	if target.ID == 0 {
		return fmt.Errorf("backup_target_id_required")
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

func (s *Service) ensureBackupTargetSSHKeyMaterialized(target *clusterModels.BackupTarget) error {
	if s == nil {
		return fmt.Errorf("backup_target_ssh_key_service_unavailable")
	}
	s.backupTargetKeyMu.Lock()
	defer s.backupTargetKeyMu.Unlock()
	return s.materializeBackupTargetSSHKeyLocked(target)
}

// MaterializeBackupTargetSSHKey atomically installs the exact managed version
// selected by replicated target state. It does not persist a node-local path.
func (s *Service) MaterializeBackupTargetSSHKey(target *clusterModels.BackupTarget) error {
	return s.ensureBackupTargetSSHKeyMaterialized(target)
}

// AcquireBackupTargetSSHKey materializes and leases one immutable managed
// version so reconciliation cannot collect it while an operation is using or
// committing that exact target configuration.
func (s *Service) AcquireBackupTargetSSHKey(target *clusterModels.BackupTarget) (func(), error) {
	return s.acquireBackupTargetSSHKey(target)
}

func (s *Service) acquireBackupTargetSSHKey(target *clusterModels.BackupTarget) (func(), error) {
	if s == nil {
		return func() {}, fmt.Errorf("backup_target_ssh_key_service_unavailable")
	}
	s.backupTargetKeyMu.Lock()
	if err := s.materializeBackupTargetSSHKeyLocked(target); err != nil {
		s.backupTargetKeyMu.Unlock()
		return func() {}, err
	}
	path := ""
	if target != nil && strings.TrimSpace(target.SSHKey) != "" {
		path = strings.TrimSpace(target.SSHKeyPath)
		if path != "" {
			if s.backupTargetKeyUsers == nil {
				s.backupTargetKeyUsers = make(map[string]uint)
			}
			s.backupTargetKeyUsers[path]++
		}
	}
	s.backupTargetKeyMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			if path == "" {
				return
			}
			s.backupTargetKeyMu.Lock()
			defer s.backupTargetKeyMu.Unlock()
			if users := s.backupTargetKeyUsers[path]; users > 1 {
				s.backupTargetKeyUsers[path] = users - 1
			} else {
				delete(s.backupTargetKeyUsers, path)
			}
		})
	}, nil
}

func (s *Service) ReconcileBackupTargetSSHKeys() error {
	if s.Cluster == nil {
		return nil
	}

	targets, err := s.Cluster.ListBackupTargetsForSync()
	if err != nil {
		return err
	}

	var result error
	for i := range targets {
		if err := s.ensureBackupTargetSSHKeyMaterialized(&targets[i]); err != nil {
			result = errors.Join(result, err)
		}
	}
	if err := s.cleanupOrphanTargetSSHKeys(targets); err != nil {
		result = errors.Join(result, err)
	}
	return result
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

	currentPaths := make(map[string]struct{}, len(targets))
	for i := range targets {
		path, pathErr := s.targetSSHKeyPath(&targets[i])
		if pathErr != nil {
			return pathErr
		}
		if path != "" && pathWithinDir(path, sshDir) && isManagedSSHKeyName(filepath.Base(path)) {
			currentPaths[filepath.Clean(path)] = struct{}{}
		}
	}

	s.backupTargetKeyMu.Lock()
	defer s.backupTargetKeyMu.Unlock()
	var cleaned int
	for _, entry := range entries {
		if entry.IsDir() || !isManagedSSHKeyName(entry.Name()) {
			continue
		}
		keyPath := filepath.Clean(filepath.Join(sshDir, entry.Name()))
		if _, current := currentPaths[keyPath]; current || s.backupTargetKeyUsers[keyPath] != 0 {
			continue
		}
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

type BackupTargetValidationResult struct {
	RootExists               bool
	RootProvisioningRequired bool
}

type BackupTargetProvisionError struct {
	Err       error
	Ambiguous bool
}

func (e *BackupTargetProvisionError) Error() string {
	if e == nil || e.Err == nil {
		return "backup_target_provision_failed"
	}
	return e.Err.Error()
}

func (e *BackupTargetProvisionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func BackupTargetProvisionFailureIsAmbiguous(err error) bool {
	var provisionErr *BackupTargetProvisionError
	return errors.As(err, &provisionErr) && provisionErr.Ambiguous
}

func (s *Service) ValidateTarget(ctx context.Context, target *clusterModels.BackupTarget) error {
	if target != nil && strings.TrimSpace(target.SSHKey) != "" {
		return s.ValidateTargetCandidate(ctx, target)
	}
	result, err := s.inspectTarget(ctx, target, target != nil && target.CreateBackupRoot)
	if err == nil && result.RootProvisioningRequired {
		return fmt.Errorf("backup_root_provisioning_required")
	}
	return err
}

// InspectTargetCandidate validates uncommitted managed key material without
// provisioning a remote dataset. A missing authorized root is returned as a
// plan that must be durably prepared before execution.
func (s *Service) InspectTargetCandidate(
	ctx context.Context,
	target *clusterModels.BackupTarget,
) (BackupTargetValidationResult, error) {
	candidate, cleanup, err := prepareBackupTargetValidationCandidate(target)
	if err != nil {
		return BackupTargetValidationResult{}, err
	}
	defer cleanup()
	return s.inspectTarget(ctx, candidate, candidate.CreateBackupRoot)
}

func (s *Service) ValidateTargetCandidate(ctx context.Context, target *clusterModels.BackupTarget) error {
	result, err := s.InspectTargetCandidate(ctx, target)
	if err == nil && result.RootProvisioningRequired {
		return fmt.Errorf("backup_root_provisioning_required")
	}
	return err
}

// ValidateTargetCandidateReadiness validates a create/update candidate with
// its staged key but never treats a missing root as provisionable.
func (s *Service) ValidateTargetCandidateReadiness(ctx context.Context, target *clusterModels.BackupTarget) error {
	candidate, cleanup, err := prepareBackupTargetValidationCandidate(target)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = s.inspectTarget(ctx, candidate, false)
	return err
}

func prepareBackupTargetValidationCandidate(
	target *clusterModels.BackupTarget,
) (*clusterModels.BackupTarget, func(), error) {
	if target == nil {
		return nil, func() {}, fmt.Errorf("backup_target_required")
	}
	key := strings.TrimSpace(target.SSHKey)
	if key == "" {
		return nil, func() {}, fmt.Errorf("managed_ssh_key_required")
	}
	keyPath, err := SaveTemporarySSHKey(key)
	if err != nil {
		return nil, func() {}, fmt.Errorf("stage_backup_target_ssh_key_failed: %w", err)
	}
	candidate := *target
	candidate.SSHKey = ""
	candidate.SSHKeyPath = keyPath
	return &candidate, func() { RemoveTemporarySSHKey(keyPath) }, nil
}

// ValidateTargetReadiness performs a runner-side observational check.
func (s *Service) ValidateTargetReadiness(ctx context.Context, target *clusterModels.BackupTarget) error {
	_, err := s.inspectTarget(ctx, target, false)
	return err
}

func (s *Service) inspectTarget(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	allowProvisionPlan bool,
) (BackupTargetValidationResult, error) {
	var result BackupTargetValidationResult
	if target == nil {
		return result, fmt.Errorf("backup_target_required")
	}
	backupRoot := strings.TrimSpace(target.BackupRoot)
	if backupRoot == "" {
		return result, fmt.Errorf("backup_root_required")
	}
	releaseKey, err := s.acquireBackupTargetSSHKey(target)
	if err != nil {
		return result, fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	defer releaseKey()
	if err := s.ensureSSHConnectivity(ctx, target); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	rootExists, _, err := s.remoteDatasetExists(ctx, target, backupRoot)
	if err != nil {
		return result, fmt.Errorf("backup_root_check_failed: %w", err)
	}
	if rootExists {
		result.RootExists = true
		return result, nil
	}
	if !allowProvisionPlan {
		return result, fmt.Errorf("backup_root_not_found: dataset '%s' does not exist on target", backupRoot)
	}
	pool := parseZFSPoolNameFromDataset(backupRoot)
	if pool == "" {
		return result, fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)
	}
	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		return result, fmt.Errorf("backup_pool_check_failed: %s", poolOutput)
	}
	if !poolExists {
		return result, fmt.Errorf("backup_pool_not_found: pool '%s' does not exist on target", pool)
	}
	result.RootProvisioningRequired = true
	return result, nil
}

// ProvisionBackupTargetRoot performs only an already-durably-prepared create.
// It is idempotent, verifies the exact dataset, and never destroys anything.
func (s *Service) ProvisionBackupTargetRoot(ctx context.Context, target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}
	backupRoot := strings.TrimSpace(target.BackupRoot)
	if backupRoot == "" {
		return fmt.Errorf("backup_root_required")
	}
	if !target.CreateBackupRoot {
		return fmt.Errorf("backup_target_root_creation_not_authorized")
	}
	releaseKey, err := s.acquireBackupTargetSSHKey(target)
	if err != nil {
		return fmt.Errorf("backup_target_ssh_key_materialize_failed: %w", err)
	}
	defer releaseKey()
	if err := s.ensureSSHConnectivity(ctx, target); err != nil {
		return &BackupTargetProvisionError{Err: err, Ambiguous: false}
	}
	exists, _, err := s.remoteDatasetExists(ctx, target, backupRoot)
	if err != nil {
		return &BackupTargetProvisionError{Err: fmt.Errorf("backup_root_check_failed: %w", err), Ambiguous: false}
	}
	if exists {
		return nil
	}
	pool := parseZFSPoolNameFromDataset(backupRoot)
	if pool == "" {
		return &BackupTargetProvisionError{Err: fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)}
	}
	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		return &BackupTargetProvisionError{Err: fmt.Errorf("backup_pool_check_failed: %s", poolOutput)}
	}
	if !poolExists {
		return &BackupTargetProvisionError{Err: fmt.Errorf("backup_pool_not_found: pool '%s' does not exist on target", pool)}
	}

	createErr := s.remoteCreateDataset(ctx, target, backupRoot)
	created, verifyOutput, verifyErr := s.remoteDatasetExists(ctx, target, backupRoot)
	if created && verifyErr == nil {
		return nil
	}
	if createErr != nil && verifyErr == nil && !created {
		return &BackupTargetProvisionError{Err: createErr, Ambiguous: false}
	}
	verifyFailure := fmt.Errorf("backup_root_create_verify_failed: dataset '%s' is not durably verified (output: %s)", backupRoot, strings.TrimSpace(verifyOutput))
	if verifyErr != nil {
		verifyFailure = fmt.Errorf("backup_root_create_verify_failed: %w (output: %s)", verifyErr, strings.TrimSpace(verifyOutput))
	}
	if createErr != nil {
		verifyFailure = errors.Join(createErr, verifyFailure)
	}
	return &BackupTargetProvisionError{Err: verifyFailure, Ambiguous: true}
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
		if replicationDatasetMissingResult(output, err) {
			return false, output, nil
		}
		return false, output, fmt.Errorf("%w (output: %q)", err, output)
	}

	return replicationDatasetListedExactly(output, dataset), output, nil
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
	keyPath := ""
	if target != nil && (strings.TrimSpace(target.SSHKey) != "" || strings.TrimSpace(target.SSHKeyPath) != "") {
		keyPath = s.resolvedSSHKeyPath(target)
	}

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
