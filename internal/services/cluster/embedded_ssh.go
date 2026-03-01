// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
	"golang.org/x/crypto/ssh"
)

func (s *Service) StartEmbeddedSSHServer(ctx context.Context) error {
	var startErr error
	s.embeddedSSHOnce.Do(func() {
		privatePath, err := s.ClusterSSHPrivateKeyPath()
		if err != nil {
			startErr = fmt.Errorf("embedded_ssh_private_key_failed: %w", err)
			return
		}

		privateRaw, err := os.ReadFile(privatePath)
		if err != nil {
			startErr = fmt.Errorf("embedded_ssh_private_key_read_failed: %w", err)
			return
		}

		hostSigner, err := ssh.ParsePrivateKey(privateRaw)
		if err != nil {
			startErr = fmt.Errorf("embedded_ssh_private_key_parse_failed: %w", err)
			return
		}

		serverConfig := &ssh.ServerConfig{
			PublicKeyCallback: s.embeddedSSHPublicKeyCallback,
		}
		serverConfig.AddHostKey(hostSigner)

		listenAddr := fmt.Sprintf("0.0.0.0:%d", ClusterEmbeddedSSHPort)
		listener, err := net.Listen("tcp", listenAddr)
		if err != nil {
			startErr = fmt.Errorf("embedded_ssh_listen_failed: %w", err)
			return
		}

		logger.L.Info().
			Str("addr", listenAddr).
			Msg("embedded_cluster_ssh_started")

		go func() {
			<-ctx.Done()
			_ = listener.Close()
		}()

		go s.embeddedSSHAcceptLoop(ctx, listener, serverConfig)
	})

	return startErr
}

func (s *Service) embeddedSSHPublicKeyCallback(conn ssh.ConnMetadata, presentedKey ssh.PublicKey) (*ssh.Permissions, error) {
	if strings.TrimSpace(conn.User()) != "root" {
		return nil, fmt.Errorf("invalid_user")
	}

	identities, err := s.ListClusterSSHIdentities()
	if err != nil {
		return nil, fmt.Errorf("list_cluster_identities_failed: %w", err)
	}

	for _, identity := range identities {
		pub := strings.TrimSpace(identity.PublicKey)
		if pub == "" {
			continue
		}

		parsedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub + "\n"))
		if err != nil {
			continue
		}

		if bytes.Equal(parsedKey.Marshal(), presentedKey.Marshal()) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"node_uuid": identity.NodeUUID,
				},
			}, nil
		}
	}

	return nil, fmt.Errorf("unauthorized_key")
}

func (s *Service) embeddedSSHAcceptLoop(ctx context.Context, listener net.Listener, serverConfig *ssh.ServerConfig) {
	for {
		rawConn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.L.Warn().Err(err).Msg("embedded_ssh_accept_failed")
			continue
		}

		go s.handleEmbeddedSSHConn(ctx, rawConn, serverConfig)
	}
}

func (s *Service) handleEmbeddedSSHConn(ctx context.Context, rawConn net.Conn, serverConfig *ssh.ServerConfig) {
	defer rawConn.Close()

	_, chans, reqs, err := ssh.NewServerConn(rawConn, serverConfig)
	if err != nil {
		logger.L.Warn().Err(err).Msg("embedded_ssh_handshake_failed")
		return
	}
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			logger.L.Warn().Err(err).Msg("embedded_ssh_channel_accept_failed")
			continue
		}

		go s.handleEmbeddedSSHSession(ctx, channel, requests)
	}
}

func parseExecRequestPayload(payload []byte) (string, error) {
	if len(payload) < 4 {
		return "", fmt.Errorf("invalid_exec_payload")
	}

	size := int(binary.BigEndian.Uint32(payload[:4]))
	if size < 0 || len(payload) < 4+size {
		return "", fmt.Errorf("invalid_exec_payload_size")
	}

	return string(payload[4 : 4+size]), nil
}

func exitCodeFromErr(err error) uint32 {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if ok := strings.Contains(err.Error(), "signal: killed"); ok {
		return 137
	}
	if ok := strings.Contains(err.Error(), "signal: terminated"); ok {
		return 143
	}
	if ok := strings.Contains(err.Error(), "signal: interrupt"); ok {
		return 130
	}

	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return uint32(code)
		}
	}
	return 1
}

func (s *Service) handleEmbeddedSSHSession(ctx context.Context, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	execReceived := false
	for req := range requests {
		switch req.Type {
		case "exec":
			if execReceived {
				_ = req.Reply(false, nil)
				continue
			}
			execReceived = true

			command, err := parseExecRequestPayload(req.Payload)
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}

			command = strings.TrimSpace(command)
			if command == "" {
				_ = req.Reply(false, nil)
				return
			}

			_ = req.Reply(true, nil)

			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			cmd.Stdin = channel
			cmd.Stdout = channel
			cmd.Stderr = channel.Stderr()

			runErr := cmd.Run()
			exitCode := exitCodeFromErr(runErr)
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: exitCode}))
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}
