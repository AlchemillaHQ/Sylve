// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// The package rc script preserves administrator daemon flags, but its one*
// actions normally target every daemon. Limit it to the captured running set.
const recoveryServiceScript = `
sylve_recovery_action=$1
sylve_recovery_daemons=$2
sylve_recovery_rc=$3
sylve_recovery_config=$4
set -- configtest
. "$sylve_recovery_rc" || exit $?
case "$samba_server_role" in
    "member server"|"standalone server") ;;
    *) exit 1 ;;
esac
[ "$samba_server_config" = "$sylve_recovery_config" ] || exit 1
samba_daemons=$sylve_recovery_daemons
run_rc_command "$sylve_recovery_action"
`

func (s *Service) sambaJailID(ctx context.Context) (string, error) {
	jid, err := s.command(ctx, "/sbin/sysctl", []string{"-n", "security.jail.jid"}, "", nil)
	if err != nil {
		return "", err
	}
	jid = strings.TrimSpace(jid)
	if jid == "" || strings.IndexFunc(jid, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", fmt.Errorf("%w: unable to verify the Samba service state", ErrUnavailable)
	}
	return jid, nil
}

func (s *Service) runningRecoveryServices(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	jid, err := s.sambaJailID(ctx)
	if err != nil {
		return nil, err
	}
	var running []string
	for _, daemon := range []string{"nmbd", "smbd", "winbindd", "samba"} {
		_, err := s.run(ctx, "/usr/bin/pgrep", []string{"-j", jid, "-x", daemon}, "", nil)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: unable to verify the Samba service state", ErrUnavailable)
		}
		running = append(running, daemon)
	}
	return running, nil
}

func (s *Service) recoveryServiceAction(ctx context.Context, action string, daemons []string) error {
	if action == "onestart" && s.RecoveryPending() {
		return s.recoveryReady()
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err := s.run(ctx, "/bin/sh", []string{"-c", recoveryServiceScript, "sylve-directory-recovery", action, strings.Join(daemons, " "), s.recoveryServicePath, s.smbPath}, "", nil)
	if err != nil {
		if action == "onestop" {
			return fmt.Errorf("%w: Samba and Winbind could not be stopped for recovery; no recovery state was applied", ErrUnavailable)
		}
		return fmt.Errorf("%w: Samba and Winbind could not be resumed after recovery; check their service status", ErrUnavailable)
	}
	return nil
}

func (s *Service) resumeRecoveryServices(ctx context.Context, previous []string) error {
	running, err := s.runningRecoveryServices(ctx)
	if err != nil {
		return fmt.Errorf("%w: Samba and Winbind state could not be verified after recovery; services may remain stopped", ErrUnavailable)
	}
	var missing []string
	for _, daemon := range previous {
		if !slices.Contains(running, daemon) {
			missing = append(missing, daemon)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := s.recoveryServiceAction(ctx, "onestart", missing); err != nil {
		return err
	}
	running, err = s.runningRecoveryServices(ctx)
	if err != nil {
		return fmt.Errorf("%w: Samba and Winbind state could not be verified after recovery; services may remain stopped", ErrUnavailable)
	}
	for _, daemon := range previous {
		if !slices.Contains(running, daemon) {
			return fmt.Errorf("%w: Samba and Winbind did not fully resume after recovery; check their service status", ErrUnavailable)
		}
	}
	return nil
}

// Caller holds both directory-configuration and service-settings locks.
func (s *Service) withRecoveryServices(ctx context.Context, manage bool, resume []string, operation func([]string) error) (err error) {
	if !manage {
		return operation(nil)
	}
	previous, err := s.runningRecoveryServices(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(previous, "samba") {
		return fmt.Errorf("%w: automatic recovery service handling is only supported for Samba member services, not a domain controller", ErrConflict)
	}
	resume = append(slices.Clone(previous), resume...)
	slices.Sort(resume)
	resume = slices.Compact(resume)
	if len(resume) == 0 {
		return operation(nil)
	}
	defer func() {
		if s.RecoveryPending() {
			if err == nil {
				err = fmt.Errorf("%w: recovery is incomplete; Samba remains stopped until rollback completes", ErrUnavailable)
			}
			return
		}
		// A disconnected browser must not strand services after a safe operation
		// or a rejected restore. An incomplete apply always stays stopped.
		resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if resumeErr := s.resumeRecoveryServices(resumeCtx, resume); resumeErr != nil {
			s.setState("FAULTED", resumeErr)
			err = errors.Join(err, resumeErr)
		}
	}()
	if len(previous) > 0 {
		if err := s.recoveryServiceAction(ctx, "onestop", previous); err != nil {
			return err
		}
	}
	if err := s.requireStoppedSamba(ctx); err != nil {
		return err
	}
	return operation(resume)
}
