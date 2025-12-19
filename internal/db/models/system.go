// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package models

import "time"

type DefaultRoutes struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

type System struct {
	ID            int           `json:"id" gorm:"primaryKey"`
	Initialized   bool          `json:"initialized"`
	Hostname      string        `json:"hostname"`
	DefaultRoutes DefaultRoutes `json:"defaultRoutes" gorm:"embedded"`
	ISODir        string        `json:"isoDir"`
}

type PassedThroughIDs struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	Domain    int    `json:"domain"`
	OldDriver string `json:"oldDriver"`
	DeviceID  string `json:"deviceID" gorm:"uniqueIndex"`
}

type Triggers struct {
	ID          int       `json:"id" gorm:"primaryKey"`
	Action      string    `json:"action"`
	Data        string    `json:"data"`
	Completed   bool      `json:"completed"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
	CompletedAt time.Time `json:"completedAt" gorm:"autoUpdateTime"`
}

type DevdEvent struct {
	ID        uint              `json:"id" gorm:"primaryKey"`
	System    string            `json:"system"`
	Subsystem string            `json:"subsystem"`
	Type      string            `json:"type"`
	Attrs     map[string]string `json:"attrs" gorm:"serializer:json"`
	Raw       string            `json:"raw"`
	Processed bool              `json:"processed"`
	CreatedAt time.Time         `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time         `json:"updatedAt" gorm:"autoUpdateTime"`
}
