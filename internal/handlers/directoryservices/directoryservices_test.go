// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryServiceHandlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	directoryModels "github.com/alchemillahq/sylve/internal/db/models/directoryservices"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	"github.com/alchemillahq/sylve/internal/services/directoryservices"
	"github.com/alchemillahq/sylve/internal/testutil"
	"github.com/gin-gonic/gin"
)

func request(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rr, req)
	return rr
}

func TestMutationHandlersRejectInvalidJSONBeforeDoingWork(t *testing.T) {
	router := gin.New()
	router.Use(middleware.LimitRequestBody(directoryservices.MaxRequestBodyBytes))
	s := &directoryservices.Service{}
	for path, handler := range map[string]gin.HandlerFunc{"/ad": UpdateAD(s), "/join": Join(s), "/leave": Leave(s), "/enabled": SetEnabled(s), "/realms": CreateRealm(s), "/discover": DiscoverRealm(s), "/ad-discover": DiscoverAD(s), "/keytabs": CreateKeytab(s), "/settings": UpdateSettings(s), "/recovery-export": ExportRecovery(s), "/recovery-restore": RestoreRecovery(s), "/recovery-rollback": RollbackRecovery(s)} {
		router.POST(path, handler)
		for _, body := range []string{"null", "[]", `{"unknown":true}`, `{} {}`, `{"password":`, `"text"`} {
			t.Run(path+body, func(t *testing.T) {
				rr := request(router, http.MethodPost, path, body)
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
				}
			})
		}
		body := `{"padding":"` + strings.Repeat("x", int(directoryservices.MaxRequestBodyBytes)) + `"}`
		if rr := request(router, http.MethodPost, path, body); rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized %s: %d", path, rr.Code)
		}
	}
}

func TestEnabledRequiresExplicitBoolean(t *testing.T) {
	router := gin.New()
	router.PUT("/enabled", SetEnabled(&directoryservices.Service{}))
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"yes"}`} {
		if rr := request(router, http.MethodPut, "/enabled", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d", body, rr.Code)
		}
	}
}

func TestRecoveryServiceConsentRejectsNonBooleanValuesBeforeDoingWork(t *testing.T) {
	router := gin.New()
	s := &directoryservices.Service{}
	router.POST("/export", ExportRecovery(s))
	router.POST("/restore", RestoreRecovery(s))
	router.POST("/rollback", RollbackRecovery(s))
	for _, path := range []string{"/export", "/restore", "/rollback"} {
		for _, value := range []string{`"true"`, `1`, `[]`, `{}`} {
			body := `{"passphrase":"isolated-recovery-passphrase","confirmComputerName":"SYLVE","manageServices":` + value + `}`
			if rr := request(router, http.MethodPost, path, body); rr.Code != http.StatusBadRequest {
				t.Fatalf("invalid recovery service consent accepted: %d", rr.Code)
			}
		}
	}
}

func TestEntryIDsRejectZeroNegativeAndOverflow(t *testing.T) {
	router := gin.New()
	s := &directoryservices.Service{}
	router.DELETE("/realms/:id", DeleteRealm(s))
	router.DELETE("/keytabs/:id", DeleteKeytab(s))
	for _, id := range []string{"0", "-1", "abc", "4294967296"} {
		for _, prefix := range []string{"/realms/", "/keytabs/"} {
			if rr := request(router, http.MethodDelete, prefix+id, ""); rr.Code != http.StatusBadRequest {
				t.Fatalf("accepted ID %s: %d", id, rr.Code)
			}
		}
	}
}

func TestResponsesUseServiceErrorStatus(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{directoryservices.ErrInvalid, 400}, {directoryservices.ErrConflict, 409}, {directoryservices.ErrBusy, 409}, {directoryservices.ErrNotFound, 404}, {directoryservices.ErrUnavailable, 502}, {fmt.Errorf("internal failure"), 500}} {
		router := gin.New()
		router.GET("/error", func(c *gin.Context) { respond(c, nil, fmt.Errorf("wrapped: %w", test.err)) })
		if rr := request(router, http.MethodGet, "/error", ""); rr.Code != test.status {
			t.Fatalf("error %v: %d", test.err, rr.Code)
		}
	}
}

func TestKeytabListingReturnsOnlyMetadata(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &directoryModels.KerberosKeytab{})
	row := directoryModels.KerberosKeytab{Name: "test", Ciphertext: []byte("sensitive-test-key-material"), Principals: []string{"test@EXAMPLE.COM"}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/keytabs", ListKeytabs(directoryservices.NewService(db)))
	rr := request(router, http.MethodGet, "/keytabs", "")
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "Ciphertext") || strings.Contains(rr.Body.String(), "ciphertext") || strings.Contains(rr.Body.String(), "sensitive-test") {
		t.Fatalf("unsafe keytab response: %s", rr.Body.String())
	}
	var data struct {
		Data []directoryModels.KerberosKeytab `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil || len(data.Data) != 1 || data.Data[0].Name != "test" || data.Data[0].Ciphertext != nil {
		t.Fatalf("keytab listing: %+v %v", data, err)
	}
}

func TestSavedADCredentialsAreNeverReturnedByConfigurationHandlers(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &directoryModels.ActiveDirectory{}, &directoryModels.KerberosRealm{}, &directoryModels.KerberosKeytab{})
	ciphertext := []byte("test-encrypted-password-material")
	row := directoryModels.ActiveDirectory{ID: 1, Domain: "EXAMPLE.COM", Workgroup: "EXAMPLE", ComputerName: "SYLVE", JoinUsername: "joinuser", JoinPasswordCiphertext: ciphertext, IDMapBackend: "rid", RangeLow: 1000000, RangeHigh: 1999999999, IDMapSchema: "rfc2307"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	s := directoryservices.NewService(db)
	router.GET("/ad", GetAD(s))
	router.PUT("/ad", UpdateAD(s))
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		body := ""
		if method == http.MethodPut {
			body = `{"domain":"EXAMPLE.COM","workgroup":"EXAMPLE","computerName":"SYLVE","idmapBackend":"rid","rangeLow":1000000,"rangeHigh":1999999999,"idmapSchema":"rfc2307","joinPassword":""}`
		}
		rr := request(router, method, "/ad", body)
		var result struct {
			Data map[string]any `json:"data"`
		}
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &result) != nil {
			t.Fatalf("configuration request failed: %d %s", rr.Code, rr.Body.String())
		}
		if result.Data["joinUsername"] != "joinuser" || result.Data["joinPasswordConfigured"] != true {
			t.Fatal("configuration response is missing saved-credential metadata")
		}
		for key := range result.Data {
			if strings.Contains(strings.ToLower(key), "ciphertext") || key == "joinPassword" {
				t.Fatal("configuration response exposed enrollment password storage")
			}
		}
		if strings.Contains(rr.Body.String(), string(ciphertext)) {
			t.Fatal("configuration response exposed password ciphertext")
		}
	}
	var stored directoryModels.ActiveDirectory
	if err := db.First(&stored, 1).Error; err != nil || !bytes.Equal(stored.JoinPasswordCiphertext, ciphertext) {
		t.Fatal("metadata configuration update discarded the saved password")
	}
}
