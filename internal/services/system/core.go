// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import "github.com/alchemillahq/sylve/pkg/utils"

func (s *Service) RebootSystem() error {
	_, err := utils.RunCommand(
		"shutdown",
		"-r",
		"now",
		"Reboot initiated by Sylve",
	)

	return err
}
