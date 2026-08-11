// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/gin-gonic/gin"
)

func newClusterForwardTestContext(
	method string,
	path string,
	body string,
	headers map[string]string,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("UserID", uint(7))
	c.Set("Username", "forward-user")
	c.Set("AuthType", "sylve")
	c.Set("AuthScope", "local")
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	return c, recorder
}

func TestForwardToLeaderPreservesCompletedApplicationResponses(t *testing.T) {
	tests := []struct {
		status      int
		contentType string
		body        string
	}{
		{status: http.StatusOK, contentType: "application/json", body: `{"status":"success"}`},
		{status: http.StatusBadRequest, contentType: "application/json", body: `{"message":"invalid"}`},
		{status: http.StatusConflict, contentType: "text/plain; charset=utf-8", body: "already running"},
		{status: http.StatusServiceUnavailable, contentType: "application/problem+json", body: `{"detail":"leader busy"}`},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(r.URL.Query().Get("status"))
		if err != nil {
			t.Errorf("invalid test status: %v", err)
			status = http.StatusInternalServerError
		}
		index, err := strconv.Atoi(r.URL.Query().Get("case"))
		if err != nil || index < 0 || index >= len(tests) {
			t.Errorf("invalid test case: %q", r.URL.Query().Get("case"))
			index = 0
		}
		if r.Header.Get(clusterForwardHopHeader) != "1" {
			t.Errorf("forward hop = %q, want 1", r.Header.Get(clusterForwardHopHeader))
		}
		if r.Header.Get("Idempotency-Key") != "operation-1" {
			t.Errorf("idempotency key was not forwarded")
		}
		w.Header().Set("Content-Type", tests[index].contentType)
		w.Header().Set("X-Request-ID", "leader-request")
		w.Header().Set("X-Correlation-ID", "leader-correlation")
		w.Header().Set("Connection", "X-Private-Hop")
		w.Header().Set("X-Private-Hop", "secret")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(tests[index].body))
	}))
	defer server.Close()

	originalResolver := resolveLeaderAPIForForward
	resolveLeaderAPIForForward = func(*cluster.Service, string, string) string { return server.URL }
	t.Cleanup(func() { resolveLeaderAPIForForward = originalResolver })

	raftNode := setupSingleRaftForTest(t, "leader-forward-response")
	defer func() { _ = raftNode.Shutdown().Error() }()
	service := &cluster.Service{
		DB:          newClusterHandlerTestDB(t),
		Raft:        raftNode,
		AuthService: authForwardStub{},
	}

	for index, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			path := fmt.Sprintf("/api/cluster/test?status=%d&case=%d", test.status, index)
			c, recorder := newClusterForwardTestContext(
				http.MethodPost,
				path,
				`{"request":true}`,
				map[string]string{
					"Idempotency-Key": "operation-1",
					"X-Request-ID":    "ingress-request",
				},
			)

			forwardToLeader(c, service)

			if recorder.Code != test.status || recorder.Body.String() != test.body {
				t.Fatalf("response = %d %q, want %d %q", recorder.Code, recorder.Body.String(), test.status, test.body)
			}
			if recorder.Header().Get("Content-Type") != test.contentType {
				t.Fatalf("content type = %q, want %q", recorder.Header().Get("Content-Type"), test.contentType)
			}
			if recorder.Header().Get("X-Request-ID") != "leader-request" ||
				recorder.Header().Get("X-Correlation-ID") != "leader-correlation" {
				t.Fatalf("safe response headers were not preserved: %v", recorder.Header())
			}
			if recorder.Header().Get("X-Private-Hop") != "" || recorder.Header().Get("Connection") != "" {
				t.Fatalf("hop-by-hop response headers leaked: %v", recorder.Header())
			}
		})
	}
}

func TestClusterForwardTimeoutClassesAndOneHopLimit(t *testing.T) {
	if clusterForwardTimeout(clusterForwardShortRead) != 15*time.Second ||
		clusterForwardTimeout(clusterForwardValidation) != 70*time.Second ||
		clusterForwardTimeout(clusterForwardDurable) != 90*time.Second {
		t.Fatalf("unexpected forwarding timeout classes")
	}

	classTests := []struct {
		method string
		path   string
		want   clusterForwardTimeoutClass
	}{
		{method: http.MethodGet, path: "/api/cluster/notes", want: clusterForwardShortRead},
		{method: http.MethodPost, path: "/api/cluster/backups/targets/validate/1", want: clusterForwardValidation},
		{method: http.MethodPost, path: "/api/intra-cluster/backup-target-validation", want: clusterForwardValidation},
		{method: http.MethodPost, path: "/api/cluster/backups/jobs/run/1", want: clusterForwardDurable},
	}
	for _, test := range classTests {
		request := httptest.NewRequest(test.method, test.path, nil)
		if got := clusterForwardClassForRequest(request); got != test.want {
			t.Fatalf("%s %s class = %d, want %d", test.method, test.path, got, test.want)
		}
	}

	originalForward := clusterForwardHTTP
	calls := 0
	clusterForwardHTTP = func(
		context.Context,
		string,
		string,
		[]byte,
		map[string]string,
		time.Duration,
	) (clusterForwardResponse, error) {
		calls++
		return clusterForwardResponse{}, nil
	}
	t.Cleanup(func() { clusterForwardHTTP = originalForward })

	c, _ := newClusterForwardTestContext(
		http.MethodPost,
		"/api/cluster/test",
		`{}`,
		map[string]string{clusterForwardHopHeader: "1"},
	)
	c.Set("AuthScope", "cluster")
	c.Set("Token", "validated-user-proxy")
	c.Set("ClusterTokenUse", authService.ClusterTokenUseUserProxy)
	c.Set("ProxyAdmin", true)
	_, err := performClusterForward(
		c,
		&cluster.Service{AuthService: authForwardStub{}},
		http.MethodPost,
		"https://leader/api/cluster/test",
		[]byte(`{}`),
		clusterForwardDurable,
	)
	if err == nil || !strings.Contains(err.Error(), "forward_loop") || calls != 0 {
		t.Fatalf("second-hop result: calls=%d err=%v", calls, err)
	}
}

func TestDirectClusterForwardingPathsPreserveResponsesAndBudgets(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})
	if err := db.Create(&clusterModels.ClusterNode{
		NodeUUID: "remote-node",
		API:      "remote.example:8184",
	}).Error; err != nil {
		t.Fatalf("seed remote node: %v", err)
	}
	service := &cluster.Service{DB: db, AuthService: authForwardStub{}}

	type capturedCall struct {
		method  string
		url     string
		body    []byte
		headers map[string]string
		timeout time.Duration
	}
	var calls []capturedCall
	originalForward := clusterForwardHTTP
	clusterForwardHTTP = func(
		_ context.Context,
		method string,
		targetURL string,
		body []byte,
		headers map[string]string,
		timeout time.Duration,
	) (clusterForwardResponse, error) {
		headersCopy := make(map[string]string, len(headers))
		for key, value := range headers {
			headersCopy[key] = value
		}
		calls = append(calls, capturedCall{
			method: method, url: targetURL, body: append([]byte(nil), body...),
			headers: headersCopy, timeout: timeout,
		})

		switch {
		case strings.Contains(targetURL, "/replication/policies/"):
			return clusterForwardResponse{
				StatusCode: http.StatusConflict,
				Header: http.Header{
					"Content-Type":     []string{"application/json"},
					"X-Correlation-Id": []string{"replication-correlation"},
				},
				Body: []byte(`{"message":"already running"}`),
			}, nil
		case strings.Contains(targetURL, "/replication/events"):
			return clusterForwardResponse{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
				Body:       []byte("events unavailable"),
			}, nil
		case strings.Contains(targetURL, "/backups/events"):
			return clusterForwardResponse{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       []byte(`{"message":"event not found"}`),
			}, nil
		default:
			return clusterForwardResponse{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/problem+json"}},
				Body:       []byte(`{"detail":"invalid restore"}`),
			}, nil
		}
	}
	t.Cleanup(func() { clusterForwardHTTP = originalForward })

	t.Run("replication run", func(t *testing.T) {
		c, recorder := newClusterForwardTestContext(http.MethodPost, "/run", `{}`, nil)
		response, err := forwardReplicationRunToNode(c, service, 42, "remote-node")
		if err != nil {
			t.Fatalf("forward replication run: %v", err)
		}
		writeClusterForwardResponse(c, response)
		if recorder.Code != http.StatusConflict ||
			recorder.Body.String() != `{"message":"already running"}` ||
			recorder.Header().Get("X-Correlation-ID") != "replication-correlation" {
			t.Fatalf("replication response = %d %s headers=%v", recorder.Code, recorder.Body.String(), recorder.Header())
		}
	})

	t.Run("event get", func(t *testing.T) {
		c, recorder := newClusterForwardTestContext(
			http.MethodGet,
			"/events?nodeId=remote-node&limit=25",
			"",
			nil,
		)
		response, err := forwardReplicationEventsRequestToNode(
			c,
			service,
			"remote-node",
			"/api/cluster/replication/events",
		)
		if err != nil {
			t.Fatalf("forward events: %v", err)
		}
		writeClusterForwardResponse(c, response)
		if recorder.Code != http.StatusServiceUnavailable ||
			recorder.Body.String() != "events unavailable" ||
			recorder.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("event response = %d %q headers=%v", recorder.Code, recorder.Body.String(), recorder.Header())
		}
	})

	t.Run("backup event get", func(t *testing.T) {
		c, recorder := newClusterForwardTestContext(
			http.MethodGet,
			"/events?nodeId=remote-node&jobId=7",
			"",
			nil,
		)
		response, err := forwardBackupEventsRequestToNode(
			c,
			service,
			"remote-node",
			"/api/cluster/backups/events",
		)
		if err != nil {
			t.Fatalf("forward backup events: %v", err)
		}
		writeClusterForwardResponse(c, response)
		if recorder.Code != http.StatusNotFound ||
			recorder.Body.String() != `{"message":"event not found"}` {
			t.Fatalf("backup event response = %d %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("oob restore", func(t *testing.T) {
		c, recorder := newClusterForwardTestContext(
			http.MethodPost,
			"/restore",
			`{}`,
			map[string]string{"Idempotency-Key": "restore-operation-1"},
		)
		response, err := forwardBackupTargetRestoreToNode(
			c,
			service,
			9,
			"remote-node",
			map[string]any{"operationId": "restore-operation-1"},
		)
		if err != nil {
			t.Fatalf("forward restore: %v", err)
		}
		writeClusterForwardResponse(c, response)
		if recorder.Code != http.StatusBadRequest ||
			recorder.Body.String() != `{"detail":"invalid restore"}` ||
			recorder.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("restore response = %d %s headers=%v", recorder.Code, recorder.Body.String(), recorder.Header())
		}
	})

	if len(calls) != 4 {
		t.Fatalf("forward calls = %d, want 4", len(calls))
	}
	if calls[0].method != http.MethodPost || calls[0].timeout != clusterForwardDurableTimeout {
		t.Fatalf("replication call = %+v", calls[0])
	}
	if calls[1].method != http.MethodGet || calls[1].timeout != clusterForwardShortReadTimeout ||
		strings.Contains(calls[1].url, "nodeId=") || !strings.Contains(calls[1].url, "limit=25") {
		t.Fatalf("event call = %+v", calls[1])
	}
	if calls[2].method != http.MethodGet || calls[2].timeout != clusterForwardShortReadTimeout ||
		strings.Contains(calls[2].url, "nodeId=") || !strings.Contains(calls[2].url, "jobId=7") {
		t.Fatalf("backup event call = %+v", calls[2])
	}
	if calls[3].timeout != clusterForwardDurableTimeout ||
		calls[3].headers["Idempotency-Key"] != "restore-operation-1" ||
		!bytes.Contains(calls[3].body, []byte(`"operationId":"restore-operation-1"`)) {
		t.Fatalf("restore call = %+v body=%s", calls[3], calls[3].body)
	}
	for _, call := range calls {
		if call.headers[clusterForwardHopHeader] != "1" {
			t.Fatalf("forward hop missing from %+v", call)
		}
	}
}

func TestDurableForwardTimeoutDoesNotRetryAndKeepsOperationID(t *testing.T) {
	db := newClusterHandlerTestDB(t, &clusterModels.ClusterNode{})
	if err := db.Create(&clusterModels.ClusterNode{
		NodeUUID: "restore-node",
		API:      "restore.example:8184",
	}).Error; err != nil {
		t.Fatalf("seed restore node: %v", err)
	}
	service := &cluster.Service{DB: db, AuthService: authForwardStub{}}

	originalForward := clusterForwardHTTP
	requests := 0
	logicalOperations := make(map[string]struct{})
	clusterForwardHTTP = func(
		_ context.Context,
		_ string,
		_ string,
		body []byte,
		_ map[string]string,
		timeout time.Duration,
	) (clusterForwardResponse, error) {
		requests++
		if timeout != clusterForwardDurableTimeout {
			t.Fatalf("durable timeout = %s", timeout)
		}
		var payload struct {
			OperationID string `json:"operationId"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode forwarded payload: %v", err)
		}
		logicalOperations[payload.OperationID] = struct{}{}
		if requests == 1 {
			return clusterForwardResponse{}, context.DeadlineExceeded
		}
		return clusterForwardResponse{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"status":"accepted"}`),
		}, nil
	}
	t.Cleanup(func() { clusterForwardHTTP = originalForward })

	for attempt := 0; attempt < 2; attempt++ {
		c, recorder := newClusterForwardTestContext(
			http.MethodPost,
			"/restore",
			`{}`,
			map[string]string{"Idempotency-Key": "stable-restore-operation"},
		)
		response, err := forwardBackupTargetRestoreToNode(
			c,
			service,
			11,
			"restore-node",
			map[string]any{"operationId": "stable-restore-operation"},
		)
		if err != nil {
			writeClusterForwardError(c, "restore_remote_node_forward_failed", err)
		} else {
			writeClusterForwardResponse(c, response)
		}

		wantStatus := http.StatusGatewayTimeout
		if attempt == 1 {
			wantStatus = http.StatusAccepted
		}
		if recorder.Code != wantStatus {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, recorder.Code, recorder.Body.String())
		}
		if requests != attempt+1 {
			t.Fatalf("attempt %d made %d downstream requests", attempt+1, requests)
		}
	}

	if len(logicalOperations) != 1 {
		t.Fatalf("logical operation IDs = %v, want one stable ID", logicalOperations)
	}
}

func TestClusterForwardErrorClassification(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "transport", err: errors.New("connection refused"), wantStatus: http.StatusBadGateway},
		{name: "timeout", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout},
		{name: "canceled", err: context.Canceled, wantStatus: http.StatusBadGateway},
		{name: "loop", err: errors.New("cluster_forward_loop_detected"), wantStatus: http.StatusLoopDetected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := newClusterForwardTestContext(http.MethodPost, "/test", `{}`, nil)
			writeClusterForwardError(c, "forward_failed", test.err)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), test.wantStatus)
			}
		})
	}
}

func TestReplicationRunForwardRejectsLoopsAndUnavailableOwnerCreatesNoOperation(t *testing.T) {
	database := newClusterHandlerTestDB(t,
		&clusterModels.ClusterNode{},
		&clusterModels.ReplicationRunOperation{},
	)
	if err := database.Create(&clusterModels.ClusterNode{
		NodeUUID: "offline-owner", API: "offline-owner.example:8184",
	}).Error; err != nil {
		t.Fatalf("seed owner node: %v", err)
	}
	service := &cluster.Service{DB: database, AuthService: authForwardStub{}}

	originalForward := clusterForwardHTTP
	forwardCalls := 0
	clusterForwardHTTP = func(
		_ context.Context,
		_ string,
		_ string,
		_ []byte,
		_ map[string]string,
		timeout time.Duration,
	) (clusterForwardResponse, error) {
		forwardCalls++
		if timeout != clusterForwardDurableTimeout {
			t.Fatalf("replication forward timeout=%s, want %s", timeout, clusterForwardDurableTimeout)
		}
		return clusterForwardResponse{}, errors.New("connection refused")
	}
	t.Cleanup(func() { clusterForwardHTTP = originalForward })

	c, recorder := newClusterForwardTestContext(http.MethodPost, "/run", `{}`, nil)
	response, err := forwardReplicationRunToNode(c, service, 901, "offline-owner")
	if err == nil {
		writeClusterForwardResponse(c, response)
	} else {
		writeClusterForwardError(c, "replication_run_remote_forward_failed", err)
	}
	if recorder.Code != http.StatusBadGateway || forwardCalls != 1 {
		t.Fatalf("unavailable owner status=%d calls=%d body=%s", recorder.Code, forwardCalls, recorder.Body.String())
	}

	loopContext, _ := newClusterForwardTestContext(
		http.MethodPost,
		"/run",
		`{}`,
		map[string]string{clusterForwardHopHeader: "1"},
	)
	loopContext.Set("AuthScope", "cluster")
	loopContext.Set("Token", "validated-user-proxy")
	loopContext.Set("ClusterTokenUse", authService.ClusterTokenUseUserProxy)
	loopContext.Set("ProxyAdmin", true)
	_, err = forwardReplicationRunToNode(loopContext, service, 901, "offline-owner")
	if err == nil || !strings.Contains(err.Error(), "forward_loop") || forwardCalls != 1 {
		t.Fatalf("loop result: calls=%d err=%v", forwardCalls, err)
	}

	var operationCount int64
	if err := database.Model(&clusterModels.ReplicationRunOperation{}).Count(&operationCount).Error; err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if operationCount != 0 {
		t.Fatalf("unavailable/looped owner created %d operations", operationCount)
	}
}
