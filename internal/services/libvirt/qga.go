// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

const qgaCommandTimeoutSeconds int32 = 2

type qgaRequest struct {
	Execute   string `json:"execute"`
	Arguments any    `json:"arguments,omitempty"`
}

type qgaError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

type qgaResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *qgaError       `json:"error"`
}

type qgaGuestInfo struct {
	Version           string               `json:"version"`
	SupportedCommands []qgaGuestCapability `json:"supported_commands"`
}

type qgaGuestCapability struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	SuccessResponse bool   `json:"success-response"`
}

func qgaResponseReturn(resp qgaResponse) (json.RawMessage, error) {
	if resp.Error != nil {
		return nil, fmt.Errorf("qga_error_%s: %s", resp.Error.Class, resp.Error.Desc)
	}
	if len(resp.Return) == 0 {
		return nil, fmt.Errorf("invalid_qga_response: missing_return_or_error")
	}
	return resp.Return, nil
}

func decodeQGAResponse(payload []byte) (json.RawMessage, error) {
	var resp qgaResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("failed_to_decode_qga_response: %w", err)
	}
	return qgaResponseReturn(resp)
}

func qgaCallRaw(conn net.Conn, enc *json.Encoder, dec *json.Decoder, cmd string, args any) (json.RawMessage, error) {
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return nil, fmt.Errorf("failed_to_set_qga_deadline: %w", err)
	}

	if err := enc.Encode(qgaRequest{
		Execute:   cmd,
		Arguments: args,
	}); err != nil {
		return nil, fmt.Errorf("failed_to_send_qga_command: %w", err)
	}

	var resp qgaResponse
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed_to_decode_qga_response: %w", err)
	}

	return qgaResponseReturn(resp)
}

func (s *Service) RunQemuGuestAgentCommand(rid uint, cmd string) (json.RawMessage, error) {
	if rid == 0 {
		return nil, fmt.Errorf("invalid_vm_rid")
	}
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("db_not_initialized")
	}
	command := strings.TrimSpace(cmd)
	if command == "" {
		return nil, fmt.Errorf("qga_command_required")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}

	if !vm.QemuGuestAgent {
		return nil, fmt.Errorf("qemu_guest_agent_disabled")
	}

	if err := s.requireConnection(); err != nil {
		return nil, fmt.Errorf("libvirt_connection_unavailable: %w", err)
	}

	domain, err := s.conn().DomainLookupByName(strconv.Itoa(int(rid)))
	if err != nil {
		return nil, fmt.Errorf("failed_to_lookup_domain_for_qga: %w", err)
	}

	request, err := json.Marshal(qgaRequest{Execute: command})
	if err != nil {
		return nil, fmt.Errorf("failed_to_encode_qga_command: %w", err)
	}

	result, err := s.conn().QEMUDomainAgentCommand(domain, string(request), qgaCommandTimeoutSeconds, 0)
	if err == nil {
		if len(result) != 1 || strings.TrimSpace(result[0]) == "" {
			return nil, fmt.Errorf("invalid_qga_response_from_libvirt")
		}
		return decodeQGAResponse([]byte(result[0]))
	}
	if !isLibvirtErrorNumber(err, libvirt.ErrArgumentUnsupported) {
		return nil, fmt.Errorf("failed_to_run_qga_command: %w", err)
	}

	return s.runLegacyQemuGuestAgentCommand(vm.RID, command)
}

func (s *Service) runLegacyQemuGuestAgentCommand(rid uint, command string) (json.RawMessage, error) {
	dataPath, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return nil, fmt.Errorf("failed_to_get_vm_data_path: %w", err)
	}

	socketPath := filepath.Join(dataPath, "qga.sock")
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed_to_connect_qga_socket: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	return qgaCallRaw(conn, enc, dec, command, nil)
}

func (s *Service) GetQemuGuestAgentInfo(rid uint) (libvirtServiceInterfaces.QemuGuestAgentInfo, error) {
	var info libvirtServiceInterfaces.QemuGuestAgentInfo
	if rid == 0 {
		return info, fmt.Errorf("invalid_vm_rid")
	}
	if s == nil || s.DB == nil {
		return info, fmt.Errorf("db_not_initialized")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return info, fmt.Errorf("vm_not_found: %w", err)
		}
		return info, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}
	if !vm.QemuGuestAgent {
		return info, fmt.Errorf("qemu_guest_agent_disabled")
	}
	if err := s.requireConnection(); err != nil {
		return info, fmt.Errorf("libvirt_connection_unavailable: %w", err)
	}
	state, err := s.GetDomainState(int(rid))
	if err != nil {
		return info, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}
	if state != libvirt.DomainRunning {
		return info, fmt.Errorf("qga_requires_running_vm")
	}

	osInfo, err := s.RunQemuGuestAgentCommand(rid, "guest-get-osinfo")
	if err != nil {
		return info, err
	}
	if string(osInfo) != "null" {
		if err := json.Unmarshal(osInfo, &info.OSInfo); err != nil {
			return info, fmt.Errorf("failed_to_unmarshal_qga_return: %w", err)
		}
	}

	interfaces, err := s.RunQemuGuestAgentCommand(rid, "guest-network-get-interfaces")
	if err != nil {
		return info, err
	}
	if string(interfaces) != "null" {
		if err := json.Unmarshal(interfaces, &info.Interfaces); err != nil {
			return info, fmt.Errorf("failed_to_unmarshal_qga_return: %w", err)
		}
	}

	return info, nil
}

func parseQGAGuestInfo(payload json.RawMessage) (string, []libvirtServiceInterfaces.QGACapability, error) {
	var info qgaGuestInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		return "", nil, fmt.Errorf("failed_to_unmarshal_qga_guest_info: %w", err)
	}
	capabilities := make([]libvirtServiceInterfaces.QGACapability, 0, len(info.SupportedCommands))
	for _, capability := range info.SupportedCommands {
		capabilities = append(capabilities, libvirtServiceInterfaces.QGACapability{
			Name: capability.Name, Enabled: capability.Enabled, SuccessResponse: capability.SuccessResponse,
		})
	}
	sort.Slice(capabilities, func(i, j int) bool {
		return capabilities[i].Name < capabilities[j].Name
	})
	return info.Version, capabilities, nil
}

func (s *Service) InspectQemuGuestAgent(rid uint) (libvirtServiceInterfaces.QemuGuestAgentStatus, error) {
	status := libvirtServiceInterfaces.QemuGuestAgentStatus{
		RID: rid, DomainState: "unknown", Capabilities: []libvirtServiceInterfaces.QGACapability{},
	}
	if rid == 0 {
		return status, fmt.Errorf("invalid_vm_rid")
	}
	if s == nil || s.DB == nil {
		return status, fmt.Errorf("db_not_initialized")
	}
	vm, err := s.GetVMByRID(rid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status, fmt.Errorf("vm_not_found: %w", err)
		}
		return status, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}
	status.Enabled = vm.QemuGuestAgent
	if !status.Enabled {
		status.UnavailableReason = "qemu_guest_agent_disabled"
		return status, nil
	}
	if err := s.requireConnection(); err != nil {
		return status, fmt.Errorf("libvirt_connection_unavailable: %w", err)
	}
	state, err := s.GetDomainState(int(rid))
	if err != nil {
		return status, fmt.Errorf("failed_to_get_domain_state: %w", err)
	}
	status.DomainState = qgaDomainStateName(state)
	if state != libvirt.DomainRunning {
		status.UnavailableReason = "vm_not_running"
		return status, nil
	}
	payload, err := s.RunQemuGuestAgentCommand(rid, "guest-info")
	if err != nil {
		if strings.Contains(err.Error(), "qga_error_") {
			status.Reachable = true
			status.UnavailableReason = "qga_capabilities_unavailable"
			return status, nil
		}
		status.UnavailableReason = "qga_unreachable"
		return status, nil
	}
	version, capabilities, err := parseQGAGuestInfo(payload)
	if err != nil {
		return status, err
	}
	status.Reachable = true
	status.Version = version
	status.Capabilities = capabilities
	return status, nil
}

func qgaDomainStateName(state libvirt.DomainState) string {
	switch state {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainBlocked:
		return "blocked"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutdown:
		return "shutdown"
	case libvirt.DomainShutoff:
		return "shut off"
	case libvirt.DomainCrashed:
		return "crashed"
	case libvirt.DomainPmsuspended:
		return "suspended"
	default:
		return "unknown"
	}
}

func isLibvirtErrorNumber(err error, number libvirt.ErrorNumber) bool {
	var value libvirt.Error
	if errors.As(err, &value) {
		return value.Code == uint32(number)
	}

	var pointer *libvirt.Error
	return errors.As(err, &pointer) && pointer != nil && pointer.Code == uint32(number)
}
