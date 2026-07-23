//go:build freebsd

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"gorm.io/gorm"
)

const bootstrapCreateAttempts = 2

func TestBootstrapsCLIAndREPLIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}

	suite := requireConsoleIntegrationSuite(t)
	base := createBootstrapThroughCLI(t, suite, "base")
	assertCompletedBootstrap(t, suite, base)

	output := runREPLCommand(t, suite.socketPath, "jails bootstrap list "+suite.poolName+" --json")
	var entries []jailServiceInterfaces.BootstrapEntry
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		t.Fatalf("decode REPL bootstrap list: %v\noutput: %s", err, output)
	}
	listedBase := bootstrapEntry(t, entries, "base")
	if listedBase.Name != base.Name || listedBase.Status != "completed" || !listedBase.Exists {
		t.Fatalf("REPL base bootstrap entry = %#v", listedBase)
	}

	output = runREPLCommand(t, suite.socketPath,
		"jails bootstrap create "+suite.poolName+" 15.0 base --wait --json")
	var idempotent jailServiceInterfaces.BootstrapEntry
	if err := json.Unmarshal([]byte(output), &idempotent); err != nil {
		t.Fatalf("decode REPL idempotent bootstrap create: %v\noutput: %s", err, output)
	}
	if idempotent.Name != base.Name || idempotent.Status != "completed" || !idempotent.Exists {
		t.Fatalf("REPL idempotent bootstrap = %#v", idempotent)
	}

	minimal := createBootstrapThroughREPL(t, suite, "minimal")
	assertCompletedBootstrap(t, suite, minimal)

	output = runSylve(t, suite.binaryPath, suite.configPath,
		"jails", "bootstrap", "delete", "--pool", suite.poolName, "--name", minimal.Name, "--json")
	var deleted struct {
		Deleted bool   `json:"deleted"`
		Pool    string `json:"pool"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal([]byte(output), &deleted); err != nil {
		t.Fatalf("decode CLI bootstrap delete: %v\noutput: %s", err, output)
	}
	if !deleted.Deleted || deleted.Pool != suite.poolName || deleted.Name != minimal.Name {
		t.Fatalf("CLI bootstrap delete result = %#v", deleted)
	}
	assertDeletedBootstrap(t, suite, minimal)
}

func createBootstrapThroughCLI(t *testing.T, suite *consoleIntegrationSuite, bootstrapType string) jailServiceInterfaces.BootstrapEntry {
	t.Helper()
	for attempt := 1; attempt <= bootstrapCreateAttempts; attempt++ {
		output, err := runBootstrapCLI(t, suite, bootstrapType)
		if err != nil {
			if attempt < bootstrapCreateAttempts && retryableBootstrapFetchFailure(output) {
				t.Logf("bootstrap %s: pkg mirror fetch failed; retrying (%d/%d)", bootstrapType, attempt+1, bootstrapCreateAttempts)
				continue
			}
			t.Fatalf("CLI bootstrap create: %v\n%s", err, output)
		}

		var entry jailServiceInterfaces.BootstrapEntry
		if err := json.Unmarshal([]byte(output), &entry); err != nil {
			t.Fatalf("decode CLI bootstrap create: %v\noutput: %s", err, output)
		}
		return entry
	}
	return jailServiceInterfaces.BootstrapEntry{}
}

func runBootstrapCLI(t *testing.T, suite *consoleIntegrationSuite, bootstrapType string) (string, error) {
	t.Helper()
	command := exec.Command(suite.binaryPath,
		"--config", suite.configPath,
		"jails", "bootstrap", "create", "--pool", suite.poolName, "--version", "15.0", "--type", bootstrapType, "--wait", "--json")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start CLI bootstrap create: %w", err)
	}

	t.Logf("bootstrap %s: CLI request started", bootstrapType)
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			return output.String(), err
		case <-ticker.C:
			t.Logf("bootstrap %s: %s", bootstrapType, bootstrapProgress(suite, bootstrapType))
		}
	}
}

func createBootstrapThroughREPL(t *testing.T, suite *consoleIntegrationSuite, bootstrapType string) jailServiceInterfaces.BootstrapEntry {
	t.Helper()
	for attempt := 1; attempt <= bootstrapCreateAttempts; attempt++ {
		output, err := runBootstrapREPL(t, suite, bootstrapType)
		if err != nil {
			if attempt < bootstrapCreateAttempts && retryableBootstrapFetchFailure(err.Error()) {
				t.Logf("bootstrap %s: pkg mirror fetch failed; retrying (%d/%d)", bootstrapType, attempt+1, bootstrapCreateAttempts)
				continue
			}
			t.Fatal(err)
		}

		var entry jailServiceInterfaces.BootstrapEntry
		if err := json.Unmarshal([]byte(output), &entry); err != nil {
			t.Fatalf("decode REPL bootstrap create: %v\noutput: %s", err, output)
		}
		return entry
	}
	return jailServiceInterfaces.BootstrapEntry{}
}

func runBootstrapREPL(t *testing.T, suite *consoleIntegrationSuite, bootstrapType string) (string, error) {
	t.Helper()
	command := "jails bootstrap create " + suite.poolName + " 15.0 " + bootstrapType + " --wait --json"
	conn, err := net.Dial("unix", suite.socketPath)
	if err != nil {
		return "", fmt.Errorf("connect to console socket: %w", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(consoleprotocol.Request{Command: command}); err != nil {
		return "", fmt.Errorf("send REPL command: %w", err)
	}

	t.Logf("bootstrap %s: REPL request started", bootstrapType)
	type result struct {
		response consoleprotocol.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		var response consoleprotocol.Response
		err := json.NewDecoder(conn).Decode(&response)
		done <- result{response: response, err: err}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case result := <-done:
			if result.err != nil {
				return "", fmt.Errorf("read REPL response: %w", result.err)
			}
			if result.response.Error != "" {
				return "", fmt.Errorf("REPL command %q failed: %s", command, result.response.Error)
			}
			return result.response.Output, nil
		case <-ticker.C:
			t.Logf("bootstrap %s: %s", bootstrapType, bootstrapProgress(suite, bootstrapType))
		}
	}
}

func retryableBootstrapFetchFailure(message string) bool {
	return strings.Contains(message, "pkg: An error occurred while fetching package")
}

func bootstrapEntry(t *testing.T, entries []jailServiceInterfaces.BootstrapEntry, bootstrapType string) jailServiceInterfaces.BootstrapEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Type == bootstrapType {
			return entry
		}
	}
	t.Fatalf("bootstrap type %q not found in %#v", bootstrapType, entries)
	return jailServiceInterfaces.BootstrapEntry{}
}

func assertCompletedBootstrap(t *testing.T, suite *consoleIntegrationSuite, entry jailServiceInterfaces.BootstrapEntry) {
	t.Helper()
	if entry.Pool != suite.poolName || entry.Name == "" || entry.Status != "completed" || !entry.Exists {
		t.Fatalf("completed bootstrap entry = %#v", entry)
	}
	if _, err := os.Stat(entry.MountPoint); err != nil {
		t.Fatalf("stat bootstrap mount %s: %v", entry.MountPoint, err)
	}
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", entry.Dataset).CombinedOutput(); err != nil {
		t.Fatalf("zfs list bootstrap dataset %s: %v\n%s", entry.Dataset, err, output)
	}

	var record jailModels.JailBootstrap
	if err := suite.database.Where("pool = ? AND name = ?", suite.poolName, entry.Name).First(&record).Error; err != nil {
		t.Fatalf("load bootstrap record %s: %v", entry.Name, err)
	}
	if record.Status != "completed" || record.Dataset != entry.Dataset || record.MountPoint != entry.MountPoint {
		t.Fatalf("bootstrap record = %#v", record)
	}
}

func assertDeletedBootstrap(t *testing.T, suite *consoleIntegrationSuite, entry jailServiceInterfaces.BootstrapEntry) {
	t.Helper()
	if output, err := exec.Command("zfs", "list", "-H", "-o", "name", entry.Dataset).CombinedOutput(); err == nil {
		t.Fatalf("deleted bootstrap dataset still exists: %s", output)
	}

	var record jailModels.JailBootstrap
	if err := suite.database.Where("pool = ? AND name = ?", suite.poolName, entry.Name).First(&record).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("bootstrap record after delete error = %v, want not found", err)
	}
	if _, err := os.Stat(entry.MountPoint); !os.IsNotExist(err) {
		t.Fatalf("bootstrap mount after delete error = %v, want not exist", err)
	}
}

func bootstrapProgress(suite *consoleIntegrationSuite, bootstrapType string) string {
	var record jailModels.JailBootstrap
	if err := suite.database.Where("pool = ? AND bootstrap_type = ?", suite.poolName, bootstrapType).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "waiting for bootstrap record"
		}
		return fmt.Sprintf("read bootstrap record: %v", err)
	}

	used := "unavailable"
	if output, err := exec.Command("zfs", "get", "-H", "-p", "-o", "value", "used", record.Dataset).CombinedOutput(); err == nil {
		used = strings.TrimSpace(string(output))
	}
	processes := bootstrapProcesses(suite.poolName)
	return fmt.Sprintf(
		"status=%s phase=%s updated=%s used=%s pool_processes=%s",
		record.Status,
		record.Phase,
		record.UpdatedAt.UTC().Format(time.RFC3339),
		used,
		processes,
	)
}

func bootstrapProcesses(poolName string) string {
	output, err := exec.Command("ps", "ax", "-o", "pid=,etime=,command=").CombinedOutput()
	if err != nil {
		return "unavailable"
	}
	var matches []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, poolName) {
			matches = append(matches, line)
		}
	}
	if len(matches) == 0 {
		return "none"
	}
	return strings.Join(matches, " | ")
}
