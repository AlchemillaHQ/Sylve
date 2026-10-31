//go:build linux || freebsd

// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"errors"
	"testing"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	"github.com/msteinert/pam"
)

func TestAuthenticatePAMUsesConfiguredService(t *testing.T) {
	originalStart, originalConfig := pamStartFunc, config.ParsedConfig
	t.Cleanup(func() {
		pamStartFunc, config.ParsedConfig = originalStart, originalConfig
	})

	tests := []struct {
		name    string
		config  *internal.SylveConfig
		service string
	}{
		{name: "no config", service: "login"},
		{name: "default service", config: &internal.SylveConfig{}, service: "login"},
		{
			name:    "custom service",
			config:  &internal.SylveConfig{Auth: internal.AuthConfig{PAMService: "sylve"}},
			service: "sylve",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config.ParsedConfig = test.config
			startErr := errors.New("PAM start failed")
			calls := 0
			pamStartFunc = func(service, _ string, _ func(pam.Style, string) (string, error)) (*pam.Transaction, error) {
				calls++
				if service != test.service {
					t.Fatalf("PAM service = %q, want %q", service, test.service)
				}
				return nil, startErr
			}

			valid, err := (&Service{}).AuthenticatePAM("alice", "test-password")
			if valid || !errors.Is(err, startErr) {
				t.Fatalf("AuthenticatePAM = (%t, %v), want (false, %v)", valid, err, startErr)
			}
			if calls != 1 {
				t.Fatalf("PAM start called %d times, want one attempt without fallback", calls)
			}
		})
	}
}
