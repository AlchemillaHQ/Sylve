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
		Updates(map[string]interface{}{
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
			newLines = append(newLines, fmt.Sprintf(`	mount.fstab = "%s";`, fstabPath))
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
