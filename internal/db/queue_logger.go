// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import "github.com/rs/zerolog"

type zerologJobLogger struct {
	Logger zerolog.Logger
}

func (l zerologJobLogger) Info(msg string, args ...any) {
	level := zerolog.DebugLevel
	if msg == "Running job" || msg == "Ran job" {
		for i := 0; i < len(args)-1; i += 2 {
			key, _ := args[i].(string)
			name, _ := args[i+1].(string)
			if key == "name" && (name == "disk-smart-scheduler-tick" || name == "zfs_scrub_scheduler_tick") {
				level = zerolog.TraceLevel
				break
			}
		}
	}
	evt := l.Logger.WithLevel(level)
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}

func (l zerologJobLogger) Error(msg string, args ...any) {
	evt := l.Logger.Error()
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}

func (l zerologJobLogger) Warn(msg string, args ...any) {
	evt := l.Logger.Warn()
	for i := 0; i < len(args)-1; i += 2 {
		key, _ := args[i].(string)
		evt = evt.Interface(key, args[i+1])
	}
	evt.Msg(msg)
}
