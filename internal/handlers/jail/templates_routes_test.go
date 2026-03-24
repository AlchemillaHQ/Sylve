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
	"io"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	"github.com/alchemillahq/sylve/internal/services/jail"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type mockJailTemplateService struct {
	listFn           func() ([]jailServiceInterfaces.SimpleTemplateList, error)
	getFn            func(templateID uint) (*jailModels.JailTemplate, error)
	canMutateFn      func(ctID uint) (bool, error)
	preflightConvert func(ctx context.Context, ctID uint, req jail.ConvertToTemplateRequest) error
	preflightCreate  func(ctx context.Context, templateID uint, req jail.CreateFromTemplateRequest) error
	deleteFn         func(ctx context.Context, templateID uint) error
}

func (m *mockJailTemplateService) GetJailTemplatesSimple() ([]jailServiceInterfaces.SimpleTemplateList, error) {
	if m.listFn == nil {
		return []jailServiceInterfaces.SimpleTemplateList{}, nil
	}
	return m.listFn()
}

func (m *mockJailTemplateService) GetJailTemplate(templateID uint) (*jailModels.JailTemplate, error) {
	if m.getFn == nil {
		return &jailModels.JailTemplate{ID: templateID, SourceJailName: "source-jail"}, nil
	}
	return m.getFn(templateID)
}

func (m *mockJailTemplateService) CanMutateProtectedJail(ctID uint) (bool, error) {
	if m.canMutateFn == nil {
		return true, nil
	}
	return m.canMutateFn(ctID)
}

func (m *mockJailTemplateService) PreflightConvertJailToTemplate(
	ctx context.Context,
	ctID uint,
	req jail.ConvertToTemplateRequest,
) error {
	if m.preflightConvert == nil {
		return nil
	}
	return m.preflightConvert(ctx, ctID, req)
}

func (m *mockJailTemplateService) PreflightCreateJailsFromTemplate(
	ctx context.Context,
	templateID uint,
	req jail.CreateFromTemplateRequest,
) error {
	if m.preflightCreate == nil {
		return nil
	}
	return m.preflightCreate(ctx, templateID, req)
}

func (m *mockJailTemplateService) DeleteJailTemplate(ctx context.Context, templateID uint) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, templateID)
}

func setupJailTemplateLifecycle(t *testing.T) *lifecycle.Service {
	t.Helper()

	dbConn := testutil.NewSQLiteTestDB(t, &taskModels.GuestLifecycleTask{})
	cfg := &internal.SylveConfig{
		Environment: internal.Development,
		DataPath:    t.TempDir(),
	}
	if err := db.SetupQueue(cfg, true, zerolog.New(io.Discard)); err != nil {
		t.Fatalf("failed to setup queue: %v", err)
	}

	return lifecycle.NewService(dbConn, nil, nil)
}

func decodeAPIResponse(t *testing.T, rrCode int, expected int, rrBody string) {
	t.Helper()
	if rrCode != expected {
		t.Fatalf("expected status %d, got %d body=%s", expected, rrCode, rrBody)
	}
}

func TestPreflightStatusCodeMapping(t *testing.T) {
	if got := preflightStatusCode(nil); got != http.StatusBadRequest {
		t.Fatalf("expected bad request for nil err, got %d", got)
	}
	if got := preflightStatusCode(assertErr("failed_to_get_jail")); got != http.StatusInternalServerError {
		t.Fatalf("expected internal server error, got %d", got)
	}
	if got := preflightStatusCode(assertErr("invalid_ctid")); got != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", got)
	}
}

func TestListJailTemplatesSimpleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.GET("/jail/templates/simple", ListJailTemplatesSimple(&mockJailTemplateService{
			listFn: func() ([]jailServiceInterfaces.SimpleTemplateList, error) {
				return []jailServiceInterfaces.SimpleTemplateList{
					{ID: 9, Name: "web", SourceJailName: "web-101"},
				}, nil
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates/simple", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusOK, rr.Body.String())

		resp := testutil.DecodeJSONResponse[internal.APIResponse[[]jailServiceInterfaces.SimpleTemplateList]](t, rr)
		if resp.Message != "jail_templates_listed_simple" || len(resp.Data) != 1 {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("failure", func(t *testing.T) {
		r := gin.New()
		r.GET("/jail/templates/simple", ListJailTemplatesSimple(&mockJailTemplateService{
			listFn: func() ([]jailServiceInterfaces.SimpleTemplateList, error) {
				return nil, assertErr("failed_to_fetch_jail_templates")
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates/simple", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestConvertJailTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/convert/101",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusAccepted, rr.Body.String())
	})

	t.Run("conflict", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		if _, _, err := lifecycleSvc.RequestAction(
			context.Background(),
			taskModels.GuestTypeJailTemplate,
			101,
			"convert",
			taskModels.LifecycleTaskSourceUser,
			"tester",
		); err != nil {
			t.Fatalf("failed to seed lifecycle task: %v", err)
		}

		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/convert/101",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("invalid ctid", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", ConvertJailToTemplate(&mockJailTemplateService{}, setupJailTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/convert/nope",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("invalid body", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", ConvertJailToTemplate(&mockJailTemplateService{}, setupJailTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/convert/101", []byte(`{"name":`))
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("lease denied", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{
				canMutateFn: func(_ uint) (bool, error) { return false, nil },
			}, setupJailTemplateLifecycle(t))(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/convert/101",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusForbidden, rr.Body.String())
	})

	t.Run("preflight bad request", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/templates/convert/:ctid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{
				preflightConvert: func(context.Context, uint, jail.ConvertToTemplateRequest) error {
					return assertErr("invalid_ctid")
				},
			}, setupJailTemplateLifecycle(t))(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/convert/101",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})
}

func TestCreateJailFromTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid body", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/create/:id", CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/create/1", []byte(`{"mode":`))
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("template not found", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/create/:id", CreateJailFromTemplate(&mockJailTemplateService{
			preflightCreate: func(context.Context, uint, jail.CreateFromTemplateRequest) error {
				return assertErr("template_not_found")
			},
		}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/create/1", []byte(`{"mode":"single","ctid":101}`))
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/create/1",
			[]byte(`{"mode":"single","ctid":101}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusAccepted, rr.Body.String())
	})

	t.Run("conflict", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		payload := `{"mode":"single","ctid":101}`
		if _, _, err := lifecycleSvc.RequestActionWithPayload(
			context.Background(),
			taskModels.GuestTypeJailTemplate,
			1,
			"create",
			taskModels.LifecycleTaskSourceUser,
			"tester",
			payload,
		); err != nil {
			t.Fatalf("failed to seed lifecycle task: %v", err)
		}

		r := gin.New()
		r.POST("/jail/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/create/1", []byte(payload))
		decodeAPIResponse(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("preflight internal", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{
				preflightCreate: func(context.Context, uint, jail.CreateFromTemplateRequest) error {
					return assertErr("failed_to_get_template_dataset")
				},
			}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/create/1", []byte(`{"mode":"single","ctid":101}`))
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestDeleteJailTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/jail/templates/:id", DeleteJailTemplate(&mockJailTemplateService{
			deleteFn: func(context.Context, uint) error { return assertErr("template_not_found") },
		}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/jail/templates/7", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/jail/templates/:id", DeleteJailTemplate(&mockJailTemplateService{}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/jail/templates/7", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusOK, rr.Body.String())
	})
}

func assertErr(msg string) error {
	return &mockError{msg: msg}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}
