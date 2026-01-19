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
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	"github.com/alchemillahq/sylve/pkg/utils"
)

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
