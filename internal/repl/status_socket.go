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

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
)

func processStatusSocketRequest(ctx *Context, payload json.RawMessage) socketResponse {
	var request consoleprotocol.StatusPayload
	if err := decodeOperationPayload(payload, &request); err != nil {
		return socketResponse{Error: fmt.Sprintf("invalid_status_request: %v", err)}
	}
	if ctx == nil || ctx.Status == nil {
		return socketResponse{Error: "status_provider_unavailable"}
	}

	requestContext, cancel := context.WithTimeout(context.Background(), statusRequestTimeout)
	defer cancel()
	snapshot, err := ctx.Status.Snapshot(requestContext)
	if err != nil {
		return socketResponse{Error: fmt.Sprintf("status_unavailable: %v", err)}
	}
	return operationSuccess(true, snapshot, "")
}
