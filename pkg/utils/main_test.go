// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	trustAny := &tls.Config{InsecureSkipVerify: true}
	SetIntraClusterTLS(trustAny, trustAny)
	os.Exit(m.Run())
}

func TestIntraClusterTransportFailsClosedAndKeepsJoinApart(t *testing.T) {
	previous := intraClusterTransports.Load()
	t.Cleanup(func() { intraClusterTransports.Store(previous) })
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	intraClusterTransports.Store(nil)
	if _, _, err := HTTPGetJSONReadContext(context.Background(), server.URL, nil); err == nil ||
		!strings.Contains(err.Error(), "intra_cluster_tls_unconfigured") {
		t.Fatalf("unconfigured client: %v", err)
	}

	refuseAll := &tls.Config{InsecureSkipVerify: true, VerifyConnection: func(tls.ConnectionState) error {
		return errors.New("not_a_member")
	}}
	SetIntraClusterTLS(refuseAll, &tls.Config{InsecureSkipVerify: true})
	if _, _, err := HTTPGetJSONReadContext(context.Background(), server.URL, nil); err == nil ||
		!strings.Contains(err.Error(), "not_a_member") {
		t.Fatalf("peer request to a non-member: %v", err)
	}
	accept := func(tls.ConnectionState) error { return nil }
	if _, _, err := HTTPGetJSONReadContext(ForJoin(context.Background(), accept), server.URL, nil); err != nil {
		t.Fatalf("join request: %v", err)
	}
	refuse := func(tls.ConnectionState) error { return errors.New("cluster_join_key_node_mismatch") }
	if _, _, err := HTTPGetJSONReadContext(ForJoin(context.Background(), refuse), server.URL, nil); err == nil ||
		!strings.Contains(err.Error(), "cluster_join_key_node_mismatch") {
		t.Fatalf("join request to the wrong node: %v", err)
	}
}
