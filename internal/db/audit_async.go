// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"encoding/json"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	"gorm.io/gorm"
)

func FinalizeAsyncAuditRecord(telemetryDB *gorm.DB, jobType string, jobID uint, status string, errMsg string, response interface{}) {
	finalizeAsyncAuditRecords(telemetryDB, jobType, jobID, status, errMsg, response, nil)
}

func FinalizeAsyncAuditRecordsBefore(
	telemetryDB *gorm.DB,
	jobType string,
	jobID uint,
	status string,
	errMsg string,
	response interface{},
	createdBefore time.Time,
) {
	createdBefore = createdBefore.UTC()
	finalizeAsyncAuditRecords(telemetryDB, jobType, jobID, status, errMsg, response, &createdBefore)
}

func finalizeAsyncAuditRecords(
	telemetryDB *gorm.DB,
	jobType string,
	jobID uint,
	status string,
	errMsg string,
	response interface{},
	createdBefore *time.Time,
) {
	if telemetryDB == nil {
		return
	}

	query := telemetryDB.Where("async_job_id = ? AND status = ?", jobID, "pending")
	if jobType != "" {
		query = query.Where("async_job_type = ?", jobType)
	}
	var records []infoModels.AuditRecord
	if err := query.Order("created_at DESC").Find(&records).Error; err != nil {
		return
	}
	if createdBefore != nil {
		filtered := records[:0]
		for i := range records {
			if records[i].CreatedAt.UTC().Before(*createdBefore) {
				filtered = append(filtered, records[i])
			}
		}
		records = filtered
	}

	if len(records) == 0 {
		return
	}

	updates := map[string]any{
		"status": status,
		"error":  errMsg,
		"ended":  time.Now(),
	}

	for i := range records {
		if err := telemetryDB.Model(&records[i]).Updates(updates).Error; err != nil {
			continue
		}

		if response != nil {
			var action map[string]any
			if err := json.Unmarshal([]byte(records[i].Action), &action); err == nil {
				action["response"] = response
				if updated, err := json.Marshal(action); err == nil {
					telemetryDB.Model(&records[i]).Update("action", string(updated))
				}
			}
		}
	}
}
