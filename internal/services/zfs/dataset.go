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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/vmihailenco/msgpack/v5"
)

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

func (s *Service) BulkDeleteDatasetByNames(ctx context.Context, names []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var datasets []*gzfs.Dataset

	for _, name := range names {
		ds, err := s.GZFS.ZFS.Get(ctx, name, false)
		if err != nil {
			return fmt.Errorf("failed to get dataset with name %s: %w", name, err)
		}

		datasets = append(datasets, ds)
	}

	for _, dataset := range datasets {
		if err := dataset.Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_dataset_with_name_%s:_%w", dataset.Name, err)
		}
	}

	hasG := false
	hasS := false

	for _, ds := range datasets {
		if ds.Type == gzfs.DatasetTypeFilesystem || ds.Type == gzfs.DatasetTypeVolume {
			hasG = true
		} else if ds.Type == gzfs.DatasetTypeSnapshot {
			hasS = true
		}
	}

	if hasG {
		s.SignalDSChange("", "", "generic-dataset", "bulk_delete")
	}

	if hasS {
		s.SignalDSChange("", "", "snapshot", "bulk_delete")
	}

	return nil
}

func (s *Service) BulkDeleteDataset(ctx context.Context, guids []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	var count int64
	if err := s.DB.Model(&vmModels.VMStorageDataset{}).
		Where("guid IN ?", guids).
		Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check if datasets are in use: %w", err)
	}

	if count > 0 {
		return fmt.Errorf("datasets_in_use_by_vm")
	}

	datasets, err := s.GZFS.ZFS.List(
		ctx,
		true,
		"",
	)

	if err != nil {
		return err
	}

	available := make(map[string]*gzfs.Dataset)
	for _, ds := range datasets {
		available[ds.GUID] = ds
	}

	cantDelete := []string{"sylve", "sylve/virtual-machines", "sylve/jails"}

	for _, guid := range guids {
		if _, ok := available[guid]; !ok {
			return fmt.Errorf("dataset with guid %s not found", guid)
		}

		for _, name := range cantDelete {
			if strings.HasSuffix(available[guid].Name, name) {
				return fmt.Errorf("cannot_delete_critical_filesystem")
			}
		}
	}

	for _, guid := range guids {
		if err := available[guid].Destroy(ctx, true, false); err != nil {
			return fmt.Errorf("failed_to_delete_dataset_with_guid_%s:_%w", guid, err)
		}
	}

	return nil
}

func (s *Service) IsDatasetInUse(guid string, failEarly bool) bool {
	var count int64
	if err := s.DB.Model(&vmModels.Storage{}).Where("dataset = ?", guid).
		Count(&count).Error; err != nil {
		return false
	}

	if count > 0 {
		if failEarly {
			return true
		}

		var storage vmModels.Storage

		if err := s.DB.Model(&vmModels.Storage{}).Where("dataset = ?", guid).
			First(&storage).Error; err != nil {
			return false
		}

		if storage.VMID > 0 {
			var vm vmModels.VM
			if err := s.DB.Model(&vmModels.VM{}).Where("id = ?", storage.VMID).
				First(&vm).Error; err != nil {
				return false
			}

			domain, err := s.Libvirt.GetLvDomain(vm.RID)
			if err != nil {
				return false
			}

			if domain != nil {
				if domain.Status == "Running" || domain.Status == "Paused" {
					return true
				} else {
					return false
				}
			}
		}
	}

	return false
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

	if b, err := MsgpackEncode(datasets); err == nil {
		err = db.SetValue(cacheKey, b, ttl)
		if err != nil {
			logger.L.Debug().Msg("ZFS datasets cache setting failed")
		} else {
			logger.L.Debug().Msgf("ZFS datasets cache refreshed %d items", len(datasets))
		}
	}

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

func (s *Service) SignalDSChange(pool, name, t, action string) {
	enqueueCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if t != "generic-dataset" && t != "snapshot" {
		logger.L.Debug().Msg("Error signalling dataset change via queue, wrong type requested")
		return
	}

	job := zfsServiceInterfaces.ZFSHistoryBatchJob{
		Pool:     pool,
		Kind:     t,
		Datasets: []string{name},
		Actions:  []string{action},
	}

	_ = db.EnqueueJSON(enqueueCtx, "zfs_history_batch", job)
}
