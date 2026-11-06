// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterModels

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFSMDispatcherClusterSSHIdentityCommands(t *testing.T) {
	db := newClusterModelTestDB(t, &ClusterSSHIdentity{})
	fsm := NewFSMDispatcher(db)
	RegisterDefaultHandlers(fsm)

	t.Run("upsert new identity", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterSSHIdentity{
			NodeUUID: "node-uuid-1", SSHUser: "root",
			SSHHost: "192.168.1.1", SSHPort: 8183,
			PublicKey: "ssh-ed25519 AAAAC3...",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "cluster_ssh_identity", Action: "upsert", Data: raw,
		}); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}

		var identity ClusterSSHIdentity
		if err := db.Where("node_uuid = ?", "node-uuid-1").First(&identity).Error; err != nil {
			t.Fatalf("fetch identity: %v", err)
		}
		if identity.SSHHost != "192.168.1.1" {
			t.Fatalf("host mismatch: %q", identity.SSHHost)
		}
		if identity.PublicKey != "ssh-ed25519 AAAAC3..." {
			t.Fatalf("public key mismatch: %q", identity.PublicKey)
		}
	})

	t.Run("upsert same node_uuid updates key", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterSSHIdentity{
			NodeUUID: "node-uuid-1", SSHUser: "admin",
			SSHHost: "10.0.0.1", SSHPort: 22,
			PublicKey: "ssh-ed25519 BBBB...",
		})
		if err := applyFSMCommand(t, fsm, Command{
			Type: "cluster_ssh_identity", Action: "upsert", Data: raw,
		}); err != nil {
			t.Fatalf("upsert update failed: %v", err)
		}

		var count int64
		db.Model(&ClusterSSHIdentity{}).Where("node_uuid = ?", "node-uuid-1").Count(&count)
		if count != 1 {
			t.Fatalf("expected 1 identity, got %d", count)
		}

		var identity ClusterSSHIdentity
		db.Where("node_uuid = ?", "node-uuid-1").First(&identity)
		if identity.PublicKey != "ssh-ed25519 BBBB..." {
			t.Fatalf("public key not updated: %q", identity.PublicKey)
		}
		if identity.SSHUser != "admin" {
			t.Fatalf("ssh user not updated: %q", identity.SSHUser)
		}
		if identity.SSHHost != "10.0.0.1" {
			t.Fatalf("host not updated: %q", identity.SSHHost)
		}
		if identity.SSHPort != 22 {
			t.Fatalf("port not updated: %d", identity.SSHPort)
		}
	})

	t.Run("upsert empty public_key fails validation", func(t *testing.T) {
		raw, _ := json.Marshal(ClusterSSHIdentity{
			NodeUUID: "node-uuid-2", SSHHost: "host2",
			SSHUser: "root", PublicKey: "",
		})
		err := applyFSMCommand(t, fsm, Command{
			Type: "cluster_ssh_identity", Action: "upsert", Data: raw,
		})
		if err == nil {
			t.Fatal("expected validation error for empty public_key, got nil")
		}
		if !strings.Contains(err.Error(), "pubkey") {
			t.Fatalf("expected pubkey error, got: %v", err)
		}
	})

	t.Run("cert pin is stored, replaced and validated", func(t *testing.T) {
		pin := strings.Repeat("ab", 32)
		for _, want := range []string{pin, strings.Repeat("cd", 32)} {
			raw, _ := json.Marshal(ClusterSSHIdentity{
				NodeUUID: "node-uuid-3", SSHHost: "10.0.0.3", PublicKey: "ssh-ed25519 CCCC...",
				CertPin: strings.ToUpper(want),
			})
			if err := applyFSMCommand(t, fsm, Command{Type: "cluster_ssh_identity", Action: "upsert", Data: raw}); err != nil {
				t.Fatalf("upsert pin: %v", err)
			}
			var identity ClusterSSHIdentity
			if err := db.First(&identity, "node_uuid = ?", "node-uuid-3").Error; err != nil || identity.CertPin != want {
				t.Fatalf("pin = %q (err %v), want %q", identity.CertPin, err, want)
			}
		}
		for _, bad := range []string{"abcd", strings.Repeat("zz", 32)} {
			raw, _ := json.Marshal(ClusterSSHIdentity{
				NodeUUID: "node-uuid-3", SSHHost: "10.0.0.3", PublicKey: "ssh-ed25519 CCCC...", CertPin: bad,
			})
			err := applyFSMCommand(t, fsm, Command{Type: "cluster_ssh_identity", Action: "upsert", Data: raw})
			if err == nil || !strings.Contains(err.Error(), "cert_pin_invalid") {
				t.Fatalf("pin %q: error %v, want cert_pin_invalid", bad, err)
			}
		}
	})

	t.Run("malformed payload returns error", func(t *testing.T) {
		err := applyFSMCommand(t, fsm, Command{
			Type: "cluster_ssh_identity", Action: "upsert",
			Data: json.RawMessage(`"bad-payload"`),
		})
		if err == nil {
			t.Fatal("expected error for malformed payload, got nil")
		}
	})
}
