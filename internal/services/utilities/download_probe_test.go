// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/cavaliergopher/grab/v3"
)

func TestFilenameFromContentDisposition(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "quoted", header: `attachment; filename="installer.iso"`, want: "installer.iso", ok: true},
		{name: "token", header: "attachment; filename=installer.iso", want: "installer.iso", ok: true},
		{name: "extended", header: `attachment; filename*=UTF-8''my%20file.iso`, want: "my file.iso", ok: true},
		{name: "missing header", header: "", ok: false},
		{name: "no filename parameter", header: "inline", ok: false},
		{name: "malformed parameter", header: "attachment; filename=", ok: false},
		{name: "traversal reduced to basename", header: `attachment; filename="../../etc/passwd"`, want: "passwd", ok: true},
		{name: "dot rejected", header: `attachment; filename="."`, ok: false},
		{name: "dotdot rejected", header: `attachment; filename=".."`, ok: false},
		{name: "oversized rejected", header: `attachment; filename="` + strings.Repeat("a", 256) + `"`, ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := filenameFromContentDisposition(tc.header)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("filenameFromContentDisposition(%q)=(%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestProbeHTTPFilename(t *testing.T) {
	var requestsMu sync.Mutex
	var requests []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		requestsMu.Unlock()

		switch r.URL.Path {
		case "/named":
			w.Header().Set("Content-Disposition", `attachment; filename="installer.iso"`)
		case "/head-rejected":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Disposition", `attachment; filename="via-get.iso"`)
		case "/slow":
			time.Sleep(200 * time.Millisecond)
			w.Header().Set("Content-Disposition", `attachment; filename="slow.iso"`)
		}
	}))
	defer server.Close()

	service := &Service{GrabClient: &grab.Client{HTTPClient: server.Client()}}

	probe := func(t *testing.T, ctx context.Context, path string) (string, bool) {
		t.Helper()
		return service.probeHTTPFilename(ctx, server.URL+path, false)
	}

	t.Run("content disposition", func(t *testing.T) {
		got, ok := probe(t, context.Background(), "/named")
		if !ok || got != "installer.iso" {
			t.Fatalf("got (%q, %v)", got, ok)
		}

		requestsMu.Lock()
		defer requestsMu.Unlock()
		if len(requests) == 0 || requests[0] != "HEAD /named" {
			t.Fatalf("requests=%v, want HEAD /named first", requests)
		}
	})

	t.Run("no filename advertised", func(t *testing.T) {
		got, ok := probe(t, context.Background(), "/unnamed")
		if ok || got != "" {
			t.Fatalf("got (%q, %v)", got, ok)
		}
	})

	t.Run("head rejected without get fallback", func(t *testing.T) {
		got, ok := probe(t, context.Background(), "/head-rejected")
		if ok || got != "" {
			t.Fatalf("got (%q, %v)", got, ok)
		}
	})

	t.Run("caller deadline wins", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		got, ok := probe(t, ctx, "/slow")
		if ok || got != "" {
			t.Fatalf("got (%q, %v)", got, ok)
		}
	})
}

func TestDownloadFileNamesBlankHTTPDownloads(t *testing.T) {
	t.Run("probed content disposition", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Disposition", `attachment; filename="freebsd-14.3-base-amd64.txz"`)
		}))
		defer server.Close()

		service := newDownloadFileTestService(t, server.Client())
		id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
			URL:          server.URL + "/download",
			DownloadType: utilitiesModels.DownloadUTypeOther,
		})
		if err != nil {
			t.Fatalf("DownloadFile: %v", err)
		}

		stored := storedDownload(t, service, id)
		if stored.Name != "freebsd-14.3-base-amd64.txz" {
			t.Fatalf("Name=%q", stored.Name)
		}
		if filepath.Base(stored.Path) != stored.Name {
			t.Fatalf("Path=%q does not end in Name=%q", stored.Path, stored.Name)
		}
	})

	t.Run("share URL path", func(t *testing.T) {
		service := newDownloadFileTestService(t, nil)
		id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
			URL:          "https://10.10.30.103/api/utilities/downloads/02c232cb-897d-5c5c-ae8d-0eafd733da94/freebsd-14.3-base-amd64.txz?expires=1789436395&id=1&node=ares&sig=75972e",
			DownloadType: utilitiesModels.DownloadUTypeOther,
		})
		if err != nil {
			t.Fatalf("DownloadFile: %v", err)
		}

		stored := storedDownload(t, service, id)
		if stored.Name != "freebsd-14.3-base-amd64.txz" {
			t.Fatalf("Name=%q", stored.Name)
		}
	})
}

func TestDownloadFilePrefersExplicitFilename(t *testing.T) {
	probeCalled := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		probeCalled = true
		return nil, errors.New("probe must not run")
	})}
	service := newDownloadFileTestService(t, client)

	filename := "chosen.iso"
	id, err := service.DownloadFile(utilitiesServiceInterfaces.DownloadFileRequest{
		URL:          "https://example.test/download",
		Filename:     &filename,
		DownloadType: utilitiesModels.DownloadUTypeOther,
	})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if probeCalled {
		t.Fatal("filename probe ran despite an explicit filename")
	}

	stored := storedDownload(t, service, id)
	if stored.Name != filename || filepath.Base(stored.Path) != filename {
		t.Fatalf("Name=%q Path=%q", stored.Name, stored.Path)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newDownloadFileTestService(t *testing.T, client grab.HTTPClient) *Service {
	t.Helper()
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())

	service := &Service{
		DB: testutil.NewSQLiteTestDB(t,
			&utilitiesModels.Downloads{},
			&utilitiesModels.DownloadedFile{},
		),
		enqueueDownloadStartFn: func(context.Context, utilitiesServiceInterfaces.DownloadStartPayload) error {
			return nil
		},
	}
	if client != nil {
		service.GrabClient = &grab.Client{HTTPClient: client}
	}
	return service
}

func storedDownload(t *testing.T, service *Service, id uint) utilitiesModels.Downloads {
	t.Helper()
	var download utilitiesModels.Downloads
	if err := service.DB.First(&download, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return download
}
