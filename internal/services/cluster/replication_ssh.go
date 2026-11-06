// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
	"golang.org/x/crypto/ssh"
)

const (
	clusterSSHDirName         = "cluster/ssh"
	clusterSSHPrivateFileName = "id_ed25519"
	clusterSSHPublicFileName  = "id_ed25519.pub"
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

func (s *Service) CleanLocalClusterSSHKeys() error {
	dataPath, err := config.GetDataPath()
	if err != nil {
		return fmt.Errorf("cluster_ssh_data_path_failed: %w", err)
	}
	dir := filepath.Join(filepath.Clean(dataPath), clusterSSHDirName)
	for _, name := range []string{clusterSSHPrivateFileName, clusterSSHPublicFileName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cluster_ssh_key_remove_failed: %w", err)
		}
	}
	return nil
}

func (s *Service) ensureLocalClusterSSHKeyPair() (string, string, string, error) {
	dir, err := s.clusterSSHDir()
	if err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_dir_failed: %w", err)
	}

	privatePath := filepath.Join(dir, clusterSSHPrivateFileName)
	publicPath := filepath.Join(dir, clusterSSHPublicFileName)

	privateOK := false
	if fi, statErr := os.Stat(privatePath); statErr == nil && !fi.IsDir() {
		privateOK = true
	}
	if !privateOK {
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
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_private_read_failed: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(privateRaw)
	if err != nil {
		return "", "", "", fmt.Errorf("cluster_ssh_private_parse_failed: %w", err)
	}
	pubKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if existing, readErr := os.ReadFile(publicPath); readErr != nil || strings.TrimSpace(string(existing)) != pubKey {
		if err := utils.AtomicWriteFile(publicPath, []byte(pubKey+"\n"), 0644); err != nil {
			return "", "", "", err
		}
	}
	if err := os.Chmod(publicPath, 0644); err != nil {
		return "", "", "", err
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

func (s *Service) EnsureAndPublishLocalSSHIdentity(ctx context.Context) error {
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
		CertPin:   s.localCertPin(),
	}

	// Every node republishes every 30s; only an actual change is worth a
	// Raft entry.
	var current clusterModels.ClusterSSHIdentity
	if err := s.DB.Where("node_uuid = ?", identity.NodeUUID).Limit(1).Find(&current).Error; err == nil &&
		current.SSHUser == identity.SSHUser && current.SSHHost == identity.SSHHost &&
		current.SSHPort == identity.SSHPort && current.CertPin == identity.CertPin &&
		strings.TrimSpace(current.PublicKey) == strings.TrimSpace(identity.PublicKey) {
		return nil
	}

	if r := s.Raft(); r != nil {
		// Still settling after a join: the scheduler publishes it once a
		// leader is known and this node votes, so a join that's rolled back
		// leaves no identity behind.
		if leaderAddr, _ := r.LeaderWithID(); leaderAddr == "" {
			return nil
		}
		if member, err := s.ResolveCurrentRaftMember(identity.NodeUUID); err != nil || member.Suffrage != "voter" {
			return nil
		}
	}
	return s.UpsertClusterSSHIdentity(ctx, identity)
}
