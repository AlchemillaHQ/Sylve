// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package info

import (
	"github.com/alchemillahq/sylve/internal/cmd"
	"github.com/alchemillahq/sylve/internal/config"
	infoServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/info"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) GetBasicInfo() (basicInfo infoServiceInterfaces.BasicInfo, err error) {
	hostname, err := utils.GetSystemHostname()
	if err != nil {
		return infoServiceInterfaces.BasicInfo{}, err
	}

	uptime, err := utils.GetUptime()
	if err != nil {
		return infoServiceInterfaces.BasicInfo{}, err
	}

	loadAvg, err := utils.GetLoadAvg()
	if err != nil {
		return infoServiceInterfaces.BasicInfo{}, err
	}

	return infoServiceInterfaces.BasicInfo{
		Hostname:      hostname,
		OS:            utils.GetOS(),
		Uptime:        uptime,
		LoadAverage:   loadAvg,
		BootMode:      utils.BootMode(),
		SylveVersion:  cmd.Version,
		DevFSDisabled: config.IsDevFSDisabled(),
	}, nil
}
