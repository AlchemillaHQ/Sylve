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
	"io/fs"
	"log/slog"
	"net/http"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/alchemillahq/sylve/installer/backend/internal/assets"
	"github.com/alchemillahq/sylve/installer/backend/internal/engine"
	"github.com/alchemillahq/sylve/installer/backend/internal/sysinfo"
)

type Config struct {
	Version string
	Access  *Access
}

type installer struct {
	mu sync.Mutex
	en *engine.Engine
}

func Routes(cfg Config) http.Handler {
	if cfg.Access == nil {
		panic("installer access credentials are required")
	}
	mux := http.NewServeMux()
	inst := &installer{}
	inst.maybeAutoinstall()
	liveNet := newLiveNetwork()
	mux.HandleFunc("GET /api/network/live", liveNet.get)
	mux.HandleFunc("POST /api/network/live", inst.liveNetworkPost(liveNet))

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"version": cfg.Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sysinfo.Collect())
	})

	mux.HandleFunc("GET /api/disks", func(w http.ResponseWriter, _ *http.Request) {
		disks, err := engine.Disks()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, disks)
	})

	mux.HandleFunc("GET /api/preflight", func(w http.ResponseWriter, _ *http.Request) {
		checks := preflightChecks()
		ok := true
		for _, c := range checks {
			if c.Fatal && c.Status == "fail" {
				ok = false
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"checks": checks, "ok": ok})
	})

	mux.HandleFunc("POST /api/disks/wipe", func(w http.ResponseWriter, r *http.Request) {
		body, ok := diskBody(w, r, "name")
		if !ok {
			return
		}
		if inst.mutateDisks(w, func() error { return engine.WipeDisk(body["name"]) }) {
			writeDisks(w, http.StatusOK)
		}
	})

	mux.HandleFunc("POST /api/disks/gpt", func(w http.ResponseWriter, r *http.Request) {
		body, ok := diskBody(w, r, "name")
		if !ok {
			return
		}
		if inst.mutateDisks(w, func() error { return engine.InitGPT(body["name"]) }) {
			writeDisks(w, http.StatusOK)
		}
	})

	mux.HandleFunc("POST /api/disks/partitions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name      string `json:"name"`
			SizeBytes uint64 `json:"sizeBytes"`
			Type      string `json:"type"`
			Label     string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if inst.mutateDisks(w, func() error {
			return engine.AddPartition(body.Name, body.SizeBytes, body.Type, body.Label)
		}) {
			writeDisks(w, http.StatusCreated)
		}
	})

	mux.HandleFunc("POST /api/disks/partitions/delete", func(w http.ResponseWriter, r *http.Request) {
		body, ok := diskBody(w, r, "partition")
		if !ok {
			return
		}
		if inst.mutateDisks(w, func() error { return engine.DeletePartition(body["partition"]) }) {
			writeDisks(w, http.StatusOK)
		}
	})

	mux.HandleFunc("GET /api/timezones", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, listTimezones())
	})

	mux.HandleFunc("GET /api/keymaps", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, listKeymaps())
	})

	mux.HandleFunc("POST /api/install", inst.start)
	mux.HandleFunc("POST /api/autoinstall", inst.start)
	mux.HandleFunc("GET /api/install", func(w http.ResponseWriter, _ *http.Request) {
		inst.mu.Lock()
		en := inst.en
		inst.mu.Unlock()
		if en == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no install has been started"})
			return
		}
		writeJSON(w, http.StatusOK, en.Snapshot())
	})

	mux.HandleFunc("GET /api/events", inst.events)

	mux.HandleFunc("POST /api/power", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		flag := map[string]string{"reboot": "-r", "poweroff": "-p"}[body.Action]
		if flag == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be reboot or poweroff"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := exec.Command("/sbin/shutdown", flag, "now").Run(); err != nil {
				slog.Error("shutdown failed", "action", body.Action, "err", err)
			}
		}()
	})

	mux.HandleFunc("GET /api/qr", func(w http.ResponseWriter, r *http.Request) {
		text := r.URL.Query().Get("text")
		if text == "" {
			text = defaultURL(r)
		}
		qr, err := qrcode.New(text, qrcode.Medium)
		if err != nil {
			http.Error(w, "qr error", http.StatusInternalServerError)
			return
		}
		qr.DisableBorder = true
		png, err := qr.PNG(320)
		if err != nil {
			http.Error(w, "qr error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	})

	uiFS, err := fs.Sub(assets.FS, "web-files")
	if err == nil {
		mux.Handle("GET /", spa{fs: uiFS})
	}

	return logRequests(cfg.Access.protect(mux))
}

func (i *installer) start(w http.ResponseWriter, r *http.Request) {
	var plan engine.Plan
	if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan: " + err.Error()})
		return
	}
	i.mu.Lock()
	if i.en != nil {
		s := i.en.Snapshot()
		if s.Running {
			i.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "an install is already running"})
			return
		}
	}
	en := engine.New(plan)
	if err := en.Start(); err != nil {
		i.mu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	i.en = en
	i.mu.Unlock()
	writeJSON(w, http.StatusAccepted, en.Snapshot())
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32768,
	CheckOrigin:     sameOrigin,
}

func (i *installer) events(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	en := i.en
	i.mu.Unlock()
	if en == nil {
		http.Error(w, "no install has been started", http.StatusNotFound)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	ch, unsubscribe := en.Subscribe()
	defer unsubscribe()

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-disconnected:
			return
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := ws.WriteJSON(ev); err != nil {
				return
			}
		}
	}
}

type spa struct{ fs fs.FS }

func (s spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		p = "index.html"
	}
	if strings.HasPrefix(p, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if f, err := s.fs.Open(p); err == nil {
		_ = f.Close()
		http.FileServerFS(s.fs).ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func defaultURL(r *http.Request) string {
	info := sysinfo.Collect()
	for _, iface := range info.Interfaces {
		if len(iface.IPv4) > 0 {
			return "http://" + iface.IPv4[0] + ":8080"
		}
	}
	return "http://" + r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (i *installer) mutateDisks(w http.ResponseWriter, mutate func() error) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.en != nil && i.en.Snapshot().Running {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an install is running"})
		return false
	}
	if err := mutate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	return true
}

func diskBody(w http.ResponseWriter, r *http.Request, field string) (map[string]string, bool) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return nil, false
	}
	if strings.TrimSpace(body[field]) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": field + " is required"})
		return nil, false
	}
	return body, true
}

func writeDisks(w http.ResponseWriter, status int) {
	disks, err := engine.Disks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, map[string]any{"status": "ok", "disks": disks})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
	})
}
