// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

func rctlSubject(runtimeID string) string { return "jail:" + runtimeID }

func validateLimits(cores uint, memory uint64) error {
	if cores > uint(runtime.NumCPU()) {
		return fmt.Errorf("invalid_cores: host has %d", runtime.NumCPU())
	}
	if memory > 0 {
		total, err := utils.GetSystemMemoryBytes()
		if err != nil {
			return err
		}
		if memory < 16<<20 || memory > uint64(total) {
			return fmt.Errorf("invalid_memory: must be between 16 MiB and host memory")
		}
	}
	if cores > 0 || memory > 0 {
		if enabled, _ := sysctl.GetInt64("kern.racct.enable"); enabled != 1 {
			return fmt.Errorf("container_limits_require_racct: set kern.racct.enable=1 in /boot/loader.conf and reboot")
		}
	}
	return nil
}

func applyLimits(ctx context.Context, record containerModels.Container) error {
	if record.RuntimeID == "" {
		return nil
	}
	removeLimits(ctx, record.RuntimeID)
	var rules []string
	if record.Cores > 0 {
		rules = append(rules, fmt.Sprintf("%s:pcpu:deny=%d", rctlSubject(record.RuntimeID), record.Cores*100))
	}
	if record.Memory > 0 {
		rules = append(rules, fmt.Sprintf("%s:memoryuse:deny=%d", rctlSubject(record.RuntimeID), record.Memory))
	}
	for _, rule := range rules {
		if output, err := exec.CommandContext(ctx, "/usr/bin/rctl", "-a", rule).CombinedOutput(); err != nil {
			return fmt.Errorf("failed_to_apply_container_limit: %s", strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func removeLimits(ctx context.Context, runtimeID string) {
	_ = exec.CommandContext(ctx, "/usr/bin/rctl", "-r", rctlSubject(runtimeID)).Run()
}
