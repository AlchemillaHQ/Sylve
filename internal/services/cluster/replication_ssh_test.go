// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"golang.org/x/crypto/ssh"
)

func TestClusterSSHDir(t *testing.T) {
	dir := t.TempDir()
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	s := withFSM(&Service{DB: newClusterServiceTestDB(t)})

	sshDir, err := s.clusterSSHDir()
	if err != nil {
		t.Fatalf("clusterSSHDir: %v", err)
	}

	expectedSuffix := filepath.Join("cluster", "ssh")
	if !strings.HasSuffix(sshDir, expectedSuffix) {
		t.Fatalf("expected path to end with %q, got %q", expectedSuffix, sshDir)
	}

	if info, err := os.Stat(sshDir); err != nil || !info.IsDir() {
		t.Fatalf("expected directory to exist at %q, err=%v", sshDir, err)
	}
}

func TestClusterSSHPrivateKeyPath(t *testing.T) {
	dir := t.TempDir()
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	s := withFSM(&Service{DB: newClusterServiceTestDB(t)})

	privatePath, err := s.ClusterSSHPrivateKeyPath()
	if err != nil {
		t.Fatalf("ClusterSSHPrivateKeyPath: %v", err)
	}

	if !strings.HasSuffix(privatePath, "id_ed25519") {
		t.Fatalf("expected path to end with id_ed25519, got %q", privatePath)
	}

	if info, err := os.Stat(privatePath); err != nil || info.IsDir() {
		t.Fatalf("expected private key file, err=%v", err)
	}
}

func TestEnsureLocalClusterSSHKeyPair(t *testing.T) {
	dir := t.TempDir()
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	s := withFSM(&Service{DB: newClusterServiceTestDB(t)})

	privatePath, publicPath, pubKey, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		t.Fatalf("ensureLocalClusterSSHKeyPair: %v", err)
	}

	if privatePath == "" || publicPath == "" || pubKey == "" {
		t.Fatal("expected non-empty return values")
	}

	if !strings.HasPrefix(pubKey, "ssh-ed25519 ") {
		t.Fatalf("expected ssh-ed25519 key, got %q", pubKey)
	}

	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(privateRaw)
	if err != nil {
		t.Fatal(err)
	}
	if pubKey != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) {
		t.Fatal("published key does not match the signer")
	}

	if info, err := os.Stat(privatePath); err != nil || info.IsDir() {
		t.Fatalf("private key file check: err=%v", err)
	}
	if info, err := os.Stat(publicPath); err != nil || info.IsDir() {
		t.Fatalf("public key file check: err=%v", err)
	}

	// second call reuses existing keys
	privatePath2, _, pubKey2, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		t.Fatalf("second ensureLocalClusterSSHKeyPair: %v", err)
	}
	if privatePath2 != privatePath || pubKey2 != pubKey {
		t.Fatal("second call should reuse existing keypair")
	}
}

func TestEnsureLocalClusterSSHKeyPairRejectsCorruptPrivateKey(t *testing.T) {
	dir := t.TempDir()
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	s := withFSM(&Service{DB: newClusterServiceTestDB(t)})

	// generate initial keypair
	privatePath, publicPath, pubKey1, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		t.Fatalf("initial keygen: %v", err)
	}

	// corrupt the private key
	if err := os.WriteFile(privatePath, []byte("corrupted"), 0600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, _, _, err = s.ensureLocalClusterSSHKeyPair()
	if err == nil || !strings.Contains(err.Error(), "cluster_ssh_private_parse_failed") {
		t.Fatalf("corrupt key accepted: %v", err)
	}

	// now delete both and verify regeneration
	os.Remove(privatePath)
	os.Remove(publicPath)

	privatePath3, _, pubKey3, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		t.Fatalf("regeneration after delete: %v", err)
	}
	if pubKey3 == pubKey1 {
		t.Fatal("expected new key after deletion")
	}
	if info, err := os.Stat(privatePath3); err != nil || info.IsDir() {
		t.Fatalf("new private key: err=%v", err)
	}
}

func TestLocalClusterSSHHost(t *testing.T) {
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	s := withFSM(&Service{DB: db})

	// no cluster row -> falls back to 127.0.0.1
	if got := s.localClusterSSHHost(); got == "" {
		t.Fatal("expected fallback hostname")
	}

	// with cluster row, uses RaftIP
	db.Create(&clusterModels.Cluster{
		RaftIP: "10.0.0.1",
	})
	if got := s.localClusterSSHHost(); got != "10.0.0.1" {
		t.Fatalf("expected RaftIP, got %q", got)
	}

	// empty RaftIP falls back
	db.Exec("UPDATE clusters SET raft_ip = ''")
	host := s.localClusterSSHHost()
	if host == "" {
		t.Fatal("expected non-empty fallback host")
	}
}

// A clustered node whose Raft isn't running must not write the replicated
// identity table locally: the next snapshot restore would silently drop it.
func TestEnsureAndPublishLocalSSHIdentityNeedsRaftWhenClustered(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{}, &clusterModels.ClusterSSHIdentity{})
	service := withFSM(&Service{DB: db})
	if err := db.Create(&clusterModels.Cluster{
		Enabled: true, RaftIP: "10.0.0.1",
	}).Error; err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	err := service.EnsureAndPublishLocalSSHIdentity(t.Context())
	if err != nil && strings.Contains(err.Error(), "node_id_unavailable") {
		t.Skipf("Detail() unavailable in test environment: %v", err)
	}
	if err == nil {
		t.Fatal("publish succeeded without Raft on a clustered node")
	}

	identities, err := service.ListClusterSSHIdentities()
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(identities) != 0 {
		t.Fatalf("identity written locally: %+v", identities)
	}
}

func TestEnsureAndPublishLocalSSHIdentityClusterNotEnabled(t *testing.T) {
	dir := t.TempDir()
	oldVal := os.Getenv("SYLVE_DATA_PATH")
	os.Setenv("SYLVE_DATA_PATH", dir)
	defer os.Setenv("SYLVE_DATA_PATH", oldVal)

	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	s := withFSM(&Service{DB: db})

	db.Create(&clusterModels.Cluster{Enabled: false})

	err := s.EnsureAndPublishLocalSSHIdentity(t.Context())
	if err != nil {
		t.Fatalf("expected no error when cluster not enabled, got: %v", err)
	}

	var count int64
	db.Model(&clusterModels.ClusterSSHIdentity{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no identity published for disabled cluster, got %d", count)
	}
}

func TestLegacySSHPortIsRepublishedThroughRaft(t *testing.T) {
	t.Setenv("SYLVE_DATA_PATH", t.TempDir())
	detail := (&Service{}).Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		t.Skip("Detail() unavailable in this environment")
	}
	db := newClusterServiceTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Enabled: true, RaftIP: "192.0.2.10"}).Error; err != nil {
		t.Fatal(err)
	}
	service := withFSM(&Service{DB: db, NodeID: detail.NodeID})
	if err := service.SetClusterCert(testClusterCert(t)); err != nil {
		t.Fatal(err)
	}
	instance := startSoleVoter(t, service, "192.0.2.10:8180")
	_, _, publicKey, err := service.ensureLocalClusterSSHKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: detail.NodeID, SSHUser: "root", SSHHost: "192.0.2.10", SSHPort: 8122, PublicKey: publicKey,
	}).Error; err != nil {
		t.Fatal(err)
	}

	before := instance.AppliedIndex()
	if err := service.EnsureAndPublishLocalSSHIdentity(t.Context()); err != nil {
		t.Fatal(err)
	}
	var identity clusterModels.ClusterSSHIdentity
	if err := db.First(&identity, "node_uuid = ?", detail.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if identity.SSHPort != ClusterEmbeddedSSHPort || instance.AppliedIndex() <= before {
		t.Fatalf("port = %d, applied %d → %d; want %d through Raft", identity.SSHPort, before, instance.AppliedIndex(), ClusterEmbeddedSSHPort)
	}
	if identity.CertPin == "" || identity.CertPin != service.localCertPin() {
		t.Fatalf("published pin %q, want this node's %q", identity.CertPin, service.localCertPin())
	}
}
