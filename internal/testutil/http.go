// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func PerformRequest(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body io.Reader,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func PerformJSONRequest(t *testing.T, handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	return PerformRequest(
		t,
		handler,
		method,
		path,
		bytes.NewReader(body),
		map[string]string{
			"Content-Type": "application/json",
		},
	)
}

func DecodeJSONResponse[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()

	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return out
}
