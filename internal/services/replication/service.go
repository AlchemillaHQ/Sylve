// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package replication

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/services/cluster"
	"github.com/quic-go/quic-go"
	"gorm.io/gorm"
)

const (
	alpnReplication = "sylve-repl-v1"
	maxHeaderBytes  = 64 * 1024
)

type Service struct {
	DB      *gorm.DB
	Auth    serviceInterfaces.AuthServiceInterface
	GZFS    *gzfs.Client
	Cluster *cluster.Service

	mu       sync.Mutex
	listener *quic.Listener
	port     int
}

type request struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Token   string `json:"token"`
	Dataset string `json:"dataset,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

type response struct {
	OK        bool       `json:"ok"`
	Error     string     `json:"error,omitempty"`
	Snapshots []SnapInfo `json:"snapshots,omitempty"`
}

type SnapInfo struct {
	Name      string `json:"name"`
	GUID      string `json:"guid"`
	CreateTXG string `json:"createtxg"`
}

type Plan struct {
	Mode               string `json:"mode"`
	BaseSnapshot       string `json:"baseSnapshot,omitempty"`
	TargetSnapshot     string `json:"targetSnapshot,omitempty"`
	SourceDataset      string `json:"sourceDataset"`
	DestinationDataset string `json:"destinationDataset"`
	Endpoint           string `json:"endpoint"`
	Noop               bool   `json:"noop"`
}

func NewService(
	db *gorm.DB,
	auth serviceInterfaces.AuthServiceInterface,
	gzfsClient *gzfs.Client,
	cluster *cluster.Service,
) *Service {
	return &Service{
		DB:      db,
		Auth:    auth,
		GZFS:    gzfsClient,
		Cluster: cluster,
	}
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		if err := s.syncListener(ctx); err != nil {
			logger.L.Debug().Err(err).Msg("replication_listener_sync_failed")
		}

		select {
		case <-ctx.Done():
			_ = s.stopListener()
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) syncListener(ctx context.Context) error {
	var c clusterModels.Cluster
	if err := s.DB.First(&c).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		return err
	}

	shouldRun := c.Enabled && c.RaftPort > 0
	if !shouldRun {
		return s.stopListener()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil && s.port == c.RaftPort {
		return nil
	}

	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}

	tlsConf, err := s.serverTLSConfig()
	if err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", c.RaftPort)
	listener, err := quic.ListenAddr(addr, tlsConf, nil)
	if err != nil {
		return err
	}

	s.listener = listener
	s.port = c.RaftPort

	go s.acceptLoop(ctx, listener)
	logger.L.Info().Int("udp_port", c.RaftPort).Msg("Replication QUIC Listener started")

	return nil
}

func (s *Service) stopListener() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		s.port = 0
		return nil
	}

	err := s.listener.Close()
	s.listener = nil
	s.port = 0
	return err
}

func (s *Service) acceptLoop(ctx context.Context, listener *quic.Listener) {
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return
		}

		go s.handleConn(conn)
	}
}

func (s *Service) handleConn(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}

		go s.handleStream(stream)
	}
}

func (s *Service) handleStream(stream *quic.Stream) {
	defer stream.Close()

	reader := bufio.NewReader(stream)
	var req request

	if err := readJSONLine(reader, maxHeaderBytes, &req); err != nil {
		_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
		return
	}

	if req.Version != 1 {
		_ = writeJSONLine(stream, response{OK: false, Error: "unsupported_protocol_version"})
		return
	}

	if req.Token == "" {
		_ = writeJSONLine(stream, response{OK: false, Error: "missing_cluster_token"})
		return
	}

	if _, err := s.Auth.VerifyClusterJWT(req.Token); err != nil {
		_ = writeJSONLine(stream, response{OK: false, Error: "invalid_cluster_token"})
		return
	}

	switch req.Action {
	case "snapshots":
		if req.Dataset == "" {
			_ = writeJSONLine(stream, response{OK: false, Error: "dataset_required"})
			return
		}

		snaps, err := s.listSnapshots(context.Background(), req.Dataset)
		if err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		_ = writeJSONLine(stream, response{OK: true, Snapshots: snaps})
	case "receive":
		if req.Dataset == "" {
			_ = writeJSONLine(stream, response{OK: false, Error: "dataset_required"})
			return
		}

		if err := s.receiveStream(context.Background(), reader, req.Dataset, req.Force); err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		_ = writeJSONLine(stream, response{OK: true})
	default:
		_ = writeJSONLine(stream, response{OK: false, Error: "unknown_action"})
	}
}

func (s *Service) listSnapshots(ctx context.Context, dataset string) ([]SnapInfo, error) {
	snaps, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false, dataset)
	if err != nil {
		return nil, err
	}

	sort.Slice(snaps, func(i, j int) bool {
		a, _ := strconv.ParseUint(snaps[i].CreateTXG, 10, 64)
		b, _ := strconv.ParseUint(snaps[j].CreateTXG, 10, 64)
		return a < b
	})

	out := make([]SnapInfo, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, SnapInfo{
			Name:      snap.Name,
			GUID:      snap.GUID,
			CreateTXG: snap.CreateTXG,
		})
	}

	return out, nil
}

func (s *Service) serverTLSConfig() (*tls.Config, error) {
	base, err := s.Auth.GetSylveCertificate()
	if err != nil {
		return nil, err
	}

	if base == nil {
		return nil, fmt.Errorf("nil_tls_config")
	}
	if len(base.Certificates) == 0 {
		return nil, fmt.Errorf("missing_tls_certificate")
	}

	cfg := base.Clone()
	cfg.MinVersion = tls.VersionTLS13
	cfg.NextProtos = []string{alpnReplication}

	return cfg, nil
}

func (s *Service) clientTLSConfig() (*tls.Config, error) {
	// Keep parity with existing inter-node HTTPS behavior.
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{alpnReplication},
		InsecureSkipVerify: true,
	}, nil
}

func readJSONLine(reader *bufio.Reader, max int, out any) error {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	if len(line) > max {
		return fmt.Errorf("header_too_large")
	}

	return json.Unmarshal(line, out)
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func (s *Service) receiveStream(ctx context.Context, in io.Reader, dest string, force bool) error {
	if in == nil {
		return fmt.Errorf("input_reader_is_nil")
	}
	if dest == "" {
		return fmt.Errorf("destination_dataset_is_empty")
	}

	args := []string{"recv"}
	if force {
		args = append(args, "-F")
	}
	args = append(args, dest)

	cmd := exec.CommandContext(ctx, "zfs", args...)
	cmd.Stdin = in

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zfs_recv_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
