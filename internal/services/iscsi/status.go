// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetStatus() (map[string]string, error) {
	out, err := utils.RunCommandAllowExitCode("/usr/bin/iscsictl", []int{0, 1}, "-L")
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)

		if len(fields) < 3 {
			continue
		}

		targetName := fields[0]
		state := strings.Join(fields[2:], " ")
		result[targetName] = state
	}

	return result, nil
}
