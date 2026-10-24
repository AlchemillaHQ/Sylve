// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/creack/pty"
)

func (c *Client) Apply(ctx context.Context, args []string, approved Plan) error {
	if c.Target.Offline {
		return fmt.Errorf("jail_not_running")
	}
	if err := c.validateScopedChanges(approved); err != nil {
		return err
	}
	if c.Scope == ScopeSystem {
		inventory, err := c.Inventory(ctx)
		if err != nil {
			return err
		}
		info, err := c.InspectSystem(ctx, inventory)
		if err != nil {
			return err
		}
		if c.System == nil || info != *c.System {
			return fmt.Errorf("package_plan_stale")
		}
	}
	argv, err := c.args(args...)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.Binary, argv...)
	cmd.Env = Environment()
	terminal, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer terminal.Close()
	transcript := ""
	confirmed := false
	var approvalError error
	buffer := make([]byte, 4096)
	for {
		n, readErr := terminal.Read(buffer)
		if n > 0 {
			chunk := string(buffer[:n])
			if c.Log != nil {
				_, _ = io.WriteString(c.Log, Redact(chunk))
			}
			transcript += chunk
			if len(transcript) > 4<<20 {
				if !confirmed {
					approvalError = fmt.Errorf("package_confirmation_output_too_large")
				}
				transcript = transcript[len(transcript)-(4<<20):]
			}
			prompt := strings.TrimSpace(transcript)
			if strings.HasSuffix(prompt, "[y/N]:") || strings.HasSuffix(prompt, "[Y/n]:") {
				if confirmed && approvalError == nil {
					approvalError = fmt.Errorf("additional_package_transaction_unsupported: request_a_fresh_preview")
				}
				if approvalError == nil && !strings.HasSuffix(prompt, "Proceed with this action? [y/N]:") {
					approvalError = fmt.Errorf("unexpected_package_prompt")
				}
				if approvalError == nil {
					actual, parseErr := ParsePlan(transcript)
					approvalError = parseErr
					if approvalError == nil {
						approvalError = ValidateTransaction(actual, approved)
					}
				}
				if approvalError == nil {
					approvalError = ValidateTarget(ctx, c.Target)
				}
				if approvalError == nil && c.Target.Dataset != "" {
					_, approvalError = ValidatePrivateRoot(ctx, c.Target)
					if approvalError == nil {
						approvalError = ValidatePackagePaths(ctx, c.Target, c.ManagedPaths)
					}
				}
				if approvalError != nil {
					_, _ = terminal.Write([]byte("n\n"))
				} else {
					if _, err = terminal.Write([]byte("y\n")); err != nil {
						approvalError = err
						break
					}
					confirmed = true
				}
				transcript = ""
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, syscall.EIO) {
				approvalError = readErr
			}
			break
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if approvalError != nil {
		return approvalError
	}
	if err != nil {
		return fmt.Errorf("package_upgrade_failed: %w", err)
	}
	if !confirmed {
		return fmt.Errorf("package_transaction_not_confirmed")
	}
	return nil
}

func ValidateTarget(ctx context.Context, target Target) error {
	if err := target.validate(); err != nil {
		return err
	}
	if target.Host {
		return validateHostTarget()
	}
	targets, err := ListTargets(ctx)
	if err != nil {
		return err
	}
	for _, jail := range targets {
		if target.Offline {
			if jail.Name == target.Name || filepath.Clean(jail.Root) == target.Root {
				return fmt.Errorf("jail_runtime_identity_changed")
			}
			continue
		}
		if jail.JID == target.JID && jail.Name == target.Name && filepath.Clean(jail.Root) == filepath.Clean(target.Root) {
			return nil
		}
	}
	if target.Offline {
		_, err := ValidatePrivateRoot(ctx, target)
		return err
	}
	return fmt.Errorf("jail_runtime_identity_changed")
}

func ListTargets(ctx context.Context) ([]Target, error) {
	cmd := exec.CommandContext(ctx, "/usr/sbin/jls", "--libxo", "json", "jid", "name", "path")
	cmd.Env = Environment()
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var result struct {
		Info struct {
			Jails []struct {
				JID  json.Number `json:"jid"`
				Name string      `json:"name"`
				Path string      `json:"path"`
			} `json:"jail"`
		} `json:"jail-information"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	targets := make([]Target, 0, len(result.Info.Jails))
	for _, jail := range result.Info.Jails {
		jid, err := jail.JID.Int64()
		if err != nil || jid <= 0 {
			return nil, fmt.Errorf("invalid_jail_runtime_state")
		}
		targets = append(targets, Target{JID: int(jid), Name: jail.Name, Root: jail.Path})
	}
	return targets, nil
}
