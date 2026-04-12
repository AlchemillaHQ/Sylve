// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

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

	if resp.Error != nil {
		return nil, fmt.Errorf("qga_error_%s: %s", resp.Error.Class, resp.Error.Desc)
	}

	if len(resp.Return) == 0 {
		return json.RawMessage("null"), nil
	}

	return resp.Return, nil
}

func qgaCall(conn net.Conn, enc *json.Encoder, dec *json.Decoder, cmd string, out any) error {
	rawReturn, err := qgaCallRaw(conn, enc, dec, cmd, nil)
	if err != nil {
		return err
	}

	if out == nil || len(rawReturn) == 0 {
		return nil
	}
	if bytes.Equal(rawReturn, []byte("null")) {
		return nil
	}

	if err := json.Unmarshal(rawReturn, out); err != nil {
		return fmt.Errorf("failed_to_unmarshal_qga_return: %w", err)
	}

	return nil
}

func (s *Service) RunQemuGuestAgentCommand(rid uint, cmd string) (json.RawMessage, error) {
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

	dataPath, err := s.GetVMConfigDirectory(vm.RID)
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

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return info, fmt.Errorf("failed_to_get_vm_by_rid: %w", err)
	}

	if !vm.QemuGuestAgent {
		return info, fmt.Errorf("qemu_guest_agent_disabled")
	}

	dataPath, err := s.GetVMConfigDirectory(vm.RID)
	if err != nil {
		return info, fmt.Errorf("failed_to_get_vm_data_path: %w", err)
	}

	socketPath := filepath.Join(dataPath, "qga.sock")
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return info, fmt.Errorf("failed_to_connect_qga_socket: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := qgaCall(conn, enc, dec, "guest-get-osinfo", &info.OSInfo); err != nil {
		return info, err
	}

	if err := qgaCall(conn, enc, dec, "guest-network-get-interfaces", &info.Interfaces); err != nil {
		return info, err
	}

	return info, nil
}
