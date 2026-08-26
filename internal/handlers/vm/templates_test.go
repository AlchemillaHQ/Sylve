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
	"encoding/json"
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
	preflightConvert func(ctx context.Context, rid uint, req libvirtServiceInterfaces.ConvertToTemplateRequest) error
	preflightCreate  func(ctx context.Context, templateID uint, req libvirtServiceInterfaces.CreateFromTemplateRequest) error
	deleteFn         func(ctx context.Context, templateID uint) error
}

type mockVMTemplateLifecycleService struct {
	requestFn func(
		ctx context.Context,
		guestType string,
		guestID uint,
		action string,
		source string,
		requestedBy string,
		payload string,
	) (*taskModels.GuestLifecycleTask, string, error)
	listFn func(guestType string, guestID uint) ([]taskModels.GuestLifecycleTask, error)
}

func (m *mockVMTemplateLifecycleService) RequestActionWithPayload(
	ctx context.Context,
	guestType string,
	guestID uint,
	action string,
	source string,
	requestedBy string,
	payload string,
) (*taskModels.GuestLifecycleTask, string, error) {
	if m.requestFn == nil {
		return &taskModels.GuestLifecycleTask{ID: 1}, lifecycle.RequestOutcomeQueued, nil
	}
	return m.requestFn(ctx, guestType, guestID, action, source, requestedBy, payload)
}

func (m *mockVMTemplateLifecycleService) ListActiveTasks(
	guestType string,
	guestID uint,
) ([]taskModels.GuestLifecycleTask, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(guestType, guestID)
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

func (m *mockVMTemplateService) PreflightConvertVMToTemplate(
	ctx context.Context,
	rid uint,
	req libvirtServiceInterfaces.ConvertToTemplateRequest,
) error {
	if m.preflightConvert == nil {
		return nil
	}
	return m.preflightConvert(ctx, rid, req)
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

	return lifecycle.NewService(dbConn, nil, nil, nil)
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
	if got := vmTemplatePreflightStatusCode(errText("template_name_already_in_use")); got != http.StatusConflict {
		t.Fatalf("expected conflict for duplicate template name, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("vm_must_be_shut_off")); got != http.StatusConflict {
		t.Fatalf("expected conflict for VM state, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("template_not_found")); got != http.StatusNotFound {
		t.Fatalf("expected not found for missing template, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("rid_range_contains_used_values")); got != http.StatusConflict {
		t.Fatalf("expected conflict for occupied RID, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("guest_identity_inventory_conflict")); got != http.StatusConflict {
		t.Fatalf("expected conflict for dirty inventory, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("guest_identity_inventory_unavailable")); got != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable, got %d", got)
	}
	if got := vmTemplatePreflightStatusCode(errText("guest_identity_inventory_scan_failed")); got != http.StatusInternalServerError {
		t.Fatalf("expected internal server error for local scan failure, got %d", got)
	}
}

func TestListVMTemplatesSimpleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates", ListVMTemplatesSimple(&mockVMTemplateService{
			listFn: func() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
				return []libvirtServiceInterfaces.SimpleTemplateList{
					{ID: 2, Name: "web", SourceVMName: "web-150"},
				}, nil
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates", nil, nil)
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.String())
	})

	t.Run("failure", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates", ListVMTemplatesSimple(&mockVMTemplateService{
			listFn: func() ([]libvirtServiceInterfaces.SimpleTemplateList, error) {
				return nil, errText("failed_to_fetch_vm_templates")
			},
		}))

		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates", nil, nil)
		assertStatus(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestGetVMTemplateByIDHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid id", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/:templateId", GetVMTemplateByID(&mockVMTemplateService{}))
		rr := testutil.PerformRequest(t, r, http.MethodGet, "/vm/templates/nope", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.GET("/vm/templates/:templateId", GetVMTemplateByID(&mockVMTemplateService{
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
		r.POST("/vm/:rid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/200/templates",
			[]byte(`{"name":"web-template"}`),
		)
		assertStatus(t, rr.Code, http.StatusAccepted, rr.Body.String())
		var response internal.APIResponse[VMTemplateCaptureTaskResponse]
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Data.TaskID == 0 || response.Data.SourceRID != 200 ||
			response.Data.Action != "convert" || response.Data.Outcome != lifecycle.RequestOutcomeQueued {
			t.Fatalf("unexpected task response: %+v", response.Data)
		}
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
		r.POST("/vm/:rid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/200/templates",
			[]byte(`{"name":"web-template"}`),
		)
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("invalid rid", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/:rid/templates", ConvertVMToTemplate(&mockVMTemplateService{}, setupVMTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/nope/templates",
			[]byte(`{"name":"web-template"}`),
		)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("invalid body", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/:rid/templates", ConvertVMToTemplate(&mockVMTemplateService{}, setupVMTemplateLifecycle(t)))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/200/templates", []byte(`{"name":`))
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("preflight bad request", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/:rid/templates", func(c *gin.Context) {
			c.Set("Username", "tester")
			ConvertVMToTemplate(&mockVMTemplateService{
				preflightConvert: func(context.Context, uint, libvirtServiceInterfaces.ConvertToTemplateRequest) error {
					return errText("vm_must_be_shut_off")
				},
			}, setupVMTemplateLifecycle(t))(c)
		})
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/200/templates",
			[]byte(`{"name":"web-template"}`),
		)
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("missing lifecycle task", func(t *testing.T) {
		r := gin.New()
		r.POST("/vm/:rid/templates", ConvertVMToTemplate(
			&mockVMTemplateService{},
			&mockVMTemplateLifecycleService{requestFn: func(
				context.Context,
				string,
				uint,
				string,
				string,
				string,
				string,
			) (*taskModels.GuestLifecycleTask, string, error) {
				return nil, lifecycle.RequestOutcomeQueued, nil
			}},
		))
		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/200/templates",
			[]byte(`{"name":"web-template"}`),
		)
		assertStatus(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestCreateVMFromTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid body", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/:templateId/vms", CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc))
		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/templates/1/vms", []byte(`{"mode":`))
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("queued", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/:templateId/vms", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(
			t,
			r,
			http.MethodPost,
			"/vm/templates/1/vms",
			[]byte(`{"mode":"single","rid":201}`),
		)
		assertStatus(t, rr.Code, http.StatusAccepted, rr.Body.String())
		var response internal.APIResponse[VMTemplateInstantiationTaskResponse]
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Data.TaskID == 0 || response.Data.TemplateID != 1 ||
			response.Data.Action != "create" || response.Data.Outcome != lifecycle.RequestOutcomeQueued {
			t.Fatalf("unexpected task response: %+v", response.Data)
		}
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
		r.POST("/vm/templates/:templateId/vms", func(c *gin.Context) {
			c.Set("Username", "tester")
			CreateVMFromTemplate(&mockVMTemplateService{}, lifecycleSvc)(c)
		})

		rr := testutil.PerformJSONRequest(t, r, http.MethodPost, "/vm/templates/1/vms", []byte(payload))
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("preflight internal", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		r := gin.New()
		r.POST("/vm/templates/:templateId/vms", func(c *gin.Context) {
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
			"/vm/templates/1/vms",
			[]byte(`{"mode":"single","rid":201}`),
		)
		assertStatus(t, rr.Code, http.StatusInternalServerError, rr.Body.String())
	})
}

func TestDeleteVMTemplateHandlerMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid id", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:templateId", DeleteVMTemplate(&mockVMTemplateService{}, setupVMTemplateLifecycle(t)))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/invalid", nil, nil)
		assertStatus(t, rr.Code, http.StatusBadRequest, rr.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:templateId", DeleteVMTemplate(&mockVMTemplateService{
			deleteFn: func(context.Context, uint) error { return errText("template_not_found") },
		}, setupVMTemplateLifecycle(t)))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusNotFound, rr.Body.String())
	})

	t.Run("active creation conflict", func(t *testing.T) {
		lifecycleSvc := setupVMTemplateLifecycle(t)
		if _, _, err := lifecycleSvc.RequestActionWithPayload(
			context.Background(),
			taskModels.GuestTypeVMTemplate,
			8,
			"create",
			taskModels.LifecycleTaskSourceUser,
			"tester",
			`{"mode":"single","rid":208}`,
		); err != nil {
			t.Fatalf("seed lifecycle task: %v", err)
		}
		r := gin.New()
		r.DELETE("/vm/templates/:templateId", DeleteVMTemplate(&mockVMTemplateService{}, lifecycleSvc))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusConflict, rr.Body.String())
	})

	t.Run("same numeric conversion does not block deletion", func(t *testing.T) {
		deleted := false
		r := gin.New()
		r.DELETE("/vm/templates/:templateId", DeleteVMTemplate(
			&mockVMTemplateService{deleteFn: func(context.Context, uint) error {
				deleted = true
				return nil
			}},
			&mockVMTemplateLifecycleService{listFn: func(string, uint) ([]taskModels.GuestLifecycleTask, error) {
				return []taskModels.GuestLifecycleTask{{Action: "convert"}}, nil
			}},
		))
		rr := testutil.PerformRequest(t, r, http.MethodDelete, "/vm/templates/8", nil, nil)
		assertStatus(t, rr.Code, http.StatusOK, rr.Body.String())
		if !deleted {
			t.Fatal("expected template deletion to run")
		}
	})

	t.Run("success", func(t *testing.T) {
		r := gin.New()
		r.DELETE("/vm/templates/:templateId", DeleteVMTemplate(&mockVMTemplateService{}, setupVMTemplateLifecycle(t)))
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
