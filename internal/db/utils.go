// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"gorm.io/gorm"
)

func GetAll[T any](db *gorm.DB) ([]T, error) {
	var records []T

	err := db.
		Order("created_at ASC").
		Find(&records).Error

	if err != nil {
		return nil, err
	}
	return records, nil
}

func Count(db *gorm.DB, model interface{}, cond string, args ...interface{}) (int64, error) {
	var count int64
	if err := db.Model(model).
		Where(cond, args...).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
