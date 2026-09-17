// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd

package network

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	iface "github.com/alchemillahq/sylve/pkg/network/iface"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func requireHostInterfaceL3NativeFixture(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping Host IP native integration test in short mode")
	}
	if os.Geteuid() != 0 {
		t.Skip("Host IP native integration test requires root")
	}
	if _, err := exec.LookPath("/sbin/ifconfig"); err != nil {
		t.Skipf("required command /sbin/ifconfig is unavailable: %v", err)
	}

	output, err := utils.RunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		t.Skipf("epair fixtures are unavailable: %v", err)
	}
	name := strings.TrimSpace(output)
	if name == "" {
		t.Skip("epair create returned no interface name")
	}

	t.Cleanup(func() {
		_, _ = utils.RunCommand("/sbin/ifconfig", name, "destroy")
	})

	if _, err := utils.RunCommand("/sbin/ifconfig", name, "up"); err != nil {
		t.Fatalf("bring up epair fixture %s: %v", name, err)
	}

	return name
}

func useHostInterfaceL3NativeSeams(t *testing.T) {
	t.Helper()

	originalGet := syncIfaceGet
	originalRun := syncRunCommand
	originalList := hostInterfaceL3ListInterfaces
	originalSeam := hostInterfaceL3EligibilitySeam
	t.Cleanup(func() {
		syncIfaceGet = originalGet
		syncRunCommand = originalRun
		hostInterfaceL3ListInterfaces = originalList
		hostInterfaceL3EligibilitySeam = originalSeam
	})

	syncIfaceGet = iface.Get
	syncRunCommand = utils.RunCommand
	hostInterfaceL3ListInterfaces = iface.List
	hostInterfaceL3EligibilitySeam = func(interfaceObj *iface.Interface) (string, bool) {
		if interfaceObj != nil && strings.HasPrefix(interfaceObj.Name, "epair") {
			return "", true
		}
		return "", false
	}
}

func hostInterfaceL3LiveInterface(t *testing.T, name string) *iface.Interface {
	t.Helper()

	interfaceObj, err := iface.Get(name)
	if err != nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	if interfaceObj == nil {
		t.Fatalf("interface %s disappeared during the test", name)
	}
	return interfaceObj
}

func hostInterfaceL3HasIPv4(t *testing.T, name string, address string) bool {
	t.Helper()

	interfaceObj := hostInterfaceL3LiveInterface(t, name)
	for _, candidate := range interfaceObj.IPv4 {
		if candidate.IP.String() == address {
			return true
		}
	}
	return false
}

func TestIntegrationHostInterfaceL3StaticLifecycle(t *testing.T) {
	name := requireHostInterfaceL3NativeFixture(t)
	useHostInterfaceL3NativeSeams(t)

	svc, db := hostInterfaceL3TestDB(t)

	mtu := uint(1400)
	metric := uint(100)
	request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{
		MTU:    &mtu,
		Metric: &metric,
		Addresses: []networkServiceInterfaces.HostInterfaceL3AddressInput{
			{Address: "198.18.0.10/24"},
		},
	}

	entry, err := svc.SaveHostInterfaceL3(name, request)
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if entry.Phase != networkModels.PendingApplyPhaseApplied {
		t.Fatalf("expected an applied pending operation, got %+v", entry)
	}

	live := hostInterfaceL3LiveInterface(t, name)
	if !hostInterfaceL3HasIPv4(t, name, "198.18.0.10") {
		t.Fatalf("expected 198.18.0.10 to be applied, got %+v", live.IPv4)
	}
	if live.MTU != 1400 {
		t.Fatalf("expected MTU 1400, got %d", live.MTU)
	}
	if live.Metric != 100 {
		t.Fatalf("expected metric 100, got %d", live.Metric)
	}

	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}
	var row networkModels.HostInterfaceL3
	if err := db.Where("interface = ?", name).First(&row).Error; err != nil {
		t.Fatalf("load promoted row: %v", err)
	}
	if row.Revision != 1 || row.MTU == nil || *row.MTU != 1400 {
		t.Fatalf("unexpected promoted row %+v", row)
	}

	if _, err := utils.RunCommand("/sbin/ifconfig", name, "inet", "198.18.0.10", "delete"); err != nil {
		t.Fatalf("remove address for drift test: %v", err)
	}
	if err := svc.ReapplyHostInterfaceL3(name); err != nil {
		t.Fatalf("ReapplyHostInterfaceL3: %v", err)
	}
	if !hostInterfaceL3HasIPv4(t, name, "198.18.0.10") {
		t.Fatal("expected reapply to restore the missing address")
	}

	deleteEntry, err := svc.DeleteHostInterfaceL3(name, 1)
	if err != nil {
		t.Fatalf("DeleteHostInterfaceL3: %v", err)
	}
	if hostInterfaceL3HasIPv4(t, name, "198.18.0.10") {
		t.Fatal("expected the address to be removed during delete")
	}
	live = hostInterfaceL3LiveInterface(t, name)
	if live.MTU != 1500 {
		t.Fatalf("expected the MTU baseline (1500) to be restored, got %d", live.MTU)
	}

	if err := svc.ConfirmHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3(delete): %v", err)
	}
	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Where("interface = ?", name).Count(&rowCount).Error; err != nil {
		t.Fatalf("count rows after delete: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected the row to be deleted, got %d", rowCount)
	}
}

func TestIntegrationHostInterfaceL3TimeoutRevert(t *testing.T) {
	name := requireHostInterfaceL3NativeFixture(t)
	useHostInterfaceL3NativeSeams(t)

	svc, db := hostInterfaceL3TestDB(t)

	request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{
		Addresses: []networkServiceInterfaces.HostInterfaceL3AddressInput{
			{Address: "198.19.0.10/24"},
		},
	}
	if _, err := svc.SaveHostInterfaceL3(name, request); err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if !hostInterfaceL3HasIPv4(t, name, "198.19.0.10") {
		t.Fatal("expected the address to be applied before the timeout")
	}

	if err := svc.ExpireHostInterfaceL3Pending(hostInterfaceL3Now().Add(HostInterfaceL3ConfirmationWindow + 1)); err != nil {
		t.Fatalf("ExpireHostInterfaceL3Pending: %v", err)
	}
	if hostInterfaceL3HasIPv4(t, name, "198.19.0.10") {
		t.Fatal("expected the timeout to revert the applied address")
	}

	var rowCount int64
	if err := db.Model(&networkModels.HostInterfaceL3{}).Count(&rowCount).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("expected no promoted row after a timeout, got %d", rowCount)
	}
}

func TestIntegrationHostInterfaceL3IPv6SettlesDAD(t *testing.T) {
	name := requireHostInterfaceL3NativeFixture(t)
	useHostInterfaceL3NativeSeams(t)

	svc, _ := hostInterfaceL3TestDB(t)

	request := networkServiceInterfaces.HostInterfaceL3UpdateRequest{
		Addresses: []networkServiceInterfaces.HostInterfaceL3AddressInput{
			{Address: "2001:db8::10/64"},
		},
	}
	entry, err := svc.SaveHostInterfaceL3(name, request)
	if err != nil {
		t.Fatalf("SaveHostInterfaceL3: %v", err)
	}
	if entry.Phase != networkModels.PendingApplyPhaseApplied {
		t.Fatalf("expected an applied operation after DAD settled, got %+v", entry)
	}

	interfaceObj := hostInterfaceL3LiveInterface(t, name)
	found := false
	for _, address := range interfaceObj.IPv6 {
		if address.IP.String() == "2001:db8::10" {
			found = true
			if address.Tentative || address.Duplicated {
				t.Fatalf("expected DAD to be settled, got %+v", address)
			}
		}
	}
	if !found {
		t.Fatalf("expected 2001:db8::10 to be applied, got %+v", interfaceObj.IPv6)
	}

	if err := svc.ConfirmHostInterfaceL3(entry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3: %v", err)
	}

	deleteEntry, err := svc.DeleteHostInterfaceL3(name, 1)
	if err != nil {
		t.Fatalf("DeleteHostInterfaceL3: %v", err)
	}
	if err := svc.ConfirmHostInterfaceL3(deleteEntry.ID); err != nil {
		t.Fatalf("ConfirmHostInterfaceL3(delete): %v", err)
	}
}
