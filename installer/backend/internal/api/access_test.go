// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testAccess(t *testing.T) *Access {
	t.Helper()
	a, err := NewAccess()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func accessRequest(handler http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://installer"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAccessProtectsInstallerAPIs(t *testing.T) {
	t.Setenv("SYLVE_AUTOINSTALL", "0")
	a := testAccess(t)
	handler := Routes(Config{Version: "test", Access: a})
	for _, request := range []struct{ method, path string }{
		{"GET", "/api/system"}, {"GET", "/api/disks"}, {"GET", "/api/preflight"},
		{"GET", "/api/install"}, {"GET", "/api/events"}, {"GET", "/api/qr"},
		{"GET", "/api/network/live"}, {"POST", "/api/network/live"},
		{"POST", "/api/disks/wipe"}, {"POST", "/api/disks/gpt"},
		{"POST", "/api/disks/partitions"}, {"POST", "/api/disks/partitions/delete"},
		{"POST", "/api/install"}, {"POST", "/api/autoinstall"}, {"POST", "/api/power"},
	} {
		if w := accessRequest(handler, request.method, request.path, "{}", "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401", request.method, request.path, w.Code)
		}
	}
	for _, path := range []string{"/api/access", "/api/health"} {
		w := accessRequest(handler, "GET", path, "", "", nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), a.password) || strings.Contains(w.Body.String(), a.kiosk) || strings.Contains(w.Body.String(), a.session) {
			t.Fatalf("public response leaked access details or failed: %d %s", w.Code, w.Body.String())
		}
	}
	w := accessRequest(handler, "GET", "/api/access", "", "", &http.Cookie{Name: accessCookie, Value: "invalid"})
	var invalid accessStatus
	if err := json.Unmarshal(w.Body.Bytes(), &invalid); err != nil || w.Code != http.StatusOK || invalid.Authenticated || invalid.Kiosk || invalid.Password != "" {
		t.Fatal("invalid session received access details")
	}
	for _, path := range []string{"/console", "/ws/vnc", "/novnc/core/rfb.js"} {
		if w := accessRequest(handler, "GET", path, "", "", nil); w.Code != http.StatusNotFound {
			t.Fatalf("removed route %s = %d, want 404", path, w.Code)
		}
	}
}

func TestAccessLoginAndKioskSecretIsolation(t *testing.T) {
	t.Setenv("SYLVE_AUTOINSTALL", "0")
	a := testAccess(t)
	handler := Routes(Config{Access: a})
	body, _ := json.Marshal(map[string]string{"password": strings.ToLower(strings.ReplaceAll(a.password, "-", ""))})
	if w := accessRequest(handler, "POST", "/api/access", string(body), "http://evil.example", nil); w.Code != http.StatusForbidden {
		t.Fatal("cross-origin login was accepted")
	}
	if w := accessRequest(handler, "POST", "/api/access", `{"password":"wrong"}`, "", nil); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), a.password) || strings.Contains(w.Body.String(), a.kiosk) {
		t.Fatal("incorrect password was accepted or exposed access details")
	}
	login := accessRequest(handler, "POST", "/api/access", string(body), "http://installer", nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	for _, response := range []*httptest.ResponseRecorder{login, accessRequest(handler, "GET", "/api/access", "", "", cookie)} {
		var state accessStatus
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || !state.Authenticated || state.Kiosk || state.Password != a.password {
			t.Fatalf("remote session cannot display its password or received kiosk access: %+v, %v", state, err)
		}
		if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), a.kiosk) || strings.Contains(response.Body.String(), a.session) {
			t.Fatal("access response was cacheable or exposed a session/bootstrap token")
		}
	}
	if w := accessRequest(handler, "GET", "/api/install", "", "", cookie); w.Code != http.StatusNotFound {
		t.Fatalf("authenticated API was not reached: %d", w.Code)
	}
	if w := accessRequest(handler, "POST", "/api/power", "{}", "http://evil.example", cookie); w.Code != http.StatusForbidden {
		t.Fatal("authenticated cross-origin mutation was accepted")
	}
	body, _ = json.Marshal(map[string]string{"password": a.kiosk})
	login = accessRequest(handler, "POST", "/api/access", string(body), "http://installer", nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatal("local kiosk bootstrap failed")
	}
	status := accessRequest(handler, "GET", "/api/access", "", "", login.Result().Cookies()[0])
	var state accessStatus
	if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil || !state.Kiosk || state.Password != a.password {
		t.Fatal("kiosk cannot display the pairing password")
	}
	next := testAccess(t)
	if next.status(httptest.NewRequest("GET", "/", nil)).Authenticated || next.password == a.password {
		t.Fatal("new live session reused credentials")
	}
	nextHandler := Routes(Config{Access: next})
	if w := accessRequest(nextHandler, "GET", "/api/install", "", "", cookie); w.Code != http.StatusUnauthorized {
		t.Fatal("previous live-session cookie remained valid")
	}
	status = accessRequest(nextHandler, "GET", "/api/access", "", "", cookie)
	var stale accessStatus
	if err := json.Unmarshal(status.Body.Bytes(), &stale); err != nil || status.Code != http.StatusOK || stale.Authenticated || stale.Kiosk || stale.Password != "" {
		t.Fatal("previous live-session cookie received the new access password")
	}
}

func TestAccessInfoIsPrivateAndReplacesStaleFile(t *testing.T) {
	a := testAccess(t)
	dir := t.TempDir()
	outside := filepath.Join(dir, "untouched")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "access.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteInfo(path, "0.0.0.0:8080"); err != nil {
		t.Fatal(err)
	}
	info, err := ReadAccessInfo(path)
	if err != nil || info.Password != a.password || info.KioskURL != "http://127.0.0.1:8080/#kiosk="+a.kiosk {
		t.Fatalf("invalid kiosk access details: %v", err)
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatal("access file is not private")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatal("writing access details followed an existing symlink")
	}
}

func TestAccessMnemonicPassword(t *testing.T) {
	if len(accessWords) != 2048 {
		t.Fatal("expected the 2048-word BIP-39 English list")
	}
	words := make(map[string]bool, len(accessWords))
	wordPattern := regexp.MustCompile(`^[a-z]{3,8}$`)
	for _, word := range accessWords {
		if words[word] || !wordPattern.MatchString(word) {
			t.Fatalf("invalid or duplicate mnemonic word %q", word)
		}
		words[word] = true
	}
	password := testAccess(t).password
	if !regexp.MustCompile(`^[a-z]{6,16}[0-9]{2}$`).MatchString(password) {
		t.Fatal("access password must contain lowercase words followed by exactly two digits")
	}
	prefix := password[:len(password)-2]
	for first := 3; first <= 8 && first < len(prefix); first++ {
		if words[prefix[:first]] && words[prefix[first:]] {
			return
		}
	}
	t.Fatal("access password was not composed from two dictionary words")
}

func TestAccessLoginThrottling(t *testing.T) {
	t.Setenv("SYLVE_AUTOINSTALL", "0")
	a := testAccess(t)
	handler := Routes(Config{Access: a})
	for range 10 {
		if w := accessRequest(handler, "POST", "/api/access", `{"password":"wrong"}`, "", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("login throttled before the allowed attempts: %d", w.Code)
		}
	}
	body, _ := json.Marshal(map[string]string{"password": a.password})
	w := accessRequest(handler, "POST", "/api/access", string(body), "", nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatal("remote login guesses were not throttled")
	}
	body, _ = json.Marshal(map[string]string{"password": a.kiosk})
	if w := accessRequest(handler, "POST", "/api/access", string(body), "", nil); w.Code != http.StatusOK {
		t.Fatal("remote attempts blocked local kiosk startup")
	}
	a.window = time.Now().Add(-time.Minute)
	body, _ = json.Marshal(map[string]string{"password": a.password})
	if w := accessRequest(handler, "POST", "/api/access", string(body), "", nil); w.Code != http.StatusOK {
		t.Fatal("login did not recover after the throttle window")
	}
}
