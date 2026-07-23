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

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	"github.com/alchemillahq/sylve/internal/logger"
)

type socketRequest = consoleprotocol.Request
type socketResponse = consoleprotocol.Response

type SocketServer struct {
	path string
	ln   net.Listener

	closeOnce sync.Once
}

func StartSocketServer(ctx *Context, socketPath string) (*SocketServer, error) {
	return startSocketServer(ctx, socketPath)
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
	if req.Operation != "" {
		switch req.Operation {
		case consoleprotocol.OperationJailList:
			return processJailListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailGet:
			return processJailGetSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailCreate:
			return processJailCreateSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailAction:
			return processJailActionSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailDelete:
			return processJailDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailNetworks:
			return processJailNetworksSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationJailRemoveNetwork:
			return processJailRemoveNetworkSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationBootstrapList:
			return processBootstrapListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationBootstrapCreate:
			return processBootstrapCreateSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationBootstrapDelete:
			return processBootstrapDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMList:
			return processVMListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMGet:
			return processVMGetSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMCreate:
			return processVMCreateSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMAction:
			return processVMActionSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMDelete:
			return processVMDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMPurge:
			return processVMPurgeSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMNetworks:
			return processVMNetworksSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMNetworkAttach:
			return processVMNetworkAttachSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMNetworkDetach:
			return processVMNetworkDetachSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationVMQGASend:
			return processVMQGASendSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationSwitchList:
			return processSwitchListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationSwitchCreate:
			return processSwitchCreateSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationSwitchDelete:
			return processSwitchDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationSwitchEdit:
			return processSwitchEditSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationObjectList:
			return processObjectListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationObjectCreate:
			return processObjectCreateSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationObjectEdit:
			return processObjectEditSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationObjectDelete:
			return processObjectDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationDownloadList:
			return processDownloadListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationDownloadStart:
			return processDownloadStartSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationDownloadDelete:
			return processDownloadDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationNoteAdd:
			return processNoteAddSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationNoteList:
			return processNoteListSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationNoteGet:
			return processNoteGetSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationNoteDelete:
			return processNoteDeleteSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationTaskListActive:
			return processTaskActiveSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationTaskListRecent:
			return processTaskRecentSocketRequest(ctx, req.Payload)
		case consoleprotocol.OperationTaskGet:
			return processTaskGetSocketRequest(ctx, req.Payload)
		default:
			return socketResponse{Error: "unknown_operation"}
		}
	}

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

func decodeOperationPayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 {
		return fmt.Errorf("payload_required")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid_payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid_payload: contains more than one JSON value")
		}
		return fmt.Errorf("invalid_payload: %w", err)
	}

	return nil
}

func operationSuccess(jsonMode bool, result any, text string) socketResponse {
	if jsonMode {
		return socketResponse{Output: mustJSON(result) + "\n"}
	}
	return socketResponse{Output: text + "\n"}
}

func TryAttachSocketConsole(socketPath, historyPath string) (bool, error) {
	return tryAttachSocketConsole(socketPath, historyPath)
}

func tryAttachSocketConsole(socketPath, historyPath string) (bool, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		if isSocketUnavailable(err) {
			return false, nil
		}
		return false, fmt.Errorf("repl_socket_connect_failed: %w", err)
	}
	defer conn.Close()

	if err := runRemoteConsoleTUI(conn, historyPath); err != nil {
		return true, err
	}

	return true, nil
}

func prepareSocketPath(socketPath string) error {
	if strings.TrimSpace(socketPath) == "" {
		return fmt.Errorf("repl_socket_path_required")
	}

	directory := filepath.Dir(socketPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("repl_socket_dir_create_failed: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("repl_socket_dir_chmod_failed: %w", err)
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
