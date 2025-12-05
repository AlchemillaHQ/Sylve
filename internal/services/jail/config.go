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
	// Remove Sylve network sections first
	content = s.RemoveSylveNetworkFromHook(content)

	const start = "### Start User-Managed Hook ###"
	const end = "### End User-Managed Hook ###"

	// Always preserve shebang if it exists
	lines := strings.Split(content, "\n")
	shebang := ""
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
		shebang = lines[0] + "\n"
	}

	si := strings.Index(content, start)
	if si == -1 {
		// No user-managed section found, return just shebang
		if shebang == "" {
			return "#!/bin/sh\n"
		}
		return shebang
	}

	ei := strings.Index(content[si:], end)
	if ei == -1 {
		// No end marker, return shebang + everything from start
		return shebang + content[si:]
	}

	ei = si + ei + len(end)

	// Return shebang + user-managed section
	userSection := content[si:ei]
	if shebang == "" {
		return "#!/bin/sh\n" + userSection
	}
	return shebang + userSection
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

func (s *Service) AddSylveAdditionsToHook(content string, additions string) string {
	const start = "### Start User-Managed Hook ###"
	const end = "### End User-Managed Hook ###"

	// Ensure content has proper shebang
	content = s.ensureShebang(content)

	si := strings.Index(content, start)
	if si == -1 {
		// No existing user-managed section, add it
		return content + "\n" + start + "\n" + additions + "\n" + end + "\n"
	}

	ei := strings.Index(content[si:], end)
	if ei == -1 {
		// No end marker, add additions and end marker
		return content + additions + "\n" + end + "\n"
	}

	ei = si + ei + len(end)

	// Replace existing user-managed section
	return content[:si] + start + "\n" + additions + "\n" + end + content[ei:]
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
