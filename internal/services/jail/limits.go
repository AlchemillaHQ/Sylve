// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

const (
	// MaxRequestBodyBytes bounds jail JSON decoding and audit capture while
	// leaving ample room for lifecycle hooks, fstab, and metadata documents.
	MaxRequestBodyBytes int64 = 1 * 1024 * 1024
)
