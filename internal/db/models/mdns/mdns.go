// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdnsModels

import (
	"time"
)

type MdnsSettings struct {
	ID         uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Interfaces string `json:"interfaces"`
	Hostname   string `json:"hostname"`
}

type MdnsRecord struct {
	ID         uint              `json:"id" gorm:"primaryKey;autoIncrement"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Port       int               `json:"port"`
	Txt        map[string]string `json:"txt" gorm:"serializer:json;type:json"`
	Interfaces string            `json:"interfaces"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}
