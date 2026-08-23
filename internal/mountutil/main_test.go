// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mountutil

import (
	"os"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil/zfstest"
)

func TestMain(m *testing.M) {
	os.Exit(zfstest.Run(m))
}
