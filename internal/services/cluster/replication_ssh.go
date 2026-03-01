// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	clusterSSHDirName         = "cluster/ssh"
	clusterSSHPrivateFileName = "id_ed25519"
	clusterSSHPublicFileName  = "id_ed25519.pub"

	ClusterEmbeddedSSHPort = 8122
)

func (s *Service) clusterSSHDir() (string, error) {
	dataPath, err := config.GetDataPath()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dataPath, clusterSSHDirName)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return "", err
	}

	return path, nil
}

func (s *Service) ClusterSSHPrivateKeyPath() (string, error) {
	privatePath, _, _, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		return "", err
	}
	return privatePath, nil
}

func (s *Service) ensureLocalClusterSSHKeyPair() (string, string, string, error) {
	dir, err := s.clusterSSHDir()
	if err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_dir_failed: %w", err)
	}

	privatePath := filepath.Join(dir, clusterSSHPrivateFileName)
	publicPath := filepath.Join(dir, clusterSSHPublicFileName)

	privateOK := false
	publicOK := false
	if fi, statErr := os.Stat(privatePath); statErr == nil && !fi.IsDir() {
		privateOK = true
	}
	if fi, statErr := os.Stat(publicPath); statErr == nil && !fi.IsDir() {
		publicOK = true
	}

	if !privateOK || !publicOK {
		detail := s.Detail()
		keyComment := "sylve-cluster"
		if detail != nil && strings.TrimSpace(detail.NodeID) != "" {
			keyComment = "sylve-cluster-" + strings.TrimSpace(detail.NodeID)
		}

		_, keyErr := utils.RunCommand(
			"ssh-keygen",
			"-q",
			"-t", "ed25519",
			"-N", "",
			"-C", keyComment,
			"-f", privatePath,
		)
		if keyErr != nil {
			return "", "", "", fmt.Errorf("cluster_ssh_keygen_failed: %w", keyErr)
		}
	}

	if err := os.Chmod(privatePath, 0600); err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_private_chmod_failed: %w", err)
	}
	if err := os.Chmod(publicPath, 0644); err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_public_chmod_failed: %w", err)
	}

	pubRaw, err := os.ReadFile(publicPath)
	if err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_pubkey_read_failed: %w", err)
	}
	pubKey := strings.TrimSpace(string(pubRaw))
	if pubKey == "" {
		return "", "", "", fmt.Errorf("cluster_ssh_pubkey_empty")
	}

	return privatePath, publicPath, pubKey, nil
}

func (s *Service) localClusterSSHHost() string {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err == nil {
		if strings.TrimSpace(c.RaftIP) != "" {
			return strings.TrimSpace(c.RaftIP)
		}
	}

	if detail := s.Detail(); detail != nil && strings.TrimSpace(detail.Hostname) != "" {
		return strings.TrimSpace(detail.Hostname)
	}

	return "127.0.0.1"
}

func (s *Service) EnsureAndPublishLocalSSHIdentity() error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err == nil {
		if !c.Enabled {
			return nil
		}
	}

	_, _, pubKey, err := s.ensureLocalClusterSSHKeyPair()
	if err != nil {
		return err
	}

	detail := s.Detail()
	if detail == nil || strings.TrimSpace(detail.NodeID) == "" {
		return fmt.Errorf("node_id_unavailable")
	}

	identity := clusterModels.ClusterSSHIdentity{
		NodeUUID:  strings.TrimSpace(detail.NodeID),
		SSHUser:   "root",
		SSHHost:   s.localClusterSSHHost(),
		SSHPort:   ClusterEmbeddedSSHPort,
		PublicKey: pubKey,
	}

	if s.Raft != nil && s.Raft.State() != raft.Leader {
		if err := s.forwardSSHIdentityToLeader(identity); err != nil {
			return err
		}
	} else {
		if err := s.UpsertClusterSSHIdentity(identity, s.Raft == nil); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) forwardSSHIdentityToLeader(identity clusterModels.ClusterSSHIdentity) error {
	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	leaderAddr, _ := s.Raft.LeaderWithID()
	if leaderAddr == "" {
		_, electedLeaderAddr, waitErr := s.waitUntilLeader(10 * time.Second)
		if electedLeaderAddr != "" {
			leaderAddr = electedLeaderAddr
		}
		if leaderAddr == "" {
			if waitErr != nil {
				return fmt.Errorf("leader_unknown: %w", waitErr)
			}
			return fmt.Errorf("leader_unknown")
		}
	}

	host, _, err := net.SplitHostPort(string(leaderAddr))
	if err != nil {
		host = string(leaderAddr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("leader_host_unknown")
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cluster"
	}

	clusterToken, err := s.AuthService.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return fmt.Errorf("create_cluster_token_failed: %w", err)
	}

	url := fmt.Sprintf("https://%s:%d/api/cluster/replication/internal/ssh-identity", host, config.ParsedConfig.Port)
	headers := map[string]string{
		"Accept":          "application/json",
		"Content-Type":    "application/json",
		"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := utils.HTTPPostJSON(url, identity, headers); err == nil {
			return nil
		} else {
			lastErr = err
		}

		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}

	return fmt.Errorf("forward_ssh_identity_to_leader_failed: %w", lastErr)
}
