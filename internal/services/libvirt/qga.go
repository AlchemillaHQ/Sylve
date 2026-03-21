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
	"fmt"
	"net"
	"path/filepath"
	"time"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

type qgaRequest struct {
	Execute string `json:"execute"`
}

type qgaError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

type qgaResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *qgaError       `json:"error"`
}

func qgaCall(conn net.Conn, enc *json.Encoder, dec *json.Decoder, cmd string, out any) error {
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("failed_to_set_qga_deadline: %w", err)
	}

	if err := enc.Encode(qgaRequest{Execute: cmd}); err != nil {
		return fmt.Errorf("failed_to_send_qga_command: %w", err)
	}

	var resp qgaResponse
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("failed_to_decode_qga_response: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("qga_error_%s: %s", resp.Error.Class, resp.Error.Desc)
	}

	if out == nil || len(resp.Return) == 0 {
		return nil
	}

	if err := json.Unmarshal(resp.Return, out); err != nil {
		return fmt.Errorf("failed_to_unmarshal_qga_return: %w", err)
	}

	return nil
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
