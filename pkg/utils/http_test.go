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
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusConflict || !bytes.Equal(statusErr.Body, wantBody) {
		t.Fatalf("error = %#v, want typed conflict with preserved body", err)
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if !bytes.Equal(body, wantBody) {
		t.Fatalf("body = %q, want %q", body, wantBody)
	}
}

func TestHTTPPostJSONReadContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := HTTPPostJSONReadContext(ctx, "https://127.0.0.1:1", map[string]any{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestHTTPRequestReadContextPreservesCompletedResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		gzip        bool
	}{
		{name: "ok json", status: http.StatusOK, contentType: "application/json", body: `{"status":"success"}`},
		{name: "bad request json", status: http.StatusBadRequest, contentType: "application/json", body: `{"status":"error"}`},
		{name: "conflict text", status: http.StatusConflict, contentType: "text/plain; charset=utf-8", body: "already running"},
		{name: "unavailable gzip json", status: http.StatusServiceUnavailable, contentType: "application/problem+json", body: `{"detail":"offline"}`, gzip: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("X-Request-ID", "remote-request")
				w.Header().Set("Connection", "X-Private-Hop")
				w.Header().Set("X-Private-Hop", "secret")
				w.Header().Set("Keep-Alive", "timeout=5")
				if test.gzip {
					w.Header().Set("Content-Encoding", "gzip")
				}
				w.WriteHeader(test.status)
				if test.gzip {
					writer := gzip.NewWriter(w)
					_, _ = writer.Write([]byte(test.body))
					_ = writer.Close()
					return
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			response, err := HTTPRequestReadContext(
				context.Background(),
				http.MethodGet,
				server.URL,
				nil,
				nil,
				time.Second,
				1024,
			)
			if err != nil {
				t.Fatalf("HTTPRequestReadContext: %v", err)
			}
			if response.StatusCode != test.status ||
				response.Header.Get("Content-Type") != test.contentType ||
				string(response.Body) != test.body {
				t.Fatalf("response = %+v body=%q", response, response.Body)
			}
			if response.Header.Get("X-Request-ID") != "remote-request" {
				t.Fatalf("safe response header was lost: %v", response.Header)
			}
			if response.Header.Get("Content-Encoding") != "" {
				t.Fatalf("decoded content encoding leaked: %v", response.Header)
			}
			for _, header := range []string{"Connection", "Keep-Alive", "X-Private-Hop", "Transfer-Encoding"} {
				if value := response.Header.Get(header); value != "" {
					t.Fatalf("hop-by-hop header %s leaked as %q", header, value)
				}
			}
		})
	}
}

func TestHTTPRequestReadContextTimeoutAndCallerCancellation(t *testing.T) {
	t.Run("completes below budget", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		if _, err := HTTPRequestReadContext(
			context.Background(),
			http.MethodGet,
			server.URL,
			nil,
			nil,
			250*time.Millisecond,
			1024,
		); err != nil {
			t.Fatalf("request below budget failed: %v", err)
		}
	})

	t.Run("times out above budget", func(t *testing.T) {
		downstreamCanceled := make(chan struct{})
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(downstreamCanceled)
		}))
		defer server.Close()

		_, err := HTTPRequestReadContext(
			context.Background(),
			http.MethodGet,
			server.URL,
			nil,
			nil,
			100*time.Millisecond,
			1024,
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
		select {
		case <-downstreamCanceled:
		case <-time.After(time.Second):
			t.Fatal("timeout did not cancel the downstream request")
		}
	})

	t.Run("earlier caller deadline wins", func(t *testing.T) {
		downstreamCanceled := make(chan struct{})
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(downstreamCanceled)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_, err := HTTPRequestReadContext(
			ctx,
			http.MethodGet,
			server.URL,
			nil,
			nil,
			time.Second,
			1024,
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want caller deadline", err)
		}
		select {
		case <-downstreamCanceled:
		case <-time.After(time.Second):
			t.Fatal("caller deadline did not cancel downstream")
		}
	})

	t.Run("caller cancellation reaches downstream", func(t *testing.T) {
		started := make(chan struct{})
		downstreamCanceled := make(chan struct{})
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(downstreamCanceled)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, err := HTTPRequestReadContext(
				ctx,
				http.MethodGet,
				server.URL,
				nil,
				nil,
				time.Second,
				1024,
			)
			result <- err
		}()
		<-started
		cancel()

		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
		select {
		case <-downstreamCanceled:
		case <-time.After(time.Second):
			t.Fatal("caller cancellation did not reach downstream")
		}
	})
}

func TestHTTPRequestReadContextCapsResponseBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("response-too-large"))
	}))
	defer server.Close()

	_, err := HTTPRequestReadContext(
		context.Background(),
		http.MethodGet,
		server.URL,
		nil,
		nil,
		time.Second,
		8,
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds 8 bytes") {
		t.Fatalf("error = %v, want response-size failure", err)
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
