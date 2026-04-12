// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package sambaHandlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	authModels "github.com/alchemillahq/sylve/internal/db/models"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	"github.com/alchemillahq/sylve/internal/services/samba"
	"github.com/gin-gonic/gin"
)

func newSambaSharesRouter(smbService *samba.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/samba/shares", GetShares(smbService))
	r.POST("/samba/shares", CreateShare(smbService))
	r.PUT("/samba/shares", UpdateShare(smbService))
	return r
}

func TestCreateShareRejectsLegacyV1PayloadFields(t *testing.T) {
	router := newSambaSharesRouter(&samba.Service{})

	body := []byte(`{
		"name":"legacy-share",
		"dataset":"dataset-guid",
		"readOnlyGroups":["staff"],
		"writeableGroups":["admins"],
		"guestOk":false
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPost, "/samba/shares", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Message != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Message)
	}
	if !strings.Contains(resp.Error, "unknown field") {
		t.Fatalf("expected strict unknown field error, got %q", resp.Error)
	}
}

func TestUpdateShareRejectsLegacyV1PayloadFields(t *testing.T) {
	router := newSambaSharesRouter(&samba.Service{})

	body := []byte(`{
		"id":1,
		"name":"legacy-share",
		"dataset":"dataset-guid",
		"readOnlyGroups":["staff"],
		"writeableGroups":["admins"],
		"guestOk":false
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPut, "/samba/shares", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Message != "invalid_request" {
		t.Fatalf("expected invalid_request, got %q", resp.Message)
	}
	if !strings.Contains(resp.Error, "unknown field") {
		t.Fatalf("expected strict unknown field error, got %q", resp.Error)
	}
}

func TestGetSharesReturnsExpandedV2Permissions(t *testing.T) {
	db := newSambaHandlerTestDB(
		t,
		&authModels.User{},
		&authModels.Group{},
		&sambaModels.SambaShare{},
	)

	readUser := authModels.User{Username: "alice"}
	writeUser := authModels.User{Username: "bob"}
	readGroup := authModels.Group{Name: "staff_ro"}
	writeGroup := authModels.Group{Name: "staff_rw"}

	if err := db.Create(&readUser).Error; err != nil {
		t.Fatalf("failed creating read user: %v", err)
	}
	if err := db.Create(&writeUser).Error; err != nil {
		t.Fatalf("failed creating write user: %v", err)
	}
	if err := db.Create(&readGroup).Error; err != nil {
		t.Fatalf("failed creating read group: %v", err)
	}
	if err := db.Create(&writeGroup).Error; err != nil {
		t.Fatalf("failed creating write group: %v", err)
	}

	share := sambaModels.SambaShare{
		Name:          "secure",
		Dataset:       "dataset-guid",
		GuestOk:       false,
		ReadOnly:      true,
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}
	if err := db.Create(&share).Error; err != nil {
		t.Fatalf("failed creating share: %v", err)
	}

	if err := db.Model(&share).Association("ReadOnlyUsers").Append(&readUser); err != nil {
		t.Fatalf("failed appending read user: %v", err)
	}
	if err := db.Model(&share).Association("WriteableUsers").Append(&writeUser); err != nil {
		t.Fatalf("failed appending write user: %v", err)
	}
	if err := db.Model(&share).Association("ReadOnlyGroups").Append(&readGroup); err != nil {
		t.Fatalf("failed appending read group: %v", err)
	}
	if err := db.Model(&share).Association("WriteableGroups").Append(&writeGroup); err != nil {
		t.Fatalf("failed appending write group: %v", err)
	}

	svc := &samba.Service{DB: db}
	router := newSambaSharesRouter(svc)

	rr := performSambaJSONRequest(t, router, http.MethodGet, "/samba/shares", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[[]SambaShareResponse]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected one share in response, got %d", len(resp.Data))
	}

	got := resp.Data[0]
	if got.Name != "secure" || got.Dataset != "dataset-guid" {
		t.Fatalf("unexpected share identity payload: %+v", got)
	}

	if got.Guest.Enabled {
		t.Fatalf("expected authenticated share to have guest.enabled=false")
	}
	if got.Guest.Writeable {
		t.Fatalf("expected authenticated share to have guest.writeable=false")
	}

	if len(got.Permissions.Read.Users) != 1 || got.Permissions.Read.Users[0].Username != "alice" {
		t.Fatalf("unexpected read users payload: %+v", got.Permissions.Read.Users)
	}
	if len(got.Permissions.Write.Users) != 1 || got.Permissions.Write.Users[0].Username != "bob" {
		t.Fatalf("unexpected write users payload: %+v", got.Permissions.Write.Users)
	}
	if len(got.Permissions.Read.Groups) != 1 || got.Permissions.Read.Groups[0].Name != "staff_ro" {
		t.Fatalf("unexpected read groups payload: %+v", got.Permissions.Read.Groups)
	}
	if len(got.Permissions.Write.Groups) != 1 || got.Permissions.Write.Groups[0].Name != "staff_rw" {
		t.Fatalf("unexpected write groups payload: %+v", got.Permissions.Write.Groups)
	}
}

func TestCreateShareReturnsConflictForDuplicateName(t *testing.T) {
	db := newSambaHandlerTestDB(t, &sambaModels.SambaShare{})
	if err := db.Create(&sambaModels.SambaShare{
		Name:          "dupe",
		Dataset:       "dataset-guid-1",
		CreateMask:    "0664",
		DirectoryMask: "2775",
	}).Error; err != nil {
		t.Fatalf("failed to seed share: %v", err)
	}

	svc := &samba.Service{DB: db}
	router := newSambaSharesRouter(svc)

	body := []byte(`{
		"name":"dupe",
		"dataset":"dataset-guid-2",
		"permissions":{"read":{"userIds":[],"groupIds":[]},"write":{"userIds":[],"groupIds":[]}},
		"guest":{"enabled":true,"writeable":false},
		"createMask":"0664",
		"directoryMask":"2775"
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPost, "/samba/shares", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != "share_with_name_exists" {
		t.Fatalf("expected share_with_name_exists error, got %q", resp.Error)
	}
}

func TestCreateShareReturnsBadRequestForInvalidPrincipalMode(t *testing.T) {
	db := newSambaHandlerTestDB(t, &sambaModels.SambaShare{})
	svc := &samba.Service{DB: db}
	router := newSambaSharesRouter(svc)

	body := []byte(`{
		"name":"invalid",
		"dataset":"dataset-guid-2",
		"permissions":{"read":{"userIds":[],"groupIds":[]},"write":{"userIds":[],"groupIds":[]}},
		"guest":{"enabled":false,"writeable":false},
		"createMask":"0664",
		"directoryMask":"2775"
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPost, "/samba/shares", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != "no_principals_selected_and_guests_not_allowed" {
		t.Fatalf("unexpected error: %q", resp.Error)
	}
}

func TestUpdateShareReturnsNotFoundWhenShareIsMissing(t *testing.T) {
	db := newSambaHandlerTestDB(t, &sambaModels.SambaShare{})
	svc := &samba.Service{DB: db}
	router := newSambaSharesRouter(svc)

	body := []byte(`{
		"id":999,
		"name":"missing",
		"dataset":"dataset-guid",
		"permissions":{"read":{"userIds":[],"groupIds":[]},"write":{"userIds":[],"groupIds":[]}},
		"guest":{"enabled":true,"writeable":false},
		"createMask":"0664",
		"directoryMask":"2775"
	}`)

	rr := performSambaJSONRequest(t, router, http.MethodPut, "/samba/shares", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp handlerAPIResponse[any]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.HasPrefix(resp.Error, "share_not_found") {
		t.Fatalf("expected share_not_found error, got %q", resp.Error)
	}
}
