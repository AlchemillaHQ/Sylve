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
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/alchemillahq/gzfs"
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
	if len(localSnaps) == 0 {
		return nil, fmt.Errorf("no_source_snapshots")
	}

	sort.Slice(localSnaps, func(i, j int) bool {
		a, _ := strconv.ParseUint(localSnaps[i].CreateTXG, 10, 64)
		b, _ := strconv.ParseUint(localSnaps[j].CreateTXG, 10, 64)
		return a < b
	})

	targetSnapshot := localSnaps[len(localSnaps)-1]

	remoteSnaps, err := s.fetchRemoteSnapshots(ctx, endpoint, dstDataset)
	if err != nil {
		return nil, err
	}

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

	if base == nil {
		plan.Mode = "full"
		if err := s.sendSnapshot(ctx, targetSnapshot.Name, stream); err != nil {
			return nil, err
		}
	} else {
		plan.BaseSnapshot = base.Name
		if withIntermediates {
			plan.Mode = "incremental_intermediates"
			if err := s.sendIncrementalWithIntermediates(ctx, base.Name, targetSnapshot.Name, stream); err != nil {
				return nil, err
			}
		} else {
			plan.Mode = "incremental"
			if err := s.sendIncremental(ctx, base.Name, targetSnapshot.Name, stream); err != nil {
				return nil, err
			}
		}
	}

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

	return plan, nil
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

	conn, err := quic.DialAddr(ctx, endpoint, tlsConf, nil)
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

	cmd := exec.CommandContext(ctx, "zfs", "send", snapshot)
	cmd.Stdout = out

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zfs_send_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

func (s *Service) sendIncremental(ctx context.Context, baseSnapshot, targetSnapshot string, out io.Writer) error {
	if baseSnapshot == "" || targetSnapshot == "" {
		return fmt.Errorf("base_and_target_snapshots_required")
	}
	if out == nil {
		return fmt.Errorf("output_writer_is_nil")
	}

	cmd := exec.CommandContext(ctx, "zfs", "send", "-i", baseSnapshot, targetSnapshot)
	cmd.Stdout = out

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zfs_incremental_send_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

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

	cmd := exec.CommandContext(ctx, "zfs", "send", "-I", baseSnapshot, targetSnapshot)
	cmd.Stdout = out

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zfs_incremental_send_intermediates_failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}
