// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"net/http/httptest"
	"testing"

	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type handlerAPIResponse[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    T      `json:"data"`
	Error   string `json:"error"`
}

func newClusterHandlerTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	return testutil.NewSQLiteTestDB(t, migrateModels...)
}

func performJSONRequest(t *testing.T, r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	return testutil.PerformJSONRequest(t, r, method, path, body)
}
