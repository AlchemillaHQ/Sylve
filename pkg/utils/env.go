// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import "strings"

func FilterEnv(env []string, names ...string) []string {
	if len(env) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, candidate := range names {
			if name == candidate {
				drop = true
				break
			}
		}
		if !drop {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}
