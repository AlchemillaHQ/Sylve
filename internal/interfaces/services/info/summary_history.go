// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoServiceInterfaces

import (
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
)

type SummaryHistoryCursors struct {
	CPU     uint `json:"cpu"`
	RAM     uint `json:"ram"`
	Network uint `json:"network"`
}

type SummaryHistoryNetworkPoint struct {
	ID            uint      `json:"id"`
	ReceivedBytes int64     `json:"receivedBytes"`
	SentBytes     int64     `json:"sentBytes"`
	CreatedAt     time.Time `json:"createdAt"`
}

type SummaryHistory struct {
	CPU     []infoModels.CPU             `json:"cpu"`
	RAM     []infoModels.RAM             `json:"ram"`
	Network []SummaryHistoryNetworkPoint `json:"network"`
	Cursors SummaryHistoryCursors        `json:"cursors"`
}
