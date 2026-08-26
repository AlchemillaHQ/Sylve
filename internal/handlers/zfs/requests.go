// SPDX-License-Identifier: BSD-2-Clause

package zfsHandlers

import (
	"fmt"
	"strconv"
	"strings"

	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/internal/services/zfs"
	"github.com/gin-gonic/gin"
)

func invalidZFSRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", zfs.ErrInvalidRequest, fmt.Sprintf(format, args...))
}

func parseOptionalBoolQuery(c *gin.Context, name string) (bool, error) {
	values, exists := c.Request.URL.Query()[name]
	if !exists {
		return false, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return false, invalidZFSRequest("%s must be a boolean", name)
	}
	value, err := strconv.ParseBool(values[0])
	if err != nil {
		return false, invalidZFSRequest("%s must be a boolean", name)
	}
	return value, nil
}

func datasetDeletionTargetsQuery(c *gin.Context) ([]zfsServiceInterfaces.DatasetDeletionTarget, error) {
	query := c.Request.URL.Query()
	names, namesExist := query["name"]
	guids, guidsExist := query["guid"]
	if !namesExist || !guidsExist || len(names) == 0 || len(guids) == 0 {
		return nil, invalidZFSRequest("at least one name and guid query parameter pair is required")
	}
	if len(names) != len(guids) {
		return nil, invalidZFSRequest("name and guid query parameters must have matching counts")
	}

	targets := make([]zfsServiceInterfaces.DatasetDeletionTarget, 0, len(names))
	seenNames := make(map[string]string, len(names))
	seenGUIDs := make(map[string]string, len(guids))
	for i := range names {
		name := strings.Trim(strings.TrimSpace(names[i]), "/")
		guid := strings.TrimSpace(guids[i])
		if name == "" || guid == "" {
			return nil, invalidZFSRequest("dataset names and guids cannot be empty")
		}
		if existingGUID, exists := seenNames[name]; exists {
			if existingGUID != guid {
				return nil, invalidZFSRequest("dataset name %q is paired with multiple guids", name)
			}
			continue
		}
		if existingName, exists := seenGUIDs[guid]; exists {
			return nil, invalidZFSRequest("dataset guid %q is paired with multiple names: %q and %q", guid, existingName, name)
		}

		seenNames[name] = guid
		seenGUIDs[guid] = name
		targets = append(targets, zfsServiceInterfaces.DatasetDeletionTarget{Name: name, GUID: guid})
	}

	return targets, nil
}

func positiveUintPath(c *gin.Context, name string) (uint, error) {
	raw := strings.TrimSpace(c.Param(name))
	value, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	if err != nil || value == 0 {
		return 0, invalidZFSRequest("%s must be a positive integer", name)
	}
	return uint(value), nil
}
