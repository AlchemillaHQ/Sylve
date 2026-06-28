// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sysctl

// Tunable represents a single sysctl entry discovered by walking the MIB tree.
type Tunable struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Writable bool   `json:"writable"`
}
