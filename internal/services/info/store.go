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
	interfaces, err := s.GetNetworkInterfacesInfo()
	if err != nil {
		logger.L.Err(err).Msg("failed to get network interfaces info")
		return
	}
	if len(interfaces) == 0 {
		return
	}

	ifaceModels := make([]infoModels.NetworkInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		ifaceModels = append(ifaceModels, infoModels.NetworkInterface{
			Name:            iface.Name,
			Flags:           iface.Flags,
			Network:         iface.Network,
			Address:         iface.Address,
			ReceivedPackets: iface.ReceivedPackets,
			ReceivedErrors:  iface.ReceivedErrors,
			DroppedPackets:  iface.DroppedPackets,
			ReceivedBytes:   iface.ReceivedBytes,
			SentPackets:     iface.SentPackets,
			SendErrors:      iface.SendErrors,
			SentBytes:       iface.SentBytes,
			Collisions:      iface.Collisions,
		})
	}

	if err := s.DB.Create(&ifaceModels).Error; err != nil {
		logger.L.Err(err).Msg("failed to store network interface stats")
		return
	}

	now := time.Now()

	var rows []*infoModels.NetworkInterface
	if err := s.DB.
		Select("id", "name", "network", "address", "created_at").
		Where("created_at >= ?", now.Add(-retention)).
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
		batch := allDeleteIDs[i:end]

		if err := s.DB.Delete(&infoModels.NetworkInterface{}, batch).Error; err != nil {
			logger.L.Err(err).Msg("failed pruning network interface stats (batch delete)")
		}
	}
}

func (s *Service) Cron() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	s.StoreStats()
	s.StoreNetworkInterfaceStats()

	for range ticker.C {
		s.StoreStats()
		s.StoreNetworkInterfaceStats()
	}
}
