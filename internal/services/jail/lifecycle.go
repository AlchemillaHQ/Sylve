// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

const (
	defaultFreeBSDJailStartCommand = "/bin/sh /etc/rc"
	defaultFreeBSDJailStopCommand  = "/bin/sh /etc/rc.shutdown"
	jailStartHookExecPath          = "/usr/local/sylve/scripts/start.sh"
	jailStopHookExecPath           = "/usr/local/sylve/scripts/stop.sh"
)

func legacyLifecycleDefault(phase jailModels.JailHookPhase, script string) bool {
	return phase == jailModels.JailHookPhaseStart && script == defaultFreeBSDJailStartCommand ||
		phase == jailModels.JailHookPhaseStop && script == defaultFreeBSDJailStopCommand
}

func NormalizeLegacyLifecycleHooks(
	jailType jailModels.JailType,
	hooks []jailModels.JailHooks,
) ([]jailModels.JailHooks, bool) {
	normalized := make([]jailModels.JailHooks, len(hooks))
	copy(normalized, hooks)
	if jailType != jailModels.JailTypeFreeBSD {
		return normalized, false
	}

	changed := false
	for index := range normalized {
		hook := &normalized[index]
		if !legacyLifecycleDefault(hook.Phase, hook.Script) {
			continue
		}
		if hook.Enabled || hook.Script != "" {
			changed = true
		}
		hook.Enabled = false
		hook.Script = ""
	}
	return normalized, changed
}

func lifecycleHooksFromRows(rows []jailModels.JailHooks) jailServiceInterfaces.Hooks {
	var hooks jailServiceInterfaces.Hooks
	for _, row := range rows {
		phase := jailServiceInterfaces.HookPhase{Enabled: row.Enabled, Script: row.Script}
		switch row.Phase {
		case jailModels.JailHookPhasePreStart:
			hooks.Prestart = phase
		case jailModels.JailHookPhaseStart:
			hooks.Start = phase
		case jailModels.JailHookPhasePostStart:
			hooks.Poststart = phase
		case jailModels.JailHookPhasePreStop:
			hooks.Prestop = phase
		case jailModels.JailHookPhaseStop:
			hooks.Stop = phase
		case jailModels.JailHookPhasePostStop:
			hooks.Poststop = phase
		}
	}
	return hooks
}

func normalizeLifecycleHookPayload(
	jailType jailModels.JailType,
	hooks jailServiceInterfaces.Hooks,
) jailServiceInterfaces.Hooks {
	if jailType != jailModels.JailTypeFreeBSD {
		return hooks
	}
	if hooks.Start.Script == defaultFreeBSDJailStartCommand {
		hooks.Start = jailServiceInterfaces.HookPhase{}
	}
	if hooks.Stop.Script == defaultFreeBSDJailStopCommand {
		hooks.Stop = jailServiceInterfaces.HookPhase{}
	}
	return hooks
}

func normalizeTemplateLifecycleHooks(
	jailType jailModels.JailType,
	hooks []jailModels.JailTemplateHook,
) []jailModels.JailTemplateHook {
	normalized := append([]jailModels.JailTemplateHook(nil), hooks...)
	if jailType != jailModels.JailTypeFreeBSD {
		return normalized
	}
	for index := range normalized {
		if legacyLifecycleDefault(normalized[index].Phase, normalized[index].Script) {
			normalized[index].Enabled = false
			normalized[index].Script = ""
		}
	}
	return normalized
}

func canonicalJailStartStopExecLines(
	jailType jailModels.JailType,
	customStart bool,
	customStop bool,
) []string {
	lines := make([]string, 0, 2)
	if customStart {
		lines = append(lines, fmt.Sprintf("\texec.start = %s;", strconv.Quote(jailStartHookExecPath)))
	} else if jailType == jailModels.JailTypeFreeBSD {
		lines = append(lines, fmt.Sprintf("\texec.start = %s;", strconv.Quote(defaultFreeBSDJailStartCommand)))
	}
	if customStop {
		lines = append(lines, fmt.Sprintf("\texec.stop = %s;", strconv.Quote(jailStopHookExecPath)))
	} else if jailType == jailModels.JailTypeFreeBSD {
		lines = append(lines, fmt.Sprintf("\texec.stop = %s;", strconv.Quote(defaultFreeBSDJailStopCommand)))
	}
	return lines
}

func jailLifecycleConfigNeedsReconcile(content string) bool {
	inAdditional := false
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case jailAdditionalOptionsStart:
			inAdditional = true
			continue
		case jailAdditionalOptionsEnd:
			inAdditional = false
			continue
		}
		if inAdditional {
			continue
		}
		if value, appendValue, ok := jailConfigLifecycleAssignment(line, "exec.start"); ok && appendValue && value == jailStartHookExecPath {
			return true
		}
		if value, appendValue, ok := jailConfigLifecycleAssignment(line, "exec.stop"); ok && appendValue && value == jailStopHookExecPath {
			return true
		}
	}
	return false
}

func (s *Service) ReconcileLifecycleConfigs() error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("jail_service_not_initialized")
	}
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("failed_to_get_jails_path: %w", err)
	}

	var jails []jailModels.Jail
	if err := s.DB.Preload("JailHooks").Find(&jails).Error; err != nil {
		return fmt.Errorf("failed_to_load_jails: %w", err)
	}

	var reconcileErrors []error
	for index := range jails {
		jail := &jails[index]
		configPath := filepath.Join(
			jailsPath,
			strconv.FormatUint(uint64(jail.CTID), 10),
			fmt.Sprintf("%d.conf", jail.CTID),
		)
		content, err := os.ReadFile(configPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"failed_to_read_jail_config(ct_id=%d): %w",
				jail.CTID,
				err,
			))
			continue
		}
		normalized, hooksChanged := NormalizeLegacyLifecycleHooks(jail.Type, jail.JailHooks)
		if !hooksChanged && !jailLifecycleConfigNeedsReconcile(string(content)) {
			continue
		}
		allowed, err := s.canMutateProtectedJail(jail.CTID)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"failed_to_check_jail_lifecycle_normalization_authority(ct_id=%d): %w",
				jail.CTID,
				err,
			))
			continue
		}
		if !allowed {
			continue
		}
		if err := s.ModifyLifecycleHooks(jail.CTID, lifecycleHooksFromRows(normalized)); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf(
				"failed_to_normalize_jail_lifecycle(ct_id=%d): %w",
				jail.CTID,
				err,
			))
		}
	}

	return errors.Join(reconcileErrors...)
}
