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
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/hashicorp/raft"
)

const (
	clusterSSHDirName         = "cluster/ssh"
	clusterSSHPrivateFileName = "id_ed25519"
	clusterSSHPublicFileName  = "id_ed25519.pub"

	clusterManagedKeyStart = "# --- sylve cluster replication keys start ---"
	clusterManagedKeyEnd   = "# --- sylve cluster replication keys end ---"
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

func (s *Service) clusterSSHPrivateKeyPath() (string, error) {
	dir, err := s.clusterSSHDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterSSHPrivateFileName), nil
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
		SSHPort:   22,
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

	if err := s.ReconcileClusterSSHAuthorizedKeys(); err != nil {
		return err
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

func replaceManagedSSHBlock(existing string, managed []string) string {
	managedSet := make(map[string]struct{}, len(managed))
	normalized := make([]string, 0, len(managed))
	for _, line := range managed {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := managedSet[line]; ok {
			continue
		}
		managedSet[line] = struct{}{}
		normalized = append(normalized, line)
	}
	sort.Strings(normalized)

	blockLines := []string{clusterManagedKeyStart}
	blockLines = append(blockLines, normalized...)
	blockLines = append(blockLines, clusterManagedKeyEnd)
	block := strings.Join(blockLines, "\n")

	start := strings.Index(existing, clusterManagedKeyStart)
	end := strings.Index(existing, clusterManagedKeyEnd)
	if start >= 0 && end > start {
		end += len(clusterManagedKeyEnd)
		left := strings.TrimRight(existing[:start], "\n")
		right := strings.TrimLeft(existing[end:], "\n")
		parts := []string{}
		if left != "" {
			parts = append(parts, left)
		}
		parts = append(parts, block)
		if right != "" {
			parts = append(parts, right)
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n")) + "\n"
	}

	base := strings.TrimSpace(existing)
	if base == "" {
		return block + "\n"
	}
	return base + "\n\n" + block + "\n"
}

func (s *Service) ReconcileClusterSSHAuthorizedKeys() error {
	identities, err := s.ListClusterSSHIdentities()
	if err != nil {
		return err
	}

	managed := make([]string, 0, len(identities))
	for _, identity := range identities {
		pub := strings.TrimSpace(identity.PublicKey)
		if pub == "" {
			continue
		}
		managed = append(managed, pub)
	}

	sshDir := "/root/.ssh"
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("root_ssh_dir_create_failed: %w", err)
	}
	if err := os.Chmod(sshDir, 0700); err != nil {
		return fmt.Errorf("root_ssh_dir_chmod_failed: %w", err)
	}

	authKeysPath := filepath.Join(sshDir, "authorized_keys")
	existing := ""
	if raw, readErr := os.ReadFile(authKeysPath); readErr == nil {
		existing = string(raw)
	}

	next := replaceManagedSSHBlock(existing, managed)
	if err := os.WriteFile(authKeysPath, []byte(next), 0600); err != nil {
		return fmt.Errorf("authorized_keys_write_failed: %w", err)
	}
	if err := os.Chmod(authKeysPath, 0600); err != nil {
		return fmt.Errorf("authorized_keys_chmod_failed: %w", err)
	}

	logger.L.Debug().
		Int("managed_keys", len(managed)).
		Str("path", authKeysPath).
		Msg("cluster_ssh_authorized_keys_reconciled")

	return nil
}
