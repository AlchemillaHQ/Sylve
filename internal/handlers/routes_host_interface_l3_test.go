// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesIncludesHostInterfaceL3Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.NewSQLiteTestDB(t)
	r := gin.New()
	RegisterRoutes(
		r, internal.Development, false,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, db, db,
	)

	expected := map[string]bool{
		"GET /api/network/interface/l3":                 false,
		"GET /api/network/interface/l3/pending":         false,
		"PUT /api/network/interface/:name/l3":           false,
		"DELETE /api/network/interface/:name/l3":        false,
		"POST /api/network/interface/:name/l3/reapply":  false,
		"POST /api/network/host-ip/pending/:id/confirm": false,
		"POST /api/network/host-ip/pending/:id/revert":  false,
	}

	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := expected[key]; ok {
			expected[key] = true
		}
	}

	for key, found := range expected {
		if !found {
			t.Fatalf("expected route %s to be registered", key)
		}
	}
}
