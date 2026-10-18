// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const accessCookie = "sylve_installer_session"

//go:embed access-words.txt
var accessWordText string

var accessWords = strings.Fields(accessWordText)

type Access struct {
	password string
	session  string
	kiosk    string
	mu       sync.Mutex
	attempts int
	window   time.Time
}

type AccessInfo struct {
	Password string `json:"password"`
	KioskURL string `json:"kioskURL"`
	Listen   string `json:"listen"`
}

type accessStatus struct {
	Authenticated bool   `json:"authenticated"`
	Kiosk         bool   `json:"kiosk"`
	Password      string `json:"password,omitempty"`
}

func NewAccess() (*Access, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	var password strings.Builder
	for range 2 {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(accessWords))))
		if err != nil {
			return nil, err
		}
		password.WriteString(accessWords[index.Int64()])
	}
	number, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&password, "%02d", number.Int64())
	return &Access{
		password: password.String(),
		session:  base64.RawURLEncoding.EncodeToString(secret[:16]),
		kiosk:    base64.RawURLEncoding.EncodeToString(secret[16:]),
	}, nil
}

func (a *Access) WriteInfo(path, listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
		if ip != nil && ip.To4() == nil {
			host = "::1"
		}
	}
	info := AccessInfo{
		Password: a.password,
		KioskURL: "http://" + net.JoinHostPort(host, port) + "/#kiosk=" + a.kiosk,
		Listen:   listen,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".installer-access-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(info); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func ReadAccessInfo(path string) (AccessInfo, error) {
	var info AccessInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	if info.Password == "" || info.KioskURL == "" || info.Listen == "" {
		return info, fmt.Errorf("installer access details are incomplete")
	}
	return info, nil
}

func secretEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func normalizeAccessPassword(password string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, password))
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") &&
		u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && strings.EqualFold(u.Host, r.Host)
}

func (a *Access) status(r *http.Request) accessStatus {
	cookie, err := r.Cookie(accessCookie)
	if err != nil {
		return accessStatus{}
	}
	if secretEqual(cookie.Value, a.kiosk) {
		return accessStatus{Authenticated: true, Kiosk: true, Password: a.password}
	}
	if secretEqual(cookie.Value, a.session) {
		return accessStatus{Authenticated: true, Password: a.password}
	}
	return accessStatus{}
}

func (a *Access) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter the installer access password"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one JSON document"})
		return
	}
	kiosk := secretEqual(body.Password, a.kiosk)
	if !kiosk && !a.allowLogin() {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts; try again in a minute"})
		return
	}
	if !kiosk && !secretEqual(normalizeAccessPassword(body.Password), normalizeAccessPassword(a.password)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "installer access password not recognized"})
		return
	}
	token := a.session
	status := accessStatus{Authenticated: true, Password: a.password}
	if kiosk {
		token = a.kiosk
		status.Kiosk = true
	}
	http.SetCookie(w, &http.Cookie{
		Name: accessCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
	writeJSON(w, http.StatusOK, status)
}

func (a *Access) allowLogin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.window) >= time.Minute {
		a.window = now
		a.attempts = 0
	}
	if a.attempts >= 10 {
		return false
	}
	a.attempts++
	return true
}

func (a *Access) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin requests are not allowed"})
			return
		}
		if r.URL.Path == "/api/access" {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, http.StatusOK, a.status(r))
			case http.MethodPost:
				a.login(w, r)
			default:
				w.Header().Set("Allow", "GET, POST")
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		if r.URL.Path != "/api/health" && !a.status(r).Authenticated {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "installer access password required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
