// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package clusterHandlers

import (
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/services/auth"
)

const (
	joinClockProbeMaxRTT  = 2 * time.Second
	joinClockBehindMargin = 10 * time.Second
	joinClockAheadMargin  = time.Minute
)

func measureClockOffset(serverTime string, requestStart, responseAt time.Time) (time.Duration, bool) {
	peerTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(serverTime))
	if err != nil || requestStart.IsZero() || responseAt.Before(requestStart) {
		return 0, false
	}

	roundTrip := responseAt.Sub(requestStart)
	if roundTrip > joinClockProbeMaxRTT {
		return 0, false
	}

	return peerTime.Sub(requestStart.Add(roundTrip / 2)), true
}

func joinClockDriftBlocked(offset time.Duration) bool {
	return offset > auth.ClusterTokenFutureSkew-joinClockBehindMargin ||
		offset < -(auth.ClusterTokenTTL-joinClockAheadMargin)
}

func formatClockOffset(offset time.Duration) string {
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return sign + offset.Round(time.Second).String()
}
