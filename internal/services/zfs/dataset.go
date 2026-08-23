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
	"sort"
	"strings"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/vmihailenco/msgpack/v5"
)

var ErrCannotDeletePoolRootDataset = errors.New("cannot_delete_pool_root_dataset")

func validateDatasetDeletionTargets(datasets ...*gzfs.Dataset) error {
	for _, dataset := range datasets {
		if dataset != nil && dataset.Name == dataset.Pool {
			return fmt.Errorf("%w: %s", ErrCannotDeletePoolRootDataset, dataset.Name)
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
	seenGUIDs := make(map[string]string, len(targets))
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
		if existingName, exists := seenGUIDs[guid]; exists {
			return nil, classifyError(
				ErrInvalidRequest,
				"dataset_guid_%s_has_multiple_names_%s_and_%s",
				guid,
				existingName,
				name,
			)
		}

		seenNames[name] = guid
		seenGUIDs[guid] = name
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
	seenGUIDs := make(map[string]struct{}, len(roots))
	appendUnique := func(dataset *gzfs.Dataset) {
		if dataset == nil || dataset.GUID == "" {
			return
		}
		if _, exists := seenGUIDs[dataset.GUID]; exists {
			return
		}
		seenGUIDs[dataset.GUID] = struct{}{}
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

	cantDelete := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}
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
		relativeName := strings.TrimPrefix(dataset.Name, dataset.Pool+"/")
		for _, name := range cantDelete {
			if relativeName == name {
				return classifyError(ErrConflict, "cannot_delete_critical_filesystem")
			}
		}
		selected = append(selected, dataset)
	}

	// A recursive parent deletion already covers selected descendants and
	// snapshots. Keeping only independent roots avoids a second destroy call on
	// an object removed by an earlier call.
	roots := independentDatasetDeletionRoots(selected)
	affectedDatasets, err := s.datasetsAffectedByDeletion(ctx, roots)
	if err != nil {
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

	for _, dataset := range roots {
		if err := dataset.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_dataset_with_guid_%s:_%w", dataset.GUID, err)
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

func (s *Service) IsDatasetInUse(guid string, failEarly bool) bool {
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

	cacheKey := fmt.Sprintf("zfs:datasets:%s:v1", datasetType)

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
	cacheKey := fmt.Sprintf("zfs:datasets:%s:v1", datasetType)

	if b, ok := db.GetValue(cacheKey); ok {
		datasets, err := MsgpackDecode(b)
		if err == nil {
			return datasets, nil
		}
	}

	logger.L.Debug().Msg("getCachedDatasets miss, returning empty :(")
	return []*gzfs.Dataset{}, nil
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
	default:
		return
	}

	if dir == "desc" {
		sort.Slice(datasets, func(i, j int) bool {
			return !less(i, j)
		})
	} else {
		sort.Slice(datasets, less)
	}
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
	datasets, err := s.getCachedDatasets(ctx, req.DatasetType)

	if err != nil {
		return nil, err
	}

	filtered := make([]*gzfs.Dataset, 0, len(datasets))
	for _, ds := range datasets {
		if search != "" &&
			!strings.Contains(strings.ToLower(ds.Name), search) {
			continue
		}
		lowName := strings.ToLower(ds.Name)
		skip := false
		for _, f := range nameFilters {
			if strings.Contains(lowName, f) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		filtered = append(filtered, ds)
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

func (s *Service) SignalDSChange(_, _, kind, _ string) {
	if err := s.invalidateCache(context.Background(), kind); err != nil {
		logger.L.Error().Err(err).Str("kind", kind).Msg("Failed to invalidate ZFS datasets cache")
	}
}
