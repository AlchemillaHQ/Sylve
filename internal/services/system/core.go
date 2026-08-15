// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"

	"github.com/alchemillahq/sylve/pkg/utils"
)

var errRestartRequesterUnavailable = errors.New("self_restart_unavailable")

func (s *Service) IsJailed() bool {
	return s.jailed
}

func (s *Service) SetRestartRequester(requester func()) {
	s.restartRequester = requester
}

func (s *Service) RebootSystem() error {
	if s.IsJailed() {
		if s.restartRequester == nil {
			return errRestartRequesterUnavailable
		}
		s.restartRequester()
		return nil
	}

	runCommand := s.runCommand
	if runCommand == nil {
		runCommand = utils.RunCommand
	}

	_, err := runCommand(
		"/sbin/shutdown",
		"-r",
		"now",
		"Reboot initiated by Sylve",
	)

	return err
}
