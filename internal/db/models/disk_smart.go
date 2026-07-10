// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import "time"

type DiskSmartSelfTestSchedule struct {
	ID                     uint       `json:"id" gorm:"primaryKey"`
	DiskKey                string     `json:"diskKey" gorm:"not null;uniqueIndex:idx_disk_smart_self_test_schedule"`
	Device                 string     `json:"device" gorm:"not null;index"`
	Model                  string     `json:"model"`
	Serial                 string     `json:"serial"`
	TestType               string     `json:"testType" gorm:"not null;uniqueIndex:idx_disk_smart_self_test_schedule"`
	CronExpr               string     `json:"cronExpr" gorm:"not null"`
	Enabled                bool       `json:"enabled" gorm:"not null;index"`
	QueuedAt               *time.Time `json:"queuedAt" gorm:"index"`
	QueueUpdatedAt         *time.Time `json:"-" gorm:"index"`
	LastRunAt              *time.Time `json:"lastRunAt"`
	NextRunAt              *time.Time `json:"nextRunAt" gorm:"index"`
	OccurrenceKey          string     `json:"-" gorm:"index"`
	LastStatus             string     `json:"lastStatus" gorm:"not null;default:idle;index"`
	LastError              string     `json:"lastError"`
	BaselineFingerprint    string     `json:"-"`
	BaselineLogFingerprint string     `json:"-"`
	BaselineValid          bool       `json:"-" gorm:"not null;default:false"`
	ProgressPct            int        `json:"progressPct" gorm:"not null;default:-1"`
	ProgressKnown          bool       `json:"progressKnown" gorm:"not null;default:false"`
	EstimatedMinutes       int        `json:"estimatedMinutes" gorm:"not null;default:0"`
	RunningObserved        bool       `json:"-" gorm:"not null;default:false"`
	TimeoutAbortAttempted  bool       `json:"-" gorm:"not null;default:false"`
	LastResultFingerprint  string     `json:"-"`
	CreatedAt              time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt              time.Time  `json:"updatedAt" gorm:"autoUpdateTime"`
}

type DiskSmartSelfTestEvent struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	EventKey         string     `json:"-" gorm:"not null;uniqueIndex"`
	RunKey           string     `json:"-" gorm:"not null;default:'';index"`
	Source           string     `json:"source" gorm:"not null;default:'';index"`
	ScheduleID       uint       `json:"scheduleId" gorm:"not null;index"`
	DiskKey          string     `json:"diskKey" gorm:"not null;index"`
	Device           string     `json:"device" gorm:"not null"`
	TestType         string     `json:"testType" gorm:"not null"`
	Condition        string     `json:"condition" gorm:"not null"`
	Severity         string     `json:"severity" gorm:"not null"`
	Title            string     `json:"title" gorm:"not null"`
	Body             string     `json:"body" gorm:"type:text"`
	ClaimToken       string     `json:"-" gorm:"index"`
	ClaimedAt        *time.Time `json:"-" gorm:"index"`
	DeliveryPlan     string     `json:"-" gorm:"type:text"`
	DeliveredTargets string     `json:"-" gorm:"type:text"`
	AttemptCount     uint       `json:"-" gorm:"not null;default:0"`
	NextAttemptAt    *time.Time `json:"-" gorm:"index"`
	DeliveryError    string     `json:"-" gorm:"type:text"`
	SupersededAt     *time.Time `json:"-" gorm:"index"`
	DeadLetteredAt   *time.Time `json:"-" gorm:"index"`
	CreatedAt        time.Time  `json:"createdAt" gorm:"autoCreateTime;index"`
}

type DiskSmartSelfTestRun struct {
	ID                  uint       `json:"id" gorm:"primaryKey"`
	RunKey              string     `json:"-" gorm:"not null;uniqueIndex"`
	DiskKey             string     `json:"diskKey" gorm:"not null;index:idx_disk_smart_self_test_run_disk_started,priority:1;index:idx_disk_smart_self_test_run_disk_result,priority:1"`
	Device              string     `json:"device" gorm:"not null;index"`
	Model               string     `json:"model"`
	Serial              string     `json:"serial"`
	TestType            string     `json:"testType" gorm:"not null"`
	Source              string     `json:"source" gorm:"not null"`
	ScheduleID          *uint      `json:"scheduleId" gorm:"index"`
	StartedAt           time.Time  `json:"startedAt" gorm:"not null;index:idx_disk_smart_self_test_run_disk_started,priority:2"`
	CompletedAt         *time.Time `json:"completedAt"`
	Status              string     `json:"status" gorm:"not null;index;index:idx_disk_smart_self_test_run_active,priority:1"`
	BaselineFingerprint string     `json:"-" gorm:"type:text"`
	BaselineValid       bool       `json:"-" gorm:"not null;default:false"`
	ResultFingerprint   string     `json:"-" gorm:"index:idx_disk_smart_self_test_run_disk_result,priority:2;index:idx_disk_smart_self_test_run_active,priority:2"`
	ResultData          string     `json:"-" gorm:"type:text;default:'';not null"`
	CreatedAt           time.Time  `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt           time.Time  `json:"updatedAt" gorm:"autoUpdateTime"`
}

type DiskSmartSelfTestSchedulerLease struct {
	ID            uint       `json:"-" gorm:"primaryKey"`
	Token         string     `json:"-" gorm:"index"`
	ExpiresAt     time.Time  `json:"-" gorm:"index"`
	DispatchToken string     `json:"-" gorm:"index"`
	DispatchedAt  *time.Time `json:"-" gorm:"index"`
}
