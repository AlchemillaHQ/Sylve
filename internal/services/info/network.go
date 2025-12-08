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

	output, err := utils.RunCommand("netstat", "-ibdn", "--libxo", "json")
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
		CreatedAt     time.Time
		ReceivedBytes int64
		SentBytes     int64
	}

	var rows []row
	if err := s.DB.
		Model(&infoModels.NetworkInterface{}).
		Select("name, created_at, received_bytes, sent_bytes").
		Order("name, created_at ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	const sampleInterval = 10 * time.Second

	// Any gap bigger than this is treated as "restart" and we skip the delta.
	const maxGap = 4 * sampleInterval

	type prevSample struct {
		CreatedAt     time.Time
		ReceivedBytes int64
		SentBytes     int64
	}

	prevByName := make(map[string]prevSample, 8)

	// Aggregate per timestamp across interfaces.
	buckets := make(map[time.Time]*infoServiceInterfaces.HistoricalNetworkInterface)

	for _, cur := range rows {
		prev, ok := prevByName[cur.Name]
		if ok {
			dt := cur.CreatedAt.Sub(prev.CreatedAt)

			if dt <= 0 {
				// Out-of-order or duplicate timestamp; just advance baseline.
				prevByName[cur.Name] = prevSample{
					CreatedAt:     cur.CreatedAt,
					ReceivedBytes: cur.ReceivedBytes,
					SentBytes:     cur.SentBytes,
				}
				continue
			}

			if dt > maxGap {
				// Long gap: treat this as a new baseline; don't generate a delta.
				prevByName[cur.Name] = prevSample{
					CreatedAt:     cur.CreatedAt,
					ReceivedBytes: cur.ReceivedBytes,
					SentBytes:     cur.SentBytes,
				}
				continue
			}

			recvDelta := cur.ReceivedBytes - prev.ReceivedBytes
			sentDelta := cur.SentBytes - prev.SentBytes

			// Handle counter resets / wrap.
			if recvDelta < 0 {
				recvDelta = 0
			}
			if sentDelta < 0 {
				sentDelta = 0
			}

			if recvDelta > 0 || sentDelta > 0 {
				// Normalize to seconds since we want rates; here we keep raw bytes for the interval.
				ts := cur.CreatedAt.Truncate(time.Second)

				b, ok := buckets[ts]
				if !ok {
					b = &infoServiceInterfaces.HistoricalNetworkInterface{
						CreatedAt: ts,
					}
					buckets[ts] = b
				}
				b.ReceivedBytes += recvDelta
				b.SentBytes += sentDelta
			}
		}

		// Update baseline for this interface.
		prevByName[cur.Name] = prevSample{
			CreatedAt:     cur.CreatedAt,
			ReceivedBytes: cur.ReceivedBytes,
			SentBytes:     cur.SentBytes,
		}
	}

	if len(buckets) == 0 {
		return nil, nil
	}

	// Convert map -> sorted slice.
	result := make([]infoServiceInterfaces.HistoricalNetworkInterface, 0, len(buckets))
	for _, v := range buckets {
		result = append(result, *v)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return result, nil
}
