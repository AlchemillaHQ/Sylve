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
	"strings"

	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/network"
	"github.com/chzyer/readline"
)

const replHistoryFile = "/tmp/sylve.repl.history"

type Context struct {
	Auth           *auth.Service
	Jail           *jail.Service
	VirtualMachine *libvirt.Service
	Network        *network.Service
	QuitChan       chan os.Signal
	Out            io.Writer
}

func Start(ctx *Context) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "sylve> ",
		HistoryFile:     replHistoryFile,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Fprintln(outputWriter(ctx), "REPL init failed:", err)
		return
	}
	defer rl.Close()

	fmt.Fprintln(outputWriter(ctx), "Sylve REPL ready. Type `help`.")

	for {
		line, err := rl.Readline()
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !ExecuteLine(ctx, line) {
			return
		}
	}
}
