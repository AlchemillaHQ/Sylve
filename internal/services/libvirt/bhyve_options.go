// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import "strings"

func normalizeExtraBhyveOptions(options []string) []string {
	if len(options) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(options))
	for _, entry := range options {
		for _, line := range strings.Split(entry, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			normalized = append(normalized, trimmed)
		}
	}

	return normalized
}
