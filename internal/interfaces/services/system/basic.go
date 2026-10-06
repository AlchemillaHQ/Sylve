// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package systemServiceInterfaces

import (
	"context"

	"github.com/alchemillahq/sylve/internal/db/models"
)

type InitializeRequest struct {
	Pools    []string                  `json:"pools"`
	Services []models.AvailableService `json:"services"`
}

type BootstrapItemStatus string

const (
	BootstrapApplied  BootstrapItemStatus = "applied"
	BootstrapWarning  BootstrapItemStatus = "warning"
	BootstrapFailed   BootstrapItemStatus = "failed"
	BootstrapDeferred BootstrapItemStatus = "deferred"
)

type BootstrapItemResult struct {
	Kind    string              `json:"kind"`
	Index   int                 `json:"index"`
	Name    string              `json:"name,omitempty"`
	Status  BootstrapItemStatus `json:"status"`
	Message string              `json:"message,omitempty"`
}

type BootstrapSettingsRequest struct {
	Initialized bool
	Pools       []string
	Services    []models.AvailableService
}

type BootstrapSettingsResult struct {
	Items           []BootstrapItemResult
	RestartRequired bool
}

type BootstrapSettingsApplier interface {
	ApplyBootstrapSettings(context.Context, BootstrapSettingsRequest) (BootstrapSettingsResult, error)
}
