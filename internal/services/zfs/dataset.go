// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/vmihailenco/msgpack/v5"
)

var ErrCannotDeletePoolRootDataset = errors.New("cannot_delete_pool_root_dataset")
var ErrCannotDeleteSystemFilesystem = errors.New("cannot_delete_system_filesystem")

func (s *Service) guardStorageTopologyMutation(ctx context.Context, datasets ...*gzfs.Dataset) (func(), error) {
	if s.StorageTopologyGuard == nil {
		return func() {}, nil
	}
	var targets []string
	for _, dataset := range datasets {
		if dataset.Name != "" {
			targets = append(targets, dataset.Name)
		}
		if filepath.IsAbs(dataset.Mountpoint) {
			targets = append(targets, dataset.Mountpoint)
		}
	}
	release, err := s.StorageTopologyGuard(ctx, targets...)
	if err != nil {
		return nil, classifyError(ErrConflict, "storage_topology_change_blocked: %v", err)
	}
	return release, nil
}

func (s *Service) guardDownloadStorageMutation(ctx context.Context, names ...string) (func(), error) {
	release, err := s.DownloadStorage.TryMutation(names...)
	if err != nil {
		return nil, downloadStorageMutationError(err)
	}
	if err := downloadstorage.RequireDatasetUnused(ctx, s.DB, names...); err != nil {
		release()
		return nil, downloadStorageMutationError(err)
	}
	return release, nil
}

func validateDatasetDeletionTargets(datasets ...*gzfs.Dataset) error {
	for _, dataset := range datasets {
		if dataset != nil && dataset.Name == dataset.Pool {
			return fmt.Errorf("%w: %s", ErrCannotDeletePoolRootDataset, dataset.Name)
		}
	}

	return nil
}

var criticalFilesystems = []string{
	"sylve",
	"sylve/virtual-machines",
	"sylve/jails",
	"sylve/downloads",
	"sylve/containers",
	containerModels.ImagesDataset,
}

func validateCriticalDatasetDeletion(dataset *gzfs.Dataset) error {
	relativeName := strings.TrimPrefix(dataset.Name, dataset.Pool+"/")
	if slices.Contains(criticalFilesystems, relativeName) {
		return classifyError(ErrConflict, "cannot_delete_critical_filesystem")
	}
	if strings.HasPrefix(relativeName, containerModels.ImagesDataset+"/") {
		return classifyError(ErrConflict, "cannot_delete_container_image_layer")
	}
	return nil
}

var systemMountpoints = []string{"/", "/etc", "/home", "/root", "/tmp"}
var systemMountpointTrees = []string{"/boot", "/usr", "/var"}

func isSystemMountpoint(mountpoint string) bool {
	mountpoint = strings.TrimSpace(mountpoint)
	if !filepath.IsAbs(mountpoint) {
		return false
	}
	mountpoint = filepath.Clean(mountpoint)
	if slices.Contains(systemMountpoints, mountpoint) {
		return true
	}
	for _, tree := range systemMountpointTrees {
		if mountpoint == tree || strings.HasPrefix(mountpoint, tree+"/") {
			return true
		}
	}
	return false
}

func validateSystemFilesystemDeletion(affected []*gzfs.Dataset) error {
	for _, dataset := range affected {
		if dataset != nil && dataset.Type == gzfs.DatasetTypeFilesystem && isSystemMountpoint(dataset.Mountpoint) {
			return fmt.Errorf("%w: %s", ErrCannotDeleteSystemFilesystem, dataset.Name)
		}
	}
	return nil
}

func MsgpackEncode(d []*gzfs.Dataset) ([]byte, error) {
	return msgpack.Marshal(d)
}

func MsgpackDecode(b []byte) ([]*gzfs.Dataset, error) {
	var d []*gzfs.Dataset
	return d, msgpack.Unmarshal(b, &d)
}

func (s *Service) GetDatasets(ctx context.Context, t gzfs.DatasetType) ([]*gzfs.Dataset, error) {
	var (
		datasets []*gzfs.Dataset
		err      error
	)

	datasets, err = s.GZFS.ZFS.ListByType(
		ctx,
		t,
		true,
		"",
	)

	if err != nil {
		return nil, err
	}
	return s.usablePoolDatasets(ctx, datasets)
}

func (s *Service) CachedDatasets(ctx context.Context, t gzfs.DatasetType) ([]*gzfs.Dataset, error) {
	datasets, err := s.datasetsForRead(ctx, t)
	if err != nil {
		return nil, err
	}
	return s.usablePoolDatasets(ctx, datasets)
}

func (s *Service) usablePoolDatasets(ctx context.Context, datasets []*gzfs.Dataset) ([]*gzfs.Dataset, error) {
	pools, err := s.GetUsablePools(ctx)
	if err != nil {
		return nil, err
	}

	usablePools := make(map[string]struct{}, len(pools))
	for _, pool := range pools {
		usablePools[pool.Name] = struct{}{}
	}

	filtered := make([]*gzfs.Dataset, 0, len(datasets))
	for _, dataset := range datasets {
		if dataset.Pool == "" {
			continue
		}

		if _, ok := usablePools[dataset.Pool]; !ok {
			continue
		}

		filtered = append(filtered, dataset)
	}

	return filtered, nil
}

func normalizeDatasetDeletionTargets(
	targets []zfsServiceInterfaces.DatasetDeletionTarget,
) ([]zfsServiceInterfaces.DatasetDeletionTarget, error) {
	if len(targets) == 0 {
		return nil, classifyError(ErrInvalidRequest, "at_least_one_dataset_target_required")
	}

	normalized := make([]zfsServiceInterfaces.DatasetDeletionTarget, 0, len(targets))
	seenNames := make(map[string]string, len(targets))
	for _, target := range targets {
		name := strings.Trim(strings.TrimSpace(target.Name), "/")
		guid := strings.TrimSpace(target.GUID)
		if name == "" || guid == "" {
			return nil, classifyError(ErrInvalidRequest, "dataset_name_and_guid_required")
		}
		if existingGUID, exists := seenNames[name]; exists {
			if existingGUID != guid {
				return nil, classifyError(ErrInvalidRequest, "dataset_name_%s_has_multiple_guids", name)
			}
			continue
		}
		seenNames[name] = guid
		normalized = append(normalized, zfsServiceInterfaces.DatasetDeletionTarget{
			Name: name,
			GUID: guid,
		})
	}

	return normalized, nil
}

func independentDatasetDeletionRoots(selected []*gzfs.Dataset) []*gzfs.Dataset {
	roots := make([]*gzfs.Dataset, 0, len(selected))
	for i, candidate := range selected {
		covered := false
		for j, other := range selected {
			if i != j && datasetNameInTree(candidate.Name, other.Name) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, candidate)
		}
	}
	return roots
}

func (s *Service) datasetsAffectedByDeletion(
	ctx context.Context,
	roots []*gzfs.Dataset,
) ([]*gzfs.Dataset, error) {
	affected := make([]*gzfs.Dataset, 0, len(roots))
	seenNames := make(map[string]struct{}, len(roots))
	appendUnique := func(dataset *gzfs.Dataset) {
		if dataset == nil || dataset.GUID == "" {
			return
		}
		if _, exists := seenNames[dataset.Name]; exists {
			return
		}
		seenNames[dataset.Name] = struct{}{}
		affected = append(affected, dataset)
	}

	for _, root := range roots {
		appendUnique(root)
		if root.Type == gzfs.DatasetTypeSnapshot {
			continue
		}

		subtree, err := s.GZFS.ZFS.ListByType(
			ctx,
			gzfs.DatasetTypeAll,
			true,
			root.Name,
		)
		if err != nil {
			return nil, fmt.Errorf("failed_to_list_dataset_subtree_%s: %w", root.Name, err)
		}
		for _, dataset := range subtree {
			appendUnique(dataset)
		}
	}

	return affected, nil
}

func (s *Service) BulkDeleteDataset(
	ctx context.Context,
	targets []zfsServiceInterfaces.DatasetDeletionTarget,
) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	targets, err := normalizeDatasetDeletionTargets(targets)
	if err != nil {
		return err
	}

	selected := make([]*gzfs.Dataset, 0, len(targets))
	for _, target := range targets {
		dataset, lookupErr := s.GZFS.ZFS.Get(ctx, target.Name, false)
		if lookupErr != nil || dataset == nil {
			return datasetLookupError(lookupErr, "dataset_%s_not_found", target.Name)
		}
		if dataset.GUID != target.GUID {
			return classifyError(ErrConflict, "dataset_identity_changed_for_%s", target.Name)
		}
		switch dataset.Type {
		case gzfs.DatasetTypeFilesystem, gzfs.DatasetTypeVolume, gzfs.DatasetTypeSnapshot:
		default:
			return classifyError(ErrInvalidRequest, "unsupported_dataset_type_%s", dataset.Type)
		}

		if err := validateDatasetDeletionTargets(dataset); err != nil {
			return err
		}
		if err := validateCriticalDatasetDeletion(dataset); err != nil {
			return err
		}
		selected = append(selected, dataset)
	}
	var names []string
	var topologyTargets []*gzfs.Dataset
	for _, dataset := range selected {
		if dataset.Type != gzfs.DatasetTypeSnapshot {
			names = append(names, dataset.Name)
			topologyTargets = append(topologyTargets, dataset)
		}
	}
	release, err := s.guardDownloadStorageMutation(ctx, names...)
	if err != nil {
		return err
	}
	defer release()
	if len(names) > 0 {
		releaseTopology, err := s.guardStorageTopologyMutation(ctx, topologyTargets...)
		if err != nil {
			return err
		}
		defer releaseTopology()
	}

	// A recursive parent deletion already covers selected descendants and
	// snapshots. Keeping only independent roots avoids a second destroy call on
	// an object removed by an earlier call.
	roots := independentDatasetDeletionRoots(selected)
	affectedDatasets, err := s.datasetsAffectedByDeletion(ctx, roots)
	if err != nil {
		return err
	}
	if err := validateSystemFilesystemDeletion(affectedDatasets); err != nil {
		return err
	}
	if err := s.requireVolumesNotShared(ctx, affectedDatasets); err != nil {
		return err
	}
	affectedGUIDs := make([]string, 0, len(affectedDatasets))
	for _, dataset := range affectedDatasets {
		affectedGUIDs = append(affectedGUIDs, dataset.GUID)
	}

	var count int64
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("guid IN ?", affectedGUIDs).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if datasets are in use: %w", err)
	}
	if count > 0 {
		return classifyError(ErrConflict, "datasets_in_use_by_vm")
	}
	inUse, err := containerModels.DatasetsInUse(s.DB, affectedGUIDs)
	if err != nil {
		return fmt.Errorf("failed to check if datasets are in use: %w", err)
	}
	if inUse {
		return classifyError(ErrConflict, "datasets_in_use_by_container")
	}

	for _, dataset := range roots {
		if err := dataset.Destroy(ctx, dataset.Type != gzfs.DatasetTypeSnapshot, false); err != nil {
			return s.reconcileFailedDeletion(ctx, affectedDatasets,
				fmt.Errorf("failed_to_delete_dataset_with_guid_%s:_%w", dataset.GUID, err))
		}
	}

	hasGenericDatasets := false
	hasSnapshots := false
	for _, dataset := range affectedDatasets {
		switch dataset.Type {
		case gzfs.DatasetTypeFilesystem, gzfs.DatasetTypeVolume:
			hasGenericDatasets = true
			if dataset.IsEncrypted() {
				cleanupEncryptionKeyForDataset(dataset)
			}
		case gzfs.DatasetTypeSnapshot:
			hasSnapshots = true
		}
	}
	if hasGenericDatasets {
		s.SignalDSChange("", "", "generic-dataset", "bulk_delete")
	}
	if hasSnapshots {
		s.SignalDSChange("", "", "snapshot", "bulk_delete")
	}

	return s.notifyDatasetsDeleted(ctx, affectedGUIDs)
}

func (s *Service) reconcileFailedDeletion(ctx context.Context, affected []*gzfs.Dataset, destroyErr error) error {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	s.SignalDSChange("", "", "generic-dataset", "delete_partial")
	s.SignalDSChange("", "", "snapshot", "delete_partial")

	gone, err := s.datasetsNoLongerPresent(reconcileCtx, affected)
	if err != nil {
		logger.L.Warn().Err(err).Msg("Could not check which datasets a failed deletion removed")
		return destroyErr
	}
	guids := make([]string, 0, len(gone))
	for _, dataset := range gone {
		guids = append(guids, dataset.GUID)
		if dataset.Type != gzfs.DatasetTypeSnapshot && dataset.IsEncrypted() {
			cleanupEncryptionKeyForDataset(dataset)
		}
	}
	if err := s.notifyDatasetsDeleted(reconcileCtx, guids); err != nil {
		return errors.Join(destroyErr, err)
	}
	return destroyErr
}

func (s *Service) datasetsNoLongerPresent(ctx context.Context, datasets []*gzfs.Dataset) ([]*gzfs.Dataset, error) {
	poolOf := func(dataset *gzfs.Dataset) string {
		if dataset.Pool != "" {
			return dataset.Pool
		}
		pool, _, _ := strings.Cut(dataset.Name, "/")
		pool, _, _ = strings.Cut(pool, "@")
		return pool
	}
	present := make(map[string]struct{})
	listedPools := make(map[string]struct{})
	for _, dataset := range datasets {
		if dataset == nil || poolOf(dataset) == "" {
			continue
		}
		pool := poolOf(dataset)
		if _, listed := listedPools[pool]; listed {
			continue
		}
		listedPools[pool] = struct{}{}
		current, err := s.GZFS.ZFS.ListByType(ctx, gzfs.DatasetTypeAll, true, pool)
		if err != nil {
			return nil, fmt.Errorf("failed to list pool %s: %w", pool, err)
		}
		for _, existing := range current {
			if existing != nil {
				present[existing.GUID] = struct{}{}
			}
		}
	}
	gone := make([]*gzfs.Dataset, 0, len(datasets))
	for _, dataset := range datasets {
		if dataset == nil || dataset.GUID == "" || poolOf(dataset) == "" {
			continue
		}
		if _, exists := present[dataset.GUID]; !exists {
			gone = append(gone, dataset)
		}
	}
	return gone, nil
}

func (s *Service) IsDatasetInUse(guid string, failEarly bool) bool {
	if err := downloadstorage.RequireDatasetGUIDUnused(context.Background(), s.DB, guid); err != nil {
		return true
	}
	if inUse, err := containerModels.DatasetsInUse(s.DB, []string{guid}); err != nil || inUse {
		return true
	}
	var count int64

	if err := s.DB.
		Model(&vmModels.Storage{}).
		Joins("JOIN vm_storage_datasets d ON d.id = vm_storages.dataset_id").
		Where("d.guid = ?", guid).
		Count(&count).Error; err != nil || count == 0 {
		return false
	}

	if failEarly {
		return true
	}

	var storage vmModels.Storage
	if err := s.DB.
		Joins("JOIN vm_storage_datasets d ON d.id = vm_storages.dataset_id").
		Where("d.guid = ?", guid).
		First(&storage).Error; err != nil {
		return false
	}

	if storage.VMID == 0 {
		return false
	}

	var vm vmModels.VM
	if err := s.DB.First(&vm, storage.VMID).Error; err != nil {
		return false
	}

	domain, err := s.Libvirt.GetLvDomain(vm.RID)
	if err != nil || domain == nil {
		return false
	}

	return domain.Status == "Running" || domain.Status == "Paused"
}

func (s *Service) RefreshDatasets(
	ctx context.Context,
	datasetType gzfs.DatasetType,
	ttl int64,
) error {
	datasets, err := s.GZFS.ZFS.ListByType(
		ctx,
		datasetType,
		true,
		"",
	)

	if err != nil {
		return err
	}

	cacheKey := datasetCacheKey(datasetType)

	b, err := MsgpackEncode(datasets)
	if err != nil {
		return fmt.Errorf("failed to encode %s datasets cache: %w", datasetType, err)
	}
	if err := db.SetValue(cacheKey, b, ttl); err != nil {
		return fmt.Errorf("failed to store %s datasets cache: %w", datasetType, err)
	}

	logger.L.Debug().Msgf("ZFS datasets cache refreshed %d items", len(datasets))
	return nil
}

func (s *Service) getCachedDatasets(
	ctx context.Context,
	datasetType gzfs.DatasetType,
) ([]*gzfs.Dataset, error) {
	cacheKey := datasetCacheKey(datasetType)

	if b, ok := db.GetValue(cacheKey); ok {
		datasets, err := MsgpackDecode(b)
		if err == nil {
			return datasets, nil
		}
	}

	logger.L.Debug().Msg("getCachedDatasets miss, returning empty :(")
	return []*gzfs.Dataset{}, nil
}

func (s *Service) DatasetByGUID(ctx context.Context, guid string) (*gzfs.Dataset, error) {
	if name := s.cachedDatasetName(ctx, guid); name != "" {
		dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err == nil && dataset != nil && dataset.GUID == guid {
			return dataset, nil
		}
	}
	return s.GZFS.ZFS.GetByGUID(ctx, guid, false)
}

func (s *Service) cachedDatasetName(ctx context.Context, guid string) string {
	if db.CacheDB == nil || guid == "" {
		return ""
	}
	name := ""
	for _, datasetType := range []gzfs.DatasetType{gzfs.DatasetTypeFilesystem, gzfs.DatasetTypeVolume} {
		datasets, _ := s.getCachedDatasets(ctx, datasetType)
		for _, dataset := range datasets {
			if dataset == nil || dataset.GUID != guid {
				continue
			}
			if name != "" {
				return ""
			}
			name = dataset.Name
		}
	}
	return name
}

func applySort(datasets []*gzfs.Dataset, field, dir string) {
	if field == "" {
		return
	}

	less := func(i, j int) bool { return false }

	switch field {
	case "name":
		less = func(i, j int) bool {
			return datasets[i].Name < datasets[j].Name
		}
	case "used":
		less = func(i, j int) bool {
			return datasets[i].Used < datasets[j].Used
		}
	case "referenced":
		less = func(i, j int) bool {
			return datasets[i].Referenced < datasets[j].Referenced
		}
	case "creation":
		less = func(i, j int) bool {
			return datasetCreation(datasets[i]) < datasetCreation(datasets[j])
		}
	default:
		return
	}

	if dir == "desc" {
		sort.SliceStable(datasets, func(i, j int) bool {
			return less(j, i)
		})
	} else {
		sort.SliceStable(datasets, less)
	}
}

func datasetCreation(dataset *gzfs.Dataset) int64 {
	created, _ := strconv.ParseInt(dataset.Properties["creation"].Value, 10, 64)
	return created
}

func (s *Service) GetPaginatedDatasets(
	ctx context.Context,
	req *zfsServiceInterfaces.PaginatedDatasetsRequest,
) (*zfsServiceInterfaces.PaginatedDatasetsResponse, error) {
	if req.Size <= 0 {
		req.Size = 25
	}
	if req.Page <= 0 {
		req.Page = 1
	}

	search := strings.ToLower(req.Search)
	nameFilter := strings.ToLower(req.NameFilter)
	var nameFilters []string
	for _, f := range strings.Split(nameFilter, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			nameFilters = append(nameFilters, f)
		}
	}
	var (
		wg           sync.WaitGroup
		summaries    []zfsServiceInterfaces.SnapshotSummary
		summariesErr error
	)
	if req.DatasetType == gzfs.DatasetTypeSnapshot || req.DatasetType == gzfs.DatasetTypeAll {
		wg.Go(func() {
			summaries, summariesErr = s.snapshotSummariesForRead(ctx)
		})
	}

	datasets, err := s.datasetsForRead(ctx, req.DatasetType)
	wg.Wait()
	if err != nil {
		return nil, err
	}

	filtered := filterPaginatedDatasets(datasets, search, nameFilters)

	if summariesErr != nil {
		logger.L.Warn().Err(summariesErr).Msg("Failed to load snapshot details for the paginated list")
	} else if summaries != nil {
		attachSnapshotDetails(filtered, summaries)
	}

	if len(req.Sort) > 0 {
		s0 := req.Sort[0]
		applySort(filtered, s0.Field, s0.Dir)
	}

	total := len(filtered)
	if total == 0 {
		return &zfsServiceInterfaces.PaginatedDatasetsResponse{
			LastPage: 0,
			Data:     []*gzfs.Dataset{},
		}, nil
	}

	lastPage := (total + req.Size - 1) / req.Size

	if req.Page > lastPage {
		return &zfsServiceInterfaces.PaginatedDatasetsResponse{
			LastPage: lastPage,
			Data:     []*gzfs.Dataset{},
		}, nil
	}

	start := (req.Page - 1) * req.Size
	end := start + req.Size
	if end > total {
		end = total
	}

	return &zfsServiceInterfaces.PaginatedDatasetsResponse{
		LastPage: lastPage,
		Data:     filtered[start:end],
	}, nil
}

func isContainerImageDataset(name string) bool {
	dataset, _, _ := strings.Cut(name, "@")
	pool, relative, ok := strings.Cut(dataset, "/")
	if !ok || pool == "" {
		return false
	}
	return relative == containerModels.ImagesDataset || strings.HasPrefix(relative, containerModels.ImagesDataset+"/")
}

func filterPaginatedDatasets(datasets []*gzfs.Dataset, search string, nameFilters []string) []*gzfs.Dataset {
	filtered := make([]*gzfs.Dataset, 0, len(datasets))
	for _, ds := range datasets {
		if ds == nil || isContainerImageDataset(ds.Name) {
			continue
		}
		lowName := strings.ToLower(ds.Name)
		if search != "" && !strings.Contains(lowName, search) {
			continue
		}
		skip := false
		for _, f := range nameFilters {
			if matchesNameFilter(lowName, f) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, ds)
		}
	}
	return filtered
}

var builtinSnapshotNameFilters = map[string][]string{
	"bk_j":      {"bk_j"},
	"svms_":     {"svms_"},
	"sjs_":      {"sjs_"},
	"ha_":       {"ha_"},
	"_template": {"sylve_template_", "sylve_vm_template_"},
}

func matchesNameFilter(lowName, filter string) bool {
	prefixes, builtin := builtinSnapshotNameFilters[filter]
	if !builtin {
		return strings.Contains(lowName, filter)
	}
	_, snapshot, isSnapshot := strings.Cut(lowName, "@")
	if !isSnapshot {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(snapshot, prefix) {
			return true
		}
	}
	return false
}

func (s *Service) SignalDSChange(_, _, kind, _ string) {
	if err := s.invalidateCache(context.Background(), kind); err != nil {
		logger.L.Error().Err(err).Str("kind", kind).Msg("Failed to invalidate ZFS datasets cache")
	}
}
