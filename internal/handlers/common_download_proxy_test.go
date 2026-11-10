// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authService "github.com/alchemillahq/sylve/internal/services/auth"
)

func TestNodeDownloadStripsOriginCredentialsAndActsForTheUser(t *testing.T) {
	var forwardedClusterHeader string
	var forwardedID string
	var forwardedHash string
	var forwardedAuth string
	var forwardedHeaders http.Header
	var forwardedPath string
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		forwardedHeaders = request.Header.Clone()
		forwardedPath = request.URL.Path
		forwardedClusterHeader = request.Header.Get(authService.ProxyUserHeader)
		forwardedID = request.URL.Query().Get("id")
		forwardedHash = request.URL.Query().Get("hash")
		forwardedAuth = request.URL.Query().Get("auth")

		w.Header().Set("Content-Disposition", `attachment; filename="remote.iso"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("remote file contents"))
	}))
	defer remote.Close()

	router, user := newOriginRouter(t, remoteMember(strings.TrimPrefix(remote.URL, "https://")), "local")

	query := url.Values{
		"id":   []string{"/zroot/images/remote.iso"},
		"hash": []string{"browser-token-hash"},
		"auth": []string{"stray"},
	}
	origin := httptest.NewServer(router)
	defer origin.Close()
	request, err := http.NewRequest(
		http.MethodGet,
		origin.URL+"/api/nodes/remote-node/system/file-explorer/download?"+query.Encode(),
		nil,
	)
	if err != nil {
		t.Fatalf("create origin request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer browser-local-token")
	request.Header.Set("Cookie", "session=browser")
	request.Header.Set("Proxy-Authorization", "Basic browser")
	request.Header.Set("X-Cluster-Key", "must-not-forward")
	request.Header.Set(authService.ProxyUserHeader, `{"id":999,"username":"forged","authType":"sylve"}`)
	request.Header.Set("X-Sylve-Cluster-Forward-Hop", "99")
	request.Header.Set("X-Sylve-Backup-Forwarded-By", "browser")
	request.Header.Set("X-Sylve-Backup-Forward-Target", "browser")
	response, err := origin.Client().Do(request)
	if err != nil {
		t.Fatalf("perform origin request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read origin response: %v", err)
	}

	if response.StatusCode != http.StatusOK || string(responseBody) != "remote file contents" {
		t.Fatalf("response=%d body=%q", response.StatusCode, responseBody)
	}
	if got := response.Header.Get("Content-Disposition"); got != `attachment; filename="remote.iso"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if forwardedPath != "/api/system/file-explorer/download" {
		t.Fatalf("the node got %q, not the plain path", forwardedPath)
	}
	if forwardedID != "/zroot/images/remote.iso" {
		t.Fatalf("forwarded id=%q hash=%q", forwardedID, forwardedHash)
	}
	if forwardedHash != "" || forwardedAuth != "" {
		t.Fatalf("origin query credentials leaked: hash=%q auth=%q", forwardedHash, forwardedAuth)
	}
	if want := (authService.ProxiedUser{Username: user.Username, AuthType: "sylve", Realm: authService.RealmSylve}).Header(); forwardedClusterHeader != want {
		t.Fatalf("forwarded proxy user %q, want the signed-in admin %q", forwardedClusterHeader, want)
	}
	for _, name := range []string{
		"Authorization", "Cookie", "Proxy-Authorization", "X-Cluster-Key",
		"X-Sylve-Backup-Forwarded-By", "X-Sylve-Backup-Forward-Target",
	} {
		if got := forwardedHeaders.Get(name); got != "" {
			t.Fatalf("origin header %s leaked: %q", name, got)
		}
	}
	if got := forwardedHeaders.Get("X-Sylve-Cluster-Forward-Hop"); got != "" {
		t.Fatalf("browser forward-hop header leaked: %q", got)
	}
}
