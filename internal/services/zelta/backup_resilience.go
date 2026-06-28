// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zelta

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/logger"
)

const backupGenerationsToKeep = 2

type backupScope struct {
	sourceDataset string
	destSuffix    string
}

func (s *Service) backupRunScopes(job *clusterModels.BackupJob, sourceDataset, destSuffix string, vmSourceDatasets []string) []backupScope {
	if job != nil && job.Mode == clusterModels.BackupJobModeVM {
		scopes := make([]backupScope, 0, len(vmSourceDatasets))
		for _, vmSource := range vmSourceDatasets {
			scopes = append(scopes, backupScope{
				sourceDataset: vmSource,
				destSuffix:    s.backupDestSuffixForVMSource(strings.TrimSpace(job.DestSuffix), vmSource),
			})
		}
		return scopes
	}
	return []backupScope{{sourceDataset: sourceDataset, destSuffix: destSuffix}}
}

func snapshotCreationTime(info SnapshotInfo) (time.Time, bool) {
	raw := strings.TrimSpace(info.Creation)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(epoch, 0).UTC(), true
	}
	return time.Time{}, false
}

func snapshotGUIDKey(info SnapshotInfo) string {
	return strings.TrimSpace(info.Guid)
}

func latestCommonBackupSnapshot(local, remote []SnapshotInfo, snapPrefix string) (SnapshotInfo, bool) {
	remoteGUIDs := make(map[string]struct{})
	remoteShort := make(map[string]struct{})
	for _, r := range remote {
		short := strings.TrimPrefix(snapshotShortName(r), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}
		if g := snapshotGUIDKey(r); g != "" {
			remoteGUIDs[g] = struct{}{}
		}
		if short != "" {
			remoteShort[short] = struct{}{}
		}
	}

	var best SnapshotInfo
	found := false
	for _, l := range local {
		short := strings.TrimPrefix(snapshotShortName(l), "@")
		if !isBKSnapshotShortName(short, snapPrefix) {
			continue
		}

		common := false
		if g := snapshotGUIDKey(l); g != "" {
			if _, ok := remoteGUIDs[g]; ok {
				common = true
			}
		}
		if !common && short != "" {
			if _, ok := remoteShort[short]; ok {
				common = true
			}
		}
		if !common {
			continue
		}

		best = l
		found = true
	}
	return best, found
}

func foreignTargetSnapshots(local, remote []SnapshotInfo, snapPrefix string) []string {
	sourceGUIDs := make(map[string]struct{})
	sourceShort := make(map[string]struct{})
	for _, l := range local {
		if g := snapshotGUIDKey(l); g != "" {
			sourceGUIDs[g] = struct{}{}
		}
		if short := strings.TrimPrefix(snapshotShortName(l), "@"); short != "" {
			sourceShort[short] = struct{}{}
		}
	}

	out := make([]string, 0)
	for _, r := range remote {
		short := strings.TrimPrefix(snapshotShortName(r), "@")
		if short == "" {
			continue
		}
		if isBKSnapshotShortName(short, snapPrefix) {
			continue
		}
		if g := snapshotGUIDKey(r); g != "" {
			if _, ok := sourceGUIDs[g]; ok {
				continue
			}
		}
		if _, ok := sourceShort[short]; ok {
			continue
		}
		name := strings.TrimSpace(r.Name)
		if isValidZFSSnapshotName(name) {
			out = append(out, name)
		}
	}
	return out
}

func generationDatasetToken(dataset string) (int64, bool) {
	dataset = strings.TrimSpace(dataset)
	idx := strings.LastIndex(dataset, "_gen-")
	if idx < 0 {
		return 0, false
	}
	token := dataset[idx+len("_gen-"):]
	if dash := strings.IndexByte(token, '-'); dash >= 0 {
		token = token[:dash]
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(token, 36, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func staleBackupGenerationDatasets(activeDataset string, lineageDatasets []string, keepNewest int) []string {
	activeDataset = normalizeDatasetPath(activeDataset)
	if activeDataset == "" {
		return nil
	}
	if keepNewest < 0 {
		keepNewest = 0
	}

	genPrefix := activeDataset + "_gen-"
	type gen struct {
		dataset string
		token   int64
		hasTok  bool
	}

	gens := make([]gen, 0)
	for _, ds := range lineageDatasets {
		ds = normalizeDatasetPath(ds)
		if ds == "" || ds == activeDataset {
			continue
		}
		if !strings.HasPrefix(ds, genPrefix) {
			continue
		}
		tok, ok := generationDatasetToken(ds)
		gens = append(gens, gen{dataset: ds, token: tok, hasTok: ok})
	}

	if len(gens) <= keepNewest {
		return nil
	}

	sort.SliceStable(gens, func(i, j int) bool {
		if gens[i].hasTok && gens[j].hasTok {
			return gens[i].token > gens[j].token
		}
		if gens[i].hasTok != gens[j].hasTok {
			return gens[i].hasTok
		}
		return gens[i].dataset > gens[j].dataset
	})

	stale := make([]string, 0, len(gens)-keepNewest)
	for _, g := range gens[keepNewest:] {
		stale = append(stale, g.dataset)
	}
	return stale
}

func buildLocalRetentionPruneCandidates(snapshots []SnapshotInfo, keepCount int, protect map[string]struct{}, snapPrefix string) []string {
	if keepCount < 0 {
		keepCount = 0
	}

	grouped := make(map[string][]string)
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if !isValidZFSSnapshotName(name) {
			continue
		}
		if !isBKSnapshotShortName(snapshotShortName(snapshot), snapPrefix) {
			continue
		}
		dataset := snapshotDatasetName(name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snapshot.Dataset)
		}
		grouped[dataset] = append(grouped[dataset], name)
	}

	if len(grouped) == 0 {
		return []string{}
	}

	datasets := make([]string, 0, len(grouped))
	for dataset := range grouped {
		datasets = append(datasets, dataset)
	}
	sort.Strings(datasets)

	candidates := make([]string, 0)
	for _, dataset := range datasets {
		names := grouped[dataset]
		if len(names) <= keepCount {
			continue
		}
		deleteUpTo := len(names) - keepCount
		for i := 0; i < deleteUpTo; i++ {
			if protect != nil {
				if _, ok := protect[names[i]]; ok {
					continue
				}
			}
			candidates = append(candidates, names[i])
		}
	}

	return candidates
}

func (s *Service) localRetentionProtectSet(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	remoteActiveDataset string,
	snapPrefix string,
	localSnapshots []SnapshotInfo,
) map[string]struct{} {
	protect := make(map[string]struct{})

	newestPerDataset := make(map[string]SnapshotInfo)
	for _, snap := range localSnapshots {
		if !isBKSnapshotShortName(snapshotShortName(snap), snapPrefix) {
			continue
		}
		dataset := snapshotDatasetName(snap.Name)
		if dataset == "" {
			dataset = normalizeDatasetPath(snap.Dataset)
		}
		newestPerDataset[dataset] = snap
	}
	for _, snap := range newestPerDataset {
		if isValidZFSSnapshotName(snap.Name) {
			protect[snap.Name] = struct{}{}
		}
	}

	remoteActiveDataset = normalizeDatasetPath(remoteActiveDataset)
	if target != nil && remoteActiveDataset != "" {
		if remoteSnaps, err := s.listRemoteSnapshotsForDataset(ctx, target, remoteActiveDataset); err == nil {
			if base, ok := latestCommonBackupSnapshot(localSnapshots, remoteSnaps, snapPrefix); ok {
				if isValidZFSSnapshotName(base.Name) {
					protect[base.Name] = struct{}{}
				}
			}
		}
	}

	return protect
}

func (s *Service) trimTargetBackupGenerations(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	activeDataset string,
	keepNewest int,
) (int, error) {
	activeDataset = normalizeDatasetPath(activeDataset)
	if target == nil {
		return 0, fmt.Errorf("target_required")
	}
	if activeDataset == "" {
		return 0, fmt.Errorf("active_dataset_required")
	}

	lineage, err := s.listRemoteLineageDatasets(ctx, target, activeDataset)
	if err != nil {
		return 0, err
	}

	stale := staleBackupGenerationDatasets(activeDataset, lineage, keepNewest)
	destroyed := 0
	for _, ds := range stale {
		ds = normalizeDatasetPath(ds)
		if ds == "" || ds == activeDataset {
			continue
		}
		if _, err := s.runTargetSSH(ctx, target, "zfs", "destroy", "-r", ds); err != nil {
			logger.L.Warn().
				Err(err).
				Str("ssh_host", target.SSHHost).
				Str("dataset", ds).
				Msg("backup_generation_destroy_failed")
			continue
		}
		destroyed++
	}

	return destroyed, nil
}

func (s *Service) neutralizeForeignTargetSnapshots(
	ctx context.Context,
	target *clusterModels.BackupTarget,
	sourceDataset string,
	activeDataset string,
	snapPrefix string,
) ([]string, error) {
	sourceDataset = normalizeDatasetPath(sourceDataset)
	activeDataset = normalizeDatasetPath(activeDataset)
	if target == nil || sourceDataset == "" || activeDataset == "" {
		return nil, nil
	}
	if strings.TrimSpace(target.SSHHost) == "" {
		return nil, nil
	}
	if !datasetWithinRoot(target.BackupRoot, activeDataset) {
		return nil, fmt.Errorf("active_dataset_outside_backup_root")
	}

	exists, err := s.targetDatasetExists(ctx, target, activeDataset)
	if err != nil || !exists {
		return nil, err
	}

	remoteSnaps, err := s.listRemoteSnapshotsForDatasetRecursive(ctx, target, activeDataset)
	if err != nil {
		return nil, err
	}
	localSnaps, err := s.listLocalSnapshotsForDataset(ctx, sourceDataset)
	if err != nil {
		return nil, err
	}

	foreign := foreignTargetSnapshots(localSnaps, remoteSnaps, snapPrefix)
	if len(foreign) == 0 {
		return nil, nil
	}

	logger.L.Info().
		Str("ssh_host", target.SSHHost).
		Str("active", activeDataset).
		Strs("snapshots", foreign).
		Msg("backup_destroying_foreign_target_snapshots")

	if err := s.DestroyTargetSnapshotsByName(ctx, target, foreign); err != nil {
		return nil, err
	}

	return foreign, nil
}

func remoteActiveDatasetForSuffix(backupRoot, destSuffix string) string {
	backupRoot = normalizeDatasetPath(strings.TrimSpace(backupRoot))
	destSuffix = normalizeDatasetPath(destSuffix)
	if backupRoot == "" {
		return destSuffix
	}
	if destSuffix == "" {
		return backupRoot
	}
	return normalizeDatasetPath(backupRoot + "/" + destSuffix)
}
