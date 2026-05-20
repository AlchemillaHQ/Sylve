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
	if telemetryDB == nil {
		return
	}

	query := telemetryDB.Where("async_job_id = ? AND status = ?", jobID, "pending")
	if jobType != "" {
		query = query.Where("async_job_type = ?", jobType)
	}

	var record infoModels.AuditRecord
	res := query.
		Order("created_at DESC").
		Limit(1).
		Find(&record)

	if res.Error != nil {
		return
	}

	if res.RowsAffected == 0 {
		return
	}

	updates := map[string]any{
		"status": status,
		"error":  errMsg,
		"ended":  time.Now(),
	}

	if err := telemetryDB.Model(&record).Updates(updates).Error; err != nil {
		return
	}

	if response != nil {
		var action map[string]any
		if err := json.Unmarshal([]byte(record.Action), &action); err == nil {
			action["response"] = response
			if updated, err := json.Marshal(action); err == nil {
				telemetryDB.Model(&record).Update("action", string(updated))
			}
		}
	}
}
