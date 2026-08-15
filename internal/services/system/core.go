// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"os"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

// Seams so the jailed self-restart path is testable: override the jail check
// and capture the re-exec instead of replacing the test process image.
var (
	rebootSysctlGetInt64 = sysctl.GetInt64
	rebootReexec         = reexecSelf
)

// reexecSelf restarts Sylve in place by replacing the current process image
// with a fresh copy of the same binary (same PID). This restarts the process
// without depending on an external supervisor, so a jailed restart works
// whether Sylve is run under s6, podman --restart, daemon(8), or standalone.
// If exec fails, it exits instead so a supervised environment can still
// relaunch it.
func reexecSelf() {
	if exe, err := os.Executable(); err == nil {
		syscall.Exec(exe, os.Args, os.Environ())
	}
	os.Exit(0)
}

// IsJailed reports whether this process is running inside a FreeBSD jail.
// Used to decide between an actual host reboot and a self-restart, and
// surfaced via /health/basic so the frontend can label the action
// accordingly ("Restart" vs "Reboot").
func (s *Service) IsJailed() bool {
	jailed, err := rebootSysctlGetInt64("security.jail.jailed")
	return err == nil && jailed == 1
}

func (s *Service) RebootSystem() error {
	// A jailed process can never reboot the host -- PRIV_REBOOT is not
	// grantable to jails (same restriction as PRIV_KLD), so /sbin/shutdown
	// -r now just fails here. What actually needs to happen is for this
	// process to restart in place, so shouldStartAdvancedStartupWorkers()
	// sees Restarted=true on the next boot. We re-exec ourselves rather than
	// rely on a supervisor, so this works within the SAME jail regardless of
	// how Sylve was launched -- no host reboot required.
	if s.IsJailed() {
		var basicSettings models.BasicSettings
		if err := s.DB.First(&basicSettings).Error; err != nil {
			return err
		}
		basicSettings.Restarted = true
		if err := s.DB.Save(&basicSettings).Error; err != nil {
			return err
		}

		go func() {
			// Give the success response time to flush to the client before
			// we re-exec in place.
			time.Sleep(500 * time.Millisecond)
			rebootReexec()
		}()
		return nil
	}

	_, err := utils.RunCommand(
		"/sbin/shutdown",
		"-r",
		"now",
		"Reboot initiated by Sylve",
	)

	return err
}
