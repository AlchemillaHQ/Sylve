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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
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
	if err := s.Cluster.InitializeBackupTargetHostTrust(); err != nil {
		return err
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
	HostKey                  string
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
	if candidate.ID == 0 && candidate.SSHHostKey == "" {
		key, err := s.learnTargetSSHHostKey(ctx, candidate)
		if err != nil {
			return BackupTargetValidationResult{}, err
		}
		candidate.SSHHostKey = key
	}
	result, err := s.inspectTarget(ctx, candidate, candidate.CreateBackupRoot)
	result.HostKey = candidate.SSHHostKey
	return result, err
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
	if err := s.EnsureBackupTargetHostKey(ctx, candidate); err != nil {
		return err
	}
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
	candidate.SSHFreshConnection = true
	return &candidate, func() { RemoveTemporarySSHKey(keyPath) }, nil
}

// ValidateTargetReadiness performs a runner-side observational check.
func (s *Service) ValidateTargetReadiness(ctx context.Context, target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}
	candidate := *target
	candidate.SSHFreshConnection = true
	_, err := s.inspectTarget(ctx, &candidate, false)
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
	if strings.TrimSpace(target.BackupRoot) == "" {
		return result, fmt.Errorf("backup_root_required")
	}
	_, rootDataset, err := canonicalizeBackupTarget(target)
	if err != nil {
		return result, err
	}
	backupRoot := rootDataset.String()
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
	pool := rootDataset.Pool()
	if pool == "" {
		return result, fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)
	}
	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		return result, backupPoolCheckError(poolOutput, poolErr)
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
	if strings.TrimSpace(target.BackupRoot) == "" {
		return fmt.Errorf("backup_root_required")
	}
	_, rootDataset, err := canonicalizeBackupTarget(target)
	if err != nil {
		return &BackupTargetProvisionError{Err: err}
	}
	backupRoot := rootDataset.String()
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
	pool := rootDataset.Pool()
	if pool == "" {
		return &BackupTargetProvisionError{Err: fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)}
	}
	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		return &BackupTargetProvisionError{Err: backupPoolCheckError(poolOutput, poolErr)}
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

func (s *Service) remoteDatasetExists(ctx context.Context, target *clusterModels.BackupTarget, dataset string) (bool, string, error) {
	parsedDataset, err := canonicalTargetDataset(target, dataset)
	if err != nil {
		return false, "", fmt.Errorf("invalid_remote_dataset: %w", err)
	}
	dataset = parsedDataset.String()
	output, err := s.runTargetSSH(ctx, target, "zfs", "list", "-H", "-o", "name", "-t", "filesystem", "-d", "0", dataset)
	if err != nil {
		if replicationDatasetMissingResult(output, err) {
			return false, output, nil
		}
		return false, output, fmt.Errorf("%w (output: %q)", err, output)
	}

	return replicationDatasetListedExactly(output, dataset), output, nil
}

func (s *Service) remoteZFSPoolExists(ctx context.Context, target *clusterModels.BackupTarget, pool string) (bool, string, error) {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return false, "", err
	}
	parsedPool, err := remoteexec.ParseZFSDataset(pool)
	if err != nil || parsedPool.String() != parsedPool.Pool() || parsedPool.String() != root.Pool() {
		return false, "", fmt.Errorf("invalid_remote_zfs_pool")
	}
	pool = parsedPool.String()
	output, err := s.runTargetSSH(ctx, target, "zpool", "list", "-H", "-o", "name", pool)
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
	parsedDataset, err := canonicalTargetDataset(target, dataset)
	if err != nil {
		return fmt.Errorf("invalid_backup_root: %w", err)
	}
	dataset = parsedDataset.String()
	output, err := s.runTargetSSH(ctx, target, "zfs", "create", "-p", dataset)
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
	_, err := s.runTargetSSH(ctx, target, "zfs", "version")
	if err != nil {
		return fmt.Errorf("ssh_connection_failed: %w", err)
	}

	return nil
}

func (s *Service) runTargetSSH(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	argv ...string,
) (string, error) {
	command, err := remoteexec.NewCommand(argv...)
	if err != nil {
		return "", err
	}
	kind, dataset := remoteCommandLogFields(argv)
	return s.runTargetRemoteCommand(ctx, target, command, kind, dataset)
}

func (s *Service) runTargetDatasetSSH(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	argv ...string,
) (string, error) {
	parsedDataset, err := canonicalTargetDataset(target, dataset)
	if err != nil {
		return "", err
	}
	command, err := remoteexec.NewCommand(argv...)
	if err != nil {
		return "", err
	}
	kind, _ := remoteCommandLogFields(argv)
	return s.runTargetRemoteCommand(ctx, target, command, kind, parsedDataset.String())
}

func (s *Service) runTargetDatasetScript(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	dataset string,
	script string,
) (string, error) {
	parsedDataset, err := canonicalTargetDataset(target, dataset)
	if err != nil {
		return "", err
	}
	command, err := remoteexec.NewScript(script)
	if err != nil {
		return "", err
	}
	return s.runTargetRemoteCommand(ctx, target, command, "script", parsedDataset.String())
}

func (s *Service) runTargetRemoteCommand(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	command remoteexec.Command,
	kind string,
	dataset string,
) (string, error) {
	sshArgs, err := s.targetRemoteCommandArgs(target, command, false, kind, dataset)
	if err != nil {
		return "", err
	}
	stdout, stderr, err := utils.RunCommandWithContextStreams(ctx, "ssh", sshArgs...)
	if err != nil {
		// Failures keep the historical stdout+stderr payload: callers match stderr-only diagnostics in output.
		combined := combinedRemoteCommandOutput(stdout, stderr)
		return combined, fmt.Errorf("%s: %w", remoteexec.SSHDiagnostic(combined), targetSSHHostKeyError(target, stderr, err))
	}
	if warning := strings.TrimSpace(stderr); warning != "" {
		event := logger.L.Debug().
			Str("command_kind", kind).
			Uint("target_id", target.ID).
			Str("stderr", remoteexec.SSHDiagnostic(warning))
		if dataset != "" {
			event.Str("dataset", remoteDatasetForLog(dataset))
		}
		event.Msg("remote_command_stderr")
	}
	return stdout, nil
}

func targetSSHHostKeyError(target *clusterModels.BackupTarget, output string, err error) error {
	if target.SSHClusterNodeID != "" {
		return remoteexec.SSHHostKeyError("cluster", target.SSHClusterNodeID, output, err)
	}
	return remoteexec.SSHHostKeyError("backup", target.Name, output, err)
}

func combinedRemoteCommandOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func backupPoolCheckError(output string, err error) error {
	if output = strings.TrimSpace(output); output == "" {
		return fmt.Errorf("backup_pool_check_failed: %w", err)
	}
	return fmt.Errorf("backup_pool_check_failed: %s: %w", output, err)
}

func (s *Service) targetRemoteCommandArgs(
	target *clusterModels.BackupTarget,
	command remoteexec.Command,
	readsStdin bool,
	kind string,
	dataset string,
) ([]string, error) {
	destination, _, err := canonicalizeBackupTarget(target)
	if err != nil {
		return nil, err
	}
	base, err := s.buildSSHArgs(target)
	if err != nil {
		return nil, err
	}
	sshArgs, err := command.SSHArgs(base, destination, readsStdin)
	if err != nil {
		return nil, err
	}
	port := target.SSHPort
	if port == 0 {
		port = 22
	}
	event := logger.L.Debug().
		Str("command_kind", kind).
		Uint("target_id", target.ID).
		Str("ssh_host", destination.String()).
		Int("ssh_port", port)
	if dataset != "" {
		event.Str("dataset", remoteDatasetForLog(dataset))
	}
	event.Msg("remote_command_execute")
	return sshArgs, nil
}

func remoteCommandLogFields(argv []string) (string, string) {
	if len(argv) == 0 {
		return "remote", ""
	}
	program := strings.TrimSpace(argv[0])
	operation := ""
	if len(argv) > 1 {
		operation = strings.TrimSpace(argv[1])
	}
	kind := "remote"
	hasDataset := false
	switch program {
	case "zfs":
		kind = "zfs"
		switch operation {
		case "list", "get", "set", "create", "destroy", "receive", "recv", "rename", "snapshot", "mount", "unmount", "hold", "release":
			kind += "." + operation
			hasDataset = true
		case "version":
			kind += ".version"
		}
	case "zpool":
		kind = "zpool"
		switch operation {
		case "list", "get":
			kind += "." + operation
			hasDataset = true
		}
	case "cat":
		kind = "metadata.read"
	}
	if !hasDataset || len(argv) < 2 {
		return kind, ""
	}
	resource := argv[len(argv)-1]
	if snapshot, err := remoteexec.ParseZFSSnapshot(resource); err == nil {
		return kind, remoteDatasetForLog(snapshot.Dataset().String())
	}
	if dataset, err := remoteexec.ParseZFSDataset(resource); err == nil {
		return kind, remoteDatasetForLog(dataset.String())
	}
	return kind, ""
}

func remoteDatasetForLog(dataset string) string {
	parts := strings.Split(dataset, "/")
	for index, part := range parts {
		for _, marker := range []string{"_gen-", "_previous-", "_restore-backup-", ".pre_"} {
			if markerIndex := strings.Index(part, marker); markerIndex > 0 {
				parts[index] = part[:markerIndex]
				break
			}
		}
	}
	return strings.Join(parts, "/")
}

func (s *Service) buildSSHArgs(target *clusterModels.BackupTarget) ([]string, error) {
	trust, err := s.targetSSHHostTrust(target)
	if err != nil {
		return nil, err
	}
	path := target.SSHHostKeyFile
	learning := path != "" && trust.PublicKey == ""
	if !learning {
		dir, err := GetSSHKeyDir()
		if err != nil {
			return nil, fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
		}
		path, err = remoteexec.PrepareSSHHostKey(filepath.Join(dir, "host-keys"), trust)
		if err != nil {
			return nil, err
		}
	}
	keyPath := ""
	if target != nil && (strings.TrimSpace(target.SSHKey) != "" || strings.TrimSpace(target.SSHKeyPath) != "") {
		keyPath, err = s.targetSSHKeyPath(target)
		if err != nil {
			return nil, err
		}
	}

	args, err := remoteexec.SSHHostKeyOptions(trust, path, keyPath, target.SSHFreshConnection, learning)
	if err != nil {
		return nil, err
	}
	args = append(args, "-n",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=3",
		"-o", "ConnectionAttempts=1",
	)

	if target.SSHPort != 0 && target.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", target.SSHPort))
	}

	if keyPath != "" {
		args = append(args, "-i", keyPath)
	}

	return args, nil
}

func (s *Service) targetSSHHostTrust(target *clusterModels.BackupTarget) (remoteexec.SSHHostTrust, error) {
	if target == nil {
		return remoteexec.SSHHostTrust{}, fmt.Errorf("backup_target_required")
	}
	destination, err := remoteexec.ParseSSHDestination(target.SSHHost)
	if err != nil {
		return remoteexec.SSHHostTrust{}, err
	}
	port := target.SSHPort
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return remoteexec.SSHHostTrust{}, fmt.Errorf("invalid_ssh_port")
	}
	trust := remoteexec.SSHHostTrust{Scope: "backup", Target: fmt.Sprint(target.ID), Endpoint: destination.String(),
		Port: port, PublicKey: target.SSHHostKey, Revision: target.SSHHostKeyRevision}
	if target.SSHClusterNodeID != "" {
		trust.Scope, trust.Target = "cluster", target.SSHClusterNodeID
		canonical, err := remoteexec.CanonicalSSHHostKey(trust.PublicKey)
		if err != nil {
			return trust, fmt.Errorf("cluster_ssh_identity_invalid: node=%s: Check that node's published SSH identity: %w", trust.Target, err)
		}
		trust.PublicKey = canonical
		return trust, nil
	}
	if target.ID == 0 {
		trust.Target = "candidate:" + destination.String() + ":" + fmt.Sprint(port)
	}
	if trust.PublicKey != "" || target.SSHHostKeyFile != "" {
		return trust, nil
	}
	record, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB, target)
	if err != nil {
		return trust, err
	}
	if trust.Revision != 0 && trust.Revision != record.Revision {
		return trust, fmt.Errorf("backup_target_host_key_revision_conflict")
	}
	if record.PublicKey == "" {
		return trust, fmt.Errorf("backup_target_host_key_unlearned: target=%s: select Validate before this operation", target.Name)
	}
	trust.PublicKey, trust.Revision = record.PublicKey, record.Revision
	return trust, nil
}

func (s *Service) learnTargetSSHHostKey(ctx context.Context, target *clusterModels.BackupTarget) (string, error) {
	if target == nil || target.SSHClusterNodeID != "" {
		return "", fmt.Errorf("backup_target_host_key_enrollment_not_permitted")
	}
	dir, err := GetSSHKeyDir()
	if err != nil {
		return "", fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
	}
	file, err := os.CreateTemp(dir, ".host-key-enrollment-*")
	if err != nil {
		return "", fmt.Errorf("ssh_host_key_file_unavailable: Check the Sylve data directory and permissions: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	defer os.Remove(path)
	candidate := *target
	candidate.SSHHostKey, candidate.SSHHostKeyFile = "", path
	trust, err := s.targetSSHHostTrust(&candidate)
	if err != nil {
		return "", err
	}
	if err := s.ensureSSHConnectivity(ctx, &candidate); err != nil {
		return "", err
	}
	return remoteexec.ReadLearnedSSHHostKey(path, trust)
}

func (s *Service) EnsureBackupTargetHostKey(ctx context.Context, target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}
	if target.SSHClusterNodeID != "" || target.SSHHostKey != "" {
		return nil
	}
	record, err := clusterModels.GetBackupTargetSSHHostTrust(s.DB, target)
	if err != nil {
		return err
	}
	if target.SSHHostKeyRevision != 0 && target.SSHHostKeyRevision != record.Revision {
		return fmt.Errorf("backup_target_host_key_revision_conflict")
	}
	if record.PublicKey != "" {
		target.SSHHostKey, target.SSHHostKeyRevision = record.PublicKey, record.Revision
		return nil
	}
	if s.Cluster == nil {
		if _, err := s.runtimeStateBypassRaft(); err != nil {
			return err
		}
	}
	release, err := s.acquireBackupTargetSSHKey(target)
	if err != nil {
		return err
	}
	defer release()
	key, err := s.learnTargetSSHHostKey(ctx, target)
	if err != nil {
		return err
	}
	change := clusterModels.BackupTargetSSHHostTrustChange{TargetID: target.ID, EndpointFingerprint: record.EndpointFingerprint,
		ExpectedRevision: record.Revision, PublicKey: key, OccurredAt: time.Now().UTC()}
	if s.Cluster != nil {
		err = s.Cluster.InstallBackupTargetHostKey(ctx, change)
	} else {
		err = clusterModels.ApplyBackupTargetSSHHostTrustTxn(s.DB, "install_v1", &change)
	}
	if err != nil {
		current, readErr := clusterModels.GetBackupTargetSSHHostTrust(s.DB, target)
		if readErr != nil || current.PublicKey == "" || current.Revision != record.Revision+1 {
			return err
		}
		target.SSHHostKey, target.SSHHostKeyRevision = current.PublicKey, current.Revision
		return s.ValidateTargetReadiness(ctx, target)
	}
	target.SSHHostKey, target.SSHHostKeyRevision = key, record.Revision+1
	s.recordBackupTargetHostKeyInstall(target)
	return nil
}

func (s *Service) recordBackupTargetHostKeyInstall(target *clusterModels.BackupTarget) {
	if s.TelemetryDB == nil {
		return
	}
	fingerprint, err := remoteexec.SSHHostKeyFingerprint(target.SSHHostKey)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	actor := s.localNodeID()
	if actor == "" {
		actor = "local"
	}
	action, err := json.Marshal(map[string]any{"method": "INSTALL", "path": "/api/cluster/backups/targets/" + fmt.Sprint(target.ID) + "/host-key",
		"response": map[string]any{"targetId": target.ID, "fingerprint": fingerprint, "revision": target.SSHHostKeyRevision}})
	if err != nil {
		return
	}
	if err := s.TelemetryDB.Create(&infoModels.AuditRecord{User: actor, AuthType: "cluster-node", Node: actor,
		Started: now, Ended: now, Status: "success", Action: string(action), Version: 2}).Error; err != nil {
		logger.L.Warn().Err(err).Uint("target_id", target.ID).Msg("backup_target_host_key_install_audit_failed")
	}
}
