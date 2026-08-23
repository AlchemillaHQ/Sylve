//go:build !linux && !freebsd

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
)

func TestCreateJWTLogsPAMPlatformErrorWithoutPassword(t *testing.T) {
	logs := captureAuthLogs(t)
	service := newAuthTestService(t)

	previousConfig := config.ParsedConfig
	config.ParsedConfig = &internal.SylveConfig{Auth: internal.AuthConfig{EnablePAM: true}}
	t.Cleanup(func() {
		config.ParsedConfig = previousConfig
	})

	const password = "unsupported-platform-pam-secret"
	_, _, err := service.CreateJWT("root", password, "pam", false)
	if err == nil || err.Error() != "pam_auth_error" {
		t.Fatalf("expected pam_auth_error, got: %v", err)
	}

	output := logs.String()
	requireLogField(t, output, "reason", "pam_error")
	if strings.Contains(output, password) {
		t.Fatalf("PAM password was written to logs: %s", output)
	}
}
