// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsModels

import "time"

type PoolScrubSchedule struct {
	ID            uint       `json:"id" gorm:"primaryKey"`
	PoolGUID      string     `json:"poolGuid" gorm:"not null;index"`
	PoolName      string     `json:"poolName" gorm:"not null;index"`
	CronExpr      string     `json:"cronExpr" gorm:"not null"`
	Enabled       bool       `json:"enabled" gorm:"not null;index"`
	QueuedAt      *time.Time `json:"queuedAt"`
	ScheduledFor  *time.Time `json:"-"`
	LastRunAt     *time.Time `json:"lastRunAt"`
	NextRunAt     *time.Time `json:"nextRunAt" gorm:"index"`
	LastStatus    string     `json:"lastStatus" gorm:"not null;default:idle;index"`
	LastError     string     `json:"lastError"`
	ProgressPct   int        `json:"progressPct" gorm:"not null;default:-1"`
	ProgressKnown bool       `json:"progressKnown"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type PoolScrubRun struct {
	ID                   uint       `json:"id" gorm:"primaryKey"`
	RunKey               string     `json:"-" gorm:"not null;uniqueIndex"`
	PoolGUID             string     `json:"poolGuid" gorm:"not null;index:idx_pool_scrub_history,priority:1"`
	PoolName             string     `json:"poolName" gorm:"not null;index"`
	Source               string     `json:"source" gorm:"not null;index"`
	ScheduleID           *uint      `json:"scheduleId" gorm:"index"`
	ScheduledFor         *time.Time `json:"scheduledFor"`
	RequestedAt          time.Time  `json:"requestedAt" gorm:"not null;index:idx_pool_scrub_history,priority:2"`
	StartedAt            *time.Time `json:"startedAt"`
	CompletedAt          *time.Time `json:"completedAt"`
	Status               string     `json:"status" gorm:"not null;index"`
	Error                string     `json:"error"`
	ProgressPct          int        `json:"progressPct" gorm:"not null;default:-1"`
	ProgressKnown        bool       `json:"progressKnown"`
	Errors               *string    `json:"errors"`
	RepairedBytes        *string    `json:"repairedBytes"`
	ExaminedBytes        *string    `json:"examinedBytes"`
	IssuedBytes          *string    `json:"issuedBytes"`
	TotalBytes           *string    `json:"totalBytes"`
	BaselineStart        *time.Time `json:"-"`
	StartedEventRecorded bool       `json:"-"`
	ObservationLostAt    *time.Time `json:"-"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type PoolScrubEvent struct {
	ID               uint       `json:"-" gorm:"primaryKey"`
	EventKey         string     `json:"-" gorm:"not null;uniqueIndex"`
	RunKey           string     `json:"-" gorm:"not null;index"`
	PoolGUID         string     `json:"-" gorm:"not null;index"`
	PoolName         string     `json:"-"`
	Source           string     `json:"-"`
	ScheduleID       uint       `json:"-"`
	Condition        string     `json:"-"`
	Severity         string     `json:"-"`
	Title            string     `json:"-"`
	Body             string     `json:"-"`
	ClaimToken       string     `json:"-"`
	ClaimedAt        *time.Time `json:"-" gorm:"index"`
	DeliveryPlan     string     `json:"-"`
	DeliveredTargets string     `json:"-"`
	FailedTargets    string     `json:"-"`
	AttemptCount     uint       `json:"-"`
	NextAttemptAt    *time.Time `json:"-" gorm:"index"`
	DeliveryError    string     `json:"-"`
	DeadLetteredAt   *time.Time `json:"-" gorm:"index"`
	CreatedAt        time.Time  `json:"-" gorm:"index"`
}

type PoolScrubSchedulerLease struct {
	ID            uint `gorm:"primaryKey"`
	Token         string
	ExpiresAt     time.Time
	DispatchToken string
	DispatchedAt  *time.Time
}

type PoolScrubCooldown struct {
	PoolGUID        string    `gorm:"primaryKey"`
	LastCompletedAt time.Time `gorm:"not null"`
}
