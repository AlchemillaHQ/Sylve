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
	var rows []infoModels.NetworkInterface
	if err := s.networkDB().
		Where("is_delta = ?", true).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]infoServiceInterfaces.HistoricalNetworkInterface, 0, len(rows))
	for _, row := range rows {
		result = append(result, infoServiceInterfaces.HistoricalNetworkInterface{
			SentBytes:     row.SentBytes,
			ReceivedBytes: row.ReceivedBytes,
			CreatedAt:     row.CreatedAt,
		})
	}

	return result, nil
}
