// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	"gorm.io/gorm"
)

func failStaticRouteDBOperation(t *testing.T, db *gorm.DB, operation string) {
	t.Helper()
	callbackName := "test:fail_static_route_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	callback := func(tx *gorm.DB) {
		tx.AddError(fmt.Errorf("simulated_%s_failure", operation))
	}

	var err error
	switch operation {
	case "update":
		err = db.Callback().Update().Before("gorm:update").Register(callbackName, callback)
		t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })
	case "delete":
		err = db.Callback().Delete().Before("gorm:delete").Register(callbackName, callback)
		t.Cleanup(func() { _ = db.Callback().Delete().Remove(callbackName) })
	default:
		t.Fatalf("unsupported DB operation %q", operation)
	}
	if err != nil {
		t.Fatalf("register %s failure callback: %v", operation, err)
	}
}

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
	previous := getNetFIBCountFunc
	getNetFIBCountFunc = func() (int64, error) {
		return int64(fibs), nil
	}
	t.Cleanup(func() {
		getNetFIBCountFunc = previous
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
		{
			name: "gateway zone with inet family",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "zone-inet",
				DestinationType: "network",
				Destination:     "0.0.0.0/0",
				Family:          "inet",
				NextHopMode:     "gateway",
				Gateway:         "198.51.100.1",
				GatewayZone:     "em0",
			},
			want: "gateway_zone_requires_inet6",
		},
		{
			name: "gateway zone with non link local gateway",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "zone-global",
				DestinationType: "network",
				Destination:     "::/0",
				Family:          "inet6",
				NextHopMode:     "gateway",
				Gateway:         "2001:db8::1",
				GatewayZone:     "em0",
			},
			want: "gateway_zone_requires_link_local",
		},
		{
			name: "gateway includes inline zone",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "inline-zone",
				DestinationType: "network",
				Destination:     "::/0",
				Family:          "inet6",
				NextHopMode:     "gateway",
				Gateway:         "fe80::1%em0",
			},
			want: "route_gateway_must_not_include_zone",
		},
		{
			name: "gateway zone in interface mode",
			req: networkServiceInterfaces.UpsertStaticRouteRequest{
				Name:            "iface-zone",
				DestinationType: "network",
				Destination:     "::/0",
				Family:          "inet6",
				NextHopMode:     "interface",
				Interface:       "em0",
				GatewayZone:     "em0",
			},
			want: "gateway_zone_not_allowed_for_next_hop_mode_interface",
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
	mockStaticRouteFIBCount(t, 4)
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
		Enabled:         true,
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

func TestReconcileManagedRoutesDoesNotDeleteDisabledRoutes(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	if err := db.Create(&networkModels.StaticRoute{
		Name:            "disabled",
		Enabled:         false,
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "0.0.0.0/0",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopGateway,
		Gateway:         "192.0.2.1",
	}).Error; err != nil {
		t.Fatal(err)
	}

	callCount := 0
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		callCount++
		return "", nil
	})
	if err := svc.ReconcileManagedRoutes(); err != nil {
		t.Fatalf("reconcile disabled routes: %v", err)
	}
	if callCount != 0 {
		t.Fatalf("disabled route reconciliation issued %d runtime commands", callCount)
	}
}

func TestValidateStaticRouteRequestAcceptsLinkLocalGatewayWithZone(t *testing.T) {
	mockStaticRouteFIBCount(t, 4)

	req := &networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "v6 default",
		DestinationType: "network",
		Destination:     "::/0",
		Family:          "inet6",
		NextHopMode:     "gateway",
		Gateway:         "fe80::200:17ff:fe44:663a",
		GatewayZone:     "dSkxOdFn",
	}

	route, err := validateStaticRouteRequest(req)
	if err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if route.GatewayZone != "dSkxOdFn" {
		t.Fatalf("expected gateway zone preserved, got %q", route.GatewayZone)
	}
	if strings.Contains(route.Gateway, "%") {
		t.Fatalf("expected gateway without zone, got %q", route.Gateway)
	}
}

func TestAddManagedRouteBuildsScopedGatewayCommand(t *testing.T) {
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		got := strings.Join(append([]string{command}, args...), " ")
		expected := "/sbin/route -n -6 add -net ::/0 fe80::200:17ff:fe44:663a%dSkxOdFn"
		if got != expected {
			t.Fatalf("unexpected command: got %q want %q", got, expected)
		}
		return "", nil
	})

	route := &networkModels.StaticRoute{
		FIB:             0,
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "::/0",
		Family:          staticRouteFamilyINET6,
		NextHopMode:     staticRouteNextHopGateway,
		Gateway:         "fe80::200:17ff:fe44:663a",
		GatewayZone:     "dSkxOdFn",
	}
	if err := addManagedRoute(route); err != nil {
		t.Fatalf("expected add route success, got: %v", err)
	}
}

func TestAddManagedRouteTreatsExistingRouteAsConflict(t *testing.T) {
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		return "", fmt.Errorf("route: writing to routing socket: File exists")
	})
	route := &networkModels.StaticRoute{
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "192.0.2.0/24",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopInterface,
		Interface:       "em0",
	}
	if err := addManagedRoute(route); !errors.Is(err, ErrStaticRouteConflict) {
		t.Fatalf("expected existing kernel route conflict, got %v", err)
	}
}

func TestCreateStaticRouteRejectsDuplicateEnabledDestination(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	existing := networkModels.StaticRoute{
		Name:            "host",
		Enabled:         true,
		DestinationType: staticRouteDestinationHost,
		Destination:     "192.0.2.10",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopInterface,
		Interface:       "em0",
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	enabled := true
	_, err := svc.CreateStaticRoute(&networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "same-prefix",
		Enabled:         &enabled,
		DestinationType: "network",
		Destination:     "192.0.2.10/32",
		Family:          "inet",
		NextHopMode:     "interface",
		Interface:       "em1",
	})
	if !errors.Is(err, ErrStaticRouteConflict) {
		t.Fatalf("expected duplicate route conflict, got %v", err)
	}

	var count int64
	if err := db.Model(&networkModels.StaticRoute{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("conflicting route was persisted, count=%d", count)
	}
}

func TestCreateStaticRouteValidatesAndResolvesObjects(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StaticRoute{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)
	destination := networkModels.Object{
		Name: "destination", Type: "Network",
		Entries: []networkModels.ObjectEntry{{Value: "198.51.100.0/24"}},
	}
	gateway := networkModels.Object{
		Name: "gateway", Type: "Host",
		Entries: []networkModels.ObjectEntry{{Value: "192.0.2.1"}},
	}
	if err := db.Create(&destination).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&gateway).Error; err != nil {
		t.Fatal(err)
	}

	disabled := false
	id, err := svc.CreateStaticRoute(&networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:             "object route",
		Enabled:          &disabled,
		DestinationType:  "network",
		DestinationObjID: &destination.ID,
		Family:           "inet",
		NextHopMode:      "gateway",
		GatewayObjID:     &gateway.ID,
	})
	if err != nil {
		t.Fatalf("create object-backed route: %v", err)
	}

	var route networkModels.StaticRoute
	if err := db.First(&route, id).Error; err != nil {
		t.Fatal(err)
	}
	if route.Destination != "198.51.100.0/24" || route.Gateway != "192.0.2.1" {
		t.Fatalf("object values were not resolved: %+v", route)
	}
	if route.DestinationRaw != "" || route.GatewayRaw != "" {
		t.Fatalf("object-backed route retained misleading raw values: %+v", route)
	}

	_, err = svc.CreateStaticRoute(&networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:             "wrong type",
		Enabled:          &disabled,
		DestinationType:  "network",
		DestinationObjID: &gateway.ID,
		Family:           "inet",
		NextHopMode:      "interface",
		Interface:        "em0",
	})
	if !errors.Is(err, ErrInvalidStaticRoute) {
		t.Fatalf("expected invalid destination object type, got %v", err)
	}
}

func TestEditStaticRouteRestoresRuntimeWhenDBSaveFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	route := networkModels.StaticRoute{
		Name:            "before",
		Enabled:         true,
		DestinationType: staticRouteDestinationNetwork,
		Destination:     "192.0.2.0/24",
		DestinationRaw:  "192.0.2.0/24",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopInterface,
		Interface:       "em0",
	}
	if err := db.Create(&route).Error; err != nil {
		t.Fatal(err)
	}

	commands := make([]string, 0)
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{command}, args...), " "))
		return "", nil
	})
	failStaticRouteDBOperation(t, db, "update")
	enabled := true
	err := svc.EditStaticRoute(route.ID, &networkServiceInterfaces.UpsertStaticRouteRequest{
		Name:            "after",
		Enabled:         &enabled,
		DestinationType: "network",
		Destination:     "198.51.100.0/24",
		Family:          "inet",
		NextHopMode:     "interface",
		Interface:       "em1",
	})
	if err == nil {
		t.Fatal("expected simulated DB update failure")
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"delete -net 192.0.2.0/24 -iface em0",
		"add -net 198.51.100.0/24 -iface em1",
		"delete -net 198.51.100.0/24 -iface em1",
		"add -net 192.0.2.0/24 -iface em0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing rollback command %q in:\n%s", want, joined)
		}
	}

	var stored networkModels.StaticRoute
	if err := db.First(&stored, route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Destination != route.Destination || stored.Interface != route.Interface {
		t.Fatalf("failed edit changed stored route: %+v", stored)
	}
}

func TestDeleteStaticRouteRestoresRuntimeWhenDBDeleteFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &networkModels.StaticRoute{})
	route := networkModels.StaticRoute{
		Name:            "keep",
		Enabled:         true,
		DestinationType: staticRouteDestinationHost,
		Destination:     "203.0.113.10",
		Family:          staticRouteFamilyINET,
		NextHopMode:     staticRouteNextHopGateway,
		Gateway:         "192.0.2.1",
	}
	if err := db.Create(&route).Error; err != nil {
		t.Fatal(err)
	}

	commands := make([]string, 0)
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{command}, args...), " "))
		return "", nil
	})
	failStaticRouteDBOperation(t, db, "delete")
	if err := svc.DeleteStaticRoute(route.ID); err == nil {
		t.Fatal("expected simulated DB delete failure")
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, "delete -host 203.0.113.10 192.0.2.1") ||
		!strings.Contains(joined, "add -host 203.0.113.10 192.0.2.1") {
		t.Fatalf("runtime route was not restored:\n%s", joined)
	}

	var count int64
	if err := db.Model(&networkModels.StaticRoute{}).Where("id = ?", route.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("failed delete removed DB row, count=%d", count)
	}
}

func TestReconcileObjectStaticRoutesRestoresRuntimeWhenDBUpdateFails(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.StaticRoute{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
	)
	object := networkModels.Object{
		Name: "new-network", Type: "Network",
		Entries: []networkModels.ObjectEntry{{Value: "198.51.100.0/24"}},
	}
	if err := db.Create(&object).Error; err != nil {
		t.Fatal(err)
	}
	route := networkModels.StaticRoute{
		Name:             "object route",
		Enabled:          true,
		DestinationType:  staticRouteDestinationNetwork,
		Destination:      "192.0.2.0/24",
		DestinationObjID: &object.ID,
		Family:           staticRouteFamilyINET,
		NextHopMode:      staticRouteNextHopInterface,
		Interface:        "em0",
	}
	if err := db.Create(&route).Error; err != nil {
		t.Fatal(err)
	}

	commands := make([]string, 0)
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{command}, args...), " "))
		return "", nil
	})
	failStaticRouteDBOperation(t, db, "update")
	if err := svc.ReconcileObjectStaticRoutes(object.ID); err == nil {
		t.Fatal("expected simulated DB update failure")
	}
	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"delete -net 192.0.2.0/24 -iface em0",
		"add -net 198.51.100.0/24 -iface em0",
		"delete -net 198.51.100.0/24 -iface em0",
		"add -net 192.0.2.0/24 -iface em0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing object rollback command %q in:\n%s", want, joined)
		}
	}

	var stored networkModels.StaticRoute
	if err := db.First(&stored, route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Destination != "192.0.2.0/24" {
		t.Fatalf("failed reconciliation changed DB destination: %s", stored.Destination)
	}
}

func TestStaticRouteSuggestionBuildsScopedIPv6Gateway(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallNATRule{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.WireGuardClient{},
	)
	rule := networkModels.FirewallNATRule{
		Name: "IPv6 policy", NATType: "snat", PolicyRoutingEnabled: true,
		EgressInterfaces: []string{"em0"}, Family: "inet6", SourceRaw: "2001:db8:1::/64",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	mockStaticRouteFIBCount(t, 4)
	mockStaticRouteRunCommand(t, func(command string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-6 get 2001:db8:1::1") {
			t.Fatalf("unexpected route probe: %s %s", command, joined)
		}
		return "gateway: fe80::1%em0\ninterface: em0\n", nil
	})

	suggestions, err := svc.SuggestStaticRoutesFromNATRule(rule.ID)
	if err != nil {
		t.Fatalf("suggest route: %v", err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("unexpected suggestions: %+v", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.NextHopMode != "gateway" || suggestion.Gateway != "fe80::1" ||
		suggestion.GatewayZone != "em0" || suggestion.Interface != "" {
		t.Fatalf("invalid scoped gateway suggestion: %+v", suggestion)
	}
}

func TestStaticRouteSuggestionRejectsMissingWireGuardClient(t *testing.T) {
	svc, db := newNetworkServiceForTest(t,
		&networkModels.FirewallNATRule{},
		&networkModels.Object{},
		&networkModels.ObjectEntry{},
		&networkModels.WireGuardClient{},
	)
	rule := networkModels.FirewallNATRule{
		Name: "missing client", NATType: "snat", PolicyRoutingEnabled: true,
		EgressInterfaces: []string{"wgc999"}, Family: "inet", SourceRaw: "192.0.2.0/24",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	_, err := svc.SuggestStaticRoutesFromNATRule(rule.ID)
	if !errors.Is(err, ErrStaticRouteSuggestionUnavailable) {
		t.Fatalf("expected missing WireGuard client conflict, got %v", err)
	}
}

func TestRouteCandidateDeduplicationAndHostPrefixProbe(t *testing.T) {
	candidates := make([]staticRouteCandidate, 0)
	seen := map[string]struct{}{}
	addRouteCandidate(&candidates, seen, staticRouteCandidate{
		DestinationType: "network", Destination: "192.0.2.10/32", SourceHint: "raw",
	}, "inet")
	addRouteCandidate(&candidates, seen, staticRouteCandidate{
		DestinationType: "network", Destination: "192.0.2.10/32", SourceHint: "object",
	}, "inet")
	if len(candidates) != 1 {
		t.Fatalf("duplicate route candidate was retained: %+v", candidates)
	}
	if target := routeProbeTargetForCandidate(candidates[0]); target != "192.0.2.10" {
		t.Fatalf("/32 probe escaped its prefix: %s", target)
	}
}
