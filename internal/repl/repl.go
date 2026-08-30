// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package repl

import (
	"context"
	"io"
	"os"

	consoleprotocol "github.com/alchemillahq/sylve/internal/console"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/services/info"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/services/network"
)

type Context struct {
	Auth           *auth.Service
	Cluster        *cluster.Service
	Info           *info.Service
	Jail           *jail.Service
	VirtualMachine *libvirt.Service
	Lifecycle      *lifecycle.Service
	Network        *network.Service
	Utilities      utilitiesServiceInterfaces.UtilitiesServiceInterface
	Status         *StatusProvider
	HistoryPath    string
	QuitChan       chan os.Signal
	Out            io.Writer

	pendingSerialConsole *consoleprotocol.VMSerialConsoleLaunch
	mutationContext      context.Context
}

func operationContext(ctx *Context) context.Context {
	if ctx != nil && ctx.mutationContext != nil {
		return ctx.mutationContext
	}
	return context.Background()
}

func (ctx *Context) queueSerialConsole(launch consoleprotocol.VMSerialConsoleLaunch) {
	if ctx == nil {
		return
	}
	copy := launch
	ctx.pendingSerialConsole = &copy
}

func (ctx *Context) takeSerialConsole() *consoleprotocol.VMSerialConsoleLaunch {
	if ctx == nil {
		return nil
	}
	launch := ctx.pendingSerialConsole
	ctx.pendingSerialConsole = nil
	return launch
}

func Start(ctx *Context) {
	startTUI(ctx)
}
