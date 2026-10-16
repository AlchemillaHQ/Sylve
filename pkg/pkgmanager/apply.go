// SPDX-License-Identifier: BSD-2-Clause

package pkgmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
	cmd := exec.Command(c.Binary, argv...)
	cmd.Env = Environment()
	if c.Guard != nil {
		cmd.ExtraFiles = []*os.File{c.Guard}
	}
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

type Work struct {
	Binary          string      `json:"binary"`
	Target          Target      `json:"target"`
	Arguments       []string    `json:"arguments"`
	Plan            Plan        `json:"plan"`
	InventoryDigest string      `json:"inventoryDigest"`
	RepositoryHash  string      `json:"repositoryHash"`
	CatalogHash     string      `json:"catalogHash"`
	ManagedPaths    []string    `json:"managedPaths"`
	Scope           Scope       `json:"scope"`
	System          *SystemInfo `json:"system,omitempty"`
}

type WorkResult struct {
	Error string `json:"error"`
}

func RunWorker(manifest string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root_privileges_required")
	}
	info, err := os.Lstat(manifest)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return fmt.Errorf("untrusted_package_worker_manifest")
	}
	directory := filepath.Dir(manifest)
	parent, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	pst, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || pst.Uid != 0 || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		return fmt.Errorf("untrusted_package_worker_directory")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var work Work
	if err = json.Unmarshal(data, &work); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(directory, "output.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	guard := os.NewFile(3, "package-worker-guard")
	if guard == nil {
		return fmt.Errorf("package_worker_guard_unavailable")
	}
	defer guard.Close()
	writer := NewLogWriter(log)
	defer writer.Flush()
	client := Client{Binary: work.Binary, Target: work.Target, Log: writer, Guard: guard, ManagedPaths: work.ManagedPaths, Scope: work.Scope, System: work.System}
	runErr := func() error {
		if work.Target.Offline {
			return fmt.Errorf("jail_not_running")
		}
		preflight, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := Probe(work.Binary); err != nil {
			return err
		}
		if err := ValidateTarget(preflight, work.Target); err != nil {
			return err
		}
		if _, err := ValidatePrivateRoot(preflight, work.Target); err != nil {
			return err
		}
		inventory, err := client.Inventory(preflight)
		if err != nil {
			return err
		}
		if InventoryDigest(inventory) != work.InventoryDigest {
			return fmt.Errorf("package_plan_stale")
		}
		repos, hash, err := client.Repositories(preflight)
		if err != nil {
			return err
		}
		if hash != work.RepositoryHash {
			return fmt.Errorf("package_plan_stale")
		}
		catalog, err := client.CatalogDigest(preflight, repos)
		if err != nil {
			return err
		}
		if catalog != work.CatalogHash {
			return fmt.Errorf("package_plan_stale")
		}
		return client.Apply(context.Background(), work.Arguments, work.Plan)
	}()
	result := WorkResult{}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	data, err = json.Marshal(result)
	if err != nil {
		return err
	}
	temporary := filepath.Join(directory, "result.tmp")
	if err = os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	writer.Flush()
	_ = log.Sync()
	if err = os.Rename(temporary, filepath.Join(directory, "result.json")); err != nil {
		return err
	}
	return runErr
}

func ValidateTarget(ctx context.Context, target Target) error {
	if err := target.validate(); err != nil {
		return err
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
