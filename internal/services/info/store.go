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

	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"github.com/alchemillahq/sylve/internal/logger"
)

const retention = 70 * 24 * time.Hour

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
	bootstrapped := false

	interfaces, err := s.GetNetworkInterfacesInfo()
	if err != nil {
		logger.L.Err(err).Msg("failed to get network interfaces info")
		return
	}

	if len(interfaces) == 0 {
		return
	}

	var prevRows []*infoModels.NetworkInterface
	if err := s.DB.
		Select(
			"id", "name", "network", "address",
			"received_packets", "received_errors", "dropped_packets",
			"received_bytes", "sent_packets", "send_errors",
			"sent_bytes", "collisions",
			"created_at",
		).
		Order("created_at DESC").
		Find(&prevRows).Error; err != nil {
		logger.L.Err(err).Msg("failed loading previous network samples")
		return
	}

	last := make(map[string]*infoModels.NetworkInterface, 8)
	for _, r := range prevRows {
		key := r.Name + "|" + r.Network + "|" + r.Address
		if _, ok := last[key]; !ok {
			last[key] = r
		}
	}

	ifaceModels := make([]infoModels.NetworkInterface, 0, len(interfaces))

	for _, iface := range interfaces {
		key := iface.Name + "|" + iface.Network + "|" + iface.Address
		prev := last[key]

		if prev == nil {
			bootstrapped = true
			ifaceModels = append(ifaceModels, infoModels.NetworkInterface{
				Name:    iface.Name,
				Flags:   iface.Flags,
				Network: iface.Network,
				Address: iface.Address,
			})
			continue
		}

		delta := func(cur, old int64) int64 {
			if cur < old {
				return cur
			}
			return cur - old
		}

		ifaceModels = append(ifaceModels, infoModels.NetworkInterface{
			Name:    iface.Name,
			Flags:   iface.Flags,
			Network: iface.Network,
			Address: iface.Address,

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

	if len(ifaceModels) == 0 {
		return
	}

	if err := s.DB.Create(&ifaceModels).Error; err != nil {
		logger.L.Err(err).Msg("failed to store network interface delta stats")
		return
	}

	if bootstrapped {
		return
	}

	now := time.Now()

	var rows []*infoModels.NetworkInterface
	if err := s.DB.
		Select("id", "name", "network", "address", "created_at").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		logger.L.Err(err).Msg("failed loading network rows for prune")
		return
	}

	groups := make(map[string][]db.ReflectRow, 8)
	for _, r := range rows {
		key := r.Name + "|" + r.Network + "|" + r.Address
		groups[key] = append(groups[key], db.ReflectRow{Ptr: r})
	}

	delSet := make(map[uint]struct{})

	for _, adapters := range groups {
		_, deleteIDs := db.ApplyGFS(now, adapters)
		for _, id := range deleteIDs {
			delSet[id] = struct{}{}
		}
	}

	if err := s.DB.
		Where("created_at < ?", now.Add(-retention)).
		Select("id").
		Find(&rows).Error; err == nil {
		for _, r := range rows {
			delSet[r.ID] = struct{}{}
		}
	}

	if len(delSet) == 0 {
		return
	}

	allDeleteIDs := make([]uint, 0, len(delSet))
	for id := range delSet {
		allDeleteIDs = append(allDeleteIDs, id)
	}

	const batchSize = 500
	for i := 0; i < len(allDeleteIDs); i += batchSize {
		end := i + batchSize
		if end > len(allDeleteIDs) {
			end = len(allDeleteIDs)
		}

		if err := s.DB.
			Delete(&infoModels.NetworkInterface{}, allDeleteIDs[i:end]).
			Error; err != nil {
			logger.L.Err(err).Msg("failed pruning network interface stats")
		}
	}
}

func (s *Service) Cron() {
	statsTicker := time.NewTicker(10 * time.Second)
	netTicker := time.NewTicker(2 * time.Minute)

	defer statsTicker.Stop()
	defer netTicker.Stop()

	for {
		select {
		case <-statsTicker.C:
			s.StoreStats()

		case <-netTicker.C:
			s.StoreNetworkInterfaceStats()
		}
	}
}
