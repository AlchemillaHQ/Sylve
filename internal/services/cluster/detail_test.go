// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"testing"
)

func TestDetail(t *testing.T) {
	s := &Service{}
	detail := s.Detail()
	if detail == nil {
		t.Fatal("expected non-nil detail")
	}
	if detail.NodeID == "" {
		t.Fatal("expected non-empty NodeID from system UUID")
	}
	if detail.Hostname == "" {
		t.Fatal("expected non-empty Hostname")
	}
	if detail.APIPort != ClusterEmbeddedHTTPSPort {
		t.Fatalf("expected APIPort=%d, got %d", ClusterEmbeddedHTTPSPort, detail.APIPort)
	}
}
