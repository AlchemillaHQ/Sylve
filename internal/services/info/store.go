// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"reflect"
	"sync"
	"time"
	"context"

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
)

const retention = 70 * 24 * time.Hour
const netRetention = 2 * time.Hour

func (s *Service) StoreStats() {
	type task struct {
		get func() (float64, error)
		ptr func(float64) interface{}
	}

	jobs := []task{
		{get: func() (float64, error) { c, err := s.GetCPUInfo(true); return c.Usage, err },
			ptr: func(v float64) interface{} { return &infoModels.CPU{Usage: v} }},
		{get: func() (float64, error) { r, err := s.GetRAMInfo(); return r.UsedPercent, err },
			ptr: func(v float64) interface{} { return &infoModels.RAM{Usage: v} }},
		{get: func() (float64, error) { sw, err := s.GetSwapInfo(); return sw.UsedPercent, err },
			ptr: func(v float64) interface{} { return &infoModels.Swap{Usage: v} }},
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(j task) {
			defer wg.Done()
			v, err := j.get()
			if err != nil {
				logger.L.Err(err).Msg("Failed to get stats")
				return
			}
			if err := s.DB.Create(j.ptr(v)).Error; err != nil {
				logger.L.Err(err).Msg("Failed to store stats")
			}
		}(job)
	}

	wg.Wait()

	prune := func(modelPtr interface{}, slicePtr interface{}) {
		if err := s.DB.Order("created_at desc").Find(slicePtr).Error; err != nil {
			logger.L.Err(err).Msg("failed loading rows for prune")
			return
		}

		sv := reflect.ValueOf(slicePtr).Elem()
		adapters := make([]db.ReflectRow, 0, sv.Len())
		for i := 0; i < sv.Len(); i++ {
			elem := sv.Index(i).Interface()
			adapters = append(adapters, db.ReflectRow{Ptr: elem})
		}

		_, deleteIDs := db.ApplyGFS(time.Now(), adapters)
		if len(deleteIDs) == 0 {
			return
		}

		if err := s.DB.Delete(modelPtr, deleteIDs).Error; err != nil {
			logger.L.Err(err).Msg("failed pruning stats")
		}
	}

	prune(&infoModels.CPU{}, &[]*infoModels.CPU{})
	prune(&infoModels.RAM{}, &[]*infoModels.RAM{})
	prune(&infoModels.Swap{}, &[]*infoModels.Swap{})
}

func (s *Service) StoreNetworkInterfaceStats() {
	interfaces, err := s.GetNetworkInterfacesInfo()
	if err != nil || len(interfaces) == 0 {
		if err != nil {
			logger.L.Err(err).Msg("failed to get network interfaces info")
		}
		return
	}

	var prevRows []*infoModels.NetworkInterface
	if err := s.DB.Raw(`
		SELECT ni.id, ni.name, ni.network,
		       ni.received_packets, ni.received_errors,
		       ni.dropped_packets, ni.received_bytes,
		       ni.sent_packets, ni.send_errors,
		       ni.sent_bytes, ni.collisions
		FROM network_interfaces ni
		JOIN (
			SELECT name, network, MAX(created_at) AS max_created_at
			FROM network_interfaces
			GROUP BY name, network
		) latest
		ON ni.name = latest.name
		AND ni.network = latest.network
		AND ni.created_at = latest.max_created_at
	`).Scan(&prevRows).Error; err != nil {
		logger.L.Err(err).Msg("failed loading previous network interface stats")
		return
	}

	last := make(map[string]*infoModels.NetworkInterface, len(prevRows))
	for _, r := range prevRows {
		last[r.Name+"|"+r.Network] = r
	}

	now := time.Now()
	rows := make([]infoModels.NetworkInterface, 0, len(interfaces))

	delta := func(cur, old int64) int64 {
		if cur < old {
			return 0
		}
		return cur - old
	}

	for _, iface := range interfaces {
		key := iface.Name + "|" + iface.Network
		prev := last[key]

		if prev == nil {
			rows = append(rows, infoModels.NetworkInterface{
				Name:            iface.Name,
				Flags:           iface.Flags,
				Network:         iface.Network,
				Address:         iface.Address,
				IsDelta:         false,
				ReceivedPackets: iface.ReceivedPackets,
				ReceivedBytes:   iface.ReceivedBytes,
				SentPackets:     iface.SentPackets,
				SentBytes:       iface.SentBytes,
			})
			continue
		}

		rows = append(rows, infoModels.NetworkInterface{
			Name:    iface.Name,
			Flags:   iface.Flags,
			Network: iface.Network,
			Address: iface.Address,
			IsDelta: true,

			ReceivedPackets: delta(iface.ReceivedPackets, prev.ReceivedPackets),
			ReceivedErrors:  delta(iface.ReceivedErrors, prev.ReceivedErrors),
			DroppedPackets:  delta(iface.DroppedPackets, prev.DroppedPackets),
			ReceivedBytes:   delta(iface.ReceivedBytes, prev.ReceivedBytes),

			SentPackets: delta(iface.SentPackets, prev.SentPackets),
			SendErrors:  delta(iface.SendErrors, prev.SendErrors),
			SentBytes:   delta(iface.SentBytes, prev.SentBytes),

			Collisions: delta(iface.Collisions, prev.Collisions),
		})
	}

	if len(rows) == 0 {
		return
	}

	if err := s.DB.Create(&rows).Error; err != nil {
		logger.L.Err(err).Msg("failed storing network interface stats")
		return
	}

	cutoff := now.Add(-netRetention)
	if err := s.DB.
		Where("is_delta = true AND created_at < ?", cutoff).
		Delete(&infoModels.NetworkInterface{}).
		Error; err != nil {
		logger.L.Err(err).Msg("failed pruning old network interface deltas")
	}
}

func (s *Service) Cron(ctx context.Context) {
	s.StoreStats()
	s.StoreNetworkInterfaceStats()

	statsTicker := time.NewTicker(10 * time.Second)
	netTicker := time.NewTicker(2 * time.Minute)
	defer statsTicker.Stop()
	defer netTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-statsTicker.C:
			s.StoreStats()

		case <-netTicker.C:
			s.StoreNetworkInterfaceStats()
		}
	}
}
