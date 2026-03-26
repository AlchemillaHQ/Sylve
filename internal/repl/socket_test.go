// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestProcessSocketRequestCommandRequired(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "   "})
	if resp.Error != "command_required" {
		t.Fatalf("expected command_required, got %q", resp.Error)
	}
	if resp.Output != "" {
		t.Fatalf("expected empty output, got %q", resp.Output)
	}
}

func TestProcessSocketRequestExecutesCommand(t *testing.T) {
	resp := processSocketRequest(&Context{}, socketRequest{Command: "ping"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if strings.TrimSpace(resp.Output) != "pong" {
		t.Fatalf("expected pong output, got %q", resp.Output)
	}
	if resp.Close {
		t.Fatalf("expected ping to keep session open")
	}
}

func TestProcessSocketRequestShutdownTriggersSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx := &Context{QuitChan: signals}

	resp := processSocketRequest(ctx, socketRequest{Command: "shutdown"})
	if resp.Error != "" {
		t.Fatalf("expected no error, got %q", resp.Error)
	}
	if !resp.Close {
		t.Fatalf("expected shutdown to close session")
	}

	select {
	case got := <-signals:
		if got != syscall.SIGTERM {
			t.Fatalf("expected SIGTERM, got %v", got)
		}
	default:
		t.Fatalf("expected shutdown signal")
	}
}

func TestHandleSocketConnMalformedRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleSocketConn(&Context{}, serverConn)
	}()

	if _, err := clientConn.Write([]byte("[]\n")); err != nil {
		t.Fatalf("failed writing malformed request: %v", err)
	}

	var resp socketResponse
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if resp.Error != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Error)
	}

	<-done
}

func TestStartSocketServerPermissionsAndCleanup(t *testing.T) {
	baseDir, err := os.MkdirTemp("/tmp", "sylve-repl-")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(baseDir)

	socketPath := filepath.Join(baseDir, "sock")

	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed creating stale socket: %v", err)
	}
	_ = stale.Close()

	server, err := startSocketServer(&Context{}, socketPath)
	if err != nil {
		t.Fatalf("startSocketServer failed: %v", err)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatalf("expected socket path to exist: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected socket file, got mode %v", info.Mode())
	}
	if perms := info.Mode().Perm(); perms != 0600 {
		t.Fatalf("expected permissions 0600, got %o", perms)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server close failed: %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket to be removed, got err=%v", err)
	}
}
