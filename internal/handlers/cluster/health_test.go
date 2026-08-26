// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/auth"
)

func TestFetchNodeVersionFromHealthUsesGETAndHeaders(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get(auth.ClusterKeyHeader); got != "cluster-secret" {
			t.Errorf("cluster key header = %q, want cluster-secret", got)
		}

		writeJoinLeaderStubJSON(w, http.StatusOK, internal.APIResponse[map[string]string]{
			Status: "success",
			Data:   map[string]string{"sylveVersion": "1.2.3"},
		})
	}))
	defer server.Close()

	version, err := fetchNodeVersionFromHealth(
		server.URL,
		map[string]string{auth.ClusterKeyHeader: "cluster-secret"},
	)
	if err != nil {
		t.Fatalf("fetch health version: %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", version)
	}
}
