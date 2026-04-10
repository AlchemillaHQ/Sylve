// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

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

func newSambaHandlerTestDB(t *testing.T, migrateModels ...any) *gorm.DB {
	t.Helper()
	return testutil.NewSQLiteTestDB(t, migrateModels...)
}

func performSambaJSONRequest(
	t *testing.T,
	router *gin.Engine,
	method string,
	path string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.PerformJSONRequest(t, router, method, path, body)
}
