// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package migration

import (
	"context"
)

type MigrateRequest struct {
	GuestType      string `json:"guestType"`
	GuestID        uint   `json:"guestId"`
	TargetNodeUUID string `json:"targetNodeUuid"`
	RequestedBy    string `json:"requestedBy"`
}

type ValidateResult struct {
	Allowed  bool     `json:"allowed"`
	Reasons  []string `json:"reasons"`
	Warnings []string `json:"warnings,omitempty"`
}

type MigrationServiceInterface interface {
	ValidateMigration(ctx context.Context, req MigrateRequest) (*ValidateResult, error)
	ExecuteMigration(ctx context.Context, taskID uint) error
	CancelMigration(ctx context.Context, taskID uint) error
}
