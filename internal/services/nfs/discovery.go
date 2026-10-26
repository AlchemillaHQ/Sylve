// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alchemillahq/gzfs"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
)

var filesystemProperties = []string{"name", "guid", "type", "mountpoint", "mounted", "sharenfs", "keystatus"}

// One bounded, narrow query, either for the editor inventory or for the exact
// applied names. Never enumerate unrelated filesystems during health checks.
func (d *nativeRuntime) filesystems(ctx context.Context, names []string) ([]*gzfs.Dataset, error) {
	args := []string{"list", "-p", "-j", "-o", strings.Join(filesystemProperties, ","), "-t", "filesystem"}
	args = append(args, names...)
	result, err := d.command(ctx, "/sbin/zfs", args...)
	if err != nil {
		return nil, fmt.Errorf("inspect NFS filesystems: %w", errors.Join(err, ctx.Err()))
	}
	var response gzfs.DatasetList
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		return nil, fmt.Errorf("decode NFS filesystem properties: %w", err)
	}
	datasets := make([]*gzfs.Dataset, 0, len(response.Datasets))
	for name, dataset := range response.Datasets {
		if dataset == nil || dataset.Name != name {
			return nil, errors.New("ZFS returned an incomplete filesystem identity")
		}
		for _, property := range filesystemProperties {
			if property == "name" {
				continue // ZFS emits the name as an identity field, not a property.
			}
			if _, ok := dataset.Properties[property]; !ok {
				return nil, fmt.Errorf("ZFS omitted filesystem property %s", property)
			}
		}
		dataset.GUID = dataset.Properties["guid"].Value
		dataset.Mountpoint = dataset.Properties["mountpoint"].Value
		if dataset.Type != gzfs.DatasetTypeFilesystem || !validGUID(dataset.GUID) {
			return nil, errors.New("ZFS returned an invalid filesystem identity")
		}
		datasets = append(datasets, dataset)
	}
	if len(names) > 0 {
		if len(datasets) != len(names) {
			return nil, errors.New("ZFS did not return every requested filesystem")
		}
		for _, dataset := range datasets {
			if !slices.Contains(names, dataset.Name) {
				return nil, errors.New("ZFS returned an unrequested filesystem")
			}
		}
	}
	return datasets, nil
}

func (s *Service) checkFilesystems(ctx context.Context, filesystems []nfsModels.ExportedFilesystem, pools []string) error {
	if len(filesystems) == 0 {
		return nil
	}
	names := make([]string, 0, len(filesystems))
	for _, filesystem := range filesystems {
		names = append(names, filesystem.Name)
	}
	datasets, err := s.inspectDatasets(ctx, names)
	if err != nil {
		return err
	}
	byName := make(map[string]*gzfs.Dataset, len(datasets))
	for _, dataset := range datasets {
		if dataset == nil || byName[dataset.Name] != nil {
			return errors.New("ZFS returned an incomplete or ambiguous filesystem identity")
		}
		byName[dataset.Name] = dataset
	}
	for _, filesystem := range filesystems {
		dataset := byName[filesystem.Name]
		if dataset == nil || dataset.GUID != filesystem.GUID || dataset.Mountpoint != filesystem.Mountpoint {
			return fmt.Errorf("exported filesystem identity changed: %s", filesystem.Name)
		}
		if err := s.checkDataset(dataset, pools); err != nil {
			return err
		}
	}
	return nil
}
