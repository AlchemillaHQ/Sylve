// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import "time"

const (
	raftLeaderPollInterval       = 50 * time.Millisecond
	raftApplyTimeout             = 5 * time.Second
	raftLeaderWaitTimeout        = 10 * time.Second
	raftTransportTimeout         = 10 * time.Second
	raftTransportMaxPool         = 3
	raftSnapshotThreshold        = 1024
	clusterNodePopulateInterval  = 60 * time.Second
	defaultEventListLimit        = 200
)
