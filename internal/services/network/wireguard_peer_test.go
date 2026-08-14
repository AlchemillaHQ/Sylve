// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
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

func seedWireGuardPeerTestServer(t *testing.T, db *gorm.DB, enabled bool) networkModels.WireGuardServer {
	t.Helper()
	privateKey := mustGenerateWireGuardPrivateKey(t)
	server := networkModels.WireGuardServer{
		Enabled:    enabled,
		Port:       51820,
		Addresses:  []string{"10.210.0.1/24"},
		PrivateKey: privateKey.String(),
		PublicKey:  privateKey.PublicKey().String(),
		MTU:        1420,
	}
	if err := db.Create(&server).Error; err != nil {
		t.Fatalf("seed wireguard server: %v", err)
	}
	if !enabled {
		if err := db.Model(&server).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable seeded wireguard server: %v", err)
		}
		server.Enabled = false
	}
	return server
}

func seedWireGuardPeerTestPeer(
	t *testing.T,
	db *gorm.DB,
	serverID uint,
	enabled bool,
	clientIP string,
) networkModels.WireGuardServerPeer {
	t.Helper()
	privateKey := mustGenerateWireGuardPrivateKey(t)
	peer := networkModels.WireGuardServerPeer{
		Name:              "existing-peer",
		Enabled:           enabled,
		WireGuardServerID: serverID,
		PrivateKey:        privateKey.String(),
		PublicKey:         privateKey.PublicKey().String(),
		ClientIPs:         []string{clientIP},
		RoutableIPs:       []string{},
	}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatalf("seed wireguard peer: %v", err)
	}
	if !enabled {
		if err := db.Model(&peer).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable seeded wireguard peer: %v", err)
		}
		peer.Enabled = false
	}
	return peer
}

func newWireGuardPeerServiceTest(t *testing.T, serverEnabled bool) (*Service, *gorm.DB, networkModels.WireGuardServer) {
	t.Helper()
	svc, db := newNetworkServiceForTest(t,
		&models.BasicSettings{},
		&networkModels.WireGuardServer{},
		&networkModels.WireGuardServerPeer{},
	)
	seedWireGuardServiceEnabled(t, db)
	server := seedWireGuardPeerTestServer(t, db, serverEnabled)
	return svc, db, server
}

func injectNextWireGuardPeerApplyFailure(t *testing.T) {
	t.Helper()
	previousConfigure := wireGuardConfigureWithWGCtrl
	previousResolveBinary := wireGuardResolveWGBinaryPath
	t.Cleanup(func() {
		wireGuardConfigureWithWGCtrl = previousConfigure
		wireGuardResolveWGBinaryPath = previousResolveBinary
	})

	failNext := true
	wireGuardConfigureWithWGCtrl = func(name string, config wgtypes.Config) error {
		if failNext {
			failNext = false
			return errors.New("injected wireguard peer apply failure")
		}
		return previousConfigure(name, config)
	}
	wireGuardResolveWGBinaryPath = func() (string, error) {
		return "", errors.New("wg binary unavailable")
	}
}

func setupWireGuardPeerRollbackTest(
	t *testing.T,
	withPeer bool,
) (*Service, *gorm.DB, networkModels.WireGuardServer, *networkModels.WireGuardServerPeer) {
	t.Helper()
	svc, db, server := newWireGuardPeerServiceTest(t, true)
	stubWireGuardServerRuntime(t)

	var peer *networkModels.WireGuardServerPeer
	if withPeer {
		seeded := seedWireGuardPeerTestPeer(t, db, server.ID, true, "10.210.0.2/32")
		peer = &seeded
	}
	if err := db.Preload("Peers").First(&server, server.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.applyWireGuardServerRuntime(&server); err != nil {
		t.Fatalf("apply initial wireguard runtime: %v", err)
	}
	injectNextWireGuardPeerApplyFailure(t)
	return svc, db, server, peer
}

func TestWireGuardServerPeerValidationAndConflicts(t *testing.T) {
	t.Run("invalid private key", func(t *testing.T) {
		svc, _, _ := newWireGuardPeerServiceTest(t, false)
		privateKey := "invalid"
		_, err := svc.AddWireGuardServerPeer(WireGuardServerPeerRequest{
			Name:       "peer",
			PrivateKey: &privateKey,
			ClientIPs:  []string{"10.210.0.2/32"},
		})
		if !errors.Is(err, ErrInvalidWireGuardServer) || WireGuardErrorCode(err) != "wireguard_invalid_peer_private_key" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("bounded name", func(t *testing.T) {
		svc, _, _ := newWireGuardPeerServiceTest(t, false)
		_, err := svc.AddWireGuardServerPeer(WireGuardServerPeerRequest{
			Name:      strings.Repeat("n", MaxWireGuardServerPeerNameBytes+1),
			ClientIPs: []string{"10.210.0.2/32"},
		})
		if !errors.Is(err, ErrInvalidWireGuardServer) || WireGuardErrorCode(err) != "wireguard_peer_name_too_long" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate public key", func(t *testing.T) {
		svc, db, server := newWireGuardPeerServiceTest(t, false)
		existing := seedWireGuardPeerTestPeer(t, db, server.ID, true, "10.210.0.2/32")
		privateKey := existing.PrivateKey
		_, err := svc.AddWireGuardServerPeer(WireGuardServerPeerRequest{
			Name:       "duplicate-key",
			PrivateKey: &privateKey,
			ClientIPs:  []string{"10.210.0.3/32"},
		})
		if !errors.Is(err, ErrWireGuardServerConflict) || WireGuardErrorCode(err) != "wireguard_peer_public_key_conflict" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate enabled allowed ip", func(t *testing.T) {
		svc, db, server := newWireGuardPeerServiceTest(t, false)
		seedWireGuardPeerTestPeer(t, db, server.ID, true, "10.210.0.2/32")
		_, err := svc.AddWireGuardServerPeer(WireGuardServerPeerRequest{
			Name:      "duplicate-ip",
			ClientIPs: []string{"10.210.0.2/32"},
		})
		if !errors.Is(err, ErrWireGuardServerConflict) || WireGuardErrorCode(err) != "wireguard_peer_allowed_ip_conflict" {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestWireGuardServerPeerEnabledStateIsIdempotent(t *testing.T) {
	svc, db, server := newWireGuardPeerServiceTest(t, true)
	stubWireGuardServerRuntime(t)
	peer := seedWireGuardPeerTestPeer(t, db, server.ID, true, "10.210.0.2/32")

	called := false
	previousConfigure := wireGuardConfigureWithWGCtrl
	wireGuardConfigureWithWGCtrl = func(string, wgtypes.Config) error {
		called = true
		return errors.New("runtime should not be applied")
	}
	t.Cleanup(func() { wireGuardConfigureWithWGCtrl = previousConfigure })

	if err := svc.SetWireGuardServerPeerEnabled(peer.ID, true); err != nil {
		t.Fatalf("repeat enabled state: %v", err)
	}
	if called {
		t.Fatal("idempotent state request reapplied the runtime")
	}
}

func TestWireGuardServerPeerRuntimeFailureRestoresDatabaseState(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		svc, db, _, _ := setupWireGuardPeerRollbackTest(t, false)
		if _, err := svc.AddWireGuardServerPeer(WireGuardServerPeerRequest{
			Name:      "new-peer",
			ClientIPs: []string{"10.210.0.2/32"},
		}); err == nil {
			t.Fatal("expected add runtime failure")
		}
		var count int64
		if err := db.Model(&networkModels.WireGuardServerPeer{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("created peer remained after rollback: count=%d", count)
		}
	})

	t.Run("edit", func(t *testing.T) {
		svc, db, _, peer := setupWireGuardPeerRollbackTest(t, true)
		id := peer.ID
		if err := svc.EditWireGuardServerPeer(WireGuardServerPeerRequest{
			ID:        &id,
			Name:      "edited-peer",
			ClientIPs: []string{"10.210.0.3/32"},
		}); err == nil {
			t.Fatal("expected edit runtime failure")
		}
		var stored networkModels.WireGuardServerPeer
		if err := db.First(&stored, peer.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Name != peer.Name || len(stored.ClientIPs) != 1 || stored.ClientIPs[0] != peer.ClientIPs[0] {
			t.Fatalf("peer edit was not rolled back: before=%+v after=%+v", peer, stored)
		}
	})

	t.Run("enabled state", func(t *testing.T) {
		svc, db, _, peer := setupWireGuardPeerRollbackTest(t, true)
		if err := svc.SetWireGuardServerPeerEnabled(peer.ID, false); err == nil {
			t.Fatal("expected state runtime failure")
		}
		var stored networkModels.WireGuardServerPeer
		if err := db.First(&stored, peer.ID).Error; err != nil {
			t.Fatal(err)
		}
		if !stored.Enabled {
			t.Fatal("peer enabled state was not rolled back")
		}
	})

	t.Run("delete", func(t *testing.T) {
		svc, db, _, peer := setupWireGuardPeerRollbackTest(t, true)
		if err := svc.RemoveWireGuardServerPeer(peer.ID); err == nil {
			t.Fatal("expected delete runtime failure")
		}
		var stored networkModels.WireGuardServerPeer
		if err := db.First(&stored, peer.ID).Error; err != nil {
			t.Fatalf("deleted peer was not restored: %v", err)
		}
		if stored.PublicKey != peer.PublicKey {
			t.Fatalf("restored peer changed: before=%+v after=%+v", peer, stored)
		}
	})
}
