// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesModels

import "time"

type CloudInitTemplate struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `json:"name" gorm:"uniqueIndex;"`
	User string `json:"user"`
	Meta string `json:"meta"`
	NetworkConfig string `json:"networkConfig"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
