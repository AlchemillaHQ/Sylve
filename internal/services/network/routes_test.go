// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
)

func mockStaticRouteRunCommand(t *testing.T, fn func(command string, args ...string) (string, error)) {
	t.Helper()
	previous := staticRouteRunCommand
	staticRouteRunCommand = fn
	t.Cleanup(func() {
		staticRouteRunCommand = previous
	})
}

func mockStaticRouteFIBCount(t *testing.T, fibs int) {
	t.Helper()
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		if command == "/sbin/sysctl" && len(args) == 2 && args[0] == "-n" && args[1] == "net.fibs" {
			return fmt.Sprintf("%d\n", fibs), nil
		}
		return "", nil
	})
}

func TestValidateStaticRouteRequestAcceptsNetworkViaInterface(t *testing.T) {
	mockStaticRouteFIBCount(t, 4)

	req := &networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "LAN return",
		DestinationType: "network",
		Destination:     "192.168.180.0/24",
		Family:          "inet",
		NextHopMode:     "interface",
		Interface:       "bridge0",
		FIB:             func(v uint) *uint { return &v }(1),
	}

	route, err := validateStaticRouteRequest(req)
	if err != nil {
		t.Fatalf("expected request validation success, got: %v", err)
	}
	if route.FIB != 1 {
		t.Fatalf("expected fib=1, got %d", route.FIB)
	}
	if route.Destination != "192.168.180.0/24" {
		t.Fatalf("unexpected destination normalization: %q", route.Destination)
	}
}

func TestValidateStaticRouteRequestAcceptsHostViaGateway(t *testing.T) {
	mockStaticRouteFIBCount(t, 4)

	req := &networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "Host route",
		DestinationType: "host",
		Destination:     "203.0.113.10",
		Family:          "inet",
		NextHopMode:     "gateway",
		Gateway:         "198.51.100.1",
	}

	_, err := validateStaticRouteRequest(req)
	if err != nil {
		t.Fatalf("expected request validation success, got: %v", err)
	}
}

func TestValidateStaticRouteRequestRejectsInvalidShapes(t *testing.T) {
	mockStaticRouteFIBCount(t, 2)

	cases := []struct {
		name string
		req  networkServiceInterfaces.UpsertStaticRouteRequest
		want string
	}{
		{
			name: "host with cidr",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "bad-host",
				DestinationType: "host",
				Destination:     "10.0.0.1/24",
				Family:          "inet",
				NextHopMode:     "interface",
				Interface:       "em0",
			},
			want: "host_destination_must_not_contain_cidr",
		},
		{
			name: "network without cidr",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "bad-network",
				DestinationType: "network",
				Destination:     "10.0.0.1",
				Family:          "inet",
				NextHopMode:     "interface",
				Interface:       "em0",
			},
			want: "invalid_network_destination",
		},
		{
			name: "family mismatch",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "family-mismatch",
				DestinationType: "host",
				Destination:     "2001:db8::10",
				Family:          "inet",
				NextHopMode:     "gateway",
				Gateway:         "198.51.100.1",
			},
			want: "destination_family_mismatch",
		},
		{
			name: "invalid fib",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "invalid-fib",
				DestinationType: "network",
				Destination:     "10.0.0.0/24",
				Family:          "inet",
				NextHopMode:     "interface",
				Interface:       "em0",
				FIB:             func(v uint) *uint { return &v }(4),
			},
			want: "invalid_route_fib",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateStaticRouteRequest(&tc.req)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAddManagedRouteBuildsSetfibNetInterfaceCommand(t *testing.T) {
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		got := strings.Join(append([]string{command}, args...), " ")
		expected := "/usr/sbin/setfib -F 1 /sbin/route -n add -net 192.168.180.0/24 -iface bridge0"
		if got != expected {
			t.Fatalf("unexpected command: got %q want %q", got, expected)
		}
		return "", nil
	})

	route := &networkModels.StaticRoute{
		FIB:             1,
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "192.168.180.0/24",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopInterface,
		Interface:       "bridge0",
	}
	if err := addManagedRoute(route); err != nil {
		t.Fatalf("expected add route success, got: %v", err)
	}
}

func TestAddManagedRouteBuildsSetfibHostGatewayCommand(t *testing.T) {
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		got := strings.Join(append([]string{command}, args...), " ")
		expected := "/usr/sbin/setfib -F 2 /sbin/route -n add -host 203.0.113.10 198.51.100.1"
		if got != expected {
			t.Fatalf("unexpected command: got %q want %q", got, expected)
		}
		return "", nil
	})

	route := &networkModels.StaticRoute{
		FIB:             2,
		DestinationType: staticRouteDestinationHost,
		Destination:     "203.0.113.10",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopGateway,
		Gateway:         "198.51.100.1",
	}
	if err := addManagedRoute(route); err != nil {
		t.Fatalf("expected add route success, got: %v", err)
	}
}

func TestCreateStaticRouteFailsWithoutPersistingWhenApplyFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		if command == "/sbin/sysctl" {
			return "4\n", nil
		}
		return "", fmt.Errorf("simulated apply failure")
	})

	req := &networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "will-fail",
		DestinationType: "network",
		Destination:     "10.1.0.0/24",
		Family:          "inet",
		NextHopMode:     "interface",
		Interface:       "bridge0",
		Enabled:         func(v bool) *bool { return &v }(true),
	}

	_, err := svc.CreateStaticRoute(req)
	if err == nil {
		t.Fatal("expected create to fail when apply fails")
	}

	var count int64
	if db.Model(&networkModels.StaticRoute{}).Count(&count).Error != nil {
		t.Fatal("failed to query route count")
	}
	if count != 0 {
		t.Fatalf("expected failed create to rollback row, got count=%d", count)
	}
}

func TestReconcileManagedRoutesReturnsErrorButContinues(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	if err := db.Create(&networkModels.StaticRoute{
		Name:            "bad",
		Enabled:         true,
		FIB:             1,
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "192.168.180.0/24",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopInterface,
		Interface:       "bridge0",
	}).Error; err != nil {
		t.Fatalf("failed to seed route: %v", err)
	}
	if err := db.Create(&networkModels.StaticRoute{
		Name:            "good",
		Enabled:         false,
		FIB:             0,
		DestinationType: staticRouteDestinationHost,
		Destination:     "203.0.113.5",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopGateway,
		Gateway:         "198.51.100.1",
	}).Error; err != nil {
		t.Fatalf("failed to seed route: %v", err)
	}

	callCount := 0
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		callCount++
		if strings.Contains(strings.Join(args, " "), "192.168.180.0/24") {
			return "", fmt.Errorf("simulated route add failure")
		}
		return "", nil
	})

	err := svc.ReconcileManagedRoutes()
	if err == nil {
		t.Fatal("expected reconcile to return error when a managed route fails")
	}
	if callCount < 2 {
		t.Fatalf("expected reconcile to continue processing routes, got %d calls", callCount)
	}
}
