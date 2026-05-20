// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import "gorm.io/gorm"

func GetVal(id *uint) uint {
	if id == nil {
		return 0
	}

	return *id
}

func Exists[T any](db *gorm.DB, query string, args ...any) (bool, error) {
	var count int64

	err := db.Model(new(T)).
		Where(query, args...).
		Limit(1).
		Count(&count).Error

	return count > 0, err
}
