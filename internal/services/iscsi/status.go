// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package iscsi

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/internal/logger"
)

type ctladmConnection struct {
	Initiator string `xml:"initiator"`
	Target    string `xml:"target"`
}

type ctladmIsList struct {
	Connections []ctladmConnection `xml:"connection"`
}

func (s *Service) GetStatus() (map[string]string, error) {
	out, err := s.runInitiatorCommand("-L")
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
	ctx, cancel := s.targetContext()
	defer cancel()
	out, err := s.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "islist", "-x")
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

func (s *Service) ensureTargetHasNoSessions(targetName string) error {
	sessions, err := s.GetTargetSessions()
	if err != nil {
		return runtimeFailed("failed_to_check_target_sessions", err)
	}
	if sessions[targetName] > 0 {
		return resourceConflict("target_has_active_connections", nil)
	}
	return nil
}

type ctlPort struct {
	ID             int    `xml:"id,attr"`
	Frontend       string `xml:"frontend_type"`
	Online         string `xml:"online"`
	Target         string `xml:"cfiscsi_target"`
	Group          string `xml:"ctld_portal_group_name"`
	TransportGroup string `xml:"ctld_transport_group_name"`
	Tag            int    `xml:"cfiscsi_portal_group_tag"`
	LUNMap         string `xml:"lun_map"`
	LUNs           []struct {
		Number int `xml:"id,attr"`
		ID     int `xml:",chardata"`
	} `xml:"lun"`
}

type ctlPorts struct {
	XMLName xml.Name  `xml:"ctlportlist"`
	Ports   []ctlPort `xml:"targ_port"`
}

type ctlLUN struct {
	ID         int    `xml:"id,attr"`
	Name       string `xml:"ctld_name"`
	Backend    string `xml:"backend_type"`
	Blocks     uint64 `xml:"size"`
	Blocksize  uint64 `xml:"blocksize"`
	File       string `xml:"file"`
	Device     string `xml:"dev"`
	Serial     string `xml:"serial_number"`
	DeviceType int    `xml:"lun_type"`
}

type ctlLUNs struct {
	XMLName xml.Name `xml:"ctllunlist"`
	LUNs    []ctlLUN `xml:"lun"`
}

func (s *Service) readCTLPorts(ctx context.Context) (*ctlPorts, error) {
	out, err := s.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "portlist", "-x")
	if err != nil {
		return nil, errors.New("failed_to_check_target_ports")
	}
	var ports ctlPorts
	if xml.Unmarshal([]byte(out), &ports) != nil {
		return nil, errors.New("invalid_target_port_inventory")
	}
	return &ports, nil
}

func (s *Service) readCTLLUNs(ctx context.Context) (*ctlLUNs, error) {
	out, err := s.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "devlist", "-x")
	if err != nil {
		return nil, errors.New("failed_to_check_target_luns")
	}
	var luns ctlLUNs
	if xml.Unmarshal([]byte(out), &luns) != nil {
		return nil, errors.New("invalid_target_lun_inventory")
	}
	return &luns, nil
}

func (s *Service) targetListeners(ctx context.Context, pid int) ([]portalEndpoint, error) {
	out, err := s.runTargetCommand(ctx, "", "/usr/bin/sockstat", "-w", "-l", "-P", "tcp")
	if err != nil {
		return nil, errors.New("failed_to_check_target_listeners")
	}
	var listeners []portalEndpoint
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || !strings.HasPrefix(fields[4], "tcp") {
			continue
		}
		rowPID, err := strconv.Atoi(fields[2])
		if err != nil || (pid != 0 && rowPID != pid) || (pid == 0 && fields[1] != "ctld") {
			continue
		}
		value := fields[5]
		i := strings.LastIndex(value, ":")
		if i < 0 {
			return nil, errors.New("invalid_target_listener_inventory")
		}
		host := value[:i]
		port, err := strconv.Atoi(value[i+1:])
		if err != nil {
			return nil, errors.New("invalid_target_listener_inventory")
		}
		if host == "*" {
			if strings.Contains(fields[4], "6") {
				host = "::"
			} else {
				host = "0.0.0.0"
			}
		}
		endpoint, err := s.endpoint(host, port)
		if err != nil {
			return nil, errors.New("invalid_target_listener_inventory")
		}
		listeners = append(listeners, endpoint)
	}
	return listeners, nil
}

func (s *Service) checkTargetRuntime(ctx context.Context, state *targetConfiguration) (string, error) {
	pid, err := s.targetPID(ctx)
	if err != nil {
		return "", err
	}
	birth, err := s.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil || strings.TrimSpace(birth) == "" {
		return "", errors.New("target_process_changed")
	}
	listeners, err := s.targetListeners(ctx, pid)
	if err != nil {
		return "", err
	}
	expected := state.activeEndpoints()
	if len(listeners) != len(expected) {
		return "", errors.New("target_listener_mismatch")
	}
	matched := make(map[string]bool)
	for _, listener := range listeners {
		found := false
		for _, endpoint := range expected {
			if listener.sameSocket(endpoint) && !matched[endpoint.key()] {
				found = true
				matched[endpoint.key()] = true
				break
			}
		}
		if !found {
			return "", errors.New("target_listener_mismatch")
		}
	}
	ports, err := s.readCTLPorts(ctx)
	if err != nil {
		return "", err
	}
	luns, err := s.readCTLLUNs(ctx)
	if err != nil {
		return "", err
	}
	if err := s.matchCTLState(state, ports, luns); err != nil {
		return "", err
	}
	endPID, err := s.targetPID(ctx)
	if err != nil || endPID != pid {
		return "", errors.New("target_process_changed")
	}
	endBirth, err := s.runTargetCommand(ctx, "", "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil || strings.TrimSpace(endBirth) != strings.TrimSpace(birth) {
		return "", errors.New("target_process_changed")
	}
	return strconv.Itoa(pid) + "|" + strings.TrimSpace(birth), nil
}

func (s *Service) matchCTLState(state *targetConfiguration, ports *ctlPorts, luns *ctlLUNs) error {
	wantedLUNs := make(map[string]targetLUN)
	wantedPorts := make(map[string]targetDefinition)
	for _, target := range state.targets {
		for _, lun := range target.luns {
			wantedLUNs[lun.name] = lun
		}
		for _, group := range target.groups {
			if len(state.groups[group].listeners) > 0 {
				wantedPorts[target.name+"\x00"+group] = target
			}
		}
	}
	actualLUNs := make(map[string]int)
	seenLUNIDs := make(map[int]bool)
	for _, lun := range luns.LUNs {
		if lun.ID < 0 || seenLUNIDs[lun.ID] {
			return errors.New("invalid_target_lun_inventory")
		}
		seenLUNIDs[lun.ID] = true
		if lun.Name == "" {
			continue
		}
		wanted, exists := wantedLUNs[lun.Name]
		if !exists {
			return errors.New("unexpected_owned_target_lun")
		}
		if _, duplicate := actualLUNs[lun.Name]; duplicate {
			return errors.New("duplicate_owned_target_lun")
		}
		if lun.Backend != "block" || lun.Blocksize != wanted.blocksize || lun.Blocksize == 0 || lun.Blocks != wanted.size/lun.Blocksize || wanted.size%lun.Blocksize != 0 || lun.DeviceType != 0 {
			return errors.New("target_lun_properties_mismatch")
		}
		path := lun.File
		if path == "" {
			path = lun.Device
		}
		actualInfo, err := s.statBacking(path)
		if err != nil {
			return errors.New("target_lun_backing_mismatch")
		}
		wantedInfo, err := s.statBacking(wanted.path)
		if err != nil || wanted.backing == nil || !os.SameFile(actualInfo, wanted.backing) || !os.SameFile(wantedInfo, wanted.backing) {
			return errors.New("target_lun_backing_mismatch")
		}
		actualLUNs[lun.Name] = lun.ID
	}
	if len(actualLUNs) != len(wantedLUNs) {
		return errors.New("target_lun_missing")
	}
	seenPorts, groupTags, tagGroups := make(map[string]bool), make(map[string]int), make(map[int]string)
	seenPortIDs := make(map[int]bool)
	for _, port := range ports.Ports {
		if port.ID < 0 || seenPortIDs[port.ID] {
			return errors.New("invalid_target_port_inventory")
		}
		seenPortIDs[port.ID] = true
		if port.Group == "" && port.TransportGroup == "" {
			continue
		}
		key := port.Target + "\x00" + port.Group
		target, exists := wantedPorts[key]
		if !exists || seenPorts[key] || port.Frontend != "iscsi" || port.TransportGroup != "" {
			return errors.New("unexpected_owned_target_port")
		}
		seenPorts[key] = true
		if port.Online != "YES" || port.Tag <= 0 || port.LUNMap != "on" {
			return errors.New("target_port_not_ready")
		}
		if tag, exists := groupTags[port.Group]; exists && tag != port.Tag {
			return errors.New("target_port_group_tag_mismatch")
		}
		if group, exists := tagGroups[port.Tag]; exists && group != port.Group {
			return errors.New("target_port_group_tag_mismatch")
		}
		groupTags[port.Group], tagGroups[port.Tag] = port.Tag, port.Group
		if len(port.LUNs) != len(target.luns) {
			return errors.New("target_lun_mapping_mismatch")
		}
		seenMappings := make(map[int]bool)
		for _, mapping := range port.LUNs {
			if seenMappings[mapping.Number] {
				return errors.New("target_lun_mapping_mismatch")
			}
			seenMappings[mapping.Number] = true
			found := false
			for _, lun := range target.luns {
				if mapping.Number == lun.number && mapping.ID == actualLUNs[lun.name] {
					found = true
					break
				}
			}
			if !found {
				return errors.New("target_lun_mapping_mismatch")
			}
		}
	}
	if len(seenPorts) != len(wantedPorts) {
		return errors.New("target_port_missing")
	}
	return nil
}
