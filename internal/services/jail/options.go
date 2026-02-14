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
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func isManagedAllowedOptionLine(trimmedLine string) bool {
	if !strings.HasPrefix(trimmedLine, "allow.") || !strings.HasSuffix(trimmedLine, ";") {
		return false
	}

	opt := strings.TrimSuffix(trimmedLine, ";")
	return utils.IsValidJailAllowedOpts([]string{opt})
}

func (s *Service) ModifyBootOrder(ctId uint, startAtBoot bool, bootOrder int) error {
	err := s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Updates(map[string]any{
			"start_order":   bootOrder,
			"start_at_boot": startAtBoot,
		}).Error
	return err
}

func (s *Service) ModifyFstab(ctId uint, fstab string) error {
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, strconv.FormatUint(uint64(ctId), 10))
	fstabPath := filepath.Join(jailDir, "fstab")

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	lines := utils.SplitLines(cfg)
	newLines := make([]string, 0, len(lines))
	found := false

	for _, line := range lines {
		if strings.Contains(line, "mount.fstab") {
			if fstab != "" {
				newLines = append(newLines, fmt.Sprintf(`	mount.fstab = "%s";`, fstabPath))
				found = true
			}
			continue
		}
		newLines = append(newLines, line)
	}

	if fstab == "" {
		if err := utils.DeleteFileIfExists(fstabPath); err != nil {
			return fmt.Errorf("failed_to_delete_fstab_file: %w", err)
		}
	} else {
		if err := utils.AtomicWriteFile(fstabPath, []byte(fstab), 0644); err != nil {
			return fmt.Errorf("failed_to_write_fstab_file: %w", err)
		}

		if !found {
			for i := len(newLines) - 1; i >= 0; i-- {
				if strings.TrimSpace(newLines[i]) == "}" {
					fstabLine := fmt.Sprintf(`	mount.fstab = "%s";`, fstabPath)
					newLines = append(newLines[:i], append([]string{fstabLine}, newLines[i:]...)...)
					break
				}
			}
		}
	}

	cfg = strings.Join(newLines, "\n")
	if err := s.SaveJailConfig(ctId, cfg); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	err = s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Update("fstab", fstab).
		Error
	if err != nil {
		return fmt.Errorf("failed_to_update_fstab_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyDevfsRuleset(ctId uint, rules string) error {
	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	lines := utils.SplitLines(cfg)
	newLines := make([]string, 0, len(lines))
	found := false

	for _, line := range lines {
		if strings.Contains(line, "devfs_ruleset") {
			if rules != "" {
				newLines = append(newLines,
					fmt.Sprintf("\tdevfs_ruleset=%d;", ctId),
				)
				found = true
			}
			continue
		}
		newLines = append(newLines, line)
	}

	if rules == "" {
		if err := s.RemoveDevfsRulesForCTID(ctId); err != nil {
			return fmt.Errorf("failed_to_remove_devfs_rules: %w", err)
		}
	} else {
		entry := fmt.Sprintf(
			"\n[devfsrules_jails_sylve_%d=%d]\n%s\n",
			ctId,
			ctId,
			strings.TrimSpace(rules),
		)

		if err := utils.AtomicAppendFile(
			"/etc/devfs.rules",
			[]byte(entry),
			0644,
		); err != nil {
			return fmt.Errorf("failed_to_write_devfs_rules: %w", err)
		}

		if !found {
			newLines = append(newLines,
				fmt.Sprintf("\tdevfs_ruleset=%d;", ctId),
			)
		}
	}

	cfg = strings.Join(newLines, "\n")
	if err := s.SaveJailConfig(ctId, cfg); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	err = s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Update("dev_fs_ruleset", rules).
		Error

	if err != nil {
		return fmt.Errorf("failed_to_update_devfs_rules_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyAdditionalOptions(ctId uint, options string) error {
	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	if jail.AdditionalOptions != "" {
		cfg = strings.Replace(
			cfg,
			"\n### These are user-defined additional options ###\n\n"+jail.AdditionalOptions+"\n",
			"",
			1,
		)
	}

	if options != "" {
		block := fmt.Sprintf(
			"\n### These are user-defined additional options ###\n\n%s\n",
			strings.TrimSpace(options),
		)

		cfg, err = s.AppendToConfig(ctId, cfg, block)
		if err != nil {
			return fmt.Errorf("failed_to_append_additional_options: %w", err)
		}
	}

	if err := s.SaveJailConfig(ctId, cfg); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	if err := s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Update("additional_options", options).
		Error; err != nil {
		return fmt.Errorf("failed_to_update_additional_options_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyAllowedOptions(ctId uint, options []string) error {
	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	normalizedOptions := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))

	for _, opt := range options {
		trimmed := strings.TrimSpace(opt)
		if trimmed == "" {
			continue
		}

		if _, exists := seen[trimmed]; exists {
			continue
		}

		seen[trimmed] = struct{}{}
		normalizedOptions = append(normalizedOptions, trimmed)
	}

	if !utils.IsValidJailAllowedOpts(normalizedOptions) {
		return fmt.Errorf("invalid_jail_allowed_options")
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	lines := utils.SplitLines(cfg)
	newLines := make([]string, 0, len(lines))
	inAdditionalOptionsSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(line, "### These are user-defined additional options ###") {
			inAdditionalOptionsSection = true
			newLines = append(newLines, line)
			continue
		}

		if !inAdditionalOptionsSection {
			if isManagedAllowedOptionLine(trimmed) {
				continue
			}

			if trimmed == "mount.devfs;" {
				continue
			}

			if strings.HasPrefix(trimmed, "devfs_ruleset=") && strings.HasSuffix(trimmed, ";") {
				continue
			}
		}

		newLines = append(newLines, line)
	}

	if len(normalizedOptions) > 0 {
		blockLines := make([]string, 0, len(normalizedOptions)+4)
		for _, opt := range normalizedOptions {
			blockLines = append(blockLines, fmt.Sprintf("\t%s;", opt))
		}

		if utils.StringInSlice("allow.mount.devfs", normalizedOptions) {
			blockLines = append(blockLines, "\tmount.devfs;")
			if strings.TrimSpace(jail.DevFSRuleset) != "" {
				blockLines = append(blockLines, fmt.Sprintf("\tdevfs_ruleset=%d;", ctId))
			} else {
				blockLines = append(blockLines, "\tdevfs_ruleset=61181;")
			}
		}

		insertIdx := len(newLines)
		for i, line := range newLines {
			if strings.Contains(line, "### These are user-defined additional options ###") {
				insertIdx = i
				break
			}

			if strings.TrimSpace(line) == "}" {
				insertIdx = i
				break
			}
		}

		if insertIdx > 0 && strings.TrimSpace(newLines[insertIdx-1]) != "" {
			blockLines = append([]string{""}, blockLines...)
		}

		if insertIdx < len(newLines) && strings.TrimSpace(newLines[insertIdx]) != "" {
			blockLines = append(blockLines, "")
		}

		newLines = append(newLines[:insertIdx], append(blockLines, newLines[insertIdx:]...)...)
	}

	cfg = strings.Join(newLines, "\n")

	if err := s.SaveJailConfig(ctId, cfg); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	if err := s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Select("allowed_options").
		Updates(&jailModels.Jail{
			AllowedOptions: normalizedOptions,
		}).
		Error; err != nil {
		return fmt.Errorf("failed_to_update_allowed_options_in_db: %w", err)
	}

	return nil
}

func (s *Service) ModifyMetadata(ctId uint, meta, env string) error {
	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	lines := utils.SplitLines(cfg)
	newLines := make([]string, 0, len(lines))

	var metaFound, envFound bool

	for _, line := range lines {
		switch {
		case strings.Contains(line, "meta ="):
			if meta != "" {
				newLines = append(newLines, fmt.Sprintf(`	meta = "%s";`, strings.TrimSpace(meta)))
				metaFound = true
			}
			continue
		case strings.Contains(line, "env ="):
			if env != "" {
				newLines = append(newLines, fmt.Sprintf(`	env = "%s";`, strings.TrimSpace(env)))
				envFound = true
			}
			continue
		default:
			newLines = append(newLines, line)
		}
	}

	cfg = strings.Join(newLines, "\n")

	if meta != "" && !metaFound {
		cfg, err = s.AppendToConfig(ctId, cfg, fmt.Sprintf(`	meta = "%s";`, strings.TrimSpace(meta)))
		if err != nil {
			return fmt.Errorf("failed_to_append_meta: %w", err)
		}
	}

	if env != "" && !envFound {
		cfg, err = s.AppendToConfig(ctId, cfg, fmt.Sprintf(`	env = "%s";`, strings.TrimSpace(env)))
		if err != nil {
			return fmt.Errorf("failed_to_append_env: %w", err)
		}
	}

	if err := s.SaveJailConfig(ctId, cfg); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	if err := s.DB.
		Model(&jailModels.Jail{}).
		Where("ct_id = ?", ctId).
		Updates(map[string]interface{}{
			"metadata_meta": meta,
			"metadata_env":  env,
		}).Error; err != nil {
		return fmt.Errorf("failed_to_update_metadata_in_db: %w", err)
	}

	return nil
}

type hookEditTarget struct {
	phase       jailModels.JailHookPhase
	execKey     string
	execPath    string
	hostPath    string
	inJailPath  string
	hookPayload jailServiceInterfaces.HookPhase
}

func (s *Service) hasHookBody(content string) bool {
	lines := strings.Split(content, "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if idx == 0 && strings.HasPrefix(trimmed, "#!") {
			continue
		}
		if trimmed != "" {
			return true
		}
	}
	return false
}

func (s *Service) removeUserManagedHookSection(content string) string {
	const start = "### Start User-Managed Hook ###"
	const end = "### End User-Managed Hook ###"

	content = s.ensureShebang(content)

	si := strings.Index(content, start)
	if si == -1 {
		return content
	}

	ei := strings.Index(content[si:], end)
	if ei == -1 {
		result := strings.TrimRight(content[:si], "\n")
		if result == "" {
			return "#!/bin/sh\n"
		}
		return result + "\n"
	}

	ei = si + ei + len(end)
	result := content[:si] + content[ei:]
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	result = s.ensureShebang(result)
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	return result
}

func (s *Service) ModifyLifecycleHooks(ctId uint, hooks jailServiceInterfaces.Hooks) error {
	jail, err := s.GetJailByCTID(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail: %w", err)
	}

	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	jailDir := filepath.Join(jailsPath, strconv.FormatUint(uint64(ctId), 10))
	hostScriptsDir := filepath.Join(jailDir, "scripts")
	if err := os.MkdirAll(hostScriptsDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_host_scripts_dir: %w", err)
	}

	mountPoint, err := s.GetJailBaseMountPoint(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_mount_point: %w", err)
	}

	inJailScriptsDir := filepath.Join(mountPoint, "usr", "local", "sylve", "scripts")
	if err := os.MkdirAll(inJailScriptsDir, 0755); err != nil {
		return fmt.Errorf("failed_to_create_in_jail_scripts_dir: %w", err)
	}

	targets := []hookEditTarget{
		{
			phase:       jailModels.JailHookPhasePreStart,
			execKey:     "exec.prestart",
			execPath:    filepath.Join(hostScriptsDir, "pre-start.sh"),
			hostPath:    filepath.Join(hostScriptsDir, "pre-start.sh"),
			hookPayload: hooks.Prestart,
		},
		{
			phase:       jailModels.JailHookPhaseStart,
			execKey:     "exec.start",
			execPath:    "/usr/local/sylve/scripts/start.sh",
			hostPath:    filepath.Join(hostScriptsDir, "start.sh"),
			inJailPath:  filepath.Join(inJailScriptsDir, "start.sh"),
			hookPayload: hooks.Start,
		},
		{
			phase:       jailModels.JailHookPhasePostStart,
			execKey:     "exec.poststart",
			execPath:    filepath.Join(hostScriptsDir, "post-start.sh"),
			hostPath:    filepath.Join(hostScriptsDir, "post-start.sh"),
			hookPayload: hooks.Poststart,
		},
		{
			phase:       jailModels.JailHookPhasePreStop,
			execKey:     "exec.prestop",
			execPath:    filepath.Join(hostScriptsDir, "pre-stop.sh"),
			hostPath:    filepath.Join(hostScriptsDir, "pre-stop.sh"),
			hookPayload: hooks.Prestop,
		},
		{
			phase:       jailModels.JailHookPhaseStop,
			execKey:     "exec.stop",
			execPath:    "/usr/local/sylve/scripts/stop.sh",
			hostPath:    filepath.Join(hostScriptsDir, "stop.sh"),
			inJailPath:  filepath.Join(inJailScriptsDir, "stop.sh"),
			hookPayload: hooks.Stop,
		},
		{
			phase:       jailModels.JailHookPhasePostStop,
			execKey:     "exec.poststop",
			execPath:    filepath.Join(hostScriptsDir, "post-stop.sh"),
			hostPath:    filepath.Join(hostScriptsDir, "post-stop.sh"),
			hookPayload: hooks.Poststop,
		},
	}

	shouldWireExec := make(map[jailModels.JailHookPhase]bool, len(targets))

	for _, target := range targets {
		currentContent, err := os.ReadFile(target.hostPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed_to_read_hook_script(%s): %w", target.phase, err)
			}
			currentContent = []byte("#!/bin/sh\n")
		}

		nextContent := string(currentContent)
		scriptBody := target.hookPayload.Script
		if target.hookPayload.Enabled && strings.TrimSpace(scriptBody) != "" {
			nextContent = s.AddSylveAdditionsToHook(nextContent, scriptBody)
		} else {
			nextContent = s.removeUserManagedHookSection(nextContent)
		}

		if err := os.WriteFile(target.hostPath, []byte(nextContent), 0755); err != nil {
			return fmt.Errorf("failed_to_write_hook_script(%s): %w", target.phase, err)
		}

		if target.inJailPath != "" {
			if err := os.WriteFile(target.inJailPath, []byte(nextContent), 0755); err != nil {
				return fmt.Errorf("failed_to_write_in_jail_hook_script(%s): %w", target.phase, err)
			}
		}

		shouldWireExec[target.phase] = s.hasHookBody(nextContent)
	}

	cfg, err := s.GetJailConfig(ctId)
	if err != nil {
		return fmt.Errorf("failed_to_get_jail_config: %w", err)
	}

	lines := utils.SplitLines(cfg)
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		shouldSkip := false
		for _, target := range targets {
			// Only remove Sylve-managed exec lines that point to managed script paths.
			if strings.HasPrefix(trimmed, target.execKey) &&
				strings.Contains(trimmed, fmt.Sprintf("\"%s\"", target.execPath)) {
				shouldSkip = true
				break
			}
		}
		if !shouldSkip {
			filtered = append(filtered, line)
		}
	}

	cfgWithoutExec := strings.Join(filtered, "\n")
	execLines := make([]string, 0, len(targets))
	for _, target := range targets {
		if shouldWireExec[target.phase] {
			execLines = append(execLines, fmt.Sprintf("\t%s += \"%s\";", target.execKey, target.execPath))
		}
	}

	if len(execLines) > 0 {
		cfgWithoutExec, err = s.AppendToConfig(ctId, cfgWithoutExec, "\n"+strings.Join(execLines, "\n")+"\n")
		if err != nil {
			return fmt.Errorf("failed_to_append_exec_lines: %w", err)
		}
	}

	if err := s.SaveJailConfig(ctId, cfgWithoutExec); err != nil {
		return fmt.Errorf("failed_to_save_jail_config: %w", err)
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed_to_begin_tx: %w", tx.Error)
	}

	for _, target := range targets {
		payload := target.hookPayload
		updateRes := tx.
			Model(&jailModels.JailHooks{}).
			Where("jid = ? AND phase = ?", jail.ID, target.phase).
			Updates(map[string]any{
				"enabled": payload.Enabled,
				"script":  payload.Script,
			})
		if updateRes.Error != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed_to_update_hook_row(%s): %w", target.phase, updateRes.Error)
		}

		if updateRes.RowsAffected == 0 {
			if err := tx.Create(&jailModels.JailHooks{
				JailID:  jail.ID,
				Phase:   target.phase,
				Enabled: payload.Enabled,
				Script:  payload.Script,
			}).Error; err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed_to_create_hook_row(%s): %w", target.phase, err)
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed_to_commit_hook_updates: %w", err)
	}

	return nil
}
