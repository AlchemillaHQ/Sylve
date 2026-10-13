// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfsHandlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	"github.com/alchemillahq/sylve/internal/db/models"
	zfsModels "github.com/alchemillahq/sylve/internal/db/models/zfs"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type scrubScheduleHandlerStub struct {
	input zfs.PoolScrubScheduleInput
	id    uint
	guid  string
	err   error
}

func (s *scrubScheduleHandlerStub) ListPoolScrubSchedules(context.Context) ([]zfs.PoolScrubScheduleView, error) {
	return []zfs.PoolScrubScheduleView{{PoolScrubSchedule: zfsModels.PoolScrubSchedule{ID: 1, PoolGUID: "101"}}}, s.err
}
func (s *scrubScheduleHandlerStub) CreatePoolScrubSchedule(_ context.Context, input zfs.PoolScrubScheduleInput) (*zfs.PoolScrubScheduleView, error) {
	s.input = input
	return &zfs.PoolScrubScheduleView{PoolScrubSchedule: zfsModels.PoolScrubSchedule{ID: 1, PoolGUID: input.PoolGUID, CronExpr: input.CronExpr, Enabled: input.Enabled}}, s.err
}
func (s *scrubScheduleHandlerStub) UpdatePoolScrubSchedule(ctx context.Context, id uint, input zfs.PoolScrubScheduleInput) (*zfs.PoolScrubScheduleView, error) {
	s.id = id
	return s.CreatePoolScrubSchedule(ctx, input)
}
func (s *scrubScheduleHandlerStub) DeletePoolScrubSchedule(_ context.Context, id uint) error {
	s.id = id
	return s.err
}
func (s *scrubScheduleHandlerStub) ListPoolScrubRuns(_ context.Context, guid string) ([]zfs.PoolScrubRunView, error) {
	s.guid = guid
	now := time.Now().UTC()
	return []zfs.PoolScrubRunView{{ID: 2, PoolGUID: guid, RunKey: "private", BaselineStart: &now}}, s.err
}

func scrubScheduleTestRouter(service *scrubScheduleHandlerStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/zfs/pools")
	group.GET("/:guid/status", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	group.GET("/:guid/scrub/runs", ListPoolScrubRuns(service))
	group.GET("/scrub/schedules", ListPoolScrubSchedules(service))
	group.POST("/scrub/schedules", CreatePoolScrubSchedule(service))
	group.PUT("/scrub/schedules/:id", UpdatePoolScrubSchedule(service))
	group.DELETE("/scrub/schedules/:id", DeletePoolScrubSchedule(service))
	return router
}

func TestPoolScrubScheduleHandlersAndStrictBodies(t *testing.T) {
	service := &scrubScheduleHandlerStub{}
	router := scrubScheduleTestRouter(service)
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/zfs/pools/scrub/schedules", "", 200},
		{"GET", "/zfs/pools/101/status", "", 204},
		{"GET", "/zfs/pools/101/scrub/runs", "", 200},
		{"POST", "/zfs/pools/scrub/schedules", `{"poolGuid":"101","cronExpr":"0 3 * * 0","enabled":false}`, 201},
		{"PUT", "/zfs/pools/scrub/schedules/7", `{"poolGuid":"101","cronExpr":"0 3 1 * *","enabled":true}`, 200},
		{"DELETE", "/zfs/pools/scrub/schedules/7", "", 200},
		{"DELETE", "/zfs/pools/scrub/schedules/0", "", 400},
		{"PUT", "/zfs/pools/scrub/schedules/nope", `{}`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `{}`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `null`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `{"poolGuid":101,"cronExpr":"0 3 * * 0"}`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `{"poolGuid":"101","cronExpr":"0 3 * * 0","extra":true}`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `{"poolGuid":"101","cronExpr":"0 3 * * 0"} {}`, 400},
		{"POST", "/zfs/pools/scrub/schedules", `{"poolGuid":"101","cronExpr":"` + strings.Repeat(" ", 8192) + `"}`, 413},
	} {
		request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s %s: got %d, want %d: %s", test.method, test.path, response.Code, test.status, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "baselineStart") || strings.Contains(response.Body.String(), "runKey") || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("internal fields exposed: %s", response.Body.String())
		}
	}
	if service.id != 7 || service.guid != "101" || service.input.PoolGUID != "101" {
		t.Fatalf("incorrect routing: %+v", service)
	}
}

func TestPoolScrubHandlerErrorStatuses(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{
		{zfs.ErrInvalidRequest, 400}, {zfs.ErrPoolNotFound, 404}, {gorm.ErrRecordNotFound, 404},
		{zfs.ErrConflict, 409}, {db.ErrPoolScrubActive, 409}, {db.ErrPoolScrubBusy, 409},
		{zfs.ErrScrubStatusUnavailable, 503}, {errors.New("database unavailable"), 500},
	} {
		router := scrubScheduleTestRouter(&scrubScheduleHandlerStub{err: test.err})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/zfs/pools/scrub/schedules", nil))
		if response.Code != test.status {
			t.Fatalf("%v: status %d, want %d", test.err, response.Code, test.status)
		}
	}
}

func TestPoolScrubScheduleAuthenticationAndAdminWrites(t *testing.T) {
	database := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{}, &models.Token{}, &models.SystemSecrets{})
	if err := database.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "scrub-handler-test-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	service := auth.NewAuthService(database).(*auth.Service)
	password := "$2a$04$q8RMvfpZU3MpIHropi1cfu0QobOdx.lw1SwiSLPC2ctgMMtPw6cc."
	users := []models.User{
		{Username: "user", Password: password},
		{Username: "admin", Admin: true, Password: password},
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	_, token, err := service.CreateJWT("admin", "correct horse battery staple", "sylve", false)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	group := router.Group("/zfs/pools/scrub/schedules", middleware.EnsureAuthenticated(service), middleware.RequireLocalAdminForWrites(service))
	stub := &scrubScheduleHandlerStub{}
	group.GET("", ListPoolScrubSchedules(stub))
	group.POST("", CreatePoolScrubSchedule(stub))
	for _, test := range []struct {
		method, token string
		status        int
	}{
		{"GET", "", 401}, {"POST", "", 401}, {"GET", token, 200}, {"POST", token, 201},
	} {
		request := httptest.NewRequest(test.method, "/zfs/pools/scrub/schedules", strings.NewReader(`{"poolGuid":"101","cronExpr":"0 3 * * 0","enabled":true}`))
		request.Header.Set("Content-Type", "application/json")
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s: %d, want %d: %s", test.method, response.Code, test.status, response.Body.String())
		}
	}
	// Login itself is admin-only. Exercise the write gate independently with an
	// authenticated non-admin identity (for example, a forwarded identity).
	nonAdmin := gin.New()
	nonAdmin.Use(func(c *gin.Context) {
		c.Set("AuthType", "sylve")
		c.Set("UserID", users[0].ID)
		c.Set("AuthScope", "local")
		c.Next()
	})
	nonAdmin.Use(middleware.RequireLocalAdminForWrites(service))
	nonAdmin.GET("/schedules", ListPoolScrubSchedules(stub))
	nonAdmin.POST("/schedules", CreatePoolScrubSchedule(stub))
	for _, test := range []struct {
		method string
		status int
	}{{"GET", 200}, {"POST", 403}} {
		response := httptest.NewRecorder()
		nonAdmin.ServeHTTP(response, httptest.NewRequest(test.method, "/schedules", nil))
		if response.Code != test.status {
			t.Fatalf("non-admin %s: %d, want %d", test.method, response.Code, test.status)
		}
	}
}
