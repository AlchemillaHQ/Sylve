// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package console

import (
	"fmt"
	"strings"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

// LoadVMCreateRequest reads a strict JSON VM creation request from path.
func LoadVMCreateRequest(path string) (libvirtServiceInterfaces.CreateVMRequest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return libvirtServiceInterfaces.CreateVMRequest{}, fmt.Errorf("VM create file is required")
	}

	return loadStrictJSONFile[libvirtServiceInterfaces.CreateVMRequest](path, "VM create")
}
