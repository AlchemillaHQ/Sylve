// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package systemHandlers

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/internal/handlers/middleware"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	authService "github.com/alchemillahq/sylve/internal/services/auth"
	"github.com/alchemillahq/sylve/internal/services/system"
	"github.com/alchemillahq/sylve/internal/testutil"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/gin-gonic/gin"
)

func ticketTestRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	database := testutil.NewSQLiteTestDB(t, &models.User{}, &models.Group{}, &models.Token{}, &models.SystemSecrets{})
	if err := database.Create(&models.SystemSecrets{Name: "JWTSecret", Data: "test-jwt-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	const passwordHash = "$2a$04$q8RMvfpZU3MpIHropi1cfu0QobOdx.lw1SwiSLPC2ctgMMtPw6cc."
	user := models.User{Username: "ticket-admin", Password: passwordHash, Admin: true}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	auth := authService.NewAuthService(database).(*authService.Service)
	_, token, err := auth.CreateJWT(user.Username, "correct horse battery staple", "sylve", false)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	authenticated := middleware.EnsureAuthenticated(auth)
	service := &system.Service{}
	api.POST("/auth/download-tickets", authenticated, middleware.RequireLocalSession(), CreateDownloadTicket(auth))
	api.GET("/system/file-explorer/archive", authenticated, DownloadFolder(service))
	api.GET("/system/file-explorer/list", authenticated, ListFolder(service))
	return router, token
}

func TestDownloadTicketAuthenticatesOneArchiveDownload(t *testing.T) {
	router, token := ticketTestRouter(t)
	folder := filepath.Join(t.TempDir(), "reports")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "q1.txt"), []byte("quarter one"), 0o644); err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}

	issued := testutil.PerformRequest(t, router, http.MethodPost, "/api/auth/download-tickets",
		strings.NewReader(`{"kind":"archive","id":"`+folder+`"}`), bearer)
	if issued.Code != http.StatusOK {
		t.Fatalf("issue status = %d; body = %s", issued.Code, issued.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[DownloadTicketResponse]](t, issued)
	if body.Data.Ticket == "" || body.Data.ExpiresAt.IsZero() || issued.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ticket response = %+v headers=%v", body, issued.Header())
	}

	target := "/api/system/file-explorer/archive?id=" + url.QueryEscape(folder) + "&ticket=" + url.QueryEscape(body.Data.Ticket)
	archive := testutil.PerformRequest(t, router, http.MethodGet, target, nil, nil)
	if archive.Code != http.StatusOK || archive.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(archive.Header().Get("Content-Disposition"), "reports.zip") {
		t.Fatalf("archive response = %d %v", archive.Code, archive.Header())
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Body.Bytes()), int64(archive.Body.Len()))
	if err != nil {
		t.Fatalf("the archive is not a valid zip: %v", err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["reports/q1.txt"] {
		t.Fatalf("archive entries = %v", names)
	}
	if again := testutil.PerformRequest(t, router, http.MethodGet, target, nil, nil); again.Code != http.StatusUnauthorized {
		t.Fatalf("reused ticket status = %d; want 401", again.Code)
	}

	invalid := testutil.PerformRequest(t, router, http.MethodPost, "/api/auth/download-tickets",
		strings.NewReader(`{"kind":"everything","id":"/"}`), bearer)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d; want 400", invalid.Code)
	}
	unauthenticated := testutil.PerformRequest(t, router, http.MethodPost, "/api/auth/download-tickets",
		strings.NewReader(`{"kind":"file","id":"/etc/passwd"}`), map[string]string{"Content-Type": "application/json"})
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ticket status = %d; want 401", unauthenticated.Code)
	}
}

func TestListFolderReturnsCappedListing(t *testing.T) {
	router, token := ticketTestRouter(t)
	folder := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(folder, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	response := testutil.PerformRequest(t, router, http.MethodGet,
		"/api/system/file-explorer/list?limit=2&id="+url.QueryEscape(folder), nil, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	listing := testutil.DecodeJSONResponse[internal.APIResponse[systemServiceInterfaces.FileListing]](t, response).Data
	if listing.Total != 3 || !listing.Truncated || len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v", listing)
	}
	bad := testutil.PerformRequest(t, router, http.MethodGet, "/api/system/file-explorer/list?limit=lots", nil, headers)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d; want 400", bad.Code)
	}
}

func TestUploadServerFailuresHideInternalDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		writeFileExplorerUploadFailure(c, uploadCore.NewFailure(
			http.StatusInternalServerError,
			"partial_create_failed",
			errors.New("open /secret/.sylve-upload-1.partial: input/output error"),
		))
	})
	response := testutil.PerformRequest(t, router, http.MethodPost, "/upload", nil, nil)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "/secret") {
		t.Fatalf("response = %d %s; want the code without the path", response.Code, response.Body.String())
	}
	body := testutil.DecodeJSONResponse[internal.APIResponse[FileExplorerUploadErrorData]](t, response)
	if body.Error != "partial_create_failed" || body.Data.Code != "partial_create_failed" {
		t.Fatalf("body = %+v", body)
	}
}

func TestDownloadTicketCoversExactlyTheSelectedItems(t *testing.T) {
	router, token := ticketTestRouter(t)
	parent := filepath.Join(t.TempDir(), "share")
	if err := os.MkdirAll(filepath.Join(parent, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"docs/readme.txt": "read me", "notes.txt": "notes"} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	docs, notes := filepath.Join(parent, "docs"), filepath.Join(parent, "notes.txt")
	bearer := map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"}
	issue := func(body string) (int, string) {
		t.Helper()
		response := testutil.PerformRequest(t, router, http.MethodPost, "/api/auth/download-tickets", strings.NewReader(body), bearer)
		if response.Code != http.StatusOK {
			return response.Code, ""
		}
		return response.Code, testutil.DecodeJSONResponse[internal.APIResponse[DownloadTicketResponse]](t, response).Data.Ticket
	}
	archiveURL := func(ticket string, ids ...string) string {
		query := url.Values{"id": ids, "ticket": {ticket}}
		return "/api/system/file-explorer/archive?" + query.Encode()
	}
	selection := `{"kind":"archive","ids":["` + docs + `","` + notes + `"]}`

	_, ticket := issue(selection)
	response := testutil.PerformRequest(t, router, http.MethodGet, archiveURL(ticket, docs, notes), nil, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Disposition"), "share - 2 items.zip") {
		t.Fatalf("archive response = %d %v", response.Code, response.Header())
	}
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatalf("the archive is not a valid zip: %v", err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["docs/readme.txt"] || !names["notes.txt"] || len(names) != 3 {
		t.Fatalf("archive entries = %v; want docs/, docs/readme.txt and notes.txt", names)
	}

	for name, ids := range map[string][]string{
		"a subset":       {docs},
		"another order":  {notes, docs},
		"an extra item":  {docs, notes, filepath.Join(parent, "other")},
		"a changed path": {docs, filepath.Join(parent, "secret.txt")},
	} {
		_, ticket := issue(selection)
		if code := testutil.PerformRequest(t, router, http.MethodGet, archiveURL(ticket, ids...), nil, nil).Code; code != http.StatusUnauthorized {
			t.Fatalf("ticket used for %s: status = %d; want 401", name, code)
		}
	}

	tooMany := make([]string, system.MaxFileExplorerBatchItems+1)
	for index := range tooMany {
		tooMany[index] = `"/tmp/item"`
	}
	for name, body := range map[string]string{
		"several files":   `{"kind":"file","ids":["` + notes + `","` + docs + `"]}`,
		"id and ids":      `{"kind":"archive","id":"` + docs + `","ids":["` + notes + `"]}`,
		"no ids":          `{"kind":"archive","ids":[]}`,
		"an empty id":     `{"kind":"archive","ids":["` + docs + `",""]}`,
		"too many":        `{"kind":"archive","ids":[` + strings.Join(tooMany, ",") + `]}`,
		"neither id form": `{"kind":"archive"}`,
	} {
		if code, _ := issue(body); code != http.StatusBadRequest {
			t.Fatalf("%s: issue status = %d; want 400", name, code)
		}
	}
}
