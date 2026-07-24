//go:build freebsd

package integration

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/repl"
	"github.com/alchemillahq/sylve/internal/services/info"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNotesCLIAndREPLIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping console integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("run console integration tests with make test-integration as root")
	}
	t.Setenv("SYLVE_DATA_PATH", "")

	dataPath := t.TempDir()
	database := openConsoleDatabase(t, filepath.Join(dataPath, "sylve.db"), &infoModels.Note{})
	infoService := &info.Service{DB: database, TelemetryDB: database}

	socketPath := consoleprotocol.SocketPath(dataPath)
	server, err := repl.StartSocketServer(&repl.Context{Info: infoService}, socketPath)
	if err != nil {
		t.Fatalf("start console socket: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close console socket: %v", err)
		}
	})

	configPath := writeConsoleConfig(t, dataPath)
	binaryPath := buildSylveBinary(t)

	cliOutput := runSylve(t, binaryPath, configPath,
		"notes", "add", "--title", "CLI note", "--content", "created through the CLI")
	if !strings.Contains(cliOutput, "Note added") {
		t.Fatalf("CLI add output = %q", cliOutput)
	}
	cliNote := noteByTitle(t, database, "CLI note")

	replOutput := runREPLCommand(t, socketPath, `notes add "REPL note" "created through the REPL"`)
	if !strings.Contains(replOutput, "Note added") {
		t.Fatalf("REPL add output = %q", replOutput)
	}
	replNote := noteByTitle(t, database, "REPL note")

	listOutput := runSylve(t, binaryPath, configPath, "notes", "list", "--json")
	var notes []infoModels.Note
	if err := json.Unmarshal([]byte(listOutput), &notes); err != nil {
		t.Fatalf("decode CLI note list: %v\noutput: %s", err, listOutput)
	}
	if len(notes) != 2 {
		t.Fatalf("CLI listed %d notes, want 2: %#v", len(notes), notes)
	}

	getOutput := runREPLCommand(t, socketPath,
		"notes get "+strconv.FormatUint(uint64(cliNote.ID), 10)+" --json")
	var got infoModels.Note
	if err := json.Unmarshal([]byte(getOutput), &got); err != nil {
		t.Fatalf("decode REPL note get: %v\noutput: %s", err, getOutput)
	}
	if got.ID != cliNote.ID || got.Content != cliNote.Content {
		t.Fatalf("REPL got note = %#v, want %#v", got, cliNote)
	}

	deleteOutput := runSylve(t, binaryPath, configPath,
		"notes", "delete", "--id", strconv.FormatUint(uint64(replNote.ID), 10))
	if !strings.Contains(deleteOutput, "deleted successfully") {
		t.Fatalf("CLI delete output = %q", deleteOutput)
	}

	replOutput = runREPLCommand(t, socketPath,
		"notes delete "+strconv.FormatUint(uint64(cliNote.ID), 10))
	if !strings.Contains(replOutput, "deleted successfully") {
		t.Fatalf("REPL delete output = %q", replOutput)
	}

	var count int64
	if err := database.Model(&infoModels.Note{}).Count(&count).Error; err != nil {
		t.Fatalf("count remaining notes: %v", err)
	}
	if count != 0 {
		t.Fatalf("remaining notes = %d, want 0", count)
	}
}

func openConsoleDatabase(t *testing.T, path string, models ...any) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open console database: %v", err)
	}
	if err := database.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate console database: %v", err)
	}
	return database
}

func writeConsoleConfig(t *testing.T, dataPath string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	contents, err := json.Marshal(struct {
		DataPath string `json:"dataPath"`
	}{DataPath: dataPath})
	if err != nil {
		t.Fatalf("marshal console config: %v", err)
	}
	if err := os.WriteFile(configPath, contents, 0600); err != nil {
		t.Fatalf("write console config: %v", err)
	}
	return configPath
}

func buildSylveBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sylve")
	command := exec.Command("go", "build", "-buildvcs=false", "-o", path, "./cmd/sylve")
	command.Dir = repositoryRoot(t)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sylve CLI: %v\n%s", err, output)
	}
	return path
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}

func runSylve(t *testing.T, binaryPath, configPath string, args ...string) string {
	t.Helper()
	command := exec.Command(binaryPath, append([]string{"--config", configPath}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run sylve %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runSylveFailure(t *testing.T, binaryPath, configPath string, args ...string) string {
	t.Helper()
	command := exec.Command(binaryPath, append([]string{"--config", configPath}, args...)...)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("run sylve %s unexpectedly succeeded:\n%s", strings.Join(args, " "), output)
	}
	return string(output)
}

func runREPLCommand(t *testing.T, socketPath, command string) string {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect to console socket: %v", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(consoleprotocol.Request{Command: command}); err != nil {
		t.Fatalf("send REPL command: %v", err)
	}
	var response consoleprotocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("read REPL response: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("REPL command %q failed: %s", command, response.Error)
	}
	return response.Output
}

func runREPLCommandFailure(t *testing.T, socketPath, command string) string {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect to console socket: %v", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(consoleprotocol.Request{Command: command}); err != nil {
		t.Fatalf("send REPL command: %v", err)
	}
	var response consoleprotocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("read REPL response: %v", err)
	}
	if response.Error == "" {
		if strings.Contains(strings.ToLower(response.Output), "error ") {
			return response.Output
		}
		t.Fatalf("REPL command %q unexpectedly succeeded: %s", command, response.Output)
	}
	return response.Error
}

func noteByTitle(t *testing.T, database *gorm.DB, title string) infoModels.Note {
	t.Helper()
	var note infoModels.Note
	if err := database.Where("title = ?", title).First(&note).Error; err != nil {
		t.Fatalf("find note %q: %v", title, err)
	}
	return note
}
