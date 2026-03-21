// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilitiesServiceInterfaces

type AddTemplateRequest struct {
	Name string `json:"name" binding:"required,min=1,max=255"`
	User string `json:"user" binding:"required"`
	Meta string `json:"meta" binding:"required"`
	NetworkConfig string `json:"networkConfig" binding:"omitempty"`
}

type EditTemplateRequest struct {
	ID   uint   `json:"-"`
	Name string `json:"name" binding:"omitempty,min=1,max=255"`
	User string `json:"user"`
	Meta string `json:"meta"`
	NetworkConfig string `json:"networkConfig"`
}
