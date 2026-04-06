// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

type StaticRoute struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	Name            string    `json:"name" gorm:"not null"`
	Description     string    `json:"description"`
	Enabled         bool      `json:"enabled" gorm:"not null;default:true"`
	FIB             uint      `json:"fib" gorm:"not null;default:0;index"`
	DestinationType string    `json:"destinationType" gorm:"not null"` // host|network
	Destination     string    `json:"destination" gorm:"not null"`
	Family          string    `json:"family" gorm:"not null"`      // inet|inet6
	NextHopMode     string    `json:"nextHopMode" gorm:"not null"` // gateway|interface
	Gateway         string    `json:"gateway"`
	Interface       string    `json:"interface"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
