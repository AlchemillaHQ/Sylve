// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package libvirt

import (
	"errors"
	"os"
	"strconv"
	"strings"

	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"gorm.io/gorm"
)

const (
	VMSerialConsoleDefaultBaud = "115200"
	VMSerialConsoleMinBaud     = 50
	VMSerialConsoleMaxBaud     = 4_000_000
)

type VMSerialConsoleRequest struct {
	RID        uint
	BaudRate   string
	DevicePath string
}

type VMSerialConsoleAccessInfo struct {
	RID         uint   `json:"rid"`
	Name        string `json:"name"`
	BaudRate    string `json:"baudRate"`
	DevicePath  string `json:"devicePath"`
	DomainState string `json:"domainState"`
	Available   bool   `json:"available"`
}

type VMSerialConsoleError struct {
	Code   string
	Detail string
}

func (e *VMSerialConsoleError) Error() string {
	if e == nil {
		return ""
	}
	detail := strings.TrimSpace(e.Detail)
	if detail == "" || detail == e.Code {
		return e.Code
	}
	return e.Code + ": " + detail
}

type VMSerialConsoleService interface {
	GetVMByRID(rid uint) (vmModels.VM, error)
	CanMutateProtectedVM(rid uint) (bool, error)
	GetLvDomain(rid uint) (*libvirtServiceInterfaces.LvDomain, error)
}

func ParseVMSerialConsoleRequest(ridText, baudRate string) (VMSerialConsoleRequest, error) {
	rid, err := strconv.ParseUint(strings.TrimSpace(ridText), 10, 32)
	if err != nil || rid == 0 || rid > 9999 {
		return VMSerialConsoleRequest{}, &VMSerialConsoleError{
			Code:   "invalid_rid_format",
			Detail: "rid must be an integer between 1 and 9999",
		}
	}

	baudRate = strings.TrimSpace(baudRate)
	if baudRate == "" {
		baudRate = VMSerialConsoleDefaultBaud
	}
	baud, err := strconv.ParseUint(baudRate, 10, 32)
	if err != nil || baud < VMSerialConsoleMinBaud || baud > VMSerialConsoleMaxBaud {
		return VMSerialConsoleRequest{}, &VMSerialConsoleError{
			Code:   "invalid_baud_rate",
			Detail: "baud must be an integer between 50 and 4000000",
		}
	}

	normalizedRID := strconv.FormatUint(rid, 10)
	return VMSerialConsoleRequest{
		RID:        uint(rid),
		BaudRate:   strconv.FormatUint(baud, 10),
		DevicePath: "/dev/nmdm" + normalizedRID + "B",
	}, nil
}

func VMDomainSupportsSerialConsole(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "blocked", "paused", "shutdown", "pmsuspended":
		return true
	default:
		return false
	}
}

func PreflightVMSerialConsole(
	service VMSerialConsoleService,
	request VMSerialConsoleRequest,
	deviceStat func(string) (os.FileInfo, error),
) (VMSerialConsoleAccessInfo, error) {
	info := VMSerialConsoleAccessInfo{
		RID: request.RID, BaudRate: request.BaudRate, DevicePath: request.DevicePath,
	}
	if service == nil {
		return info, &VMSerialConsoleError{Code: "vm_service_unavailable"}
	}

	vm, err := service.GetVMByRID(request.RID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(strings.ToLower(err.Error()), "vm_not_found") {
			return info, &VMSerialConsoleError{Code: "vm_not_found"}
		}
		return info, &VMSerialConsoleError{Code: "failed_to_get_vm", Detail: err.Error()}
	}
	info.Name = vm.Name
	if !vm.Serial {
		return info, &VMSerialConsoleError{Code: "vm_serial_console_disabled"}
	}

	allowed, err := service.CanMutateProtectedVM(request.RID)
	if err != nil {
		return info, &VMSerialConsoleError{Code: "vm_console_guard_unavailable", Detail: err.Error()}
	}
	if !allowed {
		return info, &VMSerialConsoleError{Code: "replication_lease_not_owned"}
	}

	domain, err := service.GetLvDomain(request.RID)
	if err != nil {
		if libvirtServiceInterfaces.IsDomainNotFoundError(err) {
			return info, &VMSerialConsoleError{Code: "vm_domain_not_defined"}
		}
		return info, &VMSerialConsoleError{Code: "libvirt_connection_unavailable", Detail: err.Error()}
	}
	if domain == nil {
		return info, &VMSerialConsoleError{Code: "libvirt_connection_unavailable", Detail: "vm_domain_unavailable"}
	}
	info.DomainState = strings.ToLower(strings.TrimSpace(domain.Status))
	if !VMDomainSupportsSerialConsole(domain.Status) {
		return info, &VMSerialConsoleError{Code: "vm_console_requires_running_vm"}
	}

	if deviceStat == nil {
		deviceStat = os.Stat
	}
	if _, err := deviceStat(request.DevicePath); err != nil {
		return info, &VMSerialConsoleError{Code: "vm_serial_device_unavailable", Detail: err.Error()}
	}
	info.Available = true
	return info, nil
}
