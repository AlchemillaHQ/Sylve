// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
)

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
	if err := os.WriteFile(jailConfigPath, []byte(cfg), 0644); err != nil {
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

func (s *Service) RemoveSylveAdditionsFromHook(content string) string {
	const start = "### Start User-Managed Hook ###"
	const end = "### End User-Managed Hook ###"

	si := strings.Index(content, start)
	if si == -1 {
		return ""
	}

	ei := strings.Index(content[si:], end)
	if ei == -1 {
		return content[si:]
	}

	ei = si + ei + len(end)

	return content[si:ei]
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
