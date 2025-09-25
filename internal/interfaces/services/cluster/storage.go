// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterServiceInterfaces

import clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"

type Storages struct {
	S3          []clusterModels.ClusterS3Config        `json:"s3"`
	Directories []clusterModels.ClusterDirectoryConfig `json:"directories"`
}
