// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package console

import (
	"fmt"
	"strconv"
	"strings"

	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
)

func ValidateVMSnapshotCreate(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return "", "", fmt.Errorf("snapshot name is required")
	}
	if len(name) > 128 {
		return "", "", fmt.Errorf("snapshot name must not exceed 128 characters")
	}
	if len(description) > 4096 {
		return "", "", fmt.Errorf("snapshot description must not exceed 4096 characters")
	}
	return name, description, nil
}

func BuildVMTemplateConvertRequest(name string) (libvirtServiceInterfaces.ConvertToTemplateRequest, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return libvirtServiceInterfaces.ConvertToTemplateRequest{}, fmt.Errorf("template name is required")
	}
	if len(name) > 120 {
		return libvirtServiceInterfaces.ConvertToTemplateRequest{}, fmt.Errorf("template name must not exceed 120 characters")
	}
	return libvirtServiceInterfaces.ConvertToTemplateRequest{Name: name}, nil
}

func ParseVMTemplateStoragePoolAssignments(values []string) ([]libvirtServiceInterfaces.VMTemplateStoragePoolAssignment, error) {
	assignments := make([]libvirtServiceInterfaces.VMTemplateStoragePoolAssignment, 0, len(values))
	seen := make(map[uint]struct{}, len(values))
	for _, raw := range values {
		sourceText, pool, found := strings.Cut(strings.TrimSpace(raw), "=")
		sourceID, err := strconv.ParseUint(strings.TrimSpace(sourceText), 10, 32)
		pool = strings.TrimSpace(pool)
		if !found || err != nil || sourceID == 0 || pool == "" {
			return nil, fmt.Errorf("storage-pool %q must use SOURCE_STORAGE_ID=POOL syntax", raw)
		}
		id := uint(sourceID)
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("storage-pool source ID %d was specified more than once", id)
		}
		seen[id] = struct{}{}
		assignments = append(assignments, libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{
			SourceStorageID: id,
			Pool:            pool,
		})
	}
	return assignments, nil
}

func ValidateVMTemplateCreateRequest(
	request libvirtServiceInterfaces.CreateFromTemplateRequest,
) (libvirtServiceInterfaces.CreateFromTemplateRequest, error) {
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Mode == "" {
		request.Mode = "single"
	}
	request.Name = strings.TrimSpace(request.Name)
	request.NamePrefix = strings.TrimSpace(request.NamePrefix)
	request.CloudInitPrefix = strings.TrimSpace(request.CloudInitPrefix)
	if request.StoragePools == nil {
		request.StoragePools = []libvirtServiceInterfaces.VMTemplateStoragePoolAssignment{}
	}

	switch request.Mode {
	case "single":
		if request.RID == 0 || request.RID > 9999 {
			return request, fmt.Errorf("rid must be between 1 and 9999 in single mode")
		}
		if request.StartRID != 0 || request.Count != 0 || request.NamePrefix != "" {
			return request, fmt.Errorf("start-rid, count, and name-prefix are incompatible with single mode")
		}
	case "multiple":
		if request.StartRID == 0 || request.StartRID > 9999 {
			return request, fmt.Errorf("start-rid must be between 1 and 9999 in multiple mode")
		}
		if request.Count < 1 || request.Count > 200 {
			return request, fmt.Errorf("count must be between 1 and 200 in multiple mode")
		}
		if uint(request.Count-1) > 9999-request.StartRID {
			return request, fmt.Errorf("start-rid and count exceed the supported RID range")
		}
		if request.RID != 0 || request.Name != "" {
			return request, fmt.Errorf("rid and name are incompatible with multiple mode")
		}
	default:
		return request, fmt.Errorf("mode must be single or multiple")
	}

	if request.CloudInitPrefix != "" && !request.RewriteCloudInitIdentity {
		return request, fmt.Errorf("cloud-init-prefix requires rewrite-cloud-init-identity=true")
	}
	return request, nil
}
