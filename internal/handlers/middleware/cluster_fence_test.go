// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

type mutationAdmissionStub struct {
	err      error
	entered  int
	released int
}

func (s *mutationAdmissionStub) EnterMutation(ctx context.Context) (context.Context, func(), error) {
	s.entered++
	if s.err != nil {
		return ctx, nil, s.err
	}
	return ctx, func() { s.released++ }, nil
}

func TestClusterMutationAdmissionClassification(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/vm", false},
		{http.MethodGet, "/api/vm/1/console", true},
		{http.MethodPost, "/api/auth/login", false},
		{http.MethodPost, "/api/intra-cluster/leave", false},
		{http.MethodPost, "/api/cluster/membership-status", false},
		{http.MethodPost, "/api/cluster/remove-node/force", true},
		{http.MethodDelete, "/api/cluster/reset-node", false},
		{http.MethodDelete, "/api/cluster/reset-node/force", false},
		{http.MethodPost, "/api/vm", true},
		{http.MethodDelete, "/api/unknown", true},
	}
	for _, test := range tests {
		if got := clusterMutationRequiresAdmission(test.method, test.path); got != test.want {
			t.Fatalf("%s %s = %v, want %v", test.method, test.path, got, test.want)
		}
	}
}

func TestClusterMutationAdmissionRejectsFence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &mutationAdmissionStub{err: cluster.ErrNodeLeaveFenced}
	router := gin.New()
	router.Use(ClusterMutationAdmission(stub))
	router.POST("/api/vm", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := testutil.PerformRequest(t, router, http.MethodPost, "/api/vm", nil, nil)
	if response.Code != http.StatusLocked {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if stub.entered != 1 || stub.released != 0 || !errors.Is(stub.err, cluster.ErrNodeLeaveFenced) {
		t.Fatalf("stub = %#v", stub)
	}
}

func TestClusterMutationAdmissionReleasesPermit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &mutationAdmissionStub{}
	router := gin.New()
	router.Use(ClusterMutationAdmission(stub))
	router.POST("/api/vm", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := testutil.PerformRequest(t, router, http.MethodPost, "/api/vm", nil, nil)
	if response.Code != http.StatusNoContent || stub.entered != 1 || stub.released != 1 {
		t.Fatalf("status=%d stub=%#v", response.Code, stub)
	}
}
