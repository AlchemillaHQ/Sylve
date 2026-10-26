// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

func TestSecurityFlavorsNormalizeAndRejectUnsafeValues(t *testing.T) {
	input := shareInput("1", "192.0.2.1")
	input.Security = []string{" KRB5P ", "sys", "krb5", "krb5p"}
	share, err := normalizeShare(input)
	if err != nil || !slices.Equal(share.Security, []string{"sys", "krb5", "krb5p"}) {
		t.Fatalf("security normalization: %v, %v", share.Security, err)
	}
	for _, flavors := range [][]string{{}, {"none"}, {"sys -maproot=root"}, {"krb5:sys"}} {
		input.Security = flavors
		if _, err := normalizeShare(input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unsafe flavors accepted: %v, %v", flavors, err)
		}
	}
	for _, principal := range []string{"host/server@REALM", "nfs/server\n@REALM", "nfs/-bad@REALM", "nfs/server", "nfs/server@REALM -bad"} {
		if _, err := normalizeConfig(nfsModels.NFSConfig{Protocol: "v4", KerberosPrincipal: principal}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unsafe principal accepted: %s", principal)
		}
	}
}

func TestKerberosPolicyRequiresInstalledServiceKeysAndNamedIDs(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"))
	input := shareInput("1", "192.0.2.1")
	input.Security = []string{"krb5p"}
	if _, err := service.SaveShare(t.Context(), 0, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("keyless Kerberos share accepted: %v", err)
	}
	config := nfsModels.NFSConfig{Protocol: "v4", KerberosPrincipal: "nfs/server.example.com@EXAMPLE.COM"}
	if err := service.SetConfig(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	service.KerberosState = func(context.Context, string) (string, error) { return "", errors.New("no installed key") }
	if _, err := service.SaveShare(t.Context(), 0, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing service key accepted: %v", err)
	}
	service.KerberosState = func(ctx context.Context, principal string) (string, error) {
		if principal != config.KerberosPrincipal {
			t.Fatal("wrong principal checked")
		}
		return "installed", nil
	}
	if _, err := service.SaveShare(t.Context(), 0, input); err != nil {
		t.Fatal(err)
	}
	config.NumericIDs = true
	if err := service.SetConfig(t.Context(), config); !errors.Is(err, ErrConflict) {
		t.Fatalf("numeric-ID Kerberos policy accepted: %v", err)
	}
	config.Protocol = "v3"
	if err := service.SetConfig(t.Context(), config); err != nil {
		t.Fatalf("inactive NFSv4 numeric-ID setting prevented NFSv3 Kerberos: %v", err)
	}
}

func TestKerberosRootUsesUnionWithoutWeakeningShareSecurity(t *testing.T) {
	service := newTestService(t, testDataset("1", "tank/data", "/mnt/data"), testDataset("2", "tank/other", "/mnt/other"))
	service.KerberosState = func(context.Context, string) (string, error) { return "keys", nil }
	if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v4", KerberosPrincipal: "nfs/server@REALM"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []nfsModels.NFSShareConfig{
		{Dataset: "1", Enabled: true, Clients: []string{"192.0.2.1"}, Security: []string{"krb5p"}},
		{Dataset: "2", Enabled: true, Clients: []string{"192.0.2.2"}, Security: []string{"sys", "krb5i"}},
	} {
		if _, err := service.SaveShare(t.Context(), 0, input); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := service.PreviewExports(t.Context())
	if err != nil || strings.Count(preview.Exports, "V4:") != 1 || !strings.Contains(preview.Exports, "V4: / -sec=sys:krb5i:krb5p") || !strings.Contains(preview.Exports, "/mnt/data -sec=krb5p 192.0.2.1") || !strings.Contains(preview.Exports, "/mnt/other -sec=sys:krb5i 192.0.2.2") {
		t.Fatalf("incorrect per-share Kerberos policy: %s, %v", preview.Exports, err)
	}
}

func TestKerberosKeyDriftWithdrawsServing(t *testing.T) {
	service, driver, share := configuredRuntime(t)
	identity := "first key"
	service.KerberosState = func(context.Context, string) (string, error) { return identity, nil }
	if err := service.SetConfig(t.Context(), nfsModels.NFSConfig{Protocol: "v4", KerberosPrincipal: "nfs/server@REALM"}); err != nil {
		t.Fatal(err)
	}
	share.Security = []string{"krb5p"}
	if _, err := service.SaveShare(t.Context(), share.ID, share.NFSShareConfig); err != nil {
		t.Fatal(err)
	}
	identity = "replacement key"
	status, err := service.GetStatus(t.Context())
	if err != nil || status.CheckedAt == nil {
		t.Fatalf("missing last runtime observation: %#v, %v", status, err)
	}
	if err := service.checkRuntime(t.Context()); !errors.Is(err, ErrApplyFailed) || driver.running {
		t.Fatalf("stale Kerberos service not withdrawn: %v", err)
	}
	status, err = service.GetStatus(t.Context())
	if err != nil || status.ApplyStatus != "pending" || status.AppliedExports != 0 {
		t.Fatalf("detected stale key reported applied: %#v, %v", status, err)
	}
}

func TestNativeKerberosStartsSharedGSSAndUsesServiceHostname(t *testing.T) {
	f := newNativeFixture(t)
	exports := exportsMarker + "\nV4: / -sec=krb5p\n/mnt/data -sec=krb5p\n"
	if err := f.driver.Publish(t.Context(), exports); err != nil {
		t.Fatal(err)
	}
	state := &nfsModels.NFSRuntime{Managed: true, Hash: exportsHash(exports), KerberosHash: "installed", Filesystems: []nfsModels.ExportedFilesystem{{GUID: "1", Name: "tank/data", Mountpoint: "/mnt/data"}}}
	config := nfsModels.NFSConfig{Protocol: "v4", KerberosPrincipal: "nfs/server.example.com@EXAMPLE.COM"}
	if err := f.driver.Start(t.Context(), config, state, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.commands, "/usr/sbin/gssd ") || !slices.Contains(f.commands, "/usr/sbin/nfsd -t -P "+f.driver.nfsdPIDPath+" -V server.example.com") {
		t.Fatalf("Kerberos daemon setup: %v", f.commands)
	}
	for pid, name := range f.processes {
		if name == "gssd" {
			delete(f.processes, pid)
		}
	}
	if err := f.driver.Check(t.Context(), config, state); err == nil {
		t.Fatal("missing GSS service ignored")
	}
}
