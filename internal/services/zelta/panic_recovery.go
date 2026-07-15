// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
)

// recoverOperationPanic converts a panic into the operation's named error.
// Register it after the operation finalizer so it runs first during unwind and
// the existing finalizer persists a failed result instead of a false success.
func recoverOperationPanic(scope string, resultErr *error) {
	recovered := recover()
	if recovered == nil {
		return
	}

	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "operation"
	}
	panicErr := fmt.Errorf("%s_panicked: %v", scope, recovered)
	if resultErr != nil {
		*resultErr = panicErr
	}
	logger.L.Error().
		Err(panicErr).
		Interface("panic", recovered).
		Str("stack", string(debug.Stack())).
		Msg(scope + "_panicked")
}
