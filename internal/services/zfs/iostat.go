// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	poolIOSampleIntervalSeconds = 10
	poolIOStaleAfter            = 3 * poolIOSampleIntervalSeconds * time.Second
	poolIOMonitorRestartDelay   = 15 * time.Second
)

// poolIOStat contains the per-second logical I/O rates emitted by zpool
// iostat. Latency values are average total wait time (queueing plus device
// service time) for the sample interval.
type poolIOStat struct {
	ReadIOPS              uint64
	WriteIOPS             uint64
	ReadBytesPerSecond    uint64
	WriteBytesPerSecond   uint64
	ReadLatencyNanos      uint64
	WriteLatencyNanos     uint64
	ReadLatencyAvailable  bool
	WriteLatencyAvailable bool
	SampledAt             time.Time
}

func parseZpoolIOStatLine(line string) (string, poolIOStat, error) {
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return "", poolIOStat{}, fmt.Errorf("expected at least 7 zpool iostat fields, got %d", len(fields))
	}

	parseRequired := func(index int, label string) (uint64, error) {
		value, err := strconv.ParseUint(fields[index], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s %q: %w", label, fields[index], err)
		}
		return value, nil
	}
	parseOptional := func(index int, label string) (uint64, error) {
		if len(fields) <= index || fields[index] == "-" {
			return 0, nil
		}
		return parseRequired(index, label)
	}

	readIOPS, err := parseRequired(3, "read IOPS")
	if err != nil {
		return "", poolIOStat{}, err
	}
	writeIOPS, err := parseRequired(4, "write IOPS")
	if err != nil {
		return "", poolIOStat{}, err
	}
	readBytes, err := parseRequired(5, "read bandwidth")
	if err != nil {
		return "", poolIOStat{}, err
	}
	writeBytes, err := parseRequired(6, "write bandwidth")
	if err != nil {
		return "", poolIOStat{}, err
	}
	readLatency, err := parseOptional(7, "read latency")
	if err != nil {
		return "", poolIOStat{}, err
	}
	writeLatency, err := parseOptional(8, "write latency")
	if err != nil {
		return "", poolIOStat{}, err
	}

	return fields[0], poolIOStat{
		ReadIOPS:              readIOPS,
		WriteIOPS:             writeIOPS,
		ReadBytesPerSecond:    readBytes,
		WriteBytesPerSecond:   writeBytes,
		ReadLatencyNanos:      readLatency,
		WriteLatencyNanos:     writeLatency,
		ReadLatencyAvailable:  len(fields) > 7 && fields[7] != "-",
		WriteLatencyAvailable: len(fields) > 8 && fields[8] != "-",
	}, nil
}

func (s *Service) setPoolIOStat(name string, stat poolIOStat) {
	s.poolIOMutex.Lock()
	defer s.poolIOMutex.Unlock()

	if s.poolIOStats == nil {
		s.poolIOStats = make(map[string]poolIOStat)
	}
	s.poolIOStats[name] = stat
}

func (s *Service) getPoolIOStat(name string, now time.Time) poolIOStat {
	s.poolIOMutex.RLock()
	stat, ok := s.poolIOStats[name]
	s.poolIOMutex.RUnlock()

	if !ok || stat.SampledAt.IsZero() || now.Sub(stat.SampledAt) > poolIOStaleAfter {
		return poolIOStat{}
	}
	return stat
}

func (s *Service) consumePoolIOStats(ctx context.Context, includeLatency bool) error {
	args := []string{"iostat", "-H", "-p"}
	if includeLatency {
		args = append(args, "-l")
	}
	args = append(args, "-y", strconv.Itoa(poolIOSampleIntervalSeconds))

	cmd := exec.CommandContext(ctx, "/sbin/zpool", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open zpool iostat stdout: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start zpool iostat: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		name, stat, err := parseZpoolIOStatLine(scanner.Text())
		if err != nil {
			logger.L.Debug().Err(err).Str("line", scanner.Text()).Msg("zfs_iostat_sample_ignored")
			continue
		}
		stat.SampledAt = time.Now().UTC()
		s.setPoolIOStat(name, stat)
	}

	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return fmt.Errorf("scan zpool iostat output: %w", scanErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("zpool iostat: %w: %s", waitErr, message)
		}
		return fmt.Errorf("zpool iostat: %w", waitErr)
	}
	return fmt.Errorf("zpool iostat exited unexpectedly")
}

func isZpoolIOStatLatencyUnsupported(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid option") ||
		strings.Contains(message, "illegal option") ||
		strings.Contains(message, "unrecognized option") ||
		strings.Contains(message, "unknown option")
}

func (s *Service) monitorPoolIO(ctx context.Context) {
	includeLatency := true

	for ctx.Err() == nil {
		err := s.consumePoolIOStats(ctx, includeLatency)
		if ctx.Err() != nil {
			return
		}

		if includeLatency && isZpoolIOStatLatencyUnsupported(err) {
			// Older OpenZFS releases may not support -l. Keep bandwidth and
			// operation telemetry available even when latency is unavailable.
			logger.L.Warn().Err(err).Msg("zfs_iostat_latency_unavailable_falling_back")
			includeLatency = false
			continue
		}

		logger.L.Warn().Err(err).Msg("zfs_iostat_monitor_restarting")
		timer := time.NewTimer(poolIOMonitorRestartDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}
