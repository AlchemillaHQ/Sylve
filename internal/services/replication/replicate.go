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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/quic-go/quic-go"
)

func (s *Service) ReplicateDatasetToNode(
	ctx context.Context,
	srcDataset string,
	dstDataset string,
	target string,
	force bool,
	withIntermediates bool,
) (*Plan, error) {
	return s.replicateDatasetToNode(ctx, srcDataset, dstDataset, target, force, withIntermediates, nil)
}

func (s *Service) replicateDatasetToNode(
	ctx context.Context,
	srcDataset string,
	dstDataset string,
	target string,
	force bool,
	withIntermediates bool,
	jobID *uint,
) (*Plan, error) {
	if srcDataset == "" || dstDataset == "" || target == "" {
		return nil, fmt.Errorf("src_dataset_dst_dataset_and_target_are_required")
	}

	endpoint, err := s.resolvePeerEndpoint(target)
	if err != nil {
		return nil, err
	}

	localSnaps, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false, srcDataset)
	if err != nil {
		return nil, fmt.Errorf("source_snapshots: %w", err)
	}

	remoteSnaps, err := s.fetchRemoteSnapshots(ctx, endpoint, dstDataset)
	if err != nil {
		if !isDatasetMissingErr(err) {
			return nil, err
		}
		remoteSnaps = []SnapInfo{}
	}

	// For backup jobs, always create a new snapshot on each run
	// For manual replications, only create if none exist
	shouldCreateSnapshot := len(localSnaps) == 0 || jobID != nil

	if shouldCreateSnapshot {
		if len(localSnaps) == 0 {
			logger.L.Info().Str("dataset", srcDataset).Msg("no_snapshots_found_creating_automatic_snapshot")
		} else {
			logger.L.Info().Str("dataset", srcDataset).Msg("creating_new_snapshot_for_backup_job")
		}

		dataset, err := s.GZFS.ZFS.Get(ctx, srcDataset, false)
		if err != nil {
			return nil, fmt.Errorf("failed_to_get_source_dataset: %w", err)
		}

		snapName := "backup-" + time.Now().UTC().Format("2006-01-02-15-04-05")
		snap, err := dataset.Snapshot(ctx, snapName, false)
		if err != nil {
			return nil, fmt.Errorf("failed_to_create_snapshot: %w", err)
		}

		logger.L.Info().Str("snapshot", snap.Name).Msg("automatic_snapshot_created")
		localSnaps = append(localSnaps, snap)
	}

	sort.Slice(localSnaps, func(i, j int) bool {
		a, _ := strconv.ParseUint(localSnaps[i].CreateTXG, 10, 64)
		b, _ := strconv.ParseUint(localSnaps[j].CreateTXG, 10, 64)
		return a < b
	})

	targetSnapshot := localSnaps[len(localSnaps)-1]

	remoteByGUID := make(map[string]struct{}, len(remoteSnaps))
	for _, snap := range remoteSnaps {
		remoteByGUID[snap.GUID] = struct{}{}
	}

	var base *gzfs.Dataset
	for _, snap := range localSnaps {
		if _, ok := remoteByGUID[snap.GUID]; ok {
			base = snap
		}
	}

	plan := &Plan{
		SourceDataset:      srcDataset,
		DestinationDataset: dstDataset,
		Endpoint:           endpoint,
		TargetSnapshot:     targetSnapshot.Name,
	}

	if base != nil && base.GUID == targetSnapshot.GUID {
		plan.Mode = "noop"
		plan.Noop = true
		plan.BaseSnapshot = base.Name
		return plan, nil
	}

	token, err := s.clusterToken()
	if err != nil {
		return nil, err
	}

	conn, stream, err := s.openStream(ctx, endpoint, request{
		Version: 1,
		Action:  "receive",
		Token:   token,
		Dataset: dstDataset,
		Force:   force,
	})
	if err != nil {
		return nil, err
	}
	defer conn.CloseWithError(0, "done")

	mode := "full"
	baseSnapshotName := ""
	if base != nil {
		baseSnapshotName = base.Name
		if withIntermediates {
			mode = "incremental_intermediates"
		} else {
			mode = "incremental"
		}
	}
	event := s.beginReplicationEvent(
		"send",
		endpoint,
		srcDataset,
		dstDataset,
		baseSnapshotName,
		targetSnapshot.Name,
		mode,
		jobID,
	)
	defer func() {
		if r := recover(); r != nil {
			s.completeReplicationEvent(event, fmt.Errorf("panic: %v", r))
			panic(r)
		}
	}()

	if base == nil {
		plan.Mode = "full"
		if err := s.sendSnapshot(ctx, targetSnapshot.Name, stream); err != nil {
			s.completeReplicationEvent(event, err)
			return nil, err
		}
	} else {
		plan.BaseSnapshot = base.Name
		if withIntermediates {
			plan.Mode = "incremental_intermediates"
			if err := s.sendIncrementalWithIntermediates(ctx, base.Name, targetSnapshot.Name, stream); err != nil {
				s.completeReplicationEvent(event, err)
				return nil, err
			}
		} else {
			plan.Mode = "incremental"
			if err := s.sendIncremental(ctx, base.Name, targetSnapshot.Name, stream); err != nil {
				s.completeReplicationEvent(event, err)
				return nil, err
			}
		}
	}

	if err := stream.Close(); err != nil {
		s.completeReplicationEvent(event, err)
		return nil, err
	}

	reader := bufio.NewReader(stream)
	var resp response
	if err := readJSONLine(reader, maxHeaderBytes, &resp); err != nil {
		s.completeReplicationEvent(event, err)
		return nil, err
	}
	if !resp.OK {
		runErr := errors.New(resp.Error)
		s.completeReplicationEvent(event, runErr)
		return nil, runErr
	}
	s.completeReplicationEvent(event, nil)

	return plan, nil
}

func (s *Service) PullDatasetFromNode(
	ctx context.Context,
	srcDataset string,
	dstDataset string,
	target string,
	targetSnapshot string,
	force bool,
	withIntermediates bool,
) (*Plan, error) {
	if srcDataset == "" || dstDataset == "" || target == "" {
		return nil, fmt.Errorf("src_dataset_dst_dataset_and_target_are_required")
	}

	endpoint, err := s.resolvePeerEndpoint(target)
	if err != nil {
		return nil, err
	}

	remoteSnaps, err := s.fetchRemoteSnapshots(ctx, endpoint, srcDataset)
	if err != nil {
		return nil, err
	}
	if len(remoteSnaps) == 0 {
		return nil, fmt.Errorf("no_remote_snapshots")
	}

	targetName := normalizeSnapshotName(srcDataset, targetSnapshot)
	if targetName == "" {
		targetName = remoteSnaps[len(remoteSnaps)-1].Name
	}

	targetIndex := -1
	for i, snap := range remoteSnaps {
		if snap.Name == targetName {
			targetIndex = i
			break
		}
	}
	if targetIndex == -1 {
		return nil, fmt.Errorf("target_snapshot_not_found")
	}

	localSnaps, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeSnapshot, false, dstDataset)
	if err != nil && !isDatasetMissingErr(err) {
		return nil, fmt.Errorf("destination_snapshots: %w", err)
	}

	localByGUID := make(map[string]*gzfs.Dataset, len(localSnaps))
	for _, snap := range localSnaps {
		localByGUID[snap.GUID] = snap
	}

	var baseLocal *gzfs.Dataset
	var baseRemote SnapInfo
	for _, remoteSnap := range remoteSnaps[:targetIndex+1] {
		if localSnap, ok := localByGUID[remoteSnap.GUID]; ok {
			baseLocal = localSnap
			baseRemote = remoteSnap
		}
	}

	plan := &Plan{
		SourceDataset:      srcDataset,
		DestinationDataset: dstDataset,
		Endpoint:           endpoint,
		TargetSnapshot:     targetName,
	}

	if baseLocal != nil {
		plan.BaseSnapshot = baseRemote.Name
		if baseRemote.Name == targetName {
			plan.Mode = "noop"
			plan.Noop = true
			return plan, nil
		}
	}

	token, err := s.clusterToken()
	if err != nil {
		return nil, err
	}

	conn, stream, err := s.openStream(ctx, endpoint, request{
		Version:           1,
		Action:            "send",
		Token:             token,
		Dataset:           srcDataset,
		TargetSnapshot:    targetName,
		BaseSnapshot:      plan.BaseSnapshot,
		WithIntermediates: withIntermediates,
	})
	if err != nil {
		return nil, err
	}
	defer conn.CloseWithError(0, "done")

	if err := stream.Close(); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(stream)
	var resp response
	if err := readJSONLine(reader, maxHeaderBytes, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	if resp.TargetSnapshot != "" {
		plan.TargetSnapshot = resp.TargetSnapshot
	}

	if plan.BaseSnapshot == "" {
		plan.Mode = "pull_full"
	} else if withIntermediates {
		plan.Mode = "pull_incremental_intermediates"
	} else {
		plan.Mode = "pull_incremental"
	}

	if err := s.receiveStream(ctx, reader, dstDataset, force); err != nil {
		return nil, err
	}

	return plan, nil
}

func (s *Service) ListTargetDatasets(ctx context.Context, target string, prefix string) ([]DatasetInfo, error) {
	endpoint, err := s.resolvePeerEndpoint(target)
	if err != nil {
		return nil, err
	}

	token, err := s.clusterToken()
	if err != nil {
		return nil, err
	}

	conn, stream, err := s.openStream(ctx, endpoint, request{
		Version: 1,
		Action:  "datasets",
		Token:   token,
		Prefix:  prefix,
	})
	if err != nil {
		return nil, err
	}
	defer conn.CloseWithError(0, "done")

	if err := stream.Close(); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(stream)
	var resp response
	if err := readJSONLine(reader, maxHeaderBytes, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}

	return resp.Datasets, nil
}

func (s *Service) ListTargetStatus(ctx context.Context, target string, limit int) ([]ReplicationEventInfo, error) {
	endpoint, err := s.resolvePeerEndpoint(target)
	if err != nil {
		return nil, err
	}

	token, err := s.clusterToken()
	if err != nil {
		return nil, err
	}

	conn, stream, err := s.openStream(ctx, endpoint, request{
		Version: 1,
		Action:  "status",
		Token:   token,
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	defer conn.CloseWithError(0, "done")

	if err := stream.Close(); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(stream)
	var resp response
	if err := readJSONLine(reader, maxHeaderBytes, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}

	return resp.Events, nil
}

// ListTargetSnapshots lists snapshots for a dataset on a remote target.
// Used for restore operations to see available backup points.
func (s *Service) ListTargetSnapshots(ctx context.Context, target string, dataset string) ([]SnapInfo, error) {
	endpoint, err := s.resolvePeerEndpoint(target)
	if err != nil {
		return nil, err
	}

	return s.fetchRemoteSnapshots(ctx, endpoint, dataset)
}

func (s *Service) fetchRemoteSnapshots(ctx context.Context, endpoint, dataset string) ([]SnapInfo, error) {
	token, err := s.clusterToken()
	if err != nil {
		return nil, err
	}

	conn, stream, err := s.openStream(ctx, endpoint, request{
		Version: 1,
		Action:  "snapshots",
		Token:   token,
		Dataset: dataset,
	})
	if err != nil {
		return nil, err
	}
	defer conn.CloseWithError(0, "done")

	if err := stream.Close(); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(stream)
	var resp response
	if err := readJSONLine(reader, maxHeaderBytes, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}

	return resp.Snapshots, nil
}

func (s *Service) openStream(ctx context.Context, endpoint string, req request) (*quic.Conn, *quic.Stream, error) {
	tlsConf, err := s.clientTLSConfig()
	if err != nil {
		return nil, nil, err
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout: 4 * time.Hour,
	}

	conn, err := quic.DialAddr(ctx, endpoint, tlsConf, quicConfig)
	if err != nil {
		return nil, nil, err
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(1, "open_stream_failed")
		return nil, nil, err
	}

	if err := writeJSONLine(stream, req); err != nil {
		conn.CloseWithError(1, "write_header_failed")
		return nil, nil, err
	}

	return conn, stream, nil
}

func (s *Service) resolvePeerEndpoint(target string) (string, error) {
	if host, port, err := net.SplitHostPort(target); err == nil {
		return net.JoinHostPort(host, port), nil
	}

	if s.Cluster == nil || s.Cluster.Raft == nil {
		return "", fmt.Errorf("raft_not_initialized")
	}

	fut := s.Cluster.Raft.GetConfiguration()
	if err := fut.Error(); err != nil {
		return "", err
	}

	for _, server := range fut.Configuration().Servers {
		id := string(server.ID)
		host, port, err := net.SplitHostPort(string(server.Address))
		if err != nil {
			continue
		}

		if id == target || host == target {
			return net.JoinHostPort(host, port), nil
		}
	}

	return "", fmt.Errorf("target_peer_not_found")
}

func (s *Service) clusterToken() (string, error) {
	hostname, err := utils.GetSystemHostname()
	if err != nil || hostname == "" {
		hostname = "cluster"
	}

	token, err := s.Auth.CreateClusterJWT(0, hostname, "", "")
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("empty_cluster_token")
	}
	return token, nil
}

func (s *Service) sendSnapshot(ctx context.Context, snapshot string, out io.Writer) error {
	if snapshot == "" {
		return fmt.Errorf("snapshot_name_is_empty")
	}
	if out == nil {
		return fmt.Errorf("output_writer_is_nil")
	}

	// Wrap writer with progress tracking
	progressOut := newProgressWriter(out, snapshot, "send")
	logger.L.Debug().Str("snapshot", snapshot).Msg("starting_zfs_send")

	if err := s.GZFS.ZFS.SendSnapshot(ctx, snapshot, progressOut); err != nil {
		logger.L.Debug().
			Str("snapshot", snapshot).
			Int64("bytes_sent", progressOut.BytesWritten()).
			Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
			Err(err).
			Msg("zfs_send_failed")
		return fmt.Errorf("zfs_send_failed: %w", err)
	}

	logger.L.Debug().
		Str("snapshot", snapshot).
		Int64("bytes_sent", progressOut.BytesWritten()).
		Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
		Msg("zfs_send_completed")

	return nil
}

func (s *Service) sendIncremental(ctx context.Context, baseSnapshot, targetSnapshot string, out io.Writer) error {
	if baseSnapshot == "" || targetSnapshot == "" {
		return fmt.Errorf("base_and_target_snapshots_required")
	}
	if out == nil {
		return fmt.Errorf("output_writer_is_nil")
	}

	// Wrap writer with progress tracking
	progressOut := newProgressWriter(out, targetSnapshot, "send_incremental")
	logger.L.Debug().
		Str("base_snapshot", baseSnapshot).
		Str("target_snapshot", targetSnapshot).
		Msg("starting_zfs_incremental_send")

	if err := s.GZFS.ZFS.SendIncremental(ctx, baseSnapshot, targetSnapshot, progressOut); err != nil {
		logger.L.Debug().
			Str("base_snapshot", baseSnapshot).
			Str("target_snapshot", targetSnapshot).
			Int64("bytes_sent", progressOut.BytesWritten()).
			Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
			Err(err).
			Msg("zfs_incremental_send_failed")
		return fmt.Errorf("zfs_incremental_send_failed: %w", err)
	}

	logger.L.Debug().
		Str("base_snapshot", baseSnapshot).
		Str("target_snapshot", targetSnapshot).
		Int64("bytes_sent", progressOut.BytesWritten()).
		Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
		Msg("zfs_incremental_send_completed")

	return nil
}

func (s *Service) sendIncrementalWithIntermediates(
	ctx context.Context,
	baseSnapshot,
	targetSnapshot string,
	out io.Writer,
) error {
	if baseSnapshot == "" || targetSnapshot == "" {
		return fmt.Errorf("base_and_target_snapshots_required")
	}
	if out == nil {
		return fmt.Errorf("output_writer_is_nil")
	}

	// Wrap writer with progress tracking
	progressOut := newProgressWriter(out, targetSnapshot, "send_incremental_intermediates")
	logger.L.Debug().
		Str("base_snapshot", baseSnapshot).
		Str("target_snapshot", targetSnapshot).
		Msg("starting_zfs_incremental_send_with_intermediates")

	if err := s.GZFS.ZFS.SendIncrementalWithIntermediates(ctx, baseSnapshot, targetSnapshot, progressOut); err != nil {
		logger.L.Debug().
			Str("base_snapshot", baseSnapshot).
			Str("target_snapshot", targetSnapshot).
			Int64("bytes_sent", progressOut.BytesWritten()).
			Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
			Err(err).
			Msg("zfs_incremental_send_intermediates_failed")
		return fmt.Errorf("zfs_incremental_send_intermediates_failed: %w", err)
	}

	logger.L.Debug().
		Str("base_snapshot", baseSnapshot).
		Str("target_snapshot", targetSnapshot).
		Int64("bytes_sent", progressOut.BytesWritten()).
		Float64("mb_sent", float64(progressOut.BytesWritten())/(1024*1024)).
		Msg("zfs_incremental_send_intermediates_completed")

	return nil
}

func isDatasetMissingErr(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dataset does not exist") || strings.Contains(msg, "not found")
}
