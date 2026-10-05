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
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type ctladmConnection struct {
	Initiator string `xml:"initiator"`
	Target    string `xml:"target"`
}

type ctladmIsList struct {
	XMLName     xml.Name           `xml:"ctlislist"`
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

type targetBackingIdentity struct {
	Device   uint64      `json:"device"`
	Inode    uint64      `json:"inode"`
	Rdev     uint64      `json:"rdev"`
	Mode     os.FileMode `json:"mode"`
	Modified int64       `json:"modified"`
}

type targetRecoveryLUN struct {
	LUN     ctlLUN                `json:"lun"`
	Backing targetBackingIdentity `json:"backing"`
}

type targetPortRecovery struct {
	Version  int                 `json:"version"`
	Config   string              `json:"config"`
	Boot     string              `json:"boot"`
	Ports    []ctlPort           `json:"ports"`
	LUNs     []targetRecoveryLUN `json:"luns"`
	Removing []int               `json:"removing,omitempty"`
}

type targetStartBaseline struct {
	boot   string
	config [sha256.Size]byte
	ports  *ctlPorts
	luns   *ctlLUNs
}

func (s *Service) targetRecoveryPath() string { return s.targetPath() + ".recovery.json" }

func privateTargetFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return errors.New("failed_to_check_target_recovery_file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || int(stat.Uid) != os.Geteuid() {
		return errors.New("target_recovery_file_not_private")
	}
	return nil
}

func (s *Service) lockTargetRecovery() (*os.File, error) {
	file, err := os.OpenFile(s.targetPath()+".recovery.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errors.New("failed_to_lock_target_recovery")
	}
	if err := privateTargetFile(file); err != nil {
		file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("target_recovery_busy")
	}
	return file, nil
}

func (s *Service) targetBoot(ctx context.Context) (string, error) {
	out, err := s.runTargetCommand(ctx, "", "/sbin/sysctl", "-n", "kern.boottime")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", errors.New("failed_to_check_target_boot_identity")
	}
	return strings.TrimSpace(out), nil
}

func (s *Service) checkTargetRecoveryQuiet(ctx context.Context) error {
	out, err := s.runTargetCommand(ctx, "", "/bin/pgrep", "-x", "ctld")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || strings.TrimSpace(out) != "" || ctx.Err() != nil {
		return errors.New("target_recovery_requires_stopped_daemon")
	}
	listeners, err := s.targetListeners(ctx, 0)
	if err != nil || len(listeners) != 0 {
		return errors.New("target_recovery_requires_no_listeners")
	}
	out, err = s.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "islist", "-x")
	var sessions ctladmIsList
	if err != nil || xml.Unmarshal([]byte(out), &sessions) != nil {
		return errors.New("failed_to_check_target_recovery_sessions")
	}
	if len(sessions.Connections) != 0 {
		return errors.New("target_recovery_requires_no_sessions")
	}
	return nil
}

func recoveryPort(port ctlPort) bool {
	return port.Frontend == "iscsi" || port.Group != "" || port.TransportGroup != ""
}

func sortCTLInventory(ports *ctlPorts, luns *ctlLUNs) {
	sort.Slice(ports.Ports, func(i, j int) bool { return ports.Ports[i].ID < ports.Ports[j].ID })
	for i := range ports.Ports {
		mappings := ports.Ports[i].LUNs
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].Number < mappings[j].Number })
	}
	sort.Slice(luns.LUNs, func(i, j int) bool { return luns.LUNs[i].ID < luns.LUNs[j].ID })
}

func (s *Service) targetStartInventory(ctx context.Context) (*targetStartBaseline, error) {
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return nil, err
	}
	boot, err := s.targetBoot(ctx)
	if err != nil {
		return nil, err
	}
	ports, err := s.readCTLPorts(ctx)
	if err != nil {
		return nil, err
	}
	for _, port := range ports.Ports {
		if recoveryPort(port) {
			return nil, errors.New("target_start_has_unrecorded_ports")
		}
	}
	luns, err := s.readCTLLUNs(ctx)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.targetPath())
	if err != nil {
		return nil, errors.New("failed_to_read_target_config")
	}
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return nil, err
	}
	sortCTLInventory(ports, luns)
	return &targetStartBaseline{boot: boot, config: sha256.Sum256(data), ports: ports, luns: luns}, nil
}

func (s *Service) targetBackingIdentity(lun ctlLUN) (targetBackingIdentity, error) {
	path := lun.File
	if path == "" {
		path = lun.Device
	}
	info, err := s.statBacking(path)
	if err != nil {
		return targetBackingIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return targetBackingIdentity{}, errors.New("invalid_target_backing_identity")
	}
	return targetBackingIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Rdev: uint64(stat.Rdev), Mode: info.Mode(), Modified: info.ModTime().UnixNano()}, nil
}

func (s *Service) rememberFailedTargetStart(ctx context.Context, state *targetConfiguration, before *targetStartBaseline) error {
	if before == nil {
		return errors.New("target_start_ownership_not_recorded")
	}
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return err
	}
	boot, err := s.targetBoot(ctx)
	if err != nil || boot != before.boot {
		return errors.New("target_recovery_boot_changed")
	}
	data, err := os.ReadFile(s.targetPath())
	if err != nil || sha256.Sum256(data) != before.config {
		return errors.New("target_start_config_changed")
	}
	ports, err := s.readCTLPorts(ctx)
	if err != nil {
		return err
	}
	luns, err := s.readCTLLUNs(ctx)
	if err != nil {
		return err
	}
	if err := s.matchCTLState(state, ports, luns); err != nil {
		return err
	}
	sortCTLInventory(ports, luns)
	record := targetPortRecovery{Version: 1, Config: filepath.Clean(s.targetPath()), Boot: boot}
	for _, port := range ports.Ports {
		if !recoveryPort(port) {
			continue
		}
		if port.Group == "" {
			return errors.New("target_start_port_not_owned")
		}
		for _, old := range before.ports.Ports {
			if old.ID == port.ID || old.Target == port.Target && old.Tag == port.Tag {
				return errors.New("target_start_port_existed_before_attempt")
			}
		}
		record.Ports = append(record.Ports, port)
	}
	if len(record.Ports) == 0 {
		return nil
	}
	for _, lun := range luns.LUNs {
		if lun.Name == "" {
			continue
		}
		if strings.Trim(lun.Serial, " \t\r\n\x00") == "" {
			return errors.New("target_start_lun_has_no_serial")
		}
		for _, old := range before.luns.LUNs {
			if old.ID == lun.ID && old != lun {
				return errors.New("target_start_lun_identity_changed")
			}
		}
		identity, err := s.targetBackingIdentity(lun)
		if err != nil {
			return err
		}
		record.LUNs = append(record.LUNs, targetRecoveryLUN{LUN: lun, Backing: identity})
	}
	endPorts, err := s.readCTLPorts(ctx)
	if err != nil {
		return err
	}
	endLUNs, err := s.readCTLLUNs(ctx)
	if err != nil {
		return err
	}
	sortCTLInventory(endPorts, endLUNs)
	if !reflect.DeepEqual(ports.Ports, endPorts.Ports) || !reflect.DeepEqual(luns.LUNs, endLUNs.LUNs) {
		return errors.New("target_start_inventory_changed")
	}
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return err
	}
	return s.saveTargetRecovery(&record)
}

func (s *Service) saveTargetRecovery(record *targetPortRecovery) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > 1<<20 {
		return errors.New("failed_to_encode_target_recovery")
	}
	if err := utils.AtomicWriteFile(s.targetRecoveryPath(), data, 0600); err != nil {
		return errors.New("failed_to_save_target_recovery")
	}
	return nil
}

func (s *Service) readTargetRecovery() (*targetPortRecovery, error) {
	file, err := os.OpenFile(s.targetRecoveryPath(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("failed_to_read_target_recovery")
	}
	defer file.Close()
	if err := privateTargetFile(file); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	var record targetPortRecovery
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Config != filepath.Clean(s.targetPath()) || record.Boot == "" || len(record.Ports) == 0 {
		return nil, errors.New("invalid_target_recovery_record")
	}
	return &record, nil
}

func (s *Service) checkTargetRecoveryInventory(ctx context.Context, record *targetPortRecovery) ([]ctlPort, error) {
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return nil, err
	}
	boot, err := s.targetBoot(ctx)
	if err != nil || boot != record.Boot {
		return nil, errors.New("target_recovery_boot_changed")
	}
	ports, err := s.readCTLPorts(ctx)
	if err != nil {
		return nil, err
	}
	luns, err := s.readCTLLUNs(ctx)
	if err != nil {
		return nil, err
	}
	sortCTLInventory(ports, luns)
	wantedPorts, selectors := make(map[int]ctlPort), make(map[string]bool)
	wantedLUNs := make(map[int]targetRecoveryLUN)
	invalid := errors.New("invalid_target_recovery_record")
	for _, saved := range record.LUNs {
		lun := saved.LUN
		path := lun.File
		if path == "" {
			path = lun.Device
		}
		if _, exists := wantedLUNs[lun.ID]; exists || lun.ID < 0 || lun.Name == "" || lun.Backend != "block" || lun.DeviceType != 0 || lun.Blocksize == 0 || lun.Blocks == 0 || strings.Trim(lun.Serial, " \t\r\n\x00") == "" || !strings.HasPrefix(path, "/dev/zvol/") || validateZVol(strings.TrimPrefix(path, "/dev/zvol/")) != nil {
			return nil, invalid
		}
		wantedLUNs[lun.ID] = saved
	}
	for _, port := range record.Ports {
		selector := port.Target + "\x00" + strconv.Itoa(port.Tag)
		if _, exists := wantedPorts[port.ID]; exists || port.ID < 0 || port.Target == "" || validateBareConfigToken(port.Target, "target_name", maxISCSINameLength) != nil || port.Frontend != "iscsi" || !strings.HasPrefix(port.Group, "pg-") || port.TransportGroup != "" || port.Tag < 1 || port.Tag > 65535 || port.LUNMap != "on" || port.Online != "YES" || selectors[selector] {
			return nil, invalid
		}
		seen := make(map[int]bool)
		for _, mapping := range port.LUNs {
			lun, exists := wantedLUNs[mapping.ID]
			if !exists || mapping.Number < 0 || mapping.Number > maxTargetLUNNumber || seen[mapping.Number] || lun.LUN.Name != port.Target+",lun,"+strconv.Itoa(mapping.Number) {
				return nil, invalid
			}
			seen[mapping.Number] = true
		}
		wantedPorts[port.ID], selectors[selector] = port, true
	}
	removing := make(map[int]bool)
	for _, id := range record.Removing {
		if _, exists := wantedPorts[id]; !exists || removing[id] {
			return nil, invalid
		}
		removing[id] = true
	}
	actualLUNs, lunIDs := make(map[int]bool), make(map[int]bool)
	for _, lun := range luns.LUNs {
		if lun.ID < 0 || lunIDs[lun.ID] {
			return nil, errors.New("invalid_target_lun_inventory")
		}
		lunIDs[lun.ID] = true
		if lun.Name == "" {
			continue
		}
		saved, exists := wantedLUNs[lun.ID]
		identity, err := s.targetBackingIdentity(lun)
		if !exists || lun != saved.LUN || err != nil || identity != saved.Backing {
			return nil, errors.New("target_recovery_lun_identity_changed")
		}
		actualLUNs[lun.ID] = true
	}
	var remaining []ctlPort
	portIDs := make(map[int]bool)
	for _, port := range ports.Ports {
		if port.ID < 0 || portIDs[port.ID] {
			return nil, errors.New("invalid_target_port_inventory")
		}
		portIDs[port.ID] = true
		saved, exists := wantedPorts[port.ID]
		if !recoveryPort(port) {
			if exists {
				return nil, errors.New("target_recovery_port_identity_changed")
			}
			continue
		}
		identity := port
		if removing[port.ID] && port.Online == "NO" {
			identity.Online = saved.Online
		}
		if !exists || !reflect.DeepEqual(identity, saved) {
			return nil, errors.New("target_recovery_port_identity_changed")
		}
		for _, mapping := range port.LUNs {
			if !actualLUNs[mapping.ID] {
				return nil, errors.New("target_recovery_lun_missing")
			}
		}
		remaining = append(remaining, port)
	}
	if err := s.checkTargetRecoveryQuiet(ctx); err != nil {
		return nil, err
	}
	return remaining, nil
}

func (s *Service) recoverTargetPorts(ctx context.Context) error {
	record, err := s.readTargetRecovery()
	if err != nil || record == nil {
		return err
	}
	remaining, err := s.checkTargetRecoveryInventory(ctx, record)
	if err != nil {
		return err
	}
	_, interval := s.targetTimings()
	for _, port := range remaining {
		current, err := s.checkTargetRecoveryInventory(ctx, record)
		if err != nil {
			return err
		}
		present := func(candidate ctlPort) bool { return candidate.ID == port.ID }
		if !slices.ContainsFunc(current, present) {
			continue
		}
		if !slices.Contains(record.Removing, port.ID) {
			record.Removing = append(record.Removing, port.ID)
			if err := s.saveTargetRecovery(record); err != nil {
				return err
			}
		}
		current, err = s.checkTargetRecoveryInventory(ctx, record)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(current, present)
		if index < 0 {
			continue
		}
		if current[index].Online != "NO" {
			if _, err := s.runTargetCommand(ctx, "", "/usr/sbin/ctladm", "port", "-r", "-d", "iscsi", "-p", strconv.Itoa(port.ID), "-O", "cfiscsi_target="+port.Target, "-O", "cfiscsi_portal_group_tag="+strconv.Itoa(port.Tag)); err != nil {
				return errors.New("failed_to_remove_recorded_target_port")
			}
		}
		for {
			current, err := s.checkTargetRecoveryInventory(ctx, record)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(current, present) {
				break
			}
			if err := waitTarget(ctx, interval); err != nil {
				return errors.New("target_recovery_port_removal_pending")
			}
		}
	}
	current, err := s.checkTargetRecoveryInventory(ctx, record)
	if err != nil {
		return err
	}
	if len(current) != 0 {
		return errors.New("target_recovery_ports_remain")
	}
	if err := os.Remove(s.targetRecoveryPath()); err != nil {
		return errors.New("failed_to_clear_target_recovery")
	}
	return nil
}
