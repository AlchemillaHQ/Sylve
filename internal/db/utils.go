// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package db

import (
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func GetHistorical[T any](db *gorm.DB, limit int) ([]T, error) {
	var records []T

	tableName := schema.NamingStrategy{}.TableName(reflect.TypeOf((*T)(nil)).Elem().Name())

	err := db.Table(tableName).
		Order("strftime('%Y-%m-%d %H:%M', created_at) ASC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

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

func StoreAndTrimRecords[T any](db *gorm.DB, model *T, limit int) {
	db.Create(model)

	var count int64
	db.Model(model).Count(&count)

	if count > int64(limit) {
		var oldestIDs []uint
		db.Model(model).
			Order("id ASC").
			Limit(int(count-int64(limit))).
			Pluck("id", &oldestIDs)

		if len(oldestIDs) > 0 {
			var emptyModel T
			db.Where("id IN ?", oldestIDs).Delete(&emptyModel)
		}
	}
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

type IntervalOption struct {
	Value int    `json:"value"`
	Label string `json:"label"`
}

func IntervalToMap(count int) []IntervalOption {
	intervalOptions := []IntervalOption{
		{Value: 1, Label: "Every Minute"},
		{Value: 60, Label: "Every Hour"},
		{Value: 1440, Label: "Every Day"},
		{Value: 10080, Label: "Every Week"},
		{Value: 40320, Label: "Every Month"},
		{Value: 483840, Label: "Every Year"},
	}

	var result []IntervalOption
	for _, opt := range intervalOptions {
		if count >= opt.Value {
			result = append(result, opt)
		}
	}
	return result
}
