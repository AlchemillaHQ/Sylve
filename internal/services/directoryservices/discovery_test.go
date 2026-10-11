// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package directoryservices

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryRejectsInvalidInputWithoutCommands(t *testing.T) {
	s := newTestService(t)
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		t.Fatal("invalid discovery used DNS")
		return "", nil, nil
	}
	for _, input := range []ADDiscoveryInput{{Domain: "--option=wrong"}, {Domain: "example.com", Site: "bad.site"}, {Domain: "example.com", Site: "bad site"}, {Domain: "example.com", DomainController: "--wrong"}} {
		if _, err := s.DiscoverAD(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid discovery accepted: %v", err)
		}
	}
}

func TestControllersSiteFallbackOrderingAndBounds(t *testing.T) {
	s := newTestService(t)
	var queries []string
	s.lookupSRV = func(ctx context.Context, service, protocol, name string) (string, []*net.SRV, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > controllerAttemptTimeout || service != "ldap" || protocol != "tcp" {
			t.Fatal("unbounded or incorrect DNS discovery")
		}
		queries = append(queries, name)
		if strings.Contains(name, "_sites") {
			return "", nil, errors.New("site has no records")
		}
		return "", []*net.SRV{nil, {Target: ".", Port: 389}, {Target: "first.example.com.", Port: 389}, {Target: "FIRST.example.com.", Port: 389}, {Target: "last.example.com.", Port: 389}}, nil
	}
	controllers, warning, err := s.controllers(context.Background(), ADDiscoveryInput{Domain: "EXAMPLE.COM", Site: "Branch", DomainController: "preferred.example.com"})
	if err != nil || !reflect.DeepEqual(controllers, []string{"preferred.example.com", "first.example.com", "last.example.com"}) || !strings.Contains(warning, "domain-wide") {
		t.Fatalf("site fallback: %v %s %v", controllers, warning, err)
	}
	if !reflect.DeepEqual(queries, []string{"Branch._sites.dc._msdcs.example.com", "dc._msdcs.example.com"}) {
		t.Fatalf("incorrect SRV queries: %v", queries)
	}
}

func TestDiscoveryUsesReachableControllerAndRealNetBIOSName(t *testing.T) {
	s := newTestService(t)
	before, _ := s.GetAD()
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", []*net.SRV{{Target: "offline.example.com.", Port: 389}, {Target: "dc.example.com.", Port: 389}}, nil
	}
	s.run = func(_ context.Context, path string, args []string, input string, env []string) (string, error) {
		if path != "/usr/local/bin/net" || !slices.Contains(args, "lookup") || input != "" {
			t.Fatal("discovery attempted authentication or mutation")
		}
		if slices.Contains(args, "--server=offline.example.com") {
			return "", errors.New("offline")
		}
		return "Domain: EXAMPLE.COM\nPre-Win2k Domain: CORP\nClient Site Name: Branch\n", nil
	}
	result, err := s.DiscoverAD(context.Background(), ADDiscoveryInput{Domain: " example.com ", Site: "Branch"})
	if err != nil || result.Workgroup != "CORP" || result.Site != "Branch" || result.DomainController != "dc.example.com" {
		t.Fatalf("discovery: %+v %v", result, err)
	}
	after, _ := s.GetAD()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("metadata discovery saved configuration")
	}
	input := testADInput()
	input.Workgroup = ""
	ad, err := s.SaveAD(context.Background(), input)
	if err != nil || ad.Workgroup != "CORP" || ad.Site != "" || ad.Joined {
		t.Fatalf("automatic workgroup save: %s %v", ad.Workgroup, err)
	}
}

func TestAutomaticSiteDiscoveryPrefersLocalController(t *testing.T) {
	s := newTestService(t)
	s.lookupSRV = func(_ context.Context, _, _, name string) (string, []*net.SRV, error) {
		if strings.Contains(name, "Branch._sites") {
			return "", []*net.SRV{{Target: "local.example.com.", Port: 389}}, nil
		}
		return "", []*net.SRV{{Target: "remote.example.com.", Port: 389}}, nil
	}
	var servers []string
	s.run = func(_ context.Context, _ string, args []string, _ string, _ []string) (string, error) {
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "--server="); ok {
				servers = append(servers, value)
			}
		}
		return "Domain: example.com\nPre-Win2k Domain: CORP\nClient Site Name: Branch\n", nil
	}
	result, err := s.DiscoverAD(context.Background(), ADDiscoveryInput{Domain: "example.com"})
	if err != nil || result.DomainController != "local.example.com" || !reflect.DeepEqual(servers, []string{"remote.example.com", "local.example.com"}) {
		t.Fatalf("automatic site preference: %+v %v %v", result, servers, err)
	}
}

func TestDiscoveryRejectsControllerForAnotherDomain(t *testing.T) {
	s := newTestService(t)
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "Domain: another.example.com\nPre-Win2k Domain: ANOTHER\n", nil
	}
	if _, err := s.DiscoverAD(context.Background(), ADDiscoveryInput{Domain: "example.com"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign domain was accepted: %v", err)
	}
}

func TestMultiControllerHealthReportsVerifiedAlternate(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.JoinWarning = joinDNSRegistrationWarning
	if err := s.DB.Save(&ad).Error; err != nil {
		t.Fatal(err)
	}
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", []*net.SRV{{Target: "dc1.example.com.", Port: 389}, {Target: "dc2.example.com.", Port: 389}}, nil
	}
	healthy := healthyCommands(t, s, "")
	var verified []string
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		text := strings.Join(args, " ")
		if slices.Contains(args, "--dc-info") {
			return "dc2.example.com 192.0.2.2", nil
		}
		if strings.Contains(text, "ads testjoin") {
			for _, arg := range args {
				if host, ok := strings.CutPrefix(arg, "--server="); ok {
					verified = append(verified, host)
				}
			}
		}
		return healthy(ctx, path, args, input, env)
	}
	for range 2 {
		status, err := s.Status(context.Background(), true)
		if err != nil || status.State != "HEALTHY" || status.DomainController != "dc2.example.com" || status.AlternateController != "dc1.example.com" || !status.AlternateVerified || status.Warning != joinDNSRegistrationWarning {
			t.Fatalf("controller health: %+v %v", status, err)
		}
	}
	if !reflect.DeepEqual(verified, []string{"dc2.example.com", "dc1.example.com", "dc2.example.com", "dc1.example.com"}) {
		t.Fatalf("trust verification order: %v", verified)
	}
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", nil, errors.New("DNS offline")
	}
	status, err := s.Status(context.Background(), true)
	if err != nil || status.State != "FAULTED" || status.AlternateVerified || status.DomainController != "" || status.Warning != joinDNSRegistrationWarning {
		t.Fatalf("failed refresh retained healthy controller metadata: %+v %v", status, err)
	}
}

func TestSingleControllerHealthWarnsWithoutFailingMembership(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.run = healthyCommands(t, s, "")
	status, err := s.Status(context.Background(), true)
	if err != nil || status.State != "HEALTHY" || status.AlternateVerified || !strings.Contains(status.Warning, "redundancy is unverified") {
		t.Fatalf("single controller health: %+v %v", status, err)
	}
}

func TestControllerHealthFallsBackWhenPreferredCannotAuthenticate(t *testing.T) {
	s := newTestService(t)
	ad := saveTestAD(t, s, true)
	ad.DomainController = "offline.example.com"
	healthy := healthyCommands(t, s, "")
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if slices.Contains(args, "--server=offline.example.com") {
			return "", errors.New("offline")
		}
		return healthy(ctx, path, args, input, env)
	}
	result, err := s.checkControllers(context.Background(), ad)
	if err != nil || result.DomainController != "dc.example.com" || result.AlternateVerified {
		t.Fatalf("preferred controller fallback: %+v %v", result, err)
	}
}

func TestDiscoveryCancellationStopsFurtherControllerAttempts(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	s.run = func(ctx context.Context, _ string, _ []string, _ string, _ []string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if _, err := s.DiscoverAD(ctx, ADDiscoveryInput{Domain: "example.com"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancelled discovery: %v", err)
	}
}

func TestAlternateControllerTimeoutDoesNotFaultHealthyPrimary(t *testing.T) {
	s := newTestService(t)
	saveTestAD(t, s, true)
	s.lookupSRV = func(context.Context, string, string, string) (string, []*net.SRV, error) {
		return "", []*net.SRV{{Target: "dc1.example.com.", Port: 389}, {Target: "dc2.example.com.", Port: 389}}, nil
	}
	healthy := healthyCommands(t, s, "")
	s.run = func(ctx context.Context, path string, args []string, input string, env []string) (string, error) {
		if slices.Contains(args, "--server=dc2.example.com") {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return healthy(ctx, path, args, input, env)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	status, err := s.Status(ctx, true)
	if err != nil || status.State != "HEALTHY" || status.DomainController != "dc1.example.com" || status.AlternateVerified {
		t.Fatalf("optional alternate timeout poisoned primary health: %+v %v", status, err)
	}
}

func TestDiscoveryWarnsWhenSelectedSiteFallsBackToAnotherSite(t *testing.T) {
	s := newTestService(t)
	s.run = func(context.Context, string, []string, string, []string) (string, error) {
		return "Domain: example.com\nPre-Win2k Domain: EXAMPLE\nClient Site Name: Branch\nServer Site Name: Headquarters\n", nil
	}
	result, err := s.DiscoverAD(context.Background(), ADDiscoveryInput{Domain: "example.com", Site: "Branch"})
	if err != nil || !strings.Contains(result.Warning, "another site") {
		t.Fatalf("cross-site fallback warning: %s %v", result.Warning, err)
	}
}

func TestAutomaticSiteDiscoveryKeepsOverallControllerLimit(t *testing.T) {
	for _, offline := range []int{0, maxControllers - 1} {
		t.Run(fmt.Sprintf("offline-%d", offline), func(t *testing.T) {
			s := newTestService(t)
			s.lookupSRV = func(_ context.Context, _, _, name string) (string, []*net.SRV, error) {
				prefix := "domain"
				if strings.Contains(name, "_sites") {
					prefix = "site"
				}
				var records []*net.SRV
				for i := range maxControllers + 4 {
					records = append(records, &net.SRV{Target: fmt.Sprintf("%s%d.example.com.", prefix, i), Port: 389})
				}
				return "", records, nil
			}
			attempts := 0
			s.run = func(_ context.Context, _ string, _ []string, _ string, _ []string) (string, error) {
				attempts++
				if attempts <= offline {
					return "", errors.New("offline")
				}
				return "Domain: example.com\nPre-Win2k Domain: EXAMPLE\nClient Site Name: Branch\n", nil
			}
			result, err := s.DiscoverAD(context.Background(), ADDiscoveryInput{Domain: "example.com"})
			if err != nil || attempts > maxControllers || len(result.Controllers) > maxControllers {
				t.Fatalf("site rediscovery exceeded controller limit: %d attempts, %d controllers, %v", attempts, len(result.Controllers), err)
			}
		})
	}
}
