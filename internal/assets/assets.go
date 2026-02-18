// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package assets

import "embed"

//go:embed all:web-files
var SvelteKitFiles embed.FS

//go:embed all:zelta
var ZeltaFiles embed.FS
