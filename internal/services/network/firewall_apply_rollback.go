// SPDX-License-Identifier: BSD-2-Clause

package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var firewallManagedTableNamePattern = regexp.MustCompile(`^sylve_obj_[0-9]+_inet6?$`)

type firewallFileState struct {
	path    string
	content []byte
	mode    os.FileMode
	exists  bool
}

type firewallApplyState struct {
	enabled         bool
	files           []firewallFileState
	main            firewallFileState
	tables          map[string]string
	candidateTables []string
}

func captureFirewallFile(path string) (firewallFileState, error) {
	state := firewallFileState{path: path, mode: 0644}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() {
		return state, fmt.Errorf("cannot snapshot non-regular firewall file: %s", path)
	}
	state.content, err = os.ReadFile(path)
	state.exists, state.mode = true, info.Mode().Perm()
	return state, err
}

func captureFirewallApplyState(tables map[uint]firewallObjectTable) (*firewallApplyState, error) {
	status, err := firewallRunCommand("/sbin/pfctl", "-si")
	if err != nil {
		return nil, fmt.Errorf("inspect previous PF enabled state: %w", err)
	}
	status = strings.ToLower(status)
	if !strings.Contains(status, "status: enabled") && !strings.Contains(status, "status: disabled") {
		return nil, fmt.Errorf("cannot determine previous PF enabled state")
	}
	state := &firewallApplyState{enabled: strings.Contains(status, "status: enabled")}
	paths := map[string]bool{
		pfMainConfPath: true, pfObjectTablesPath: true, pfNatRulesPath: true,
		pfTrafficRulesPath: true, firewallRCConfPath: true,
	}
	entries, err := os.ReadDir(pfObjectTableEntriesDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if firewallManagedTableNamePattern.MatchString(entry.Name()) && !entry.IsDir() {
			paths[filepath.Join(pfObjectTableEntriesDir, entry.Name())] = true
		}
	}
	for _, table := range tables {
		for _, name := range []string{table.InetName, table.Inet6Name} {
			if name != "" {
				paths[filepath.Join(pfObjectTableEntriesDir, name)] = true
				state.candidateTables = append(state.candidateTables, name)
			}
		}
	}
	names := make([]string, 0, len(paths))
	for path := range paths {
		names = append(names, path)
	}
	sort.Strings(names)
	for _, path := range names {
		file, err := captureFirewallFile(path)
		if err != nil {
			return nil, err
		}
		state.files = append(state.files, file)
		if path == pfMainConfPath {
			state.main = file
		}
	}
	if !state.main.exists {
		if state.enabled {
			return nil, fmt.Errorf("running_pf_has_no_restorable_config: %s", pfMainConfPath)
		}
	} else {
		if _, err := firewallRunCommand("/sbin/pfctl", "-nf", pfMainConfPath); err != nil {
			return nil, fmt.Errorf("previous_pf_config_is_not_restorable: %w", err)
		}
	}
	output, err := firewallRunCommand("/sbin/pfctl", "-s", "Tables")
	if err != nil {
		return nil, fmt.Errorf("snapshot managed PF table names: %w", err)
	}
	state.tables = map[string]string{}
	for _, name := range strings.Fields(output) {
		if !firewallManagedTableNamePattern.MatchString(name) {
			continue
		}
		values, err := firewallRunCommand("/sbin/pfctl", "-t", name, "-T", "show")
		if err != nil {
			return nil, fmt.Errorf("snapshot PF table %s: %w", name, err)
		}
		state.tables[name] = values
	}
	return state, nil
}

func (state *firewallApplyState) restore(loadAttempted bool) error {
	var restoreErr error
	for _, file := range state.files {
		var err error
		if file.exists {
			err = atomicWriteFile(file.path, file.content, file.mode)
		} else {
			err = os.Remove(file.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", file.path, err))
		}
	}
	if !loadAttempted {
		return restoreErr
	}
	if !state.enabled {
		if _, err := firewallRunCommand("/sbin/pfctl", "-d"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not enabled") {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore disabled PF state: %w", err))
		}
	}
	if restoreErr != nil {
		return restoreErr
	}
	tmpDir, err := os.MkdirTemp("", "sylve-pf-rollback-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	content := string(state.main.content)
	if !state.main.exists {
		content = "# PF was disabled with no configuration file\n"
	}
	names := make([]string, 0, len(state.tables))
	for name := range state.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	var declarations strings.Builder
	for _, name := range names {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(state.tables[name]), 0600); err != nil {
			return err
		}
		declaration := fmt.Sprintf("table <%s> persist file %q", name, path)
		pattern := regexp.MustCompile(`(?m)^table <` + regexp.QuoteMeta(name) + `> persist[^\n]*$`)
		if pattern.MatchString(content) {
			content = pattern.ReplaceAllStringFunc(content, func(string) string { return declaration })
		} else {
			declarations.WriteString(declaration + "\n")
		}
	}
	path := filepath.Join(tmpDir, "pf.conf")
	if err := os.WriteFile(path, []byte(declarations.String()+content), 0600); err != nil {
		return err
	}
	if _, err := firewallRunCommand("/sbin/pfctl", "-nf", path); err != nil {
		return fmt.Errorf("validate previous PF policy for restoration: %w", err)
	}
	if _, err := firewallRunCommand("/sbin/pfctl", "-f", path); err != nil {
		return fmt.Errorf("restore previous PF policy: %w", err)
	}
	for _, name := range uniqueStrings(state.candidateTables) {
		if _, existed := state.tables[name]; existed {
			continue
		}
		referenced := false
		for _, file := range state.files {
			if strings.Contains(string(file.content), "<"+name+">") {
				referenced = true
				break
			}
		}
		if referenced {
			continue
		}
		if _, err := firewallRunCommand("/sbin/pfctl", "-t", name, "-T", "kill"); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("remove candidate PF table %s: %w", name, err))
		}
	}
	return restoreErr
}
