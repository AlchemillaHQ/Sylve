// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/internal/assets"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var zeltaFS = assets.ZeltaFiles
var zfsSnapshotNamePattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+@[A-Za-z0-9._:-]+$`)

var ZeltaInstallDir string

func GetZeltaInstallDir() (string, error) {
	if ZeltaInstallDir != "" {
		return ZeltaInstallDir, nil
	}

	data, err := config.GetDataPath()
	if err != nil {
		logger.L.Err(err).Msg("failed_getting_data_path_for_zelta")
		return "", err
	}

	ZeltaInstallDir = filepath.Join(data, "zelta")

	return ZeltaInstallDir, nil
}

func EnsureZeltaInstalled() error {
	zeltaInstallDir, err := GetZeltaInstallDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(zeltaInstallDir, "bin")
	shareDir := filepath.Join(zeltaInstallDir, "share", "zelta")

	zeltaBin := filepath.Join(binDir, "zelta")
	if _, err := os.Stat(zeltaBin); err == nil {
		return nil
	}

	logger.L.Info().Msg("extracting_embedded_zelta_scripts")

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create_zelta_bin_dir: %w", err)
	}
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		return fmt.Errorf("create_zelta_share_dir: %w", err)
	}

	binEntries, err := zeltaFS.ReadDir("zelta/bin")
	if err != nil {
		return fmt.Errorf("read_zelta_bin_entries: %w", err)
	}

	for _, entry := range binEntries {
		if entry.IsDir() {
			continue
		}
		data, err := zeltaFS.ReadFile("zelta/bin/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read_zelta_bin_%s: %w", entry.Name(), err)
		}
		dst := filepath.Join(binDir, entry.Name())
		if err := os.WriteFile(dst, data, 0755); err != nil {
			return fmt.Errorf("write_zelta_bin_%s: %w", entry.Name(), err)
		}
	}

	shareEntries, err := zeltaFS.ReadDir("zelta/share/zelta")
	if err != nil {
		return fmt.Errorf("read_zelta_share_entries: %w", err)
	}

	for _, entry := range shareEntries {
		if entry.IsDir() {
			continue
		}
		data, err := zeltaFS.ReadFile("zelta/share/zelta/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read_zelta_share_%s: %w", entry.Name(), err)
		}
		dst := filepath.Join(shareDir, entry.Name())
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("write_zelta_share_%s: %w", entry.Name(), err)
		}
	}

	logger.L.Info().Str("path", zeltaInstallDir).Msg("zelta_scripts_extracted")
	return nil
}

func zeltaBinPath() string {
	zeltaInstallDir, err := GetZeltaInstallDir()
	if err != nil {
		logger.L.Err(err).Msg("failed_getting_zelta_install_dir")
		return ""
	}

	return filepath.Join(zeltaInstallDir, "bin", "zelta")
}

func zeltaShareDir() string {
	zeltaInstallDir, err := GetZeltaInstallDir()
	if err != nil {
		logger.L.Err(err).Msg("failed_getting_zelta_install_dir")
		return ""
	}

	return filepath.Join(zeltaInstallDir, "share", "zelta")
}

func runZeltaWithEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	bin := zeltaBinPath()
	cmd := exec.CommandContext(ctx, bin, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	zeltaInstallDir, err := GetZeltaInstallDir()
	if err != nil {
		logger.L.Err(err).Msg("failed_getting_zelta_install_dir")
		return "", err
	}

	binDir := filepath.Join(zeltaInstallDir, "bin")
	env := append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"ZELTA_SHARE="+zeltaShareDir(),
		"ZELTA_ETC="+filepath.Join(zeltaInstallDir, "etc"),
		"ZELTA_ENV="+filepath.Join(zeltaInstallDir, "etc", "zelta.env"),
	)
	env = append(env, extraEnv...)
	cmd.Env = env

	logger.L.Debug().Str("bin", bin).Strs("args", args).Msg("exec_zelta_with_env")

	err = cmd.Run()
	output := strings.TrimSpace(stdout.String() + "\n" + stderr.String())

	if err != nil {
		return output, fmt.Errorf("zelta_failed: %s: %s", err, output)
	}

	return output, nil
}

func (s *Service) BackupWithTarget(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	return runZeltaWithEnv(ctx, extraEnv, "backup", "--json", sourceDataset, zeltaEndpoint)
}

func (s *Service) MatchWithTarget(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	return runZeltaWithEnv(ctx, extraEnv, "match", sourceDataset, zeltaEndpoint)
}

func (s *Service) RotateWithTarget(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string) (string, error) {
	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)
	return runZeltaWithEnv(ctx, extraEnv, "rotate", "--json", sourceDataset, zeltaEndpoint)
}

func (s *Service) PruneCandidatesWithTarget(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string, keepLast int) ([]string, string, error) {
	if keepLast < 0 {
		return nil, "", fmt.Errorf("invalid_prune_keep_last")
	}

	zeltaEndpoint := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)

	output, err := runZeltaWithEnv(
		ctx,
		extraEnv,
		"prune",
		"--no-ranges",
		fmt.Sprintf("--keep-snap-num=%d", keepLast),
		"--keep-snap-days=0",
		sourceDataset,
		zeltaEndpoint,
	)
	if err != nil {
		return nil, output, err
	}

	lines := strings.Split(output, "\n")
	candidates := make([]string, 0, len(lines))
	for _, line := range lines {
		name := parsePruneCandidateLine(line)
		if name == "" {
			continue
		}
		candidates = append(candidates, name)
	}

	return candidates, output, nil
}

func (s *Service) PruneTargetCandidatesWithSource(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string, keepLast int) ([]string, string, error) {
	if keepLast < 0 {
		return nil, "", fmt.Errorf("invalid_prune_keep_last")
	}

	remoteSource := target.ZeltaEndpoint(destSuffix)
	extraEnv := s.buildZeltaEnv(target)

	output, err := runZeltaWithEnv(
		ctx,
		extraEnv,
		"prune",
		"--no-ranges",
		fmt.Sprintf("--keep-snap-num=%d", keepLast),
		"--keep-snap-days=0",
		remoteSource,
		sourceDataset,
	)
	if err != nil {
		return nil, output, err
	}

	lines := strings.Split(output, "\n")
	candidates := make([]string, 0, len(lines))
	for _, line := range lines {
		name := parsePruneCandidateLine(line)
		if name == "" {
			continue
		}
		candidates = append(candidates, name)
	}

	return candidates, output, nil
}

func (s *Service) DestroySnapshots(ctx context.Context, snapshots []string) error {
	for _, snapshot := range snapshots {
		snap := strings.TrimSpace(snapshot)
		if !isValidZFSSnapshotName(snap) {
			continue
		}

		if _, err := utils.RunCommandWithContext(ctx, "zfs", "destroy", snap); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) DestroyTargetSnapshots(ctx context.Context, target *clusterModels.BackupTarget, sourceSnapshots []string, sourceRoot, targetRoot string) error {
	for _, sourceSnapshot := range sourceSnapshots {
		targetSnapshot, err := mapSourceSnapshotToTarget(sourceSnapshot, sourceRoot, targetRoot)
		if err != nil {
			return err
		}

		sshArgs := s.buildSSHArgs(target)
		sshArgs = append(sshArgs, target.SSHHost, "zfs", "destroy", targetSnapshot)
		if _, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) DestroyTargetSnapshotsByName(ctx context.Context, target *clusterModels.BackupTarget, targetSnapshots []string) error {
	for _, targetSnapshot := range targetSnapshots {
		snap := strings.TrimSpace(targetSnapshot)
		if !isValidZFSSnapshotName(snap) {
			continue
		}

		sshArgs := s.buildSSHArgs(target)
		sshArgs = append(sshArgs, target.SSHHost, "zfs", "destroy", snap)
		if _, err := utils.RunCommandWithContext(ctx, "ssh", sshArgs...); err != nil {
			return err
		}
	}

	return nil
}

func isValidZFSSnapshotName(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, " \t\r\n\"'{}[](),") {
		return false
	}
	return zfsSnapshotNamePattern.MatchString(name)
}

func parsePruneCandidateLine(line string) string {
	name := strings.TrimSpace(line)
	if name == "" {
		return ""
	}

	if strings.HasPrefix(strings.ToLower(name), "notice:") {
		name = strings.TrimSpace(name[len("notice:"):])
	}

	if !isValidZFSSnapshotName(name) {
		return ""
	}

	return name
}

func mapSourceSnapshotToTarget(sourceSnapshot, sourceRoot, targetRoot string) (string, error) {
	sourceSnapshot = strings.TrimSpace(sourceSnapshot)
	if !isValidZFSSnapshotName(sourceSnapshot) {
		return "", fmt.Errorf("invalid_source_snapshot")
	}

	at := strings.LastIndex(sourceSnapshot, "@")
	if at <= 0 || at >= len(sourceSnapshot)-1 {
		return "", fmt.Errorf("invalid_source_snapshot")
	}

	sourceDataset := sourceSnapshot[:at]
	snapshotName := sourceSnapshot[at+1:]

	if sourceDataset == sourceRoot {
		return targetRoot + "@" + snapshotName, nil
	}

	prefix := sourceRoot + "/"
	if strings.HasPrefix(sourceDataset, prefix) {
		suffix := strings.TrimPrefix(sourceDataset, sourceRoot)
		return targetRoot + suffix + "@" + snapshotName, nil
	}

	return "", fmt.Errorf("source_snapshot_outside_root")
}
