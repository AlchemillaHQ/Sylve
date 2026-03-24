// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirtHandlers

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db"
	taskModels "github.com/alchemillahq/sylve/internal/db/models/task"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/internal/services/lifecycle"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type mockVMTemplateService struct {
	listFn           func() ([]libvirtServiceInterfaces.SimpleTemplateList, error)
	getFn            func(templateID uint) (*vmModels.VMTemplate, error)
	preflightConvert func(ctx context.Context, rid uint) error
	preflightCreate  func(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error
	deleteFn         func(ctx context.Context, templateID uint) error
}

func (m *mockVMTemplateService) GetVMTemplatesSimple() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
	if m.listFn == nil {
		return []libvirtServiceInterfaces.SimpleTemplateList{}, nil
	}
	return m.listFn()
}

func (m *mockVMTemplateService) GetVMTemplate(templateID uint) (*vmModels.VMTemplate, error) {
	if m.getFn == nil {
		return &vmModels.VMTemplate{ID: templateID, Name: "template"}, nil
	}
	return m.getFn(templateID)
}

func (m *mockVMTemplateService) PreflightConvertVMToTemplate(ctx context.Context, rid uint) error {
	if m.preflightConvert == nil {
		return nil
	}
	return m.preflightConvert(ctx, rid)
}

func (m *mockVMTemplateService) PreflightCreateVMsFromTemplate(
	ctx context.Context,
	templateID uint,
	req libvirtServiceInterfaces.CreateFromTemplateRequest,
) error {
	if m.preflightCreate == nil {
		return nil
	}
	return m.preflightCreate(ctx, templateID, req)
}

func (m *mockVMTemplateService) DeleteVMTemplate(ctx context.Context, templateID uint) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, templateID)
}

func setupVMTemplateLifecycle(t *testing.T) *lifecycle.Service {
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

func assertStatus(t *testing.T, actual, expected int, body string) {
	t.Helper()
	if actual != expected {
		t.Fatalf("expected status %d, got %d body=%s", expected, actual, body)
	}
}

func TestVMTemplatePreflightStatusCodeMapping(t *testing.T) {
	if got := vmTemplatePreflightStatusCode(errText("replication_lease_not_owned")); got != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("failed_to_get_vm")); got != http.StatusInternalServerError {
		t.Fatalf("expected internal server error, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("invalid_rid")); got != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d", got)
	}
}

func TestListVMTemplatesSimpleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/simple", ListVMTemplatesSimple(&mockVMTemplateService{
			listFn: func() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
				return []libvirtServiceInterfaces.SimpleTemplateList{
					{ID: 2, Name: "web", SourceRID: 150, SourceVMName: "web-150"},
				}, nil
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates/simple", nil, nil)
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.String())
	})

	t.Run("failure", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/simple", ListVMTemplatesSimple(&mockVMTemplateService{
			listFn: func() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
				return nil, errText("failed_to_fetch_vm_templates")
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates/simple", nil, nil)
		assertStatus(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestGetVMTemplateByIDHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid id", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/:id", GetVMTemplateByID(&mockVMTemplateService{}))
		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates/nope", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/:id", GetVMTemplateByID(&mockVMTemplateService{
			getFn: func(uint) (*vmModels.VMTemplate, error) {
				return nil, errText("template_not_found")
			},
		}))
		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates/9", nil, nil)
		assertStatus(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})
}

func TestConvertVMTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/convert/:rid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/templates/convert/200", nil, nil)
		assertStatus(t, rr.Code, http.StatusAccepted, rr.Body.String())
	})

	t.Run("conflict", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		if _, _, err := lifecycleSvc.RequestAction(
			context.Background(),
			taskModels.GuestTypeVMTemplate,
			200,
			"convert",
			taskModels.LifecycleTaskSourceUser,
			"tester",
		); err != nil {
			t.Fatalf("failed to seed lifecycle task: %v", err)
		}

		r := gin.New()
		r.POST("/vm/templates/convert/:rid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/templates/convert/200", nil, nil)
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("invalid rid", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/templates/convert/:rid", ConvertVMToTemplate(&mockVMTemplateService{}, setupVMTemplateLifecycle(t)))
		rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/templates/convert/nope", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("preflight bad request", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/templates/convert/:rid", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{
				preflightConvert: func(context.Context, uint) error {
					return errText("vm_must_be_shut_off")
				},
			}, setupVMTemplateLifecycle(t))(c)
		})
		rr := testutil.PerformRequest(t, r, http.MethodPost, "/vm/templates/convert/200", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})
}

func TestCreateVMFromTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid body", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/create/:id", CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/templates/create/1", []byte(`{"mode":`))
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/templates/create/1",
			[]byte(`{"mode":"single","rid":201}`),
		)
		assertStatus(t, rr.Code, http.StatusAccepted, rr.Body.String())
	})

	t.Run("conflict", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		payload := `{"mode":"single","rid":201}`
		if _, _, err := lifecycleSvc.RequestActionWithPayload(
			context.Background(),
			taskModels.GuestTypeVMTemplate,
			1,
			"create",
			taskModels.LifecycleTaskSourceUser,
			"tester",
			payload,
		); err != nil {
			t.Fatalf("failed to seed lifecycle task: %v", err)
		}

		r := gin.New()
		r.POST("/vm/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/templates/create/1", []byte(payload))
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("preflight internal", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/create/:id", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateVMFromTemplate(&mockVMTemplateService{
				preflightCreate: func(context.Context, uint, libvirtServiceInterfaces.CreateFromTemplateRequest) error {
					return errText("failed_to_get_template_storage_dataset")
				},
			}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/templates/create/1",
			[]byte(`{"mode":"single","rid":201}`),
		)
		assertStatus(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestDeleteVMTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid id", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:id", DeleteVMTemplate(&mockVMTemplateService{}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/invalid", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:id", DeleteVMTemplate(&mockVMTemplateService{
			deleteFn: func(context.Context, uint) error { return errText("template_not_found") },
		}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})

	t.Run("forbidden", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:id", DeleteVMTemplate(&mockVMTemplateService{
			deleteFn: func(context.Context, uint) error { return errText("replication_lease_not_owned") },
		}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusForbidden, rr.Body.String())
	})

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:id", DeleteVMTemplate(&mockVMTemplateService{}))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.String())
	})
}

type textErr struct {
	msg string
}

func (e textErr) Error() string {
	return e.msg
}

func errText(msg string) error {
	return textErr{msg: msg}
}
