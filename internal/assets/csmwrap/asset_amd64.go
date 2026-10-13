// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package csmwrap

import _ "embed"

//go:embed CSMWrap.iso.gz
var compressedISO []byte

//go:embed CSMWrap-build-manifest.json
var buildManifest []byte
