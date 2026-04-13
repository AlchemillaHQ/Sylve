// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package notifications

import (
	"context"
	"errors"
	"sync"
)

var ErrEmitterNotConfigured = errors.New("notifications_emitter_not_configured")

type EventInput struct {
	Kind        string            `json:"kind"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	Severity    string            `json:"severity"`
	Source      string            `json:"source"`
	Fingerprint string            `json:"fingerprint"`
	Metadata    map[string]string `json:"metadata"`
}

type EmitResult struct {
	NotificationID uint `json:"notificationId"`
	Suppressed     bool `json:"suppressed"`
	SentNtfy       bool `json:"sentNtfy"`
	SentEmail      bool `json:"sentEmail"`
}

type Emitter interface {
	Emit(ctx context.Context, input EventInput) (EmitResult, error)
}

var (
	emitterMu sync.RWMutex
	emitter   Emitter
)

func SetEmitter(next Emitter) {
	emitterMu.Lock()
	emitter = next
	emitterMu.Unlock()
}

func Emit(ctx context.Context, input EventInput) (EmitResult, error) {
	emitterMu.RLock()
	active := emitter
	emitterMu.RUnlock()

	if active == nil {
		return EmitResult{}, ErrEmitterNotConfigured
	}

	return active.Emit(ctx, input)
}
