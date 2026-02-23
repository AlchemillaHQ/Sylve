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
	"strconv"
	"strings"
)

func (s *Service) RemoveDevfsRulesForCTID(ctid uint) error {
	const devfsRulesPath = "/etc/devfs.rules"

	data, err := os.ReadFile(devfsRulesPath)
	if err != nil {
		return fmt.Errorf("failed_to_read_devfs_rules: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	headerPrefix := fmt.Sprintf("[devfsrules_jails_sylve_%d=", ctid)

	var (
		inBlock bool
		out     = make([]string, 0, len(lines))
	)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inBlock &&
			strings.HasPrefix(trimmed, headerPrefix) &&
			strings.HasSuffix(trimmed, "]") {
			inBlock = true
			continue
		}

		if inBlock &&
			strings.HasPrefix(trimmed, "[") &&
			strings.HasSuffix(trimmed, "]") {
			inBlock = false
			out = append(out, line)
			continue
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

	tmpPath := devfsRulesPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed_to_write_temp_devfs_rules: %w", err)
	}

	if err := os.Rename(tmpPath, devfsRulesPath); err != nil {
		return fmt.Errorf("failed_to_replace_devfs_rules: %w", err)
	}

	return nil
}

func (s *Service) GetJailCTIDFromDataset(dataset string) (uint, error) {
	dataset = strings.TrimRight(dataset, "/")
	parts := strings.Split(dataset, "/")

	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid_dataset_format: %s", dataset)
	}

	ctidStr := parts[len(parts)-1]
	n, err := strconv.ParseUint(ctidStr, 10, 32)

	if err != nil {
		return 0, fmt.Errorf("failed_to_parse_ctid '%s': %w", ctidStr, err)
	}

	return uint(n), nil
}
