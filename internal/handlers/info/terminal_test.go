// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoHandlers

import (
	"net/http/httptest"
	"os/exec"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHostTerminalSessionsAreIndependentPerConnection(t *testing.T) {
	originalCommand := hostTerminalCommand
	hostTerminalCommand = func(string) *exec.Cmd {
		return exec.Command("sh")
	}
	t.Cleanup(func() {
		hostTerminalCommand = originalCommand
	})

	first, err := newHostTerminalSession("root", nil)
	if err != nil {
		t.Fatalf("start first host terminal: %v", err)
	}
	t.Cleanup(first.Close)

	second, err := newHostTerminalSession("root", nil)
	if err != nil {
		t.Fatalf("start second host terminal: %v", err)
	}
	t.Cleanup(second.Close)

	if first == second {
		t.Fatal("host terminal connections reused the same session")
	}
	if first.Pty == second.Pty || first.Pty.Fd() == second.Pty.Fd() {
		t.Fatal("host terminal connections reused the same PTY")
	}
	if first.Cmd.Process.Pid == second.Cmd.Process.Pid {
		t.Fatal("host terminal connections reused the same login process")
	}

	first.Close()
	if err := second.Cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("closing first host terminal affected second process: %v", err)
	}
}

func TestHostTerminalUser(t *testing.T) {
	tests := []struct {
		scope, username, want string
	}{
		{"local", "root", "root"},
		{"local", "admin", "admin"},
		{"cluster", "root", ""},
		{"cluster", "admin", ""},
	}
	for _, test := range tests {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("AuthScope", test.scope)
		c.Set("Username", test.username)
		if got := hostTerminalUser(c); got != test.want {
			t.Fatalf("%s %s: logs in as %q, want %q", test.scope, test.username, got, test.want)
		}
	}
}
