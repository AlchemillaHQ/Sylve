//go:build freebsd

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
	"github.com/alchemillahq/sylve/internal/db/models"
	"github.com/alchemillahq/sylve/pkg/system"
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

func TestSylveRealmPasswordForPAMUsers(t *testing.T) {
	svc := newLocalTestService(t)
	originalAuth, originalConfig := authenticateHostPassword, config.ParsedConfig
	t.Cleanup(func() { authenticateHostPassword, config.ParsedConfig = originalAuth, originalConfig })
	hash, err := svc.passwordHasher.Hash("stored-password")
	if err != nil {
		t.Fatal(err)
	}
	user := &models.User{Username: "alice", Source: "pam", Password: hash}
	hostPassword := "host-password"
	authenticateHostPassword = func(_ *Service, _ string, password string) (bool, error) {
		return password == hostPassword, nil
	}
	hostLine := "alice:$6$x:1001:1001::0:0:Alice:/home/alice:/bin/sh"
	t.Cleanup(system.SetRunCommand(func(string, ...string) (string, error) { return hostLine, nil }))

	config.ParsedConfig = &internal.SylveConfig{Auth: internal.AuthConfig{EnablePAM: true}}
	for password, want := range map[string]bool{"host-password": true, "stored-password": false} {
		if valid, _, _ := svc.verifySylvePassword(user, password); valid != want {
			t.Errorf("PAM on, %q: valid = %v, want %v", password, valid, want)
		}
	}

	config.ParsedConfig = &internal.SylveConfig{}
	for _, tt := range []struct {
		name, line, password string
		want                 bool
	}{
		{"the stored password", hostLine, "stored-password", true},
		{"the host password", hostLine, "host-password", false},
		{"a locked account", "alice:*LOCKED*$6$x:1001:1001::0:0:Alice:/home/alice:/bin/sh", "stored-password", false},
		{"an expired account", "alice:$6$x:1001:1001::0:1000:Alice:/home/alice:/bin/sh", "stored-password", false},
	} {
		hostLine = tt.line
		if valid, _, err := svc.verifySylvePassword(user, tt.password); valid != tt.want || err != nil {
			t.Errorf("PAM off, %s: valid = %v, %v; want %v", tt.name, valid, err, tt.want)
		}
	}
	t.Cleanup(system.SetRunCommand(func(string, ...string) (string, error) {
		return "pw: no such user `alice'", errors.New("exit status 67")
	}))
	if valid, _, err := svc.verifySylvePassword(user, "stored-password"); valid || err != nil {
		t.Errorf("PAM off, a removed account: valid = %v, %v", valid, err)
	}

	local := &models.User{Username: "bob", Source: "local", Password: hash}
	config.ParsedConfig = &internal.SylveConfig{Auth: internal.AuthConfig{EnablePAM: true}}
	if valid, _, _ := svc.verifySylvePassword(local, "stored-password"); !valid {
		t.Error("a Sylve user's own password stopped working")
	}
}

func TestPasskeySignInStopsAtAnUnusableHostAccount(t *testing.T) {
	svc := newLocalTestService(t)
	line := "alice:$6$x:1001:1001::0:0:Alice:/home/alice:/bin/sh"
	t.Cleanup(system.SetRunCommand(func(string, ...string) (string, error) { return line, nil }))
	alice := &models.User{Username: "alice", Source: "pam", Admin: true}
	bob := &models.User{Username: "bob", Source: "local", Admin: true}

	if err := svc.passkeySignInRefusal(alice); err != nil {
		t.Fatalf("usable host account refused: %v", err)
	}
	line = "alice:*LOCKED*$6$x:1001:1001::0:0:Alice:/home/alice:/bin/sh"
	if err := svc.passkeySignInRefusal(alice); err == nil || err.Error() != "account_locked" {
		t.Fatalf("locked host account: %v, want account_locked", err)
	}
	if err := svc.passkeySignInRefusal(bob); err != nil {
		t.Fatalf("a Sylve user was checked against the host: %v", err)
	}
}
