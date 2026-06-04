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
	"strings"
	"sync"
)

var ErrEmitterNotConfigured = errors.New("notifications_emitter_not_configured")

const ZFSPoolStateKindPrefix = "system.zfs.pool_state."

const (
	DiskSmartTemperatureKindPrefix = "system.disk.smart.temperature."
	DiskSmartWearoutKindPrefix     = "system.disk.smart.wearout."
	DiskSmartHealthKindPrefix      = "system.disk.smart.health."
	DiskSmartNvmeKindPrefix        = "system.disk.smart.nvme."
)

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

func KindForZFSPoolState(pool string) string {
	pool = normalizePoolName(pool)
	if pool == "" {
		return ZFSPoolStateKindPrefix
	}

	return ZFSPoolStateKindPrefix + pool
}

func PoolFromZFSPoolStateKind(kind string) (string, bool) {
	normalized := strings.TrimSpace(strings.ToLower(kind))
	if !strings.HasPrefix(normalized, ZFSPoolStateKindPrefix) {
		return "", false
	}

	pool := strings.TrimSpace(normalized[len(ZFSPoolStateKindPrefix):])
	if pool == "" {
		return "", false
	}

	return pool, true
}

func KindForDiskSmart(prefix, diskName string) string {
	diskName = strings.TrimSpace(strings.ToLower(diskName))
	if diskName == "" {
		return prefix
	}

	return prefix + diskName
}

func DiskNameFromSmartKind(kind string) (prefix string, diskName string, ok bool) {
	normalized := strings.TrimSpace(strings.ToLower(kind))
	for _, prefix := range []string{
		DiskSmartTemperatureKindPrefix,
		DiskSmartWearoutKindPrefix,
		DiskSmartHealthKindPrefix,
		DiskSmartNvmeKindPrefix,
	} {
		if strings.HasPrefix(normalized, prefix) {
			disk := strings.TrimSpace(normalized[len(prefix):])
			if disk == "" {
				return prefix, "", false
			}

			return prefix, disk, true
		}
	}

	return "", "", false
}

func normalizePoolName(pool string) string {
	return strings.TrimSpace(strings.ToLower(pool))
}

var diskSmartKindPrefixes = []string{
	DiskSmartTemperatureKindPrefix,
	DiskSmartWearoutKindPrefix,
	DiskSmartHealthKindPrefix,
	DiskSmartNvmeKindPrefix,
}

func IsDiskSmartKind(kind string) bool {
	kind = strings.TrimSpace(strings.ToLower(kind))
	for _, prefix := range diskSmartKindPrefixes {
		if strings.HasPrefix(kind, prefix) {
			return true
		}
	}

	return false
}
