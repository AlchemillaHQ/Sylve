// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/urfave/cli/v3"
)

func TestAsciiArt(t *testing.T) {
	var buf bytes.Buffer
	AsciiArt(&buf)

	out := buf.String()

	if !strings.Contains(out, "____") {
		t.Fatalf("expected ascii art, got %q", out)
	}

	if !strings.Contains(out, "v"+Version) {
		t.Fatalf("expected version in output, got %q", out)
	}
}

func TestNewRootCommand_Name(t *testing.T) {
	root := NewRootCommand(nil)
	if root.Name != "sylve" {
		t.Fatalf("expected name sylve, got %q", root.Name)
	}
}

func TestNewRootCommand_Flags_Defaults(t *testing.T) {
	var configPath string
	var console bool

	root := newRootCommand(func(ctx context.Context, c *cli.Command) error {
		configPath = c.String("config")
		console = c.Bool("console")
		return nil
	}, func() bool { return true })

	if err := root.Run(context.Background(), []string{"sylve"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configPath != "" {
		t.Fatalf("expected empty config path, got %q", configPath)
	}
	if console {
		t.Fatal("expected console false by default")
	}
}

func TestNewRootCommand_Flags_Config(t *testing.T) {
	var configPath string

	root := newRootCommand(func(ctx context.Context, c *cli.Command) error {
		configPath = c.String("config")
		return nil
	}, func() bool { return true })

	if err := root.Run(context.Background(), []string{"sylve", "--config", "/tmp/test.json"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configPath != "/tmp/test.json" {
		t.Fatalf("expected /tmp/test.json, got %q", configPath)
	}
}

func TestNewRootCommand_Flags_Console(t *testing.T) {
	var console bool

	root := newRootCommand(func(ctx context.Context, c *cli.Command) error {
		console = c.Bool("console")
		return nil
	}, func() bool { return true })

	if err := root.Run(context.Background(), []string{"sylve", "--console"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !console {
		t.Fatal("expected console true")
	}
}

func TestNewRootCommand_Subcommands(t *testing.T) {
	root := NewRootCommand(nil)
	want := map[string]bool{
		"notes":     false,
		"jails":     false,
		"vms":       false,
		"tasks":     false,
		"switches":  false,
		"objects":   false,
		"downloads": false,
	}
	for _, sub := range root.Commands {
		if _, ok := want[sub.Name]; ok {
			want[sub.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("expected %s subcommand", name)
		}
	}
}

func TestNewRootCommandRequiresRootForActions(t *testing.T) {
	testCases := [][]string{
		{"sylve"},
		{"sylve", "--console"},
		{"sylve", "notes", "list"},
		{"sylve", "jails", "console"},
	}

	for _, args := range testCases {
		name := "root"
		if len(args) > 1 {
			name = strings.Join(args[1:], " ")
		}
		t.Run(name, func(t *testing.T) {
			called := false
			root := newRootCommand(func(ctx context.Context, c *cli.Command) error {
				called = true
				return nil
			}, func() bool { return false })

			err := root.Run(context.Background(), args)
			if err == nil || err.Error() != "root privileges required" {
				t.Fatalf("expected root error, got %v", err)
			}
			if called {
				t.Fatal("expected action not to run without root privileges")
			}
		})
	}
}

func TestConsoleSocketPathUsesConfiguredDataPath(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", "")
	configDir := t.TempDir()
	dataPath := filepath.Join(configDir, "data")
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataPath":"`+dataPath+`"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := consoleSocketPath(configPath)
	if err != nil {
		t.Fatalf("resolve socket path: %v", err)
	}
	want := consoleprotocol.SocketPath(dataPath)
	if got != want {
		t.Fatalf("socket path = %q, want %q", got, want)
	}
}

func TestDirectCLIUsesConfiguredSocketPath(t *testing.T) {
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
	if err := root.Run(context.Background(), []string{"sylve", "--config", configPath, "downloads", "list"}); err != nil {
		t.Fatalf("run downloads list: %v", err)
	}

	request := <-requests
	if request.Operation != consoleprotocol.OperationDownloadList {
		t.Fatalf("operation = %q, want %q", request.Operation, consoleprotocol.OperationDownloadList)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}

func TestResolveConfigPath_Explicit(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(cfg, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg {
		t.Fatalf("expected %q, got %q", cfg, got)
	}
}

func TestResolveConfigPath_ExplicitMissing(t *testing.T) {
	_, err := ResolveConfigPath("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing explicit config, got nil")
	}
}

func TestResolveConfigPath_NoArgs_NoneFound(t *testing.T) {
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	_, err = ResolveConfigPath("")
	if err == nil {
		t.Fatal("expected error when no config found, got nil")
	}
}

func TestResolveConfigPath_NoArgs_LocalFound(t *testing.T) {
	tmp := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	if err := os.WriteFile("config.json", []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DefaultConfigLocal {
		t.Fatalf("expected %q, got %q", DefaultConfigLocal, got)
	}
}
