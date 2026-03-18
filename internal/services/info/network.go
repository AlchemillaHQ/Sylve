// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"encoding/json"
	"sort"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetNetworkInterfacesInfo() ([]infoServiceInterfaces.NetworkInterface, error) {
	var tOutput struct {
		Statistics struct {
			Interfaces []infoServiceInterfaces.NetworkInterface `json:"interface"`
		}
	}

	output, err := utils.RunCommand("/usr/bin/netstat", "-ibdn", "--libxo", "json")
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal([]byte(output), &tOutput)
	if err != nil {
		return nil, err
	}

	if len(tOutput.Statistics.Interfaces) > 0 {
		return tOutput.Statistics.Interfaces, nil
	}

	return nil, nil
}

func (s *Service) GetNetworkInterfacesHistorical() ([]infoServiceInterfaces.HistoricalNetworkInterface, error) {
	type row struct {
		Name          string
		Network       string
		CreatedAt     time.Time
		ReceivedBytes int64
		SentBytes     int64
	}

	var rows []row
	if err := s.DB.
		Model(&infoModels.NetworkInterface{}).
		Select("name, network, created_at, received_bytes, sent_bytes").
		Where("is_delta = false").
		Order("created_at ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	buckets := make(map[int64]*infoServiceInterfaces.HistoricalNetworkInterface)
	last := make(map[string]struct {
		ReceivedBytes int64
		SentBytes     int64
	})
	delta := func(cur, prev int64) int64 {
		if cur < prev {
			return 0
		}
		return cur - prev
	}

	for _, cur := range rows {
		key := cur.Name + "|" + cur.Network
		prev, ok := last[key]
		last[key] = struct {
			ReceivedBytes int64
			SentBytes     int64
		}{
			ReceivedBytes: cur.ReceivedBytes,
			SentBytes:     cur.SentBytes,
		}

		if !ok {
			continue
		}

		receivedDelta := delta(cur.ReceivedBytes, prev.ReceivedBytes)
		sentDelta := delta(cur.SentBytes, prev.SentBytes)
		sec := cur.CreatedAt.Truncate(time.Second).Unix()

		b, ok := buckets[sec]
		if !ok {
			b = &infoServiceInterfaces.HistoricalNetworkInterface{
				CreatedAt: time.Unix(sec, 0).In(cur.CreatedAt.Location()),
			}
			buckets[sec] = b
		}

		b.ReceivedBytes += receivedDelta
		b.SentBytes += sentDelta
	}

	result := make([]infoServiceInterfaces.HistoricalNetworkInterface, 0, len(buckets))
	for _, v := range buckets {
		result = append(result, *v)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return result, nil
}
