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

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
)

type SambaServiceInterface interface {
	WriteConfig(ctx context.Context, reload bool) error
	ParseAuditLogs() error
}

type AuditLogsResponse struct {
	LastPage int                         `json:"last_page"`
	Data     []sambaModels.SambaAuditLog `json:"data"`
}
