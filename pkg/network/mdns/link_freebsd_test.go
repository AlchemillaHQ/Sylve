// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd && cgo

package dnssd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

var epairNamePattern = regexp.MustCompile(`^epair[0-9]+a$`)
var interfaceNamePattern = regexp.MustCompile(`^[[:alnum:]_.-]{1,15}$`)

func TestIntegrationMDNSFreeBSDIFNETWatcher(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real FreeBSD IFNET event")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to create an epair")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	watcher := &freebsdLinkWatcher{}
	updates, err := watcher.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe to IFNET events: %v", err)
	}

	epair := createMDNSTestEpair(t)
	epairExists := true
	t.Cleanup(func() {
		if epairExists {
			if err := destroyMDNSTestEpair(epair); err != nil {
				t.Errorf("clean up %s: %v", epair, err)
			}
		}
	})

	select {
	case _, ok := <-updates:
		if !ok {
			t.Fatal("FreeBSD IFNET watcher stopped before delivering the epair event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FreeBSD IFNET watcher did not deliver the epair event")
	}

	if err := destroyMDNSTestEpair(epair); err != nil {
		t.Fatalf("destroy %s: %v", epair, err)
	}
	epairExists = false
	cancel()

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case _, ok := <-updates:
			if !ok {
				return
			}
		case <-deadline.C:
			t.Fatal("FreeBSD IFNET watcher did not stop after cancellation")
		}
	}
}

func createMDNSTestEpair(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("/sbin/ifconfig", "epair", "create").CombinedOutput()
	if err != nil {
		t.Fatalf("create epair: %v: %s", err, strings.TrimSpace(string(output)))
	}

	name := strings.TrimSpace(string(output))
	if !epairNamePattern.MatchString(name) {
		if interfaceNamePattern.MatchString(name) {
			_ = exec.Command("/sbin/ifconfig", name, "destroy").Run()
		}
		t.Fatalf("ifconfig returned unsafe epair name %q", name)
	}
	return name
}

func destroyMDNSTestEpair(name string) error {
	if !epairNamePattern.MatchString(name) {
		return fmt.Errorf("refusing to destroy unsafe epair name %q", name)
	}
	output, err := exec.Command("/sbin/ifconfig", name, "destroy").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ifconfig %s destroy: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
