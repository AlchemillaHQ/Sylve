package iscsiHandlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestService(t *testing.T) *iscsi.Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&iscsiModels.ISCSIInitiator{},
	)
	return &iscsi.Service{DB: db}
}

func setupTestConfig(t *testing.T) {
	t.Helper()
	iscsi.SetConfigPath(t.TempDir() + "/iscsi.conf")
}

func enableMockExec() func() {
	return utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestMockExecHelper", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_MOCK_EXEC=1")
		return cmd
	})
}

func TestMockExecHelper(t *testing.T) {
	if os.Getenv("GO_MOCK_EXEC") != "1" {
		return
	}
	os.Exit(0)
}

func TestGetInitiatorsHandler(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "test-nick",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
	})

	router := gin.New()
	router.GET("/initiators", GetInitiators(svc))

	rr := testutil.PerformRequest(t, router, "GET", "/initiators", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[internal.APIResponse[[]iscsiModels.ISCSIInitiator]](t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s", resp.Status)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 initiator, got %d", len(resp.Data))
	}
}

func TestCreateInitiatorHandler(t *testing.T) {
	defer enableMockExec()()
	setupTestConfig(t)

	svc := newTestService(t)

	router := gin.New()
	router.POST("/initiators", CreateInitiator(svc))

	body, _ := json.Marshal(ISCSIInitiatorRequest{
		Nickname:      "test-nick",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	})

	rr := testutil.PerformJSONRequest(t, router, "POST", "/initiators", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s: %s", resp.Status, resp.Message)
	}
}

func TestCreateInitiatorHandlerValidationError(t *testing.T) {
	svc := newTestService(t)

	router := gin.New()
	router.POST("/initiators", CreateInitiator(svc))

	body, _ := json.Marshal(ISCSIInitiatorRequest{
		Nickname:   "",
		AuthMethod: "None",
	})

	rr := testutil.PerformJSONRequest(t, router, "POST", "/initiators", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUpdateInitiatorHandler(t *testing.T) {
	defer enableMockExec()()
	setupTestConfig(t)

	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "test-nick",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
	})

	router := gin.New()
	router.PUT("/initiators/:id", UpdateInitiator(svc))

	body, _ := json.Marshal(ISCSIInitiatorRequest{
		Nickname:      "test-nick-updated",
		TargetAddress: "10.0.0.2",
		TargetName:    "iqn.2025-01.com.example:target1",
		AuthMethod:    "None",
	})

	rr := testutil.PerformJSONRequest(t, router, "PUT", "/initiators/1", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateInitiatorHandlerNotFound(t *testing.T) {
	svc := newTestService(t)

	router := gin.New()
	router.PUT("/initiators/:id", UpdateInitiator(svc))

	body, _ := json.Marshal(ISCSIInitiatorRequest{
		Nickname:      "ghost",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "None",
	})

	rr := testutil.PerformJSONRequest(t, router, "PUT", "/initiators/999", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestDeleteInitiatorHandler(t *testing.T) {
	defer enableMockExec()()
	setupTestConfig(t)

	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "test-nick",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
	})

	router := gin.New()
	router.DELETE("/initiators/:id", DeleteInitiator(svc))

	rr := testutil.PerformRequest(t, router, "DELETE", "/initiators/1", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteInitiatorHandlerNotFound(t *testing.T) {
	svc := newTestService(t)

	router := gin.New()
	router.DELETE("/initiators/:id", DeleteInitiator(svc))

	rr := testutil.PerformRequest(t, router, "DELETE", "/initiators/999", nil, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestConnectInitiatorHandler(t *testing.T) {
	defer enableMockExec()()

	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "test-nick",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
	})

	router := gin.New()
	router.POST("/initiators/:id/connect", ConnectInitiator(svc))

	rr := testutil.PerformRequest(t, router, "POST", "/initiators/1/connect", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	resp := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, rr)
	if resp.Status != "success" {
		t.Fatalf("expected success, got %s: %s", resp.Status, resp.Message)
	}
}

func TestConnectInitiatorHandlerNotFound(t *testing.T) {
	svc := newTestService(t)

	router := gin.New()
	router.POST("/initiators/:id/connect", ConnectInitiator(svc))

	rr := testutil.PerformRequest(t, router, "POST", "/initiators/999/connect", nil, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for not found, got %d", rr.Code)
	}
}

func TestGetInitiatorsHandlerRedactsSecrets(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "secret-test",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
		AuthMethod:    "MutualCHAP",
		CHAPName:      "initiator-user",
		CHAPSecret:    "secretpassw0rd",
		TgtCHAPName:   "target-user",
		TgtCHAPSecret: "targetpassw0rd",
	})

	router := gin.New()
	router.GET("/initiators", GetInitiators(svc))
	rr := testutil.PerformRequest(t, router, http.MethodGet, "/initiators", nil, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	for _, secret := range []string{"secretpassw0rd", "targetpassw0rd", "chapSecret", "tgtChapSecret"} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Fatalf("response exposed %q: %s", secret, rr.Body.String())
		}
	}
}

func TestCreateInitiatorHandlerConflict(t *testing.T) {
	svc := newTestService(t)
	svc.DB.Create(&iscsiModels.ISCSIInitiator{
		Nickname:      "duplicate",
		TargetAddress: "10.0.0.1",
		TargetName:    "iqn.2025-01.com.example:target0",
	})

	router := gin.New()
	router.POST("/initiators", CreateInitiator(svc))
	body, _ := json.Marshal(ISCSIInitiatorRequest{
		Nickname:      "duplicate",
		TargetAddress: "10.0.0.2",
		TargetName:    "iqn.2025-01.com.example:target1",
		AuthMethod:    "None",
	})
	rr := testutil.PerformJSONRequest(t, router, http.MethodPost, "/initiators", body)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateInitiatorHandlerRejectsOversizedBody(t *testing.T) {
	svc := newTestService(t)
	router := gin.New()
	router.Use(middleware.LimitRequestBody(iscsi.MaxRequestBodyBytes))
	router.POST("/initiators", CreateInitiator(svc))
	body := []byte(`{"nickname":"` + strings.Repeat("x", int(iscsi.MaxRequestBodyBytes)) + `"}`)
	rr := testutil.PerformJSONRequest(t, router, http.MethodPost, "/initiators", body)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestConnectInitiatorHandlerInvalidID(t *testing.T) {
	svc := newTestService(t)

	router := gin.New()
	router.POST("/initiators/:id/connect", ConnectInitiator(svc))

	rr := testutil.PerformRequest(t, router, "POST", "/initiators/abc/connect", nil, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid ID, got %d", rr.Code)
	}
}

func TestDeleteInitiatorHandlerRejectsZeroID(t *testing.T) {
	svc := newTestService(t)
	router := gin.New()
	router.DELETE("/initiators/:id", DeleteInitiator(svc))

	rr := testutil.PerformRequest(t, router, http.MethodDelete, "/initiators/0", nil, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero ID, got %d: %s", rr.Code, rr.Body.String())
	}
}
