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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/installer/backend/internal/engine"
)

const autoinstallURLEnv = "SYLVE_AUTOINSTALL_URL"

var leaseURLPattern = regexp.MustCompile(`(?i)option\s+(?:captive-portal|option-114|unknown-114|option-224|unknown-224|option-238|unknown-238)\s+"(https?://[^"]+)"`)

func autoinstallURL() (planURL, source string) {
	if v := strings.TrimSpace(os.Getenv(autoinstallURLEnv)); v != "" {
		return v, "environment"
	}
	if out, err := exec.Command("/bin/kenv", "-q", "sylve.autoinstall_url").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v, "iPXE/kernel environment"
		}
	}
	matches, _ := filepath.Glob("/var/db/dhclient.leases.*")
	for _, path := range matches {
		blob, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if m := leaseURLPattern.FindStringSubmatch(string(blob)); m != nil {
			return m[1], "dhcp (" + filepath.Base(path) + ")"
		}
	}
	return "", ""
}

func (i *installer) maybeAutoinstall() {
	if os.Getenv("SYLVE_AUTOINSTALL") == "0" {
		return
	}
	planURL, source := autoinstallURL()
	if planURL == "" {
		return
	}
	slog.Info("autoinstall: plan URL advertised", "url", planURL, "source", source)
	go func() {
		plan, err := fetchAutoinstallPlan(planURL)
		if err != nil {
			slog.Warn("autoinstall: plan fetch failed, leaving the kiosk flow", "url", planURL, "err", err)
			return
		}
		i.mu.Lock()
		if i.en != nil && i.en.Snapshot().Running {
			i.mu.Unlock()
			return
		}
		en := engine.New(plan)
		if err := en.Start(); err != nil {
			i.mu.Unlock()
			slog.Warn("autoinstall: plan rejected", "url", planURL, "err", err)
			return
		}
		i.en = en
		i.mu.Unlock()
		slog.Info("autoinstall: install started from advertised plan", "url", planURL, "source", source)
	}()
}

func fetchAutoinstallPlan(planURL string) (engine.Plan, error) {
	var plan engine.Plan
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(planURL)
	if err != nil {
		return plan, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return plan, fmt.Errorf("plan URL returned HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return plan, err
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		return plan, fmt.Errorf("decode plan: %w", err)
	}
	return plan, nil
}
