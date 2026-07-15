// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHTTPGetJSONReadContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := HTTPGetJSONReadContext(ctx, "https://127.0.0.1:1", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestHTTPPostJSONReadReturnsErrorResponseBody(t *testing.T) {
	wantBody := []byte(`{"status":"error","message":"conflict","data":{"guestId":100}}`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write(wantBody)
	}))
	defer server.Close()

	body, status, err := HTTPPostJSONRead(server.URL, map[string]any{"request": true}, nil)
	if err == nil {
		t.Fatal("expected non-2xx HTTP error")
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if !bytes.Equal(body, wantBody) {
		t.Fatalf("body = %q, want %q", body, wantBody)
	}
}

func TestGetTokenFromHeader(t *testing.T) {
	tests := []struct {
		name       string
		headers    http.Header
		expected   string
		shouldFail bool
	}{
		{
			name: "Valid Authorization header",
			headers: http.Header{
				"Authorization": []string{"Bearer mytoken123"},
			},
			expected:   "mytoken123",
			shouldFail: false,
		},
		{
			name: "Authorization header with spaces",
			headers: http.Header{
				"Authorization": []string{"Bearer   my token "},
			},
			expected:   "mytoken",
			shouldFail: false,
		},
		{
			name: "Invalid Authorization (prefix)",
			headers: http.Header{
				"Authorization": []string{"Basic abc123"},
			},
			shouldFail: true,
		},
		{
			name: "Invalid Authorization (too short)",
			headers: http.Header{
				"Authorization": []string{"Bear"},
			},
			shouldFail: true,
		},
		{
			name: "Valid WebSocket header",
			headers: http.Header{
				"Sec-Websocket-Protocol": []string{"Bearer, mytoken123"},
			},
			expected:   "mytoken123",
			shouldFail: false,
		},
		{
			name: "Invalid WebSocket header",
			headers: http.Header{
				"Sec-WebSocket-Protocol": []string{"Token, mytoken123"},
			},
			shouldFail: true,
		},
		{
			name:       "No headers",
			headers:    http.Header{},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := GetTokenFromHeader(tt.headers)
			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error but got none (token: %s)", token)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if token != tt.expected {
					t.Errorf("expected token %q, got %q", tt.expected, token)
				}
			}
		})
	}
}

func TestGetIdFromParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("valid integer id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		c.Params = []gin.Param{{Key: "id", Value: "42"}}

		id, err := GetIdFromParam(c)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if id != 42 {
			t.Errorf("expected id 42, got %d", id)
		}
	})

	t.Run("invalid integer id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		c.Params = []gin.Param{{Key: "id", Value: "abc"}}

		id, err := GetIdFromParam(c)
		if err == nil {
			t.Error("expected error for invalid id, got nil")
		}
		if id != 0 {
			t.Errorf("expected id 0 on error, got %d", id)
		}
	})

	t.Run("missing id param", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		c.Params = []gin.Param{}

		id, err := GetIdFromParam(c)
		if err == nil {
			t.Error("expected error for missing id, got nil")
		}
		if id != 0 {
			t.Errorf("expected id 0 on error, got %d", id)
		}
	})
}
