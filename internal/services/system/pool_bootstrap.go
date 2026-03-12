// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"fmt"
	"strings"

	"github.com/alchemillahq/gzfs"
)

var requiredSylveDatasets = []string{
	"sylve",
	"sylve/virtual-machines",
	"sylve/jails",
}

func (s *Service) ensureSylveDatasetsOnPool(ctx context.Context, poolName string) ([]*gzfs.Dataset, error) {
	var created []*gzfs.Dataset

	for _, dataset := range requiredSylveDatasets {
		fullDatasetName := fmt.Sprintf("%s/%s", poolName, dataset)
		found, err := s.GZFS.ZFS.Get(ctx, fullDatasetName, false)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return nil, fmt.Errorf("error_checking_dataset_%s: %w", fullDatasetName, err)
		}

		if found != nil {
			continue
		}

		newDataset, err := s.GZFS.ZFS.CreateFilesystem(ctx, fullDatasetName, nil)
		if err != nil {
			return nil, fmt.Errorf("error_creating_dataset_%s: %w", fullDatasetName, err)
		}

		created = append(created, newDataset)
	}

	return created, nil
}
