// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import "encoding/json"

// Request carries either an interactive command or a typed operation. Typed
// operations let direct CLI callers receive real errors instead of parsing
// formatted REPL output.
type Request struct {
	Command   string          `json:"command,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Close  bool   `json:"close,omitempty"`
}
