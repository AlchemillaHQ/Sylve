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

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/zfsutil"
)

func (s *Service) ensureSylveDatasetsOnPool(ctx context.Context, poolName string) ([]*gzfs.Dataset, error) {
	return zfsutil.EnsureSylveNamespace(ctx, s.GZFS, poolName)
}
