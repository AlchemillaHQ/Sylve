// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import "path/filepath"

func SocketPath(dataPath string) string {
	return filepath.Join(dataPath, "run", "console.sock")
}

func HistoryPath(dataPath string) string {
	return filepath.Join(dataPath, "repl", "history")
}
