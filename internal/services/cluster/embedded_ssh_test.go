// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestParseExecRequestPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    string
		wantErr bool
	}{
		{"empty", []byte{}, "", true},
		{"too short", []byte{0, 0, 0}, "", true},
		{"size exceeds payload", func() []byte {
			b := make([]byte, 6)
			binary.BigEndian.PutUint32(b[0:4], 100)
			return b
		}(), "", true},
		{"single command", sshPayload("echo hello"), "echo hello", false},
		{"empty command", sshPayload(""), "", false},
		{"multi word", sshPayload("/usr/bin/env FOO=bar"), "/usr/bin/env FOO=bar", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseExecRequestPayload(tt.payload)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExitCodeFromErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want uint32
	}{
		{"nil", nil, 0},
		{"killed", fmt.Errorf("signal: killed"), 137},
		{"terminated", fmt.Errorf("signal: terminated"), 143},
		{"interrupt", fmt.Errorf("signal: interrupt"), 130},
		{"generic", fmt.Errorf("something broke"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFromErr(tt.err); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("exit error with code", func(t *testing.T) {
		err := &exec.ExitError{}
		got := exitCodeFromErr(err)
		if got != 1 {
			t.Fatalf("expected 1 from zero ExitError (ExitCode returns -1, falls through), got %d", got)
		}
	})
}

func TestEmbeddedSSHPublicKeyCallback(t *testing.T) {
	db := testutil.NewSQLiteTestDB(t, &clusterModels.ClusterSSHIdentity{})
	svc := &Service{DB: db}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	pubKey := signer.PublicKey()
	pubKeyWire := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey)))

	if err := db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID:  "node-a",
		SSHUser:   "root",
		SSHHost:   "10.0.0.1",
		SSHPort:   8183,
		PublicKey: pubKeyWire,
	}).Error; err != nil {
		t.Fatalf("failed to seed identity: %v", err)
	}

	conn := &fakeSSHConnMeta{user: "root"}

	perms, err := svc.embeddedSSHPublicKeyCallback(conn, pubKey)
	if err != nil {
		t.Fatalf("callback failed: %v", err)
	}
	if perms == nil || perms.Extensions["node_uuid"] != "node-a" {
		t.Fatalf("unexpected permissions: %+v", perms)
	}

	_, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate second key: %v", err)
	}
	signer2, err := ssh.NewSignerFromKey(priv2)
	if err != nil {
		t.Fatalf("failed to create second signer: %v", err)
	}
	pubKey2 := signer2.PublicKey()

	_, err = svc.embeddedSSHPublicKeyCallback(conn, pubKey2)
	if err == nil || !strings.Contains(err.Error(), "unauthorized_key") {
		t.Fatalf("expected unauthorized_key error for unknown key, got %v", err)
	}

	nonRoot := &fakeSSHConnMeta{user: "operator"}
	_, err = svc.embeddedSSHPublicKeyCallback(nonRoot, pubKey)
	if err == nil || !strings.Contains(err.Error(), "invalid_user") {
		t.Fatalf("expected invalid_user error, got %v", err)
	}
}

type fakeSSHConnMeta struct {
	user          string
	sessionID     []byte
	clientVersion string
	serverVersion string
	remoteAddr    net.Addr
	localAddr     net.Addr
}

func (f *fakeSSHConnMeta) User() string         { return f.user }
func (f *fakeSSHConnMeta) SessionID() []byte     { return f.sessionID }
func (f *fakeSSHConnMeta) ClientVersion() []byte { return []byte(f.clientVersion) }
func (f *fakeSSHConnMeta) ServerVersion() []byte { return []byte(f.serverVersion) }
func (f *fakeSSHConnMeta) RemoteAddr() net.Addr  { return f.remoteAddr }
func (f *fakeSSHConnMeta) LocalAddr() net.Addr   { return f.localAddr }

func sshPayload(cmd string) []byte {
	b := make([]byte, 4+len(cmd))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(cmd)))
	copy(b[4:], cmd)
	return b
}
