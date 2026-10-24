// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import (
	"time"

	"github.com/alchemillahq/sylve/pkg/pkgmanager"
	"github.com/alchemillahq/sylve/pkg/sylveupdate"
)

type HostUpdateState struct {
	ID                      uint                   `gorm:"primaryKey" json:"-"`
	Inventory               []pkgmanager.Package   `gorm:"serializer:json;type:json" json:"packages"`
	InventoryAt             *time.Time             `json:"inventoryAt"`
	CheckedAt               *time.Time             `json:"checkedAt"`
	CheckInventoryHash      string                 `json:"-"`
	Available               pkgmanager.Plan        `gorm:"serializer:json;type:json" json:"available"`
	CheckError              string                 `json:"checkError"`
	RepositoriesRefreshedAt *time.Time             `json:"-"`
	RepositoryHash          string                 `json:"-"`
	RepositoryInventoryHash string                 `json:"-"`
	Audit                   pkgmanager.Audit       `gorm:"serializer:json;type:json" json:"audit"`
	AuditAt                 *time.Time             `json:"auditAt"`
	AuditInventoryHash      string                 `json:"-"`
	AuditError              string                 `json:"auditError"`
	Monitoring              bool                   `json:"monitoring"`
	IntervalHours           int                    `json:"intervalHours"`
	CronExpr                string                 `json:"cronExpr"`
	NextCheckAt             *time.Time             `json:"nextCheckAt"`
	LastAttemptAt           *time.Time             `json:"lastAttemptAt"`
	FailureCount            int                    `json:"failureCount"`
	HealthFailed            bool                   `json:"-"`
	ReleaseChannel          string                 `json:"releaseChannel"`
	Release                 *sylveupdate.Candidate `gorm:"serializer:json;type:json" json:"release"`
	ReleaseCheckedAt        *time.Time             `json:"releaseCheckedAt"`
	ReleaseError            string                 `json:"releaseError"`
	InstalledChannel        string                 `json:"-"`
	InstalledDigest         string                 `json:"-"`
	InstalledBuiltAt        string                 `json:"-"`
	RebootBootID            string                 `json:"-"`
	CreatedAt               time.Time              `json:"-"`
	UpdatedAt               time.Time              `json:"-"`
}

type HostUpdatePlan struct {
	ID                 string                 `gorm:"primaryKey" json:"id"`
	Kind               string                 `json:"kind"`
	Selection          []string               `gorm:"serializer:json;type:json" json:"selection"`
	Transaction        pkgmanager.Plan        `gorm:"serializer:json;type:json" json:"transaction"`
	Host               pkgmanager.HostInfo    `gorm:"serializer:json;type:json" json:"host"`
	Release            *sylveupdate.Candidate `gorm:"serializer:json;type:json" json:"release"`
	InventoryHash      string                 `json:"-"`
	RepositoryHash     string                 `json:"-"`
	CatalogHash        string                 `json:"-"`
	ExecutableHash     string                 `json:"-"`
	ExpiresAt          time.Time              `json:"expiresAt"`
	AppliedOperationID *uint                  `json:"-"`
	CreatedAt          time.Time              `json:"-"`
}

type HostUpdateOperation struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	UUID            string     `gorm:"column:worker_id;uniqueIndex" json:"-"`
	Active          *uint      `gorm:"uniqueIndex" json:"-"`
	Action          string     `json:"action"`
	Status          string     `json:"status"`
	Phase           string     `json:"phase"`
	PlanID          string     `json:"planId"`
	Channel         string     `json:"channel"`
	BootEnvironment bool       `json:"-"`
	RestartDigest   string     `json:"-"`
	Actor           string     `json:"actor"`
	Error           string     `json:"error"`
	Result          string     `json:"result"`
	CreatedAt       time.Time  `json:"createdAt"`
	FinishedAt      *time.Time `json:"finishedAt"`
}

type HostUpdateEvent struct {
	ID            string `gorm:"primaryKey"`
	Kind          string
	Title         string
	Body          string
	Severity      string
	Metadata      map[string]string `gorm:"serializer:json;type:json"`
	Targets       []string          `gorm:"serializer:json;type:json"`
	TargetsLoaded bool
	Delivered     []string `gorm:"serializer:json;type:json"`
	Attempts      int
	NextAttemptAt time.Time
	FinishedAt    *time.Time
	CreatedAt     time.Time
}
