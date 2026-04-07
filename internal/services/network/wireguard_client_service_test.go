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
	"strings"
	"testing"

	"github.com/alchemillahq/sylve/internal/db/models"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

func TestCreateWireGuardClientRequiresPrivateKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)

	enabled := false
	peerKey := mustGenerateWireGuardPrivateKey(t).PublicKey().String()
	err := svc.CreateWireGuardClient(&WireGuardClientRequest{
		Name:          "client-no-private-key",
		Enabled:       &enabled,
		EndpointHost:  "198.51.100.10",
		EndpointPort:  51820,
		PrivateKey:    "",
		PeerPublicKey: peerKey,
		AllowedIPs:    []string{"10.10.0.0/16"},
		Addresses:     []string{"10.210.1.2/32"},
	})
	if !errors.Is(err, ErrWireGuardClientPrivateKeyReq) {
		t.Fatalf("expected missing private key error, got: %v", err)
	}
}

func TestCreateWireGuardClientRejectsInvalidPrivateKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)

	enabled := false
	err := svc.CreateWireGuardClient(&WireGuardClientRequest{
		Name:          "client-invalid-private-key",
		Enabled:       &enabled,
		EndpointHost:  "198.51.100.10",
		EndpointPort:  51820,
		PrivateKey:    "invalid-key",
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{"10.10.0.0/16"},
		Addresses:     []string{"10.210.1.2/32"},
	})
	if err == nil {
		t.Fatal("expected invalid private key error")
	}
	if !strings.Contains(err.Error(), "invalid_wireguard_private_key") {
		t.Fatalf("expected invalid private key error, got: %v", err)
	}
}

func TestEditWireGuardClientRequiresPrivateKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)

	privateKey := mustGenerateWireGuardPrivateKey(t)
	client := networkModels.WireGuardClient{
		Enabled:       false,
		Name:          "client-edit-no-key",
		EndpointHost:  "198.51.100.10",
		EndpointPort:  51820,
		PrivateKey:    privateKey.String(),
		PublicKey:     privateKey.PublicKey().String(),
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{"10.10.0.0/16"},
		Addresses:     []string{"10.210.1.2/32"},
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed client: %v", err)
	}

	err := svc.EditWireGuardClient(&WireGuardClientRequest{
		ID:         &client.ID,
		PrivateKey: "",
	})
	if !errors.Is(err, ErrWireGuardClientPrivateKeyReq) {
		t.Fatalf("expected missing private key error, got: %v", err)
	}
}

func TestEditWireGuardClientRejectsInvalidPrivateKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)

	privateKey := mustGenerateWireGuardPrivateKey(t)
	client := networkModels.WireGuardClient{
		Enabled:       false,
		Name:          "client-edit-invalid-key",
		EndpointHost:  "198.51.100.10",
		EndpointPort:  51820,
		PrivateKey:    privateKey.String(),
		PublicKey:     privateKey.PublicKey().String(),
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{"10.10.0.0/16"},
		Addresses:     []string{"10.210.1.2/32"},
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed client: %v", err)
	}

	err := svc.EditWireGuardClient(&WireGuardClientRequest{
		ID:         &client.ID,
		PrivateKey: "invalid-key",
	})
	if err == nil {
		t.Fatal("expected invalid private key error")
	}
	if !strings.Contains(err.Error(), "invalid_wireguard_private_key") {
		t.Fatalf("expected invalid private key error, got: %v", err)
	}
}

func TestCreateWireGuardClientUsesProvidedPrivateKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardClientRuntime(t)

	enabled := false
	privateKey := mustGenerateWireGuardPrivateKey(t)
	peerPublicKey := mustGenerateWireGuardPrivateKey(t).PublicKey().String()

	err := svc.CreateWireGuardClient(&WireGuardClientRequest{
		Name:          "client-provided-key",
		Enabled:       &enabled,
		EndpointHost:  "198.51.100.11",
		EndpointPort:  51820,
		PrivateKey:    privateKey.String(),
		PeerPublicKey: peerPublicKey,
		AllowedIPs:    []string{"10.20.0.0/16"},
		Addresses:     []string{"10.220.1.2/32"},
	})
	if err != nil {
		t.Fatalf("expected create to succeed, got: %v", err)
	}

	var created networkModels.WireGuardClient
	if err := db.Where("name = ?", "client-provided-key").First(&created).Error; err != nil {
		t.Fatalf("failed to load created client: %v", err)
	}

	if created.PrivateKey != privateKey.String() {
		t.Fatalf("expected private key to be stored as provided")
	}
	if created.PublicKey != privateKey.PublicKey().String() {
		t.Fatalf("expected public key derived from provided private key")
	}
}

func TestCreateWireGuardClientStoresFIB(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardClientRuntime(t)

	enabled := false
	fib := uint(3)
	privateKey := mustGenerateWireGuardPrivateKey(t)
	peerPublicKey := mustGenerateWireGuardPrivateKey(t).PublicKey().String()

	err := svc.CreateWireGuardClient(&WireGuardClientRequest{
		Name:          "client-fib-create",
		Enabled:       &enabled,
		EndpointHost:  "198.51.100.11",
		EndpointPort:  51820,
		PrivateKey:    privateKey.String(),
		PeerPublicKey: peerPublicKey,
		AllowedIPs:    []string{"10.20.0.0/16"},
		Addresses:     []string{"10.220.1.2/32"},
		FIB:           &fib,
	})
	if err != nil {
		t.Fatalf("expected create to succeed, got: %v", err)
	}

	var created networkModels.WireGuardClient
	if err := db.Where("name = ?", "client-fib-create").First(&created).Error; err != nil {
		t.Fatalf("failed to load created client: %v", err)
	}
	if created.FIB != fib {
		t.Fatalf("expected client fib=%d, got %d", fib, created.FIB)
	}
}

func TestEditWireGuardClientUpdatesDerivedPublicKey(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardClientRuntime(t)

	oldPrivateKey := mustGenerateWireGuardPrivateKey(t)
	client := networkModels.WireGuardClient{
		Enabled:       false,
		Name:          "client-key-rotate",
		EndpointHost:  "198.51.100.12",
		EndpointPort:  51820,
		PrivateKey:    oldPrivateKey.String(),
		PublicKey:     oldPrivateKey.PublicKey().String(),
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{},
		Addresses:     []string{"10.230.1.2/32"},
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed client: %v", err)
	}

	newPrivateKey := mustGenerateWireGuardPrivateKey(t)
	err := svc.EditWireGuardClient(&WireGuardClientRequest{
		ID:         &client.ID,
		PrivateKey: newPrivateKey.String(),
	})
	if err != nil {
		t.Fatalf("expected edit to succeed, got: %v", err)
	}

	var updated networkModels.WireGuardClient
	if err := db.First(&updated, client.ID).Error; err != nil {
		t.Fatalf("failed to load updated client: %v", err)
	}

	if updated.PrivateKey != newPrivateKey.String() {
		t.Fatalf("expected updated private key to match request")
	}
	if updated.PublicKey != newPrivateKey.PublicKey().String() {
		t.Fatalf("expected updated public key to be derived from private key")
	}
}

func TestEditWireGuardClientUpdatesFIB(t *testing.T) {
	svc, db := newNetworkServiceForTest(t, &models.BasicSettings{}, &networkModels.WireGuardClient{})
	seedWireGuardServiceEnabled(t, db)
	stubWireGuardClientRuntime(t)

	privateKey := mustGenerateWireGuardPrivateKey(t)
	client := networkModels.WireGuardClient{
		Enabled:       false,
		Name:          "client-fib-edit",
		EndpointHost:  "198.51.100.12",
		EndpointPort:  51820,
		PrivateKey:    privateKey.String(),
		PublicKey:     privateKey.PublicKey().String(),
		PeerPublicKey: mustGenerateWireGuardPrivateKey(t).PublicKey().String(),
		AllowedIPs:    []string{},
		Addresses:     []string{"10.230.1.2/32"},
		FIB:           1,
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("failed to seed client: %v", err)
	}

	newPrivateKey := mustGenerateWireGuardPrivateKey(t)
	fib := uint(4)
	err := svc.EditWireGuardClient(&WireGuardClientRequest{
		ID:         &client.ID,
		PrivateKey: newPrivateKey.String(),
		FIB:        &fib,
	})
	if err != nil {
		t.Fatalf("expected edit to succeed, got: %v", err)
	}

	var updated networkModels.WireGuardClient
	if err := db.First(&updated, client.ID).Error; err != nil {
		t.Fatalf("failed to load updated client: %v", err)
	}
	if updated.FIB != fib {
		t.Fatalf("expected updated fib=%d, got %d", fib, updated.FIB)
	}
}

func seedWireGuardServiceEnabled(t *testing.T, db *gorm.DB) {
	t.Helper()

	basic := models.BasicSettings{
		Services: []models.AvailableService{models.WireGuard},
	}
	if err := db.Create(&basic).Error; err != nil {
		t.Fatalf("failed to seed basic settings: %v", err)
	}
}

func stubWireGuardClientRuntime(t *testing.T) {
	t.Helper()

	previousRunCommand := wireGuardRunCommand
	previousConfigureWithWGCtrl := wireGuardConfigureWithWGCtrl
	previousHasAddress := wireGuardInterfaceHasAddress
	t.Cleanup(func() {
		wireGuardRunCommand = previousRunCommand
		wireGuardConfigureWithWGCtrl = previousConfigureWithWGCtrl
		wireGuardInterfaceHasAddress = previousHasAddress
	})

	runtime := newFakeWireGuardRuntime()
	wireGuardRunCommand = runtime.runCommand
	wireGuardConfigureWithWGCtrl = func(string, wgtypes.Config) error {
		return nil
	}
	wireGuardInterfaceHasAddress = func(string, string) (bool, error) {
		return true, nil
	}
}
