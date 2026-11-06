// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"gorm.io/gorm"
)

func newPinnedTestService(t *testing.T, db *gorm.DB, nodeID string) *Service {
	t.Helper()
	cert := testClusterCert(t)
	pin := CertPin(cert.Leaf)
	if err := db.Create(&clusterModels.ClusterSSHIdentity{
		NodeUUID: nodeID, SSHHost: "10.0.0.1", PublicKey: "ssh-ed25519 AAAA", CertPin: hex.EncodeToString(pin[:]),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return withCert(t, NewClusterService(db, nil, nil).(*Service), cert)
}

func withCert(t *testing.T, s *Service, cert tls.Certificate) *Service {
	t.Helper()
	if err := s.SetClusterCert(cert); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClusterAPIRecognizesMembersByPin(t *testing.T) {
	db := newClusterServiceTestDB(t)
	server := newPinnedTestService(t, db, "node-a")
	member := newPinnedTestService(t, db, "node-b")
	server.reloadPins()
	if err := server.ReopenMutations(); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewUnstartedServer(server.PeerHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "peer="+PeerNode(r))
	})))
	api.TLS = server.ServerTLSConfig()
	api.StartTLS()
	defer api.Close()

	get := func(config *tls.Config) (string, error) {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}
		response, err := client.Get(api.URL)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return string(body), err
	}
	stranger := withCert(t, NewClusterService(newClusterServiceTestDB(t), nil, nil).(*Service), testClusterCert(t))

	if body, err := get(member.PeerTLSConfig()); err != nil || body != "peer=node-b" {
		t.Fatalf("member: %q, %v", body, err)
	}
	if body, err := get(stranger.JoinTLSConfig()); err != nil || body != "peer=" {
		t.Fatalf("stranger's cert: %q, %v", body, err)
	}
	if body, err := get(&tls.Config{InsecureSkipVerify: true}); err != nil || body != "peer=" {
		t.Fatalf("no cert: %q, %v", body, err)
	}
	if _, err := get(stranger.PeerTLSConfig()); err == nil || !strings.Contains(err.Error(), "cluster_peer_not_member") {
		t.Fatalf("a client that doesn't know the server's pin connected: %v", err)
	}
	old := member.PeerTLSConfig()
	old.MaxVersion = tls.VersionTLS12
	if _, err := get(old); err == nil {
		t.Fatal("TLS 1.2 was accepted")
	}

	if err := server.apply("cluster_member", "remove", clusterModels.ClusterMemberRemoval{NodeID: "node-b"}); err != nil {
		t.Fatal(err)
	}
	if body, err := get(member.PeerTLSConfig()); err != nil || body != "peer=" {
		t.Fatalf("a removed member is still recognized: %q, %v", body, err)
	}
}
