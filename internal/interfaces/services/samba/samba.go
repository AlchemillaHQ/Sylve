// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaServiceInterfaces

import (
	"context"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
)

type SambaServiceInterface interface {
	DisableMissingShares(ctx context.Context) error
	WriteConfig(ctx context.Context, reload bool) error
	ParseAuditLogs() error
	StartAuditWatcher(ctx context.Context)
	EnsureCanStop() error
}

type AuditLogQuery struct {
	Page      int
	Size      int
	SortField string
	SortDir   string
	Share     string
	User      string
	Actions   []string
	Search    string
	From      *time.Time
	To        *time.Time
}

type AuditLogsResponse struct {
	LastPage int                         `json:"last_page"`
	Total    int64                       `json:"total"`
	Data     []sambaModels.SambaAuditLog `json:"data"`
}
