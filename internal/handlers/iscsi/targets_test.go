// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsiHandlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	iscsiModels "github.com/alchemillahq/sylve/internal/db/models/iscsi"
	"github.com/alchemillahq/sylve/internal/services/iscsi"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/gin-gonic/gin"
)

func newTargetHandlerTestService(t *testing.T) *iscsi.Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t,
		&iscsiModels.ISCSITarget{},
		&iscsiModels.ISCSITargetPortal{},
		&iscsiModels.ISCSITargetLUN{},
	)
	return &iscsi.Service{DB: db}
}

func setupTargetTestConfig(t *testing.T) {
	t.Helper()
	iscsi.SetTargetConfigPath(t.TempDir() + "/ctl.conf")
}

func createTargetFixture(t *testing.T, svc *iscsi.Service, name string) iscsiModels.ISCSITarget {
	t.Helper()
	target := iscsiModels.ISCSITarget{TargetName: name, AuthMethod: "None"}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target fixture: %v", err)
	}
	return target
}

func TestGetTargetsHandlerRedactsSecrets(t *testing.T) {
	svc := newTargetHandlerTestService(t)
	target := iscsiModels.ISCSITarget{
		TargetName:       "iqn.2025-01.com.example:secret",
		AuthMethod:       "MutualCHAP",
		CHAPName:         "initiator-user",
		CHAPSecret:       "secretpassw0rd",
		MutualCHAPName:   "target-user",
		MutualCHAPSecret: "targetpassw0rd",
	}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target fixture: %v", err)
	}

	router := gin.New()
	router.GET("/targets", GetTargets(svc))
	rr := testutil.PerformRequest(t, router, http.MethodGet, "/targets", nil, nil)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	for _, secret := range []string{"secretpassw0rd", "targetpassw0rd", "chapSecret", "mutualChapSecret"} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Fatalf("response exposed %q: %s", secret, rr.Body.String())
		}
	}
}

func TestCreateTargetHandlerReturnsCreated(t *testing.T) {
	defer enableMockExec()()
	setupTargetTestConfig(t)
	svc := newTargetHandlerTestService(t)

	router := gin.New()
	router.POST("/targets", CreateTarget(svc))
	body, _ := json.Marshal(ISCSITargetRequest{
		TargetName: "iqn.2025-01.com.example:target0",
		AuthMethod: "None",
	})
	rr := testutil.PerformJSONRequest(t, router, http.MethodPost, "/targets", body)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateTargetHandlerReturnsAcceptedWhenSavedButReloadFails(t *testing.T) {
	setupTargetTestConfig(t)
	svc := newTargetHandlerTestService(t)
	restoreCommand := utils.SetCommandForTest(func(command string, args ...string) *exec.Cmd {
		if command == "/usr/sbin/service" && slices.Equal(args, []string{"ctld", "onereload"}) {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	})
	t.Cleanup(restoreCommand)

	router := gin.New()
	router.POST("/targets", CreateTarget(svc))
	body, _ := json.Marshal(ISCSITargetRequest{
		TargetName: "iqn.2025-01.com.example:pending",
		AuthMethod: "None",
	})
	rr := testutil.PerformJSONRequest(t, router, http.MethodPost, "/targets", body)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rr.Code, rr.Body.String())
	}
	response := testutil.DecodeJSONResponse[internal.APIResponse[any]](t, rr)
	if response.Status != "success" || response.Message != "iscsi_configuration_saved_apply_pending" {
		t.Fatalf("unexpected response: %+v", response)
	}
	var count int64
	if err := svc.DB.Model(&iscsiModels.ISCSITarget{}).Where("target_name = ?", "iqn.2025-01.com.example:pending").Count(&count).Error; err != nil {
		t.Fatalf("count target: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted targets = %d, want 1", count)
	}
}

func TestCreateTargetHandlerMapsDomainErrors(t *testing.T) {
	svc := newTargetHandlerTestService(t)
	createTargetFixture(t, svc, "iqn.2025-01.com.example:duplicate")
	router := gin.New()
	router.POST("/targets", CreateTarget(svc))

	tests := []struct {
		name   string
		body   ISCSITargetRequest
		status int
	}{
		{name: "invalid", body: ISCSITargetRequest{AuthMethod: "None"}, status: http.StatusBadRequest},
		{
			name: "conflict",
			body: ISCSITargetRequest{
				TargetName: "iqn.2025-01.com.example:duplicate",
				AuthMethod: "None",
			},
			status: http.StatusConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			rr := testutil.PerformJSONRequest(t, router, http.MethodPost, "/targets", body)
			if rr.Code != tt.status {
				t.Fatalf("expected %d, got %d: %s", tt.status, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAddTargetChildrenReturnCreated(t *testing.T) {
	defer enableMockExec()()
	setupTargetTestConfig(t)
	svc := newTargetHandlerTestService(t)
	target := createTargetFixture(t, svc, "iqn.2025-01.com.example:target0")

	router := gin.New()
	router.POST("/targets/:targetId/portals", AddPortal(svc))
	router.POST("/targets/:targetId/luns", AddLUN(svc))

	portalBody, _ := json.Marshal(ISCSITargetPortalRequest{Address: "192.0.2.10", Port: 3260})
	portalResponse := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/targets/%d/portals", target.ID),
		portalBody,
	)
	if portalResponse.Code != http.StatusCreated {
		t.Fatalf("expected portal status 201, got %d: %s", portalResponse.Code, portalResponse.Body.String())
	}

	lunBody, _ := json.Marshal(ISCSITargetLUNRequest{LUNNumber: 0, ZVol: "tank/vol0"})
	lunResponse := testutil.PerformJSONRequest(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/targets/%d/luns", target.ID),
		lunBody,
	)
	if lunResponse.Code != http.StatusCreated {
		t.Fatalf("expected LUN status 201, got %d: %s", lunResponse.Code, lunResponse.Body.String())
	}
}

func TestUpdateTargetHandlerUsesPathIDAndPreservesSecret(t *testing.T) {
	defer enableMockExec()()
	setupTargetTestConfig(t)
	svc := newTargetHandlerTestService(t)
	target := iscsiModels.ISCSITarget{
		TargetName: "iqn.2025-01.com.example:target0",
		AuthMethod: "CHAP",
		CHAPName:   "chap-user",
		CHAPSecret: "secretpassw0rd",
	}
	if err := svc.DB.Create(&target).Error; err != nil {
		t.Fatalf("create target fixture: %v", err)
	}

	router := gin.New()
	router.PUT("/targets/:targetId", UpdateTarget(svc))
	body, _ := json.Marshal(ISCSITargetRequest{
		TargetName: target.TargetName,
		Alias:      "updated",
		AuthMethod: "CHAP",
		CHAPName:   "chap-user",
	})
	rr := testutil.PerformJSONRequest(t, router, http.MethodPut, fmt.Sprintf("/targets/%d", target.ID), body)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var updated iscsiModels.ISCSITarget
	if err := svc.DB.First(&updated, target.ID).Error; err != nil {
		t.Fatalf("load updated target: %v", err)
	}
	if updated.CHAPSecret != "secretpassw0rd" {
		t.Fatalf("stored CHAP secret changed to %q", updated.CHAPSecret)
	}
}

func TestTargetChildRoutesEnforceParentOwnership(t *testing.T) {
	defer enableMockExec()()
	setupTargetTestConfig(t)
	svc := newTargetHandlerTestService(t)
	first := createTargetFixture(t, svc, "iqn.2025-01.com.example:first")
	second := createTargetFixture(t, svc, "iqn.2025-01.com.example:second")
	portal := iscsiModels.ISCSITargetPortal{TargetID: first.ID, Address: "192.0.2.10", Port: 3260}
	lun := iscsiModels.ISCSITargetLUN{TargetID: first.ID, LUNNumber: 0, ZVol: "tank/vol0"}
	if err := svc.DB.Create(&portal).Error; err != nil {
		t.Fatalf("create portal fixture: %v", err)
	}
	if err := svc.DB.Create(&lun).Error; err != nil {
		t.Fatalf("create LUN fixture: %v", err)
	}

	router := gin.New()
	router.DELETE("/targets/:targetId/portals/:portalId", RemovePortal(svc))
	router.DELETE("/targets/:targetId/luns/:lunId", RemoveLUN(svc))

	wrongPortal := testutil.PerformRequest(t, router, http.MethodDelete, fmt.Sprintf("/targets/%d/portals/%d", second.ID, portal.ID), nil, nil)
	if wrongPortal.Code != http.StatusNotFound {
		t.Fatalf("wrong-parent portal delete status=%d: %s", wrongPortal.Code, wrongPortal.Body.String())
	}
	wrongLUN := testutil.PerformRequest(t, router, http.MethodDelete, fmt.Sprintf("/targets/%d/luns/%d", second.ID, lun.ID), nil, nil)
	if wrongLUN.Code != http.StatusNotFound {
		t.Fatalf("wrong-parent LUN delete status=%d: %s", wrongLUN.Code, wrongLUN.Body.String())
	}

	correctPortal := testutil.PerformRequest(t, router, http.MethodDelete, fmt.Sprintf("/targets/%d/portals/%d", first.ID, portal.ID), nil, nil)
	if correctPortal.Code != http.StatusOK {
		t.Fatalf("correct portal delete status=%d: %s", correctPortal.Code, correctPortal.Body.String())
	}
	correctLUN := testutil.PerformRequest(t, router, http.MethodDelete, fmt.Sprintf("/targets/%d/luns/%d", first.ID, lun.ID), nil, nil)
	if correctLUN.Code != http.StatusOK {
		t.Fatalf("correct LUN delete status=%d: %s", correctLUN.Code, correctLUN.Body.String())
	}
}
