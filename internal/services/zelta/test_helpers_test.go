// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	fakeSSHHelperBinaryEnv = "ZELTA_TEST_HELPER_BINARY"
	fakeSSHScenarioFileEnv = "ZELTA_TEST_SSH_SCENARIO_FILE"
	fakeSSHStateFileEnv    = "ZELTA_TEST_SSH_STATE_FILE"
	fakeSSHLogFileEnv      = "ZELTA_TEST_SSH_LOG_FILE"
	fakeSSHHelperModeEnv   = "ZELTA_TEST_FAKE_SSH"
)

type fakeSSHResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type fakeSSHScenario struct {
	Responses map[string][]fakeSSHResponse `json:"responses"`
	Default   *fakeSSHResponse             `json:"default,omitempty"`
}

type fakeSSHHarness struct {
	t            *testing.T
	scenarioFile string
	stateFile    string
	logFile      string
}

func resetZeltaTestGlobals(t *testing.T) {
	t.Helper()

	origSSHKeyDir := SSHKeyDirectory
	origInstallDir := ZeltaInstallDir
	SSHKeyDirectory = ""
	ZeltaInstallDir = ""

	t.Cleanup(func() {
		SSHKeyDirectory = origSSHKeyDir
		ZeltaInstallDir = origInstallDir
	})
}

func newZeltaServiceTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if len(migrateModels) == 0 {
		migrateModels = []any{
			&clusterModels.BackupTarget{},
			&clusterModels.BackupJob{},
		}
	}
	if err := db.AutoMigrate(migrateModels...); err != nil {
		t.Fatalf("failed to migrate zelta test tables: %v", err)
	}

	return db
}

func newFakeSSHHarness(t *testing.T) *fakeSSHHarness {
	t.Helper()

	resetZeltaTestGlobals(t)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "ssh")
	scenarioPath := filepath.Join(dir, "scenario.json")
	statePath := filepath.Join(dir, "state.json")
	logPath := filepath.Join(dir, "calls.log")

	script := `#!/bin/sh
set -eu
: "${ZELTA_TEST_HELPER_BINARY:?}"
: "${ZELTA_TEST_SSH_SCENARIO_FILE:?}"
: "${ZELTA_TEST_SSH_STATE_FILE:?}"
: "${ZELTA_TEST_SSH_LOG_FILE:?}"
export ZELTA_TEST_FAKE_SSH=1
exec "$ZELTA_TEST_HELPER_BINARY" -test.run TestZeltaFakeSSHHelperProcess -- "$@"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake ssh script: %v", err)
	}

	h := &fakeSSHHarness{
		t:            t,
		scenarioFile: scenarioPath,
		stateFile:    statePath,
		logFile:      logPath,
	}
	h.SetScenario(fakeSSHScenario{Responses: map[string][]fakeSSHResponse{}})

	t.Setenv(fakeSSHHelperBinaryEnv, os.Args[0])
	t.Setenv(fakeSSHScenarioFileEnv, scenarioPath)
	t.Setenv(fakeSSHStateFileEnv, statePath)
	t.Setenv(fakeSSHLogFileEnv, logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return h
}

func (h *fakeSSHHarness) SetScenario(s fakeSSHScenario) {
	h.t.Helper()

	if s.Responses == nil {
		s.Responses = map[string][]fakeSSHResponse{}
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		h.t.Fatalf("failed to marshal fake ssh scenario: %v", err)
	}
	if err := os.WriteFile(h.scenarioFile, data, 0600); err != nil {
		h.t.Fatalf("failed to write fake ssh scenario file: %v", err)
	}

	if err := os.WriteFile(h.stateFile, []byte("{}"), 0600); err != nil {
		h.t.Fatalf("failed to initialize fake ssh state file: %v", err)
	}
	if err := os.WriteFile(h.logFile, nil, 0600); err != nil {
		h.t.Fatalf("failed to initialize fake ssh log file: %v", err)
	}
}

func (h *fakeSSHHarness) Calls() []string {
	h.t.Helper()

	data, err := os.ReadFile(h.logFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		h.t.Fatalf("failed to read fake ssh log file: %v", err)
	}

	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}

	return strings.Split(raw, "\n")
}

func TestZeltaFakeSSHHelperProcess(_ *testing.T) {
	if os.Getenv(fakeSSHHelperModeEnv) != "1" {
		return
	}

	args, err := fakeSSHExtractArgs(os.Args)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}

	commandSuffix := fakeSSHCommandSuffix(args)
	if commandSuffix == "" {
		_, _ = fmt.Fprintln(os.Stderr, "fake_ssh_missing_command_suffix")
		os.Exit(2)
	}

	if err := fakeSSHAppendLog(os.Getenv(fakeSSHLogFileEnv), commandSuffix); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_log_append_failed: %v", err))
		os.Exit(2)
	}

	scenario, err := fakeSSHLoadScenario(os.Getenv(fakeSSHScenarioFileEnv))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_scenario_load_failed: %v", err))
		os.Exit(2)
	}

	key, responses, ok := fakeSSHMatchResponses(scenario.Responses, commandSuffix)
	var response fakeSSHResponse
	if !ok {
		if scenario.Default == nil {
			_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_no_scenario_for_command: %s", commandSuffix))
			os.Exit(97)
		}
		response = *scenario.Default
	} else {
		state, loadErr := fakeSSHLoadState(os.Getenv(fakeSSHStateFileEnv))
		if loadErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_state_load_failed: %v", loadErr))
			os.Exit(2)
		}

		nextIndex := state[key]
		if nextIndex >= len(responses) {
			_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_response_exhausted key=%s index=%d", key, nextIndex))
			os.Exit(98)
		}

		response = responses[nextIndex]
		state[key] = nextIndex + 1
		if writeErr := fakeSSHWriteState(os.Getenv(fakeSSHStateFileEnv), state); writeErr != nil {
			_, _ = fmt.Fprintln(os.Stderr, fmt.Sprintf("fake_ssh_state_write_failed: %v", writeErr))
			os.Exit(2)
		}
	}

	if response.Stdout != "" {
		_, _ = os.Stdout.WriteString(response.Stdout)
	}
	if response.Stderr != "" {
		_, _ = os.Stderr.WriteString(response.Stderr)
	}

	os.Exit(response.ExitCode)
}

func fakeSSHExtractArgs(args []string) ([]string, error) {
	for i, arg := range args {
		if arg != "--" {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("fake_ssh_missing_args")
		}
		return args[i+1:], nil
	}
	return nil, fmt.Errorf("fake_ssh_missing_separator")
}

func fakeSSHCommandSuffix(args []string) string {
	// Skip ssh options and host so the scenario key can target command suffix only.
	i := 0
	for i < len(args) {
		arg := args[i]

		if fakeSSHOptionRequiresValue(arg) {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") {
			i++
			continue
		}

		// Host argument consumed.
		i++
		break
	}

	if i >= len(args) {
		return ""
	}

	return strings.Join(args[i:], " ")
}

func fakeSSHOptionRequiresValue(option string) bool {
	switch option {
	case "-o", "-p", "-i":
		return true
	default:
		return false
	}
}

func fakeSSHAppendLog(path, line string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("log_path_required")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	_, err = f.WriteString(line + "\n")
	return err
}

func fakeSSHLoadScenario(path string) (fakeSSHScenario, error) {
	var scenario fakeSSHScenario
	if strings.TrimSpace(path) == "" {
		return scenario, fmt.Errorf("scenario_path_required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return scenario, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		scenario.Responses = map[string][]fakeSSHResponse{}
		return scenario, nil
	}

	if err := json.Unmarshal(data, &scenario); err != nil {
		return scenario, err
	}
	if scenario.Responses == nil {
		scenario.Responses = map[string][]fakeSSHResponse{}
	}
	return scenario, nil
}

func fakeSSHMatchResponses(
	responses map[string][]fakeSSHResponse,
	command string,
) (string, []fakeSSHResponse, bool) {
	if seq, ok := responses[command]; ok {
		return command, seq, true
	}

	matches := make([]string, 0, len(responses))
	for key := range responses {
		if key == "" {
			continue
		}
		if strings.HasSuffix(command, key) {
			matches = append(matches, key)
		}
	}
	if len(matches) == 0 {
		return "", nil, false
	}

	sort.Slice(matches, func(i, j int) bool {
		if len(matches[i]) == len(matches[j]) {
			return matches[i] < matches[j]
		}
		return len(matches[i]) > len(matches[j])
	})

	key := matches[0]
	return key, responses[key], true
}

func fakeSSHLoadState(path string) (map[string]int, error) {
	state := map[string]int{}
	if strings.TrimSpace(path) == "" {
		return state, fmt.Errorf("state_path_required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return map[string]int{}, err
	}
	return state, nil
}

func fakeSSHWriteState(path string, state map[string]int) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("state_path_required")
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
