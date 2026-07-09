// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"io"
	"os"

	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/network"
)

const replHistoryFile = "/tmp/sylve.repl.history"

type Context struct {
	Auth           *auth.Service
	Info           *info.Service
	Jail           *jail.Service
	VirtualMachine *libvirt.Service
	Lifecycle      *lifecycle.Service
	Network        *network.Service
	QuitChan       chan os.Signal
	Out            io.Writer
}

func Start(ctx *Context) {
	startTUI(ctx)
}
