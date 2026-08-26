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
	"fmt"
	"io"
	"os"
)

func outputWriter(ctx *Context) io.Writer {
	if ctx != nil && ctx.Out != nil {
		return ctx.Out
	}

	return os.Stdout
}

func printf(ctx *Context, format string, args ...any) {
	fmt.Fprintf(outputWriter(ctx), format, args...)
}

func println(ctx *Context, args ...any) {
	fmt.Fprintln(outputWriter(ctx), args...)
}

type errorResponse struct {
	Error string `json:"error"`
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(b)
}

func formatMemorySize(memory int) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)

	switch {
	case memory <= 0:
		return "-"
	case memory%gib == 0:
		return fmt.Sprintf("%d GiB", memory/gib)
	case memory%mib == 0:
		return fmt.Sprintf("%d MiB", memory/mib)
	case memory%kib == 0:
		return fmt.Sprintf("%d KiB", memory/kib)
	default:
		return fmt.Sprintf("%d B", memory)
	}
}

func printOperationError(ctx *Context, jsonMode bool, message string, err error) {
	if jsonMode {
		println(ctx, mustJSON(errorResponse{Error: err.Error()}))
		return
	}
	println(ctx, styledErrorf("%s: %v", message, err))
}
