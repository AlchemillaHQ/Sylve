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

	jobMu       sync.Mutex
	runningJobs map[uint]struct{}
}

type request struct {
	Version           int    `json:"version"`
	Action            string `json:"action"`
	Token             string `json:"token"`
	Dataset           string `json:"dataset,omitempty"`
	Prefix            string `json:"prefix,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	Force             bool   `json:"force,omitempty"`
	BaseSnapshot      string `json:"baseSnapshot,omitempty"`
	TargetSnapshot    string `json:"targetSnapshot,omitempty"`
	WithIntermediates bool   `json:"withIntermediates,omitempty"`
}

type response struct {
	OK             bool                   `json:"ok"`
	Error          string                 `json:"error,omitempty"`
	Snapshots      []SnapInfo             `json:"snapshots,omitempty"`
	Datasets       []DatasetInfo          `json:"datasets,omitempty"`
	Events         []ReplicationEventInfo `json:"events,omitempty"`
	TargetSnapshot string                 `json:"targetSnapshot,omitempty"`
}

type SnapInfo struct {
	Name      string `json:"name"`
	GUID      string `json:"guid"`
	CreateTXG string `json:"createtxg"`
}

type DatasetInfo struct {
	Name            string `json:"name"`
	GUID            string `json:"guid"`
	Type            string `json:"type"`
	CreationUnix    int64  `json:"creationUnix"`
	UsedBytes       uint64 `json:"usedBytes"`
	ReferencedBytes uint64 `json:"referencedBytes"`
	AvailableBytes  uint64 `json:"availableBytes"`
	Mountpoint      string `json:"mountpoint"`
}

type ReplicationEventInfo struct {
	ID                 uint       `json:"id"`
	JobID              *uint      `json:"jobId,omitempty"`
	Direction          string     `json:"direction"`
	RemoteAddress      string     `json:"remoteAddress"`
	SourceDataset      string     `json:"sourceDataset"`
	DestinationDataset string     `json:"destinationDataset"`
	BaseSnapshot       string     `json:"baseSnapshot"`
	TargetSnapshot     string     `json:"targetSnapshot"`
	Mode               string     `json:"mode"`
	Status             string     `json:"status"`
	Error              string     `json:"error"`
	StartedAt          time.Time  `json:"startedAt"`
	CompletedAt        *time.Time `json:"completedAt"`
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

// progressReader wraps an io.Reader and logs progress at debug level
type progressReader struct {
	inner         io.Reader
	bytesRead     int64
	lastLogBytes  int64
	lastLogTime   time.Time
	logThreshold  int64 // bytes between logs
	timeThreshold time.Duration
	dataset       string
	operation     string
}

func newProgressReader(r io.Reader, dataset, operation string) *progressReader {
	return &progressReader{
		inner:         r,
		lastLogTime:   time.Now(),
		logThreshold:  10 * 1024 * 1024, // 10MB
		timeThreshold: 30 * time.Second,
		dataset:       dataset,
		operation:     operation,
	}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.inner.Read(buf)
	p.bytesRead += int64(n)

	now := time.Now()
	bytesSinceLog := p.bytesRead - p.lastLogBytes
	timeSinceLog := now.Sub(p.lastLogTime)

	if bytesSinceLog >= p.logThreshold || timeSinceLog >= p.timeThreshold {
		logger.L.Debug().
			Str("dataset", p.dataset).
			Str("operation", p.operation).
			Int64("bytes_transferred", p.bytesRead).
			Float64("mb_transferred", float64(p.bytesRead)/(1024*1024)).
			Msg("replication_progress")
		p.lastLogBytes = p.bytesRead
		p.lastLogTime = now
	}

	return n, err
}

func (p *progressReader) BytesRead() int64 {
	return p.bytesRead
}

// progressWriter wraps an io.Writer and logs progress at debug level
type progressWriter struct {
	inner         io.Writer
	bytesWritten  int64
	lastLogBytes  int64
	lastLogTime   time.Time
	logThreshold  int64
	timeThreshold time.Duration
	dataset       string
	operation     string
}

func newProgressWriter(w io.Writer, dataset, operation string) *progressWriter {
	return &progressWriter{
		inner:         w,
		lastLogTime:   time.Now(),
		logThreshold:  10 * 1024 * 1024, // 10MB
		timeThreshold: 30 * time.Second,
		dataset:       dataset,
		operation:     operation,
	}
}

func (p *progressWriter) Write(buf []byte) (int, error) {
	n, err := p.inner.Write(buf)
	p.bytesWritten += int64(n)

	now := time.Now()
	bytesSinceLog := p.bytesWritten - p.lastLogBytes
	timeSinceLog := now.Sub(p.lastLogTime)

	if bytesSinceLog >= p.logThreshold || timeSinceLog >= p.timeThreshold {
		logger.L.Debug().
			Str("dataset", p.dataset).
			Str("operation", p.operation).
			Int64("bytes_transferred", p.bytesWritten).
			Float64("mb_transferred", float64(p.bytesWritten)/(1024*1024)).
			Msg("replication_progress")
		p.lastLogBytes = p.bytesWritten
		p.lastLogTime = now
	}

	return n, err
}

func (p *progressWriter) BytesWritten() int64 {
	return p.bytesWritten
}

func NewService(
	db *gorm.DB,
	auth serviceInterfaces.AuthServiceInterface,
	gzfsClient *gzfs.Client,
	cluster *cluster.Service,
) *Service {
	return &Service{
		DB:          db,
		Auth:        auth,
		GZFS:        gzfsClient,
		Cluster:     cluster,
		runningJobs: make(map[uint]struct{}),
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

func (s *Service) RunStandalone(ctx context.Context, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid_listener_port")
	}

	if err := s.ensureListener(ctx, port); err != nil {
		return err
	}

	<-ctx.Done()
	return s.stopListener()
}

func (s *Service) syncListener(ctx context.Context) error {
	var c clusterModels.Cluster
	if err := s.DB.Order("id ASC").First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.stopListener()
		}
		return err
	}

	if !c.Enabled || c.RaftPort <= 0 {
		return s.stopListener()
	}

	return s.ensureListener(ctx, c.RaftPort)
}

func (s *Service) ensureListener(ctx context.Context, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil && s.port == port {
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

	quicConfig := &quic.Config{
		MaxIdleTimeout: 4 * 24 * time.Hour,
	}

	addr := fmt.Sprintf(":%d", port)
	listener, err := quic.ListenAddr(addr, tlsConf, quicConfig)
	if err != nil {
		return err
	}

	s.listener = listener
	s.port = port

	go s.acceptLoop(ctx, listener)
	logger.L.Info().Int("udp_port", port).Msg("Replication QUIC Listener started")

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
	remoteAddr := conn.RemoteAddr().String()

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}

		go s.handleStream(stream, remoteAddr)
	}
}

func (s *Service) handleStream(stream *quic.Stream, remoteAddr string) {
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
	case "datasets":
		datasets, err := s.listDatasets(context.Background(), req.Prefix)
		if err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		_ = writeJSONLine(stream, response{OK: true, Datasets: datasets})
	case "status":
		events, err := s.listReplicationEvents(req.Limit, nil)
		if err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		_ = writeJSONLine(stream, response{OK: true, Events: events})
	case "receive":
		if req.Dataset == "" {
			_ = writeJSONLine(stream, response{OK: false, Error: "dataset_required"})
			return
		}

		event := s.beginReplicationEvent(
			"receive",
			remoteAddr,
			"",
			req.Dataset,
			"",
			"",
			"push",
			nil,
		)

		err := s.receiveStream(context.Background(), reader, req.Dataset, req.Force)
		s.completeReplicationEvent(event, err)
		if err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		_ = writeJSONLine(stream, response{OK: true})
	case "send":
		if req.Dataset == "" {
			_ = writeJSONLine(stream, response{OK: false, Error: "dataset_required"})
			return
		}

		targetSnapshot, err := s.resolveTargetSnapshot(context.Background(), req.Dataset, req.TargetSnapshot)
		if err != nil {
			_ = writeJSONLine(stream, response{OK: false, Error: err.Error()})
			return
		}

		baseSnapshot := normalizeSnapshotName(req.Dataset, req.BaseSnapshot)
		if baseSnapshot != "" && baseSnapshot == targetSnapshot {
			_ = writeJSONLine(stream, response{OK: false, Error: "base_equals_target_snapshot"})
			return
		}

		mode := "full"
		if baseSnapshot != "" {
			if req.WithIntermediates {
				mode = "incremental_intermediates"
			} else {
				mode = "incremental"
			}
		}

		event := s.beginReplicationEvent(
			"send",
			remoteAddr,
			req.Dataset,
			"",
			baseSnapshot,
			targetSnapshot,
			mode,
			nil,
		)

		if err := writeJSONLine(stream, response{OK: true, TargetSnapshot: targetSnapshot}); err != nil {
			s.completeReplicationEvent(event, err)
			return
		}

		err = s.sendDataset(context.Background(), targetSnapshot, baseSnapshot, req.WithIntermediates, stream)
		s.completeReplicationEvent(event, err)
		if err != nil {
			logger.L.Warn().Err(err).Str("remote", remoteAddr).Msg("replication_send_failed")
		}
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

func (s *Service) listDatasets(ctx context.Context, prefix string) ([]DatasetInfo, error) {
	cmd := exec.CommandContext(
		ctx,
		"zfs",
		"list",
		"-H",
		"-p",
		"-o",
		"name,guid,type,creation,used,refer,avail,mountpoint",
		"-t",
		"filesystem,volume",
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("zfs_list_datasets_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	datasets := make([]DatasetInfo, 0, len(lines))

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}

		name := fields[0]
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}

		creation, _ := strconv.ParseInt(fields[3], 10, 64)
		used, _ := strconv.ParseUint(fields[4], 10, 64)
		refer, _ := strconv.ParseUint(fields[5], 10, 64)
		avail, _ := strconv.ParseUint(fields[6], 10, 64)

		datasets = append(datasets, DatasetInfo{
			Name:            name,
			GUID:            fields[1],
			Type:            fields[2],
			CreationUnix:    creation,
			UsedBytes:       used,
			ReferencedBytes: refer,
			AvailableBytes:  avail,
			Mountpoint:      fields[7],
		})
	}

	sort.Slice(datasets, func(i, j int) bool {
		return datasets[i].Name < datasets[j].Name
	})

	return datasets, nil
}

func (s *Service) listReplicationEvents(limit int, jobID *uint) ([]ReplicationEventInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var rows []clusterModels.BackupReplicationEvent
	q := s.DB.Order("started_at DESC").Limit(limit)
	if jobID != nil && *jobID > 0 {
		q = q.Where("job_id = ?", *jobID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]ReplicationEventInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReplicationEventInfo{
			ID:                 row.ID,
			JobID:              row.JobID,
			Direction:          row.Direction,
			RemoteAddress:      row.RemoteAddress,
			SourceDataset:      row.SourceDataset,
			DestinationDataset: row.DestinationDataset,
			BaseSnapshot:       row.BaseSnapshot,
			TargetSnapshot:     row.TargetSnapshot,
			Mode:               row.Mode,
			Status:             row.Status,
			Error:              row.Error,
			StartedAt:          row.StartedAt,
			CompletedAt:        row.CompletedAt,
		})
	}

	return out, nil
}

func (s *Service) beginReplicationEvent(
	direction string,
	remoteAddress string,
	sourceDataset string,
	destinationDataset string,
	baseSnapshot string,
	targetSnapshot string,
	mode string,
	jobID *uint,
) *clusterModels.BackupReplicationEvent {
	if s.DB == nil {
		return nil
	}

	event := &clusterModels.BackupReplicationEvent{
		Direction:          direction,
		JobID:              jobID,
		RemoteAddress:      remoteAddress,
		SourceDataset:      sourceDataset,
		DestinationDataset: destinationDataset,
		BaseSnapshot:       baseSnapshot,
		TargetSnapshot:     targetSnapshot,
		Mode:               mode,
		Status:             "running",
		StartedAt:          time.Now().UTC(),
	}

	if err := s.DB.Create(event).Error; err != nil {
		logger.L.Debug().Err(err).Msg("failed_to_create_replication_event")
		return nil
	}

	return event
}

func (s *Service) completeReplicationEvent(event *clusterModels.BackupReplicationEvent, runErr error) {
	if event == nil || s.DB == nil {
		return
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"completed_at": &now,
		"error":        "",
		"status":       "success",
	}

	if runErr != nil {
		updates["status"] = "failed"
		updates["error"] = runErr.Error()
	}

	if err := s.DB.Model(&clusterModels.BackupReplicationEvent{}).Where("id = ?", event.ID).Updates(updates).Error; err != nil {
		logger.L.Debug().Err(err).Msg("failed_to_update_replication_event")
	}
}

func (s *Service) resolveTargetSnapshot(ctx context.Context, dataset, targetSnapshot string) (string, error) {
	snaps, err := s.listSnapshots(ctx, dataset)
	if err != nil {
		return "", err
	}
	if len(snaps) == 0 {
		return "", fmt.Errorf("no_source_snapshots")
	}

	if targetSnapshot == "" {
		return snaps[len(snaps)-1].Name, nil
	}

	targetSnapshot = normalizeSnapshotName(dataset, targetSnapshot)
	for _, snap := range snaps {
		if snap.Name == targetSnapshot {
			return targetSnapshot, nil
		}
	}

	return "", fmt.Errorf("target_snapshot_not_found")
}

func normalizeSnapshotName(dataset, snapshot string) string {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return ""
	}

	if strings.Contains(snapshot, "@") {
		return snapshot
	}

	return fmt.Sprintf("%s@%s", dataset, snapshot)
}

func (s *Service) sendDataset(
	ctx context.Context,
	targetSnapshot string,
	baseSnapshot string,
	withIntermediates bool,
	out io.Writer,
) error {
	if baseSnapshot == "" {
		return s.sendSnapshot(ctx, targetSnapshot, out)
	}

	if withIntermediates {
		return s.sendIncrementalWithIntermediates(ctx, baseSnapshot, targetSnapshot, out)
	}

	return s.sendIncremental(ctx, baseSnapshot, targetSnapshot, out)
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
	if err := ensureDatasetParent(ctx, dest); err != nil {
		return err
	}
	if err := s.clearResumableReceiveState(ctx, dest); err != nil {
		return err
	}

	// Wrap reader with progress tracking
	progressIn := newProgressReader(in, dest, "receive")
	logger.L.Debug().Str("dataset", dest).Bool("force", force).Msg("starting_zfs_receive")

	if err := s.GZFS.ZFS.ReceiveStream(ctx, progressIn, dest, force); err != nil {
		logger.L.Debug().
			Str("dataset", dest).
			Int64("bytes_received", progressIn.BytesRead()).
			Float64("mb_received", float64(progressIn.BytesRead())/(1024*1024)).
			Err(err).
			Msg("zfs_receive_failed")
		return fmt.Errorf("zfs_recv_failed: %w", err)
	}

	logger.L.Debug().
		Str("dataset", dest).
		Int64("bytes_received", progressIn.BytesRead()).
		Float64("mb_received", float64(progressIn.BytesRead())/(1024*1024)).
		Msg("zfs_receive_completed")

	return nil
}

func ensureDatasetParent(ctx context.Context, dataset string) error {
	name := strings.TrimSpace(dataset)
	if name == "" {
		return fmt.Errorf("destination_dataset_is_empty")
	}

	lastSlash := strings.LastIndex(name, "/")
	if lastSlash <= 0 {
		return nil
	}

	parent := name[:lastSlash]
	checkCmd := exec.CommandContext(ctx, "zfs", "list", "-H", "-o", "name", parent)
	var checkErr bytes.Buffer
	checkCmd.Stderr = &checkErr
	if err := checkCmd.Run(); err == nil {
		return nil
	} else {
		stderr := strings.TrimSpace(checkErr.String())
		if !isDatasetMissingErr(err) && !isDatasetMissingErr(fmt.Errorf("%s", stderr)) {
			return fmt.Errorf("check_destination_parent_failed: %w: %s", err, stderr)
		}
	}

	createCmd := exec.CommandContext(ctx, "zfs", "create", "-p", parent)
	var createErr bytes.Buffer
	createCmd.Stderr = &createErr
	if err := createCmd.Run(); err != nil {
		stderr := strings.ToLower(strings.TrimSpace(createErr.String()))
		if strings.Contains(stderr, "dataset already exists") {
			return nil
		}
		return fmt.Errorf("create_destination_parent_failed: %w: %s", err, strings.TrimSpace(createErr.String()))
	}

	return nil
}

func (s *Service) clearResumableReceiveState(ctx context.Context, dataset string) error {
	name := strings.TrimSpace(dataset)
	if name == "" {
		return fmt.Errorf("destination_dataset_is_empty")
	}

	prop, err := s.GZFS.ZFS.GetProperty(ctx, name, "receive_resume_token")
	if err != nil {
		if isDatasetMissingErr(err) {
			return nil
		}
		return fmt.Errorf("check_receive_resume_token_failed: %w", err)
	}

	token := strings.TrimSpace(prop.Value)
	if token == "" || token == "-" {
		return nil
	}

	abortCmd := exec.CommandContext(ctx, "zfs", "recv", "-A", name)
	var abortErr bytes.Buffer
	abortCmd.Stderr = &abortErr
	if err := abortCmd.Run(); err != nil {
		stderr := strings.ToLower(strings.TrimSpace(abortErr.String()))
		if strings.Contains(stderr, "does not have any resumable receive state") {
			return nil
		}
		return fmt.Errorf("abort_resumable_receive_failed: %w: %s", err, strings.TrimSpace(abortErr.String()))
	}

	logger.L.Warn().Str("dataset", name).Msg("cleared_stale_resumable_receive_state")
	return nil
}
