// SPDX-License-Identifier: BSD-2-Clause

package jailServiceInterfaces

import (
	"time"

	"github.com/alchemillahq/sylve/pkg/pkgmanager"
)

type PackageRequest struct {
	Packages    []string         `json:"packages"`
	All         bool             `json:"all"`
	PlanID      string           `json:"planId"`
	Snapshot    bool             `json:"snapshot"`
	Acknowledge bool             `json:"acknowledge"`
	Scope       pkgmanager.Scope `json:"scope"`
}

type PackageMonitoring struct {
	Enabled       bool       `json:"enabled"`
	IntervalHours int        `json:"intervalHours"`
	NextAuditAt   *time.Time `json:"nextAuditAt"`
	LastAttemptAt *time.Time `json:"lastAttemptAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	FailureCount  int        `json:"failureCount"`
	SkipReason    string     `json:"skipReason"`
}

type PackagePlan struct {
	ID          string                 `json:"id"`
	ExpiresAt   time.Time              `json:"expiresAt"`
	Selection   []string               `json:"selection"`
	Transaction pkgmanager.Plan        `json:"transaction"`
	Scope       pkgmanager.Scope       `json:"scope"`
	System      *pkgmanager.SystemInfo `json:"system"`
}

type PackageJob struct {
	TaskID      uint              `json:"taskId"`
	OperationID string            `json:"operationId"`
	Action      string            `json:"action"`
	Scope       pkgmanager.Scope  `json:"scope"`
	Phase       string            `json:"phase"`
	Status      string            `json:"status"`
	Error       string            `json:"error"`
	Result      string            `json:"result"`
	SnapshotID  *uint             `json:"snapshotId"`
	Plan        *PackagePlan      `json:"plan"`
	Audit       *pkgmanager.Audit `json:"audit"`
	CreatedAt   time.Time         `json:"createdAt"`
	FinishedAt  *time.Time        `json:"finishedAt"`
}

type PackageView struct {
	JailID               uint                 `json:"jailId"`
	GUID                 string               `json:"guid"`
	Packages             []pkgmanager.Package `json:"packages"`
	Ready                bool                 `json:"ready"`
	Offline              bool                 `json:"offline"`
	Reason               string               `json:"reason"`
	SnapshotSupported    bool                 `json:"snapshotSupported"`
	InventoryAt          *time.Time           `json:"inventoryAt"`
	CheckedAt            *time.Time           `json:"checkedAt"`
	UpdatesChecked       bool                 `json:"updatesChecked"`
	AuditAt              *time.Time           `json:"auditAt"`
	AuditStale           bool                 `json:"auditStale"`
	AuditError           string               `json:"auditError"`
	CheckError           string               `json:"checkError"`
	Pkgbase              bool                 `json:"pkgbase"`
	SystemUpdatesChecked bool                 `json:"systemUpdatesChecked"`
	SystemCheckedAt      *time.Time           `json:"systemCheckedAt"`
	SystemCheckError     string               `json:"systemCheckError"`
	Audit                pkgmanager.Audit     `json:"audit"`
	Monitoring           PackageMonitoring    `json:"monitoring"`
	ActiveJob            *PackageJob          `json:"activeJob"`
}

type PackageLog struct {
	Text string `json:"text"`
	Next int64  `json:"next"`
}

type PackageDetails struct {
	Package               pkgmanager.Package      `json:"package"`
	Dependencies          []pkgmanager.Dependency `json:"dependencies"`
	DependenciesAvailable bool                    `json:"dependenciesAvailable"`
}
