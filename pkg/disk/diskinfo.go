// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package disk

import (
	"strings"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func GetDiskSize(device string) (uint64, error) {
	out, err := utils.RunCommand("/usr/sbin/diskinfo", "-v", device)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(out, "\n")

	for _, line := range lines {
		if strings.Contains(line, "mediasize in bytes") {
			size := strings.Fields(line)[0]
			return utils.StringToUint64(size), nil
		}
	}

	return 0, nil
}
