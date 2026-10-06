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
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func ExecuteOperation(socketPath, operation string, payload any) (string, error) {
	return ExecuteOperationContext(context.Background(), socketPath, operation, payload)
}

func ExecuteOperationContext(ctx context.Context, socketPath, operation string, payload any) (string, error) {
	return executeOperationContext(ctx, socketPath, operation, payload)
}

func ExecuteOperationResponse(socketPath, operation string, payload any) (Response, error) {
	return ExecuteOperationResponseContext(context.Background(), socketPath, operation, payload)
}

func ExecuteOperationResponseContext(ctx context.Context, socketPath, operation string, payload any) (Response, error) {
	return ExecuteOperationResponseWithWait(ctx, socketPath, operation, payload, 0)
}

// ExecuteOperationResponseWithWait retries unavailable daemon connections for up
// to wait. The wait timeout does not apply to command execution, and requests are
// never retried after connecting.
func ExecuteOperationResponseWithWait(ctx context.Context, socketPath, operation string, payload any, wait time.Duration) (Response, error) {
	if wait < 0 {
		return Response{}, fmt.Errorf("service wait timeout must not be negative")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("encode %s request: %w", operation, err)
	}
	return executeRequestResponseContext(ctx, socketPath, Request{Operation: operation, Payload: encoded}, wait)
}

func executeOperation(socketPath, operation string, payload any) (string, error) {
	return executeOperationContext(context.Background(), socketPath, operation, payload)
}

func executeOperationContext(ctx context.Context, socketPath, operation string, payload any) (string, error) {
	response, err := ExecuteOperationResponseContext(ctx, socketPath, operation, payload)
	if err != nil {
		return "", err
	}
	return response.Output, nil
}

func executeRequest(socketPath string, request Request) (string, error) {
	return executeRequestContext(context.Background(), socketPath, request)
}

func executeRequestContext(ctx context.Context, socketPath string, request Request) (string, error) {
	response, err := executeRequestResponseContext(ctx, socketPath, request, 0)
	if err != nil {
		return "", err
	}
	return response.Output, nil
}

func executeRequestResponseContext(ctx context.Context, socketPath string, request Request, wait time.Duration) (Response, error) {
	conn, err := dialService(ctx, socketPath, wait)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return Response{}, fmt.Errorf("set daemon deadline: %w", err)
		}
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancellation()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := enc.Encode(request); err != nil {
		return Response{}, fmt.Errorf("send command: %w", err)
	}

	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}

	if resp.Error != "" {
		return resp, fmt.Errorf("%s", resp.Error)
	}

	return resp, nil
}

func dialService(ctx context.Context, socketPath string, wait time.Duration) (net.Conn, error) {
	waitCtx := ctx
	if wait > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}

	dialer := &net.Dialer{}
	for {
		conn, err := dialer.DialContext(waitCtx, "unix", socketPath)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("connect to daemon: %w", ctx.Err())
		}
		if wait > 0 && waitCtx.Err() != nil {
			return nil, fmt.Errorf("Sylve service did not become available within %s: %w", wait, waitCtx.Err())
		}
		if !isSocketUnavailable(err) {
			return nil, fmt.Errorf("connect to daemon: %w", err)
		}
		if wait == 0 {
			return nil, fmt.Errorf("sylve daemon is not running; start it first with 'sylve'")
		}

		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func isSocketUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
