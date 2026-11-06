// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"crypto/tls"
	"os"
	"testing"

	"github.com/alchemillahq/sylve/pkg/utils"
)

func TestMain(m *testing.M) {
	trustAny := &tls.Config{InsecureSkipVerify: true}
	utils.SetIntraClusterTLS(trustAny, trustAny)
	os.Exit(m.Run())
}
