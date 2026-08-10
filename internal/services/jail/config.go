// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type jailFileSnapshot struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

func captureJailFile(path string) (jailFileSnapshot, error) {
	snapshot := jailFileSnapshot{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, fmt.Errorf("failed_to_stat_jail_file: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot, fmt.Errorf("failed_to_read_jail_file: %w", err)
	}
	snapshot.data = data
	snapshot.mode = info.Mode().Perm()
	snapshot.existed = true
	return snapshot, nil
}

func captureJailFiles(paths []string) ([]jailFileSnapshot, error) {
	seen := make(map[string]struct{}, len(paths))
	snapshots := make([]jailFileSnapshot, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}

		snapshot, err := captureJailFile(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func restoreJailFiles(snapshots []jailFileSnapshot) error {
	var restoreErr error
	for _, snapshot := range snapshots {
		if snapshot.existed {
			if err := os.MkdirAll(filepath.Dir(snapshot.path), 0o755); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_prepare_jail_file_restore: %w", err))
				continue
			}
			if err := utils.AtomicWriteFile(snapshot.path, snapshot.data, snapshot.mode); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_restore_jail_file: %w", err))
			}
			continue
		}
		if err := os.Remove(snapshot.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("failed_to_remove_new_jail_file: %w", err))
		}
	}
	return restoreErr
}

func (s *Service) GetJailConfig(ctid uint) (string, error) {
	if ctid == 0 {
		return "", fmt.Errorf("invalid_ct_id")
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctid))

	config, err := os.ReadFile(jailConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed_to_read_jail_config: %w", err)
	}

	return string(config), nil
}

func (s *Service) SaveJailConfig(ctid uint, cfg string) error {
	if ctid == 0 {
		return fmt.Errorf("invalid_ct_id")
	}

	re := regexp.MustCompile(`\n{3,}`)
	cfg = re.ReplaceAllString(cfg, "\n\n")

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	if err := os.MkdirAll(jailDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_jail_directory: %w", err)
	}

	jailConfigPath := filepath.Join(jailDir, fmt.Sprintf("%d.conf", ctid))
	if err := utils.AtomicWriteFile(jailConfigPath, []byte(cfg), 0644); err != nil {
		return fmt.Errorf("failed_to_write_jail_config: %w", err)
	}

	return nil
}

func (s *Service) AppendToConfig(ctid uint, current string, toAppend string) (string, error) {
	lastCurly := strings.LastIndex(current, "}")
	if lastCurly == -1 {
		return "", fmt.Errorf("invalid_config_format")
	}

	newConfig := current[:lastCurly] + toAppend + "\n" + current[lastCurly:]

	return newConfig, nil
}

func (s *Service) GetHookScriptPath(ctid uint, hookName string) (string, error) {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return "", fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, fmt.Sprintf("%d", ctid))
	hookScriptPath := filepath.Join(jailDir, "scripts", fmt.Sprintf("%s.sh", hookName))

	if _, err := os.Stat(hookScriptPath); os.IsNotExist(err) {
		return "", fmt.Errorf("hook_script_not_found")
	} else if err != nil {
		return "", fmt.Errorf("failed_to_stat_hook_script: %w", err)
	}

	return hookScriptPath, nil
}

func (s *Service) ensureShebang(content string) string {
	// If completely empty, return just shebang
	if strings.TrimSpace(content) == "" {
		return "#!/bin/sh\n"
	}

	// Strip leading blank lines so we can ensure the shebang is line 1
	trimmed := strings.TrimLeft(content, "\r\n")

	if !strings.HasPrefix(trimmed, "#!") {
		// Prepend shebang if it's not already there
		return "#!/bin/sh\n" + trimmed
	}

	// Already has a shebang at the top after trimming blank lines
	return trimmed
}

func (s *Service) GetJailBaseMountPoint(ctid uint) (string, error) {
	cfg, err := s.GetJailConfig(ctid)
	if err != nil {
		return "", err
	}

	re := regexp.MustCompile(`path\s*=\s*["']([^"']+)["']`)
	matches := re.FindStringSubmatch(cfg)
	if len(matches) < 2 {
		return "", fmt.Errorf("jail_path_not_found_in_config")
	}

	return matches[1], nil
}

func (s *Service) AddSylveNetworkToHook(content string, networkContent string) string {
	const start = "### Start Sylve-Managed Network ###"
	const end = "### End Sylve-Managed Network ###"

	// Ensure content has proper shebang
	content = s.ensureShebang(content)

	// Remove existing Sylve network section if it exists
	content = s.RemoveSylveNetworkFromHook(content)

	// If no network content, just return cleaned content
	if strings.TrimSpace(networkContent) == "" {
		return content
	}

	// Add new network content at the end before any user-managed sections
	userStart := strings.Index(content, "### Start User-Managed Hook ###")
	if userStart != -1 {
		// Insert before user-managed section
		return content[:userStart] + start + "\n" + networkContent + "\n" + end + "\n\n" + content[userStart:]
	} else {
		// Add at the end
		return content + "\n" + start + "\n" + networkContent + "\n" + end + "\n"
	}
}

func (s *Service) AddSylveNetworkToHookAtEnd(content string, networkContent string) string {
	const start = "### Start Sylve-Managed Network ###"
	const end = "### End Sylve-Managed Network ###"

	content = s.ensureShebang(content)
	content = s.RemoveSylveNetworkFromHook(content)

	trimmedNetwork := strings.TrimSpace(networkContent)
	if trimmedNetwork == "" {
		return content
	}

	trimmedContent := strings.TrimRight(content, "\n")
	if trimmedContent == "" {
		trimmedContent = "#!/bin/sh"
	}

	return trimmedContent + "\n\n" + start + "\n" + trimmedNetwork + "\n" + end + "\n"
}

func (s *Service) RemoveSylveNetworkFromHook(content string) string {
	const start = "### Start Sylve-Managed Network ###"
	const end = "### End Sylve-Managed Network ###"

	si := strings.Index(content, start)
	if si == -1 {
		return content // No Sylve network section found
	}

	ei := strings.Index(content[si:], end)
	if ei == -1 {
		return content // No end marker found
	}

	ei = si + ei + len(end)

	// Remove the entire section including trailing newlines
	result := content[:si] + content[ei:]

	// Clean up any double newlines
	result = strings.ReplaceAll(result, "\n\n\n", "\n\n")

	return result
}
