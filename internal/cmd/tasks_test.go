package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
)

func TestNewTasksCommandIncludesExpectedWorkflows(t *testing.T) {
	command := newTasksCommand()
	want := map[string]bool{
		"active": false,
		"recent": false,
		"get":    false,
	}

	for _, subcommand := range command.Commands {
		if _, ok := want[subcommand.Name]; ok {
			want[subcommand.Name] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Fatalf("expected tasks %s command", name)
		}
	}
}

func TestTasksGetUsesTypedSocketOperation(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", "")
	configDir := t.TempDir()
	dataPath := filepath.Join(configDir, "data")
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"`+dataPath+`"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	socketPath := consoleprotocol.SocketPath(dataPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	requests := make(chan consoleprotocol.Request, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var request consoleprotocol.Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		requests <- request
		serverErr <- json.NewEncoder(conn).Encode(consoleprotocol.Response{Output: "ok\n"})
	}()

	root := newRootCommand(nil, func() bool { return true })
	if err := root.Run(context.Background(), []string{
		"sylve", "--config", configPath, "tasks", "get", "--id", "41", "--json",
	}); err != nil {
		t.Fatalf("run tasks get: %v", err)
	}

	request := <-requests
	if request.Operation != consoleprotocol.OperationTaskGet {
		t.Fatalf("operation = %q, want %q", request.Operation, consoleprotocol.OperationTaskGet)
	}
	var payload consoleprotocol.TaskGetPayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.TaskID != 41 || !payload.JSON {
		t.Fatalf("payload = %#v", payload)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}
