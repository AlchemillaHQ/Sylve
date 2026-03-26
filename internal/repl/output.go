// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
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
