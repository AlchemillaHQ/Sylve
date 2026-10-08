// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jailHandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alchemillahq/gzfs"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/gin-gonic/gin"
)

type jailCreationQueueStub struct {
	task                                         *taskModels.GuestLifecycleTask
	err                                          error
	guestType, action, source, username, payload string
	guestID                                      uint
}

func (s *jailCreationQueueStub) RequestActionWithPayload(_ context.Context, guestType string, guestID uint, action, source, username, payload string) (*taskModels.GuestLifecycleTask, string, error) {
	s.guestType, s.guestID, s.action, s.source, s.username, s.payload = guestType, guestID, action, source, username, payload
	return s.task, lifecycle.RequestOutcomeQueued, s.err
}

func TestAsyncCreateJailReturnsAcceptedTaskWithoutSynchronousExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &jailCoreHandlerStub{}
	queue := &jailCreationQueueStub{task: &taskModels.GuestLifecycleTask{ID: 23, GuestType: "jail", GuestID: 904, Action: "create", Status: "queued"}}
	router := gin.New()
	var auditJob any
	router.Use(func(c *gin.Context) { c.Set("Username", "tester"); c.Next(); auditJob, _ = c.Get("AuditAsyncJobID") })
	router.POST("/jail", CreateJail(service, queue))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/jail?async=true", strings.NewReader(`{"name":"copied-jail","ctId":904,"pool":"zroot","type":"freebsd","zfsSource":{"dataset":"zroot/foreign/root","guid":"123"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.createCalled {
		t.Fatal("handler executed synchronous creation")
	}
	if queue.guestType != "jail" || queue.guestID != 904 || queue.action != "create" || queue.username != "tester" {
		t.Fatalf("queue = %+v", queue)
	}
	var req jailServiceInterfaces.CreateJailRequest
	if err := json.Unmarshal([]byte(queue.payload), &req); err != nil {
		t.Fatal(err)
	}
	if req.ZFSSource == nil || req.ZFSSource.GUID != "123" {
		t.Fatalf("source lost: %+v", req)
	}
	if recorder.Header().Get("Location") != "/api/tasks/lifecycle/23" || auditJob != uint(23) {
		t.Fatalf("location/audit = %q/%v", recorder.Header().Get("Location"), auditJob)
	}
}

func TestAsyncJailCreationAdmissionFailuresAreNotAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const valid = `{"name":"queued-jail","ctId":904,"pool":"zroot","type":"freebsd"}`
	for _, tc := range []struct {
		name           string
		queue          jailCreationLifecycleService
		query, payload string
		status         int
	}{
		{"invalid async", &jailCreationQueueStub{}, "async=invalid", valid, 400},
		{"unavailable queue", nil, "async=true", valid, 503},
		{"missing ID", &jailCreationQueueStub{}, "async=true", `{}`, 400},
		{"queue failure", &jailCreationQueueStub{err: errors.New("queue_not_ready")}, "async=true", valid, 500},
		{"no task", &jailCreationQueueStub{}, "async=true", valid, 500},
		{"duplicate", &jailCreationQueueStub{task: &taskModels.GuestLifecycleTask{ID: 24, Action: "create"}, err: lifecycle.ErrTaskInProgress}, "async=true", valid, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &jailCoreHandlerStub{}
			router := gin.New()
			router.POST("/jail", CreateJail(service, tc.queue))
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/jail?"+tc.query, strings.NewReader(tc.payload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			if recorder.Code != tc.status || service.createCalled {
				t.Fatalf("status=%d body=%s synchronous=%v", recorder.Code, recorder.Body.String(), service.createCalled)
			}
		})
	}
}

func TestZFSSourceCreationErrorDetailsKeepAuthoritativeStatus(t *testing.T) {
	for _, tc := range []struct {
		code, detail string
		status       int
	}{
		{"zfs_source_layout_unsupported", "external_mount_supplies_bin/sh", 400},
		{"zfs_source_protected", "read_only_or_replication_protected", 409},
		{"jail_creation_in_progress", "source_is_not_ready", 409},
	} {
		status, code := classifyCreateJailError(errors.New(tc.code + ": " + tc.detail))
		if status != tc.status || code != tc.code {
			t.Fatalf("%s: status=%d code=%s", tc.code, status, code)
		}
	}
}

type jailZFSSourceHandlerStub struct {
	sources        []*gzfs.Dataset
	err            error
	validateCalled bool
}

func (s *jailZFSSourceHandlerStub) ListZFSSources(context.Context) ([]*gzfs.Dataset, error) {
	return s.sources, s.err
}

func (s *jailZFSSourceHandlerStub) ValidateCreate(context.Context, jailServiceInterfaces.CreateJailRequest) error {
	s.validateCalled = true
	return s.err
}

func TestJailCreationRejectsSnapshotFieldsBeforeValidationOrQueueing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/jail", "/jail?async=true", "/jail/validate"} {
		for _, field := range []string{`"snapshot":"selected"`, `"snapshot":""`, `"snapshot":null`, `"Snapshot":"selected"`, `"snapshotGuid":"123"`, `"snapshotGuid":""`, `"snapshotGuid":null`} {
			service := &jailCoreHandlerStub{}
			queue := &jailCreationQueueStub{}
			validator := &jailZFSSourceHandlerStub{}
			router := gin.New()
			router.POST("/jail", CreateJail(service, queue))
			router.POST("/jail/validate", ValidateCreateJail(validator))
			payload := `{"name":"copied-jail","ctId":904,"pool":"tank","type":"freebsd","zfsSource":{"dataset":"tank/foreign/root","guid":"123",` + field + `}}`
			request := httptest.NewRequest("POST", path, strings.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") || service.createCalled || validator.validateCalled || queue.payload != "" {
				t.Fatalf("%s %s: status=%d body=%s; create=%v validate=%v queue=%q", path, field, recorder.Code, recorder.Body.String(), service.createCalled, validator.validateCalled, queue.payload)
			}
		}
	}
}

func TestListJailZFSSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		sources []*gzfs.Dataset
		err     error
		status  int
	}{
		{"sources", []*gzfs.Dataset{{Name: "tank/foreign/root", GUID: "123"}}, nil, 200},
		{"empty", []*gzfs.Dataset{}, nil, 200},
		{"unavailable", nil, errors.New("zfs_client_not_initialized"), 503},
		{"failure", nil, errors.New("failed_to_inspect_zfs_source_mounts"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/jail/zfs-sources", ListJailZFSSources(&jailZFSSourceHandlerStub{sources: tc.sources, err: tc.err}))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest("GET", "/jail/zfs-sources", nil))
			if recorder.Code != tc.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if tc.err == nil {
				var response struct {
					Data []*gzfs.Dataset `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Data == nil || len(response.Data) != len(tc.sources) {
					t.Fatalf("source response=%s error=%v", recorder.Body.String(), err)
				}
				if len(tc.sources) != 0 && (response.Data[0].Name != tc.sources[0].Name || response.Data[0].GUID != tc.sources[0].GUID) {
					t.Fatal("source dataset identity lost")
				}
			}
		})
	}
}
