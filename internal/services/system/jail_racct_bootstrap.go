// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"fmt"
	"os"
	"strings"
)

const (
	jailRacctLoaderKey   = "kern.racct.enable"
	jailRacctLoaderValue = "1"
)

func upsertLoaderConfSetting(lines []string, key, expectedValue string) ([]string, bool) {
	updated := make([]string, 0, len(lines)+1)
	seen := false
	changed := false

	for _, line := range lines {
		parsedKey, rawValue, ok := parseLoaderConfAssignment(line)
		if !ok || parsedKey != key {
			updated = append(updated, line)
			continue
		}

		if !seen {
			if parseLoaderPPTValue(rawValue) == expectedValue {
				updated = append(updated, line)
			} else {
				updated = append(updated, fmt.Sprintf("%s=%s", key, expectedValue))
				changed = true
			}
			seen = true
			continue
		}

		// Remove duplicate active assignments to avoid ambiguous boot behavior.
		changed = true
	}

	if !seen {
		updated = append(updated, fmt.Sprintf("%s=%s", key, expectedValue))
		changed = true
	}

	return updated, changed
}

func (s *Service) ensureJailRacctEnabledAtBoot() (bool, error) {
	lines, perm, err := readLoaderConf()
	if err != nil {
		return false, fmt.Errorf("failed_to_read_loader_conf_for_jails_racct: %w", err)
	}

	updated, changed := upsertLoaderConfSetting(lines, jailRacctLoaderKey, jailRacctLoaderValue)
	if !changed {
		return false, nil
	}

	out := ""
	if len(updated) > 0 {
		out = strings.Join(updated, "\n") + "\n"
	}

	if err := os.WriteFile(loaderConfPath, []byte(out), perm); err != nil {
		return false, fmt.Errorf("failed_to_write_loader_conf_for_jails_racct: %w", err)
	}

	return true, nil
}
