// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package infoModels

import "time"

type CPU struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Usage     float64   `json:"usage"`
	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"createdAt"`
}

type RAM struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Usage     float64   `json:"usage"`
	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"createdAt"`
}

type Swap struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Usage     float64   `json:"usage"`
	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"createdAt"`
}

func (c CPU) GetID() uint             { return c.ID }
func (c CPU) GetCreatedAt() time.Time { return c.CreatedAt }

func (r RAM) GetID() uint             { return r.ID }
func (r RAM) GetCreatedAt() time.Time { return r.CreatedAt }

func (s Swap) GetID() uint             { return s.ID }
func (s Swap) GetCreatedAt() time.Time { return s.CreatedAt }
