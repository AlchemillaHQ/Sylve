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
	preflightConvert func(ctx context.Context, ctID uint, req jail.ConvertToTemplateRequest) error
	preflightCreate  func(ctx context.Context, templateID uint, req jail.CreateFromTemplateRequest) error
	deleteFn         func(ctx context.Context, templateID uint) error
}

type mockJailTemplateLifecycleService struct {
	requestFn func() (*taskModels.GuestLifecycleTask, string, error)
	listFn    func() ([]taskModels.GuestLifecycleTask, error)
}

func (m *mockJailTemplateLifecycleService) RequestActionWithPayload(
	context.Context,
	string,
	uint,
	string,
	string,
	string,
	string,
) (*taskModels.GuestLifecycleTask, string, error) {
	if m.requestFn == nil {
		return &taskModels.GuestLifecycleTask{ID: 1}, "queued", nil
	}
	return m.requestFn()
}

func (m *mockJailTemplateLifecycleService) ListActiveTasks(string, uint) ([]taskModels.GuestLifecycleTask, error) {
	if m.listFn == nil {
		return []taskModels.GuestLifecycleTask{}, nil
	}
	return m.listFn()
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

	return lifecycle.NewService(dbConn, nil, nil, nil)
}

func decodeAPIResponse(t *testing.T, rrCode int, expected int, rrBody string) {
	t.Helper()
	if rrCode != expected {
		t.Fatalf("expected status %d, got %d body=%s", expected, rrCode, rrBody)
	}
}

func TestJailTemplatePreflightStatusCodeMapping(t *testing.T) {
	if got := jailTemplatePreflightStatusCode(nil); got != http.StatusBadRequest {
		t.Fatalf("expected bad request for nil err, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("failed_to_get_jail")); got != http.StatusInternalServerError {
		t.Fatalf("expected internal server error, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("invalid_ctid")); got != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("ctid_range_contains_used_values")); got != http.StatusConflict {
		t.Fatalf("expected conflict for occupied CTID, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("guest_identity_inventory_conflict")); got != http.StatusConflict {
		t.Fatalf("expected conflict for dirty inventory, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("guest_identity_inventory_unavailable")); got != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("guest_identity_inventory_scan_failed")); got != http.StatusInternalServerError {
		t.Fatalf("expected internal server error for local scan failure, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("template_not_found")); got != http.StatusNotFound {
		t.Fatalf("expected not found for missing template, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("jail_must_be_stopped")); got != http.StatusConflict {
		t.Fatalf("expected conflict for running source jail, got %d", got)
	}
	if got := jailTemplatePreflightStatusCode(assertErr("replication_lease_not_owned")); got != http.StatusForbidden {
		t.Fatalf("expected forbidden for unowned lease, got %d", got)
	}
}

func TestListJailTemplatesSimpleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.GET("/jail/templates", ListJailTemplatesSimple(&mockJailTemplateService{
			listFn: func() ([]jailServiceInterfaces.SimpleTemplateList, error) {
				return []jailServiceInterfaces.SimpleTemplateList{
					{ID: 9, Name: "web", SourceJailName: "web-101"},
				}, nil
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusOK, rr.Body.String())

		resp := testutil.DecodeJSONResponse[internal.APIResponse[[]jailServiceInterfaces.SimpleTemplateList]](t, rr)
		if resp.Message != "jail_templates_listed_simple" || len(resp.Data) != 1 {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("failure", func(t *testing.T) {
		r := gin.New()
		r.GET("/jail/templates", ListJailTemplatesSimple(&mockJailTemplateService{
			listFn: func() ([]jailServiceInterfaces.SimpleTemplateList, error) {
				return nil, assertErr("failed_to_fetch_jail_templates")
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/jail/templates", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestConvertJailTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/:ctid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/101/templates",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusAccepted, rr.Body.String())
		resp := testutil.DecodeJSONResponse[internal.APIResponse[JailTemplateCaptureTaskResponse]](t, rr)
		if resp.Data.TaskID == 0 || resp.Data.SourceCTID != 101 || resp.Data.Action != "convert" {
			t.Fatalf("unexpected queued task response: %+v", resp.Data)
		}
	})

	t.Run("missing lifecycle task", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/:ctid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(
				&mockJailTemplateService{},
				&mockJailTemplateLifecycleService{
					requestFn: func() (*taskModels.GuestLifecycleTask, string, error) {
						return nil, "queued", nil
					},
				},
			)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/101/templates",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
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
		r.POST("/jail/:ctid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/101/templates",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("invalid ctid", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/:ctid/templates", ConvertJailToTemplate(&mockJailTemplateService{}, setupJailTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/nope/templates",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("invalid body", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/:ctid/templates", ConvertJailToTemplate(&mockJailTemplateService{}, setupJailTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/101/templates", []byte(`{"name":`))
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("lease denied", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/:ctid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertJailToTemplate(&mockJailTemplateService{
				preflightConvert: func(context.Context, uint, jail.ConvertToTemplateRequest) error {
					return assertErr("replication_lease_not_owned")
				},
			}, setupJailTemplateLifecycle(t))(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/101/templates",
			[]byte(`{"name":"jail-template"}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusForbidden, rr.Body.String())
	})

	t.Run("preflight bad request", func(t *testing.T) {
		r := gin.New()
		r.POST("/jail/:ctid/templates", func(c *gin.Context) {
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
			"/jail/101/templates",
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
		r.POST("/jail/templates/:templateId/jails", CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/1/jails", []byte(`{"mode":`))
		decodeAPIResponse(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("template not found", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/:templateId/jails", CreateJailFromTemplate(&mockJailTemplateService{
			preflightCreate: func(context.Context, uint, jail.CreateFromTemplateRequest) error {
				return assertErr("template_not_found")
			},
		}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/1/jails", []byte(`{"mode":"single","ctid":101}`))
		decodeAPIResponse(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/:templateId/jails", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/1/jails",
			[]byte(`{"mode":"single","ctid":101}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusAccepted, rr.Body.String())
		resp := testutil.DecodeJSONResponse[internal.APIResponse[JailTemplateInstantiationTaskResponse]](t, rr)
		if resp.Data.TaskID == 0 || resp.Data.TemplateID != 1 || resp.Data.Action != "create" {
			t.Fatalf("unexpected queued task response: %+v", resp.Data)
		}
	})

	t.Run("missing lifecycle task", func(t *testing.T) {
		r := gin.New()
		r.POST(
			"/jail/templates/:templateId/jails",
			CreateJailFromTemplate(
				&mockJailTemplateService{},
				&mockJailTemplateLifecycleService{
					requestFn: func() (*taskModels.GuestLifecycleTask, string, error) {
						return &taskModels.GuestLifecycleTask{}, "queued", nil
					},
				},
			),
		)
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/jail/templates/1/jails",
			[]byte(`{"mode":"single","ctid":101}`),
		)
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
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
		r.POST("/jail/templates/:templateId/jails", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/1/jails", []byte(payload))
		decodeAPIResponse(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("preflight internal", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		r := gin.New()
		r.POST("/jail/templates/:templateId/jails", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateJailFromTemplate(&mockJailTemplateService{
				preflightCreate: func(context.Context, uint, jail.CreateFromTemplateRequest) error {
					return assertErr("failed_to_get_template_dataset")
				},
			}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/jail/templates/1/jails", []byte(`{"mode":"single","ctid":101}`))
		decodeAPIResponse(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestDeleteJailTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/jail/templates/:templateId", DeleteJailTemplate(&mockJailTemplateService{
			deleteFn: func(context.Context, uint) error { return assertErr("template_not_found") },
		}, setupJailTemplateLifecycle(t)))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/jail/templates/7", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.DELETE(
			"/jail/templates/:templateId",
			DeleteJailTemplate(&mockJailTemplateService{}, setupJailTemplateLifecycle(t)),
		)
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/jail/templates/7", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusOK, rr.Body.String())
	})

	t.Run("active creation", func(t *testing.T) {
		lifecycleSvc := setupJailTemplateLifecycle(t)
		if _, _, err := lifecycleSvc.RequestActionWithPayload(
			context.Background(),
			taskModels.GuestTypeJailTemplate,
			7,
			"create",
			taskModels.LifecycleTaskSourceUser,
			"tester",
			`{"mode":"single","ctid":107}`,
		); err != nil {
			t.Fatalf("failed to seed lifecycle task: %v", err)
		}

		r := gin.New()
		r.DELETE(
			"/jail/templates/:templateId",
			DeleteJailTemplate(&mockJailTemplateService{}, lifecycleSvc),
		)
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/jail/templates/7", nil, nil)
		decodeAPIResponse(t, rr.Code, http.StatusConflict, rr.Body.String())
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
