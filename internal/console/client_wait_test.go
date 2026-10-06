// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServiceWaitStartup(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "missing socket"
		if stale {
			name = "stale socket"
		}
		t.Run(name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "s.sock")
			if stale {
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
				if err != nil {
					t.Fatalf("create stale socket: %v", err)
				}
				listener.SetUnlinkOnClose(false)
				if err := listener.Close(); err != nil {
					t.Fatalf("close stale socket: %v", err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type dialResult struct {
				conn net.Conn
				err  error
			}
			result := make(chan dialResult, 1)
			go func() {
				conn, err := dialService(ctx, socketPath, 2*time.Second)
				result <- dialResult{conn, err}
			}()

			time.Sleep(50 * time.Millisecond)
			if stale {
				if err := os.Remove(socketPath); err != nil {
					t.Fatalf("remove stale test socket: %v", err)
				}
			}
			listenServiceWait(t, socketPath)
			got := <-result
			if got.err != nil {
				t.Fatalf("connect after service startup: %v", got.err)
			}
			got.conn.Close()
		})
	}
}

func TestServiceWaitTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := ExecuteOperationResponseWithWait(ctx, filepath.Join(t.TempDir(), "s.sock"), OperationSwitchList, SwitchListPayload{}, 40*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not become available within 40ms") {
		t.Fatalf("error = %v, want service wait timeout", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("timed out too early: %s", elapsed)
	}
}

func TestServiceWaitDefaultFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := ExecuteOperationResponseContext(ctx, filepath.Join(t.TempDir(), "s.sock"), OperationSwitchList, SwitchListPayload{})
	if err == nil || !strings.Contains(err.Error(), "sylve daemon is not running") {
		t.Fatalf("error = %v, want immediate unavailable-daemon error", err)
	}
}

func TestServiceWaitCancellation(t *testing.T) {
	for _, name := range []string{"already canceled", "canceled while waiting", "parent deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			switch name {
			case "already canceled":
				cancel()
			case "canceled while waiting":
				timer := time.AfterFunc(40*time.Millisecond, cancel)
				defer timer.Stop()
			case "parent deadline":
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			_, err := ExecuteOperationResponseWithWait(ctx, filepath.Join(t.TempDir(), "s.sock"), OperationSwitchList, SwitchListPayload{}, 5*time.Second)
			if !errors.Is(err, want) || strings.Contains(err.Error(), "within 5s") {
				t.Fatalf("error = %v, want parent context error %v", err, want)
			}
		})
	}
}

func TestServiceWaitPermanentError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := ExecuteOperationResponseWithWait(ctx, filepath.Join(file, "s.sock"), OperationSwitchList, SwitchListPayload{}, 5*time.Second)
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("error = %v, want immediate permanent connection error", err)
	}
	for _, err := range []error{os.ErrPermission, syscall.EACCES, syscall.EPERM, syscall.ENOTDIR} {
		if isSocketUnavailable(&net.OpError{Op: "dial", Net: "unix", Err: err}) {
			t.Fatalf("permanent error %v classified as retryable", err)
		}
	}
}

func TestServiceWaitDoesNotLimitExecution(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "s.sock")
	listener := listenServiceWait(t, socketPath)
	serverErr := serveServiceWait(listener, &Response{Output: "done\n"}, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := ExecuteOperationResponseWithWait(ctx, socketPath, OperationSwitchList, SwitchListPayload{}, 40*time.Millisecond)
	if err != nil || response.Output != "done\n" {
		t.Fatalf("response = %+v, error = %v; service wait must not limit execution", response, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("serve response: %v", err)
	}
}

func TestServiceWaitDoesNotReplay(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "command error"
		response := &Response{Output: "partial\n", Error: "command_failed"}
		if lost {
			name = "lost response"
			response = nil
		}
		t.Run(name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "s.sock")
			listener := listenServiceWait(t, socketPath)
			serverErr := serveServiceWait(listener, response, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := ExecuteOperationResponseWithWait(ctx, socketPath, OperationSwitchCreate, StandardSwitchCreateRequest{Name: "once"}, time.Second)
			if lost {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("error = %v, want original lost-response error", err)
				}
			} else if err == nil || err.Error() != "command_failed" || got.Output != "partial\n" {
				t.Fatalf("response = %+v, error = %v, want original command failure", got, err)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("serve response: %v", err)
			}
			if err := listener.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
				t.Fatalf("set accept deadline: %v", err)
			}
			if conn, err := listener.Accept(); err == nil {
				conn.Close()
				t.Fatal("command was replayed on a second connection")
			}
		})
	}
}

func listenServiceWait(t *testing.T, socketPath string) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set accept deadline: %v", err)
	}
	return listener
}

func serveServiceWait(listener net.Listener, response *Response, delay time.Duration) <-chan error {
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverErr <- err
			return
		}
		var request Request
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		if response == nil {
			serverErr <- nil
			return
		}
		time.Sleep(delay)
		serverErr <- json.NewEncoder(conn).Encode(response)
	}()
	return serverErr
}
