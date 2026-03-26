// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/chzyer/readline"
)

const ConsoleSocketPath = "/var/run/sylve-console.sock"

type socketRequest struct {
	Command string `json:"command"`
}

type socketResponse struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Close  bool   `json:"close,omitempty"`
}

type SocketServer struct {
	path string
	ln   net.Listener

	closeOnce sync.Once
}

func StartSocketServer(ctx *Context) (*SocketServer, error) {
	return startSocketServer(ctx, ConsoleSocketPath)
}

func startSocketServer(ctx *Context, socketPath string) (*SocketServer, error) {
	if err := prepareSocketPath(socketPath); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("repl_socket_listen_failed: %w", err)
	}

	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("repl_socket_chmod_failed: %w", err)
	}

	server := &SocketServer{
		path: socketPath,
		ln:   ln,
	}

	go server.acceptLoop(ctx)
	return server, nil
}

func (s *SocketServer) Close() error {
	if s == nil {
		return nil
	}

	var closeErr error
	s.closeOnce.Do(func() {
		if s.ln != nil {
			closeErr = s.ln.Close()
		}
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			if closeErr == nil {
				closeErr = err
			}
		}
	})

	return closeErr
}

func (s *SocketServer) acceptLoop(ctx *Context) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			logger.L.Warn().Err(err).Msg("repl_socket_accept_failed")
			continue
		}

		go handleSocketConn(ctx, conn)
	}
}

func handleSocketConn(ctx *Context, conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	for {
		var req socketRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}

			_ = enc.Encode(socketResponse{Error: "invalid_request"})
			return
		}

		resp := processSocketRequest(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return
		}

		if resp.Close {
			return
		}
	}
}

func processSocketRequest(ctx *Context, req socketRequest) socketResponse {
	if strings.TrimSpace(req.Command) == "" {
		return socketResponse{Error: "command_required"}
	}

	var localCtx Context
	if ctx != nil {
		localCtx = *ctx
	}

	var out bytes.Buffer
	localCtx.Out = &out

	shouldContinue := ExecuteLine(&localCtx, req.Command)
	return socketResponse{
		Output: out.String(),
		Close:  !shouldContinue,
	}
}

func TryAttachSocketConsole() (bool, error) {
	return tryAttachSocketConsole(ConsoleSocketPath)
}

func tryAttachSocketConsole(socketPath string) (bool, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		if isSocketUnavailable(err) {
			return false, nil
		}
		return false, fmt.Errorf("repl_socket_connect_failed: %w", err)
	}
	defer conn.Close()

	if err := runRemoteConsole(conn, os.Stdin, os.Stdout); err != nil {
		return true, err
	}

	return true, nil
}

func runRemoteConsole(conn net.Conn, in io.ReadCloser, out io.Writer) error {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "sylve> ",
		HistoryFile:     replHistoryFile,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		Stdin:           in,
		Stdout:          out,
		Stderr:          out,
	})
	if err != nil {
		return fmt.Errorf("repl_client_init_failed: %w", err)
	}
	defer rl.Close()

	fmt.Fprintln(out, "Connected to Sylve daemon REPL. Type `help`.")

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	for {
		line, err := rl.Readline()
		if err != nil {
			return nil
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if err := enc.Encode(socketRequest{Command: line}); err != nil {
			return fmt.Errorf("repl_client_send_failed: %w", err)
		}

		var resp socketResponse
		if err := dec.Decode(&resp); err != nil {
			return fmt.Errorf("repl_client_read_failed: %w", err)
		}

		if resp.Error != "" {
			fmt.Fprintf(out, "Error: %s\n", resp.Error)
		}

		if resp.Output != "" {
			fmt.Fprint(out, resp.Output)
		}

		if resp.Close {
			return nil
		}
	}
}

func prepareSocketPath(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return fmt.Errorf("repl_socket_dir_create_failed: %w", err)
	}

	info, err := os.Lstat(socketPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("repl_socket_stat_failed: %w", err)
	}

	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("repl_socket_path_not_socket: %s", socketPath)
	}

	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("repl_socket_cleanup_failed: %w", err)
	}

	return nil
}

func isSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ENOENT) || errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}

	return false
}
