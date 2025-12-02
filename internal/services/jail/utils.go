package jail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) RemoveDevfsRulesForCTID(ctid uint) error {
	devFsRulesetPath := filepath.Join("/etc", "devfs.rules")

	data, err := os.ReadFile(devFsRulesetPath)
	if err != nil {
		return fmt.Errorf("failed_to_read_devfs_rules: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	headerPrefix := fmt.Sprintf("[devfsrules_jails_sylve_%d=", ctid)
	inBlock := false
	var out []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inBlock && strings.HasPrefix(trimmed, headerPrefix) {
			inBlock = true
			continue
		}

		if inBlock && strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "=") {
			inBlock = false
		}

		if inBlock {
			continue
		}

		out = append(out, line)
	}

	newContent := strings.Join(out, "\n")

	if string(data) == newContent {
		return nil
	}

	tmpPath := devFsRulesetPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed_to_write_temp_devfs_rules: %w", err)
	}

	if err := os.Rename(tmpPath, devFsRulesetPath); err != nil {
		return fmt.Errorf("failed_to_replace_devfs_rules: %w", err)
	}

	return nil
}
