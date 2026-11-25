// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/vmihailenco/msgpack/v5"
)

const snapshotSummaryCacheKey = "zfs:snapshots:summary:v3"

var runSnapshotSummaryList = func(ctx context.Context) (string, error) {
	return utils.RunCommandWithContext(
		ctx,
		"zfs", "list", "-H", "-p", "-t", "snapshot",
		"-o", "name,guid,creation,used,referenced,clones,userrefs,createtxg",
	)
}

func parseSnapshotClones(value string) []string {
	clones := []string{}
	for _, clone := range strings.Split(value, ",") {
		if clone = strings.TrimSpace(clone); clone != "" && clone != "-" {
			clones = append(clones, clone)
		}
	}
	return clones
}

func parseSnapshotSummaries(output string) []zfsServiceInterfaces.SnapshotSummary {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	summaries := make([]zfsServiceInterfaces.SnapshotSummary, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			continue
		}
		dataset, snapshot, ok := strings.Cut(fields[0], "@")
		if !ok || dataset == "" || snapshot == "" {
			continue
		}
		pool, _, _ := strings.Cut(dataset, "/")
		used, _ := strconv.ParseUint(fields[3], 10, 64)
		referenced, _ := strconv.ParseUint(fields[4], 10, 64)
		holds, _ := strconv.Atoi(fields[6])
		createTXG, _ := strconv.ParseUint(fields[7], 10, 64)
		summary := zfsServiceInterfaces.SnapshotSummary{
			Name:       fields[0],
			Dataset:    dataset,
			Snapshot:   snapshot,
			Pool:       pool,
			GUID:       fields[1],
			CreateTXG:  createTXG,
			Used:       used,
			Referenced: referenced,
			Clones:     parseSnapshotClones(fields[5]),
			Holds:      holds,
		}
		if created, err := strconv.ParseInt(fields[2], 10, 64); err == nil && created > 0 {
			summary.CreatedAt = time.Unix(created, 0).UTC()
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func (s *Service) listSnapshotSummaries(ctx context.Context) ([]zfsServiceInterfaces.SnapshotSummary, error) {
	output, err := runSnapshotSummaryList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}
	return parseSnapshotSummaries(output), nil
}

func storeSnapshotSummaries(summaries []zfsServiceInterfaces.SnapshotSummary, ttl int64) error {
	b, err := msgpack.Marshal(summaries)
	if err != nil {
		return fmt.Errorf("failed to encode snapshot summaries cache: %w", err)
	}
	if err := db.SetValue(snapshotSummaryCacheKey, b, ttl); err != nil {
		return fmt.Errorf("failed to store snapshot summaries cache: %w", err)
	}
	return nil
}

func (s *Service) RefreshSnapshotSummaries(ctx context.Context, ttl int64) error {
	summaries, err := s.listSnapshotSummaries(ctx)
	if err != nil {
		return err
	}
	return storeSnapshotSummaries(summaries, ttl)
}

func attachSnapshotDetails(datasets []*gzfs.Dataset, summaries []zfsServiceInterfaces.SnapshotSummary) {
	byGUID := make(map[string]zfsServiceInterfaces.SnapshotSummary, len(summaries))
	for _, summary := range summaries {
		byGUID[summary.GUID] = summary
	}
	for i, dataset := range datasets {
		if dataset == nil || dataset.Type != gzfs.DatasetTypeSnapshot {
			continue
		}
		summary, ok := byGUID[dataset.GUID]
		if !ok || summary.Name != dataset.Name {
			continue
		}
		detailed := *dataset
		detailed.Properties = maps.Clone(dataset.Properties)
		if detailed.Properties == nil {
			detailed.Properties = map[string]gzfs.ZFSProperty{}
		}
		source := gzfs.ZFSPropertySource{Type: "NONE", Data: "-"}
		if !summary.CreatedAt.IsZero() {
			detailed.Properties["creation"] = gzfs.ZFSProperty{
				Value:  strconv.FormatInt(summary.CreatedAt.Unix(), 10),
				Source: source,
			}
		}
		detailed.Properties["clones"] = gzfs.ZFSProperty{Value: strings.Join(summary.Clones, ","), Source: source}
		detailed.Properties["userrefs"] = gzfs.ZFSProperty{Value: strconv.Itoa(summary.Holds), Source: source}
		datasets[i] = &detailed
	}
}

func (s *Service) GetSnapshotSummaries(ctx context.Context) ([]zfsServiceInterfaces.SnapshotSummary, error) {
	summaries, err := s.snapshotSummariesForRead(ctx)
	if err != nil {
		return nil, err
	}

	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return nil, err
	}
	usable := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		usable[pool.Name] = struct{}{}
	}

	filtered := make([]zfsServiceInterfaces.SnapshotSummary, 0, len(summaries))
	for _, summary := range summaries {
		if _, ok := usable[summary.Pool]; ok {
			filtered = append(filtered, summary)
		}
	}
	return filtered, nil
}
