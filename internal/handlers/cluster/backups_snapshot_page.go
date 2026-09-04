// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.

package clusterHandlers

import (
	"net/http"
	"strings"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/services/zelta"
	"github.com/gin-gonic/gin"
)

func backupSnapshotPageRequest(c *gin.Context) (zelta.SnapshotPageRequest, bool) {
	query := c.Request.URL.Query()
	limitValues := query["limit"]
	cursorValues := query["cursor"]
	if len(limitValues) > 1 {
		writeBackupSnapshotPageQueryError(c, "invalid_snapshot_page_limit")
		return zelta.SnapshotPageRequest{}, false
	}
	if len(cursorValues) > 1 {
		writeBackupSnapshotPageQueryError(c, "invalid_snapshot_page_cursor")
		return zelta.SnapshotPageRequest{}, false
	}

	limitRaw := ""
	if len(limitValues) == 1 {
		limitRaw = limitValues[0]
	}
	limit, err := zelta.ParseSnapshotPageLimit(limitRaw)
	if err != nil {
		writeBackupSnapshotPageQueryError(c, "invalid_snapshot_page_limit")
		return zelta.SnapshotPageRequest{}, false
	}
	cursor := ""
	if len(cursorValues) == 1 {
		cursor = strings.TrimSpace(cursorValues[0])
	}
	request, err := zelta.NewSnapshotPageRequest(limit, cursor)
	if err != nil {
		writeBackupSnapshotPageQueryError(c, "invalid_snapshot_page_cursor")
		return zelta.SnapshotPageRequest{}, false
	}
	return request, true
}

func writeBackupSnapshotPageQueryError(c *gin.Context, code string) {
	c.JSON(http.StatusBadRequest, internal.APIResponse[any]{
		Status:  "error",
		Message: code,
		Error:   code,
		Data:    nil,
	})
}
