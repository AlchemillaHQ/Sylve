// SPDX-License-Identifier: BSD-2-Clause

package jailModels

import (
	"time"

	"github.com/alchemillahq/sylve/pkg/pkgmanager"
)

type JailPackageState struct {
	JailID                   uint                 `gorm:"primaryKey" json:"jailId"`
	CTID                     uint                 `gorm:"index;not null" json:"ctId"`
	GUID                     string               `gorm:"not null" json:"-"`
	Inventory                []pkgmanager.Package `gorm:"serializer:json;type:json" json:"packages"`
	InventoryAt              *time.Time           `json:"inventoryAt"`
	CheckedAt                *time.Time           `json:"checkedAt"`
	AuditAt                  *time.Time           `json:"auditAt"`
	Audit                    pkgmanager.Audit     `gorm:"serializer:json;type:json" json:"audit"`
	AuditError               string               `json:"auditError"`
	AuditInventoryHash       string               `json:"-"`
	CheckInventoryHash       string               `json:"-"`
	CheckError               string               `json:"checkError"`
	SystemCheckedAt          *time.Time           `json:"systemCheckedAt"`
	SystemCheckInventoryHash string               `json:"-"`
	SystemCheckError         string               `json:"systemCheckError"`
	RepositoriesRefreshedAt  *time.Time           `json:"-"`
	RepositoryHash           string               `json:"-"`
	SystemReposRefreshedAt   *time.Time           `json:"-"`
	SystemRepositoryHash     string               `json:"-"`
	NextAuditAt              *time.Time           `json:"nextAuditAt"`
	LastAttemptAt            *time.Time           `json:"lastAttemptAt"`
	FailureCount             int                  `gorm:"not null;default:0" json:"failureCount"`
	SkipReason               string               `json:"skipReason"`
	HealthFailed             bool                 `gorm:"not null;default:false" json:"-"`
	CreatedAt                time.Time            `json:"-"`
	UpdatedAt                time.Time            `json:"-"`
}

type JailPackageOperation struct {
	ID           string            `gorm:"primaryKey" json:"id"`
	TaskID       *uint             `gorm:"uniqueIndex" json:"taskId"`
	JailID       uint              `gorm:"index;not null" json:"-"`
	ActiveJailID *uint             `gorm:"uniqueIndex" json:"-"`
	CTID         uint              `gorm:"index;not null" json:"ctId"`
	GUID         string            `gorm:"not null" json:"-"`
	Action       string            `gorm:"not null" json:"action"`
	Scope        pkgmanager.Scope  `gorm:"not null;default:applications" json:"scope"`
	Phase        string            `gorm:"not null" json:"phase"`
	Selection    []string          `gorm:"serializer:json;type:json" json:"selection"`
	All          bool              `json:"all"`
	PlanID       string            `json:"planId"`
	Snapshot     bool              `json:"snapshot"`
	SnapshotID   *uint             `json:"snapshotId"`
	Acknowledge  bool              `json:"-"`
	Result       string            `gorm:"type:text" json:"result"`
	Audit        *pkgmanager.Audit `gorm:"serializer:json;type:json" json:"audit"`
	Error        string            `gorm:"type:text" json:"error"`
	FinishedAt   *time.Time        `json:"finishedAt"`
	CreatedAt    time.Time         `gorm:"index" json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type JailPackagePlan struct {
	ID                 string                 `gorm:"primaryKey" json:"id"`
	JailID             uint                   `gorm:"index;not null" json:"-"`
	GUID               string                 `gorm:"not null" json:"-"`
	Selection          []string               `gorm:"serializer:json;type:json" json:"selection"`
	Scope              pkgmanager.Scope       `gorm:"not null;default:applications" json:"scope"`
	System             *pkgmanager.SystemInfo `gorm:"serializer:json;type:json" json:"system"`
	RepositoryHash     string                 `json:"-"`
	CatalogHash        string                 `json:"-"`
	InventoryHash      string                 `json:"-"`
	Plan               pkgmanager.Plan        `gorm:"serializer:json;type:json" json:"transaction"`
	AppliedOperationID string                 `json:"-"`
	ExpiresAt          time.Time              `json:"expiresAt"`
	CreatedAt          time.Time              `json:"createdAt"`
}

type JailPackageNotificationEvent struct {
	ID            string `gorm:"primaryKey"`
	JailID        uint   `gorm:"index;not null"`
	GUID          string `gorm:"not null"`
	Kind          string
	Title         string
	Body          string `gorm:"type:text"`
	Severity      string
	Metadata      map[string]string `gorm:"serializer:json;type:json"`
	Targets       []string          `gorm:"serializer:json;type:json"`
	TargetsLoaded bool
	Delivered     []string `gorm:"serializer:json;type:json"`
	Attempts      int
	NextAttemptAt time.Time `gorm:"index"`
	FinishedAt    *time.Time
	CreatedAt     time.Time
}
