// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/assets"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/remoteexec"
	"github.com/alchemillahq/sylve/pkg/utils"
)

var zeltaFS = assets.ZeltaFiles

var ZeltaInstallDir string

func zeltaSnapshotName(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "zl"
	}
	return fmt.Sprintf("%s_%s", prefix, compactNowToken())
}

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

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create_zelta_bin_dir: %w", err)
	}
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		return fmt.Errorf("create_zelta_share_dir: %w", err)
	}

	binChanged, err := syncEmbeddedZeltaFiles("zelta/bin", binDir, 0755)
	if err != nil {
		return err
	}
	shareChanged, err := syncEmbeddedZeltaFiles("zelta/share/zelta", shareDir, 0644)
	if err != nil {
		return err
	}

	if binChanged || shareChanged {
		logger.L.Info().Str("path", zeltaInstallDir).Msg("zelta_scripts_synced")
	}
	return nil
}

func syncEmbeddedZeltaFiles(sourceDir, destinationDir string, perm os.FileMode) (bool, error) {
	entries, err := zeltaFS.ReadDir(sourceDir)
	if err != nil {
		return false, fmt.Errorf("read_zelta_entries_%s: %w", sourceDir, err)
	}

	changed := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		source := filepath.Join(sourceDir, entry.Name())
		data, err := zeltaFS.ReadFile(source)
		if err != nil {
			return false, fmt.Errorf("read_zelta_file_%s: %w", source, err)
		}

		destination := filepath.Join(destinationDir, entry.Name())
		installed, readErr := os.ReadFile(destination)
		if readErr == nil && bytes.Equal(installed, data) {
			if info, statErr := os.Stat(destination); statErr == nil && info.Mode().Perm() == perm {
				continue
			} else if statErr != nil {
				return false, fmt.Errorf("stat_zelta_file_%s: %w", destination, statErr)
			}
		} else if readErr != nil && !os.IsNotExist(readErr) {
			return false, fmt.Errorf("read_installed_zelta_file_%s: %w", destination, readErr)
		}

		if err := utils.AtomicWriteFile(destination, data, perm); err != nil {
			return false, fmt.Errorf("write_zelta_file_%s: %w", destination, err)
		}
		changed = true
	}

	return changed, nil
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
	return runZeltaWithEnvStreaming(ctx, extraEnv, nil, args...)
}

func runZeltaWithEnvStreaming(
	ctx context.Context,
	extraEnv []string,
	onLine func(string),
	args ...string,
) (string, error) {
	bin := zeltaBinPath()
	cmd := exec.CommandContext(ctx, bin, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("prepare_zelta_stdout_pipe_failed: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("prepare_zelta_stderr_pipe_failed: %w", err)
	}

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

	commandKind := "zelta"
	if len(args) > 0 {
		switch args[0] {
		case "backup", "prune", "restore", "match", "report":
			commandKind += "." + args[0]
		}
	}
	logger.L.Debug().Str("command_kind", commandKind).Msg("exec_zelta_with_env")

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start_zelta_failed: %w", err)
	}

	var outputMu sync.Mutex
	var callbackMu sync.Mutex
	var output bytes.Buffer

	appendLine := func(line string) {
		cleaned := strings.TrimRight(line, "\r\n")

		outputMu.Lock()
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(cleaned)
		outputMu.Unlock()

		if onLine != nil {
			callbackMu.Lock()
			onLine(cleaned)
			callbackMu.Unlock()
		}
	}

	readPipe := func(reader io.Reader) error {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			appendLine(scanner.Text())
		}
		return scanner.Err()
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		if readErr := readPipe(stdout); readErr != nil {
			errCh <- readErr
		}
	}()

	go func() {
		defer wg.Done()
		if readErr := readPipe(stderr); readErr != nil {
			errCh <- readErr
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	close(errCh)

	nonBenignReadErrs := make([]error, 0, 2)
	for readErr := range errCh {
		if readErr == nil || waitErr != nil {
			continue
		}

		if isBenignPipeReadError(readErr) {
			logger.L.Debug().
				Err(readErr).
				Str("command_kind", commandKind).
				Msg("ignoring_benign_zelta_pipe_read_error")
			continue
		}

		nonBenignReadErrs = append(nonBenignReadErrs, readErr)
	}

	if waitErr == nil && len(nonBenignReadErrs) > 0 {
		waitErr = nonBenignReadErrs[0]
	}

	finalOutput := strings.TrimSpace(output.String())

	if waitErr != nil {
		return finalOutput, fmt.Errorf("zelta_failed: %s: %s", waitErr, finalOutput)
	}

	return finalOutput, nil
}

func isBenignPipeReadError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "file already closed") ||
		strings.Contains(lower, "use of closed file")
}

func (s *Service) PruneCandidatesWithTarget(ctx context.Context, target *clusterModels.BackupTarget, sourceDataset, destSuffix string, keepLast int) ([]string, string, error) {
	if keepLast < 0 {
		return nil, "", fmt.Errorf("invalid_prune_keep_last")
	}
	source, err := remoteexec.ParseZFSDataset(sourceDataset)
	if err != nil {
		return nil, "", fmt.Errorf("invalid_prune_source_dataset: %w", err)
	}
	zeltaEndpoint, err := canonicalZeltaEndpoint(target, destSuffix)
	if err != nil {
		return nil, "", err
	}
	extraEnv := s.buildZeltaEnv(target)

	output, err := runZeltaWithEnv(
		ctx,
		extraEnv,
		"prune",
		"--no-ranges",
		fmt.Sprintf("--keep-snap-num=%d", keepLast),
		"--keep-snap-days=0",
		source.String(),
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
	source, err := remoteexec.ParseZFSDataset(sourceDataset)
	if err != nil {
		return nil, "", fmt.Errorf("invalid_prune_source_dataset: %w", err)
	}
	remoteSource, err := canonicalZeltaEndpoint(target, destSuffix)
	if err != nil {
		return nil, "", err
	}
	extraEnv := s.buildZeltaEnv(target)

	output, err := runZeltaWithEnv(
		ctx,
		extraEnv,
		"prune",
		"--no-ranges",
		fmt.Sprintf("--keep-snap-num=%d", keepLast),
		"--keep-snap-days=0",
		remoteSource,
		source.String(),
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
		snap, err := remoteexec.ParseZFSSnapshot(snapshot)
		if err != nil {
			continue
		}
		if err := s.destroyLocalDataset(ctx, snap.String(), false); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) DestroyTargetSnapshotsByName(ctx context.Context, target *clusterModels.BackupTarget, targetSnapshots []string) error {
	_, root, err := canonicalizeBackupTarget(target)
	if err != nil {
		return err
	}
	for _, targetSnapshot := range targetSnapshots {
		snapshot, err := remoteexec.ParseZFSSnapshot(targetSnapshot)
		if err != nil || !snapshot.Dataset().Within(root) {
			continue
		}
		snap := snapshot.String()

		out, err := s.runTargetSSH(ctx, target, "zfs", "destroy", snap)
		if err != nil {
			if isRemoteSubcommandBlocked(out) {
				logger.L.Warn().
					Str("ssh_host", target.SSHHost).
					Str("dataset", remoteDatasetForLog(snapshot.Dataset().String())).
					Msg("remote_zfs_destroy_not_permitted_skipped")
				continue
			}
			return err
		}
	}

	return nil
}

func isValidZFSSnapshotName(name string) bool {
	_, err := remoteexec.ParseZFSSnapshot(name)
	return err == nil
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
