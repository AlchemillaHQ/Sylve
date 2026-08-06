// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"encoding/xml"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type ctladmConnection struct {
	Initiator string `xml:"initiator"`
	Target    string `xml:"target"`
}

type ctladmIsList struct {
	Connections []ctladmConnection `xml:"connection"`
}

func (s *Service) GetStatus() (map[string]string, error) {
	out, err := utils.RunCommandAllowExitCode("/usr/bin/iscsictl", []int{0}, "-L")
	if err != nil {
		logger.L.Error().Err(err).Msg("failed to get iSCSI initiator status")
		return nil, applyFailed("failed_to_get_status", err)
	}

	result := make(map[string]string)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)

		if len(fields) < 3 {
			continue
		}

		targetName := fields[0]
		state := fields[2]
		state = strings.TrimRight(state, ":")
		result[targetName] = state
	}

	return result, nil
}

func (s *Service) GetTargetSessions() (map[string]int, error) {
	out, err := utils.RunCommandAllowExitCode("/usr/sbin/ctladm", []int{0}, "islist", "-x")
	if err != nil {
		logger.L.Error().Err(err).Msg("failed to get iSCSI target sessions")
		return nil, applyFailed("failed_to_get_target_sessions", err)
	}

	var list ctladmIsList
	if err := xml.Unmarshal([]byte(out), &list); err != nil {
		logger.L.Error().Err(err).Msg("failed to parse iSCSI target sessions")
		return nil, applyFailed("failed_to_parse_target_sessions", err)
	}

	result := make(map[string]int)
	for _, c := range list.Connections {
		result[c.Target]++
	}
	return result, nil
}
