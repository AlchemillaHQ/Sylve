// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	statusRefreshInterval = 10 * time.Second
	statusRequestTimeout  = 5 * time.Second
)

type statusMsg struct {
	snapshot consoleprotocol.StatusSnapshot
	err      error
}

type statusFetcher func(context.Context) (consoleprotocol.StatusSnapshot, error)

func requestStatus(fetch statusFetcher, delay time.Duration) tea.Cmd {
	if fetch == nil {
		return nil
	}

	request := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), statusRequestTimeout)
		defer cancel()
		snapshot, err := fetch(ctx)
		return statusMsg{snapshot: snapshot, err: err}
	}
	if delay <= 0 {
		return request
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return request() })
}

func localStatusFetcher(ctx *Context) statusFetcher {
	if ctx == nil || ctx.Status == nil {
		return nil
	}
	return ctx.Status.Snapshot
}

func remoteStatusFetcher(socketPath string) statusFetcher {
	if socketPath == "" {
		return nil
	}
	return func(ctx context.Context) (consoleprotocol.StatusSnapshot, error) {
		output, err := consoleprotocol.ExecuteOperationContext(
			ctx,
			socketPath,
			consoleprotocol.OperationStatus,
			consoleprotocol.StatusPayload{},
		)
		if err != nil {
			return consoleprotocol.StatusSnapshot{}, err
		}

		var snapshot consoleprotocol.StatusSnapshot
		if err := json.Unmarshal([]byte(output), &snapshot); err != nil {
			return consoleprotocol.StatusSnapshot{}, fmt.Errorf("decode console status: %w", err)
		}
		return snapshot, nil
	}
}
