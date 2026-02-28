package zelta

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func recoverDefaultSSHKeyPath(targetID uint) (string, error) {
	if targetID == 0 {
		return "", nil
	}

	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", fmt.Errorf("resolve_ssh_key_dir: %w", err)
	}

	path := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", nil
	}

	return path, nil
}

func recoverSingleSSHKeyPathCandidate() (string, error) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		return "", fmt.Errorf("resolve_ssh_key_dir: %w", err)
	}

	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return "", nil
	}

	candidates := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasPrefix(name, "target-") || !strings.HasSuffix(name, "_id") {
			continue
		}
		candidates = append(candidates, filepath.Join(sshDir, name))
	}

	if len(candidates) != 1 {
		return "", nil
	}

	return candidates[0], nil
}

func RemoveSSHKey(targetID uint) {
	sshDir, err := GetSSHKeyDir()
	if err != nil {
		logger.L.Warn().Err(err).Uint("target_id", targetID).Msg("failed_to_get_ssh_key_dir_for_removal")
		return
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("target-%d_id", targetID))
	_ = os.Remove(keyPath)
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, prefix+value)
	return out
}

func (s *Service) ensureBackupTargetSSHKeyMaterialized(target *clusterModels.BackupTarget) error {
	if target == nil {
		return fmt.Errorf("backup_target_required")
	}

	target.SSHKeyPath = strings.TrimSpace(target.SSHKeyPath)
	keyData := strings.TrimSpace(target.SSHKey)

	if keyData == "" {
		if target.SSHKeyPath == "" {
			recoveredPath, err := recoverDefaultSSHKeyPath(target.ID)
			if err != nil {
				return fmt.Errorf("recover_target_ssh_key_path id=%d: %w", target.ID, err)
			}
			if recoveredPath != "" {
				target.SSHKeyPath = recoveredPath
				if s != nil && s.DB != nil && target.ID != 0 {
					if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", recoveredPath).Error; err != nil {
						return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
					}
				}
			}

			if target.SSHKeyPath == "" && s != nil && s.DB != nil {
				var targetCount int64
				if err := s.DB.Model(&clusterModels.BackupTarget{}).Count(&targetCount).Error; err == nil && targetCount == 1 {
					recoveredSinglePath, recErr := recoverSingleSSHKeyPathCandidate()
					if recErr != nil {
						return fmt.Errorf("recover_target_ssh_key_path id=%d: %w", target.ID, recErr)
					}
					if recoveredSinglePath != "" {
						target.SSHKeyPath = recoveredSinglePath
						if target.ID != 0 {
							if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", recoveredSinglePath).Error; err != nil {
								return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
							}
						}
					}
				}
			}
		}

		return nil
	}

	if target.SSHKeyPath == "" {
		generatedPath, err := SaveSSHKey(target.ID, keyData)
		if err != nil {
			return fmt.Errorf("materialize_target_ssh_key id=%d: %w", target.ID, err)
		}

		target.SSHKeyPath = generatedPath
		if s != nil && s.DB != nil && target.ID != 0 {
			if err := s.DB.Model(&clusterModels.BackupTarget{}).Where("id = ?", target.ID).Update("ssh_key_path", generatedPath).Error; err != nil {
				return fmt.Errorf("persist_target_ssh_key_path id=%d: %w", target.ID, err)
			}
		}

		return nil
	}

	if err := ensureSSHKeyFileAtPath(target.SSHKeyPath, keyData); err != nil {
		return fmt.Errorf("materialize_target_ssh_key id=%d: %w", target.ID, err)
	}

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

	return nil
}

func (s *Service) ValidateTarget(ctx context.Context, target *clusterModels.BackupTarget) error {
	backupRoot := strings.TrimSpace(target.BackupRoot)
	if backupRoot == "" {
		return fmt.Errorf("backup_root_required")
	}

	rootExists, output, err := s.remoteDatasetExists(ctx, target, backupRoot)
	if err == nil && rootExists {
		return nil
	}

	if !target.CreateBackupRoot {
		if err := s.ensureSSHConnectivity(ctx, target); err != nil {
			return err
		}
		return fmt.Errorf("backup_root_not_found: dataset '%s' does not exist on target (but SSH connection works): %s", backupRoot, output)
	}

	pool := parseZFSPoolNameFromDataset(backupRoot)
	if pool == "" {
		return fmt.Errorf("invalid_backup_root: dataset '%s' is invalid", backupRoot)
	}

	poolExists, poolOutput, poolErr := s.remoteZFSPoolExists(ctx, target, pool)
	if poolErr != nil {
		if err := s.ensureSSHConnectivity(ctx, target); err != nil {
			return err
		}
		if !poolExists {
			return fmt.Errorf("backup_pool_not_found: pool '%s' does not exist on target", pool)
		}
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
		return false, output, err
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
		return false, output, err
	}

	return strings.TrimSpace(output) == pool, output, nil
}

func (s *Service) remoteCreateDataset(ctx context.Context, target *clusterModels.BackupTarget, dataset string) error {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "create", "-p", dataset)

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		if err := s.ensureSSHConnectivity(ctx, target); err != nil {
			return err
		}
		return fmt.Errorf("backup_root_create_failed: failed to create dataset '%s': %s", dataset, output)
	}

	return nil
}

func (s *Service) ensureSSHConnectivity(ctx context.Context, target *clusterModels.BackupTarget) error {
	sshArgs := s.buildSSHArgs(target)
	sshArgs = append(sshArgs, target.SSHHost, "zfs", "version")

	output, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...)
	if err != nil {
		return fmt.Errorf("ssh_connection_failed: %s: %s", err, output)
	}

	return nil
}

func (s *Service) buildSSHArgs(target *clusterModels.BackupTarget) []string {
	args := []string{"-n", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new"}
	if target.SSHPort != 0 && target.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", target.SSHPort))
	}
	if target.SSHKeyPath != "" {
		args = append(args, "-i", target.SSHKeyPath)
	}
	return args
}
