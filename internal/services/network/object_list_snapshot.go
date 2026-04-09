// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	listSnapshotEncodingGzipJSONV1 = "gzip-json-v1"
	listSnapshotMigrationName      = "network_object_list_snapshot_migration_1"
)

func encodeListSnapshotPayload(values []string) ([]byte, error) {
	normalized := uniqueStrings(values)

	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func decodeListSnapshotPayload(payload []byte, encoding string) ([]string, error) {
	if len(payload) == 0 {
		return []string{}, nil
	}

	enc := strings.TrimSpace(strings.ToLower(encoding))
	if enc != listSnapshotEncodingGzipJSONV1 {
		return nil, fmt.Errorf("unsupported_list_snapshot_encoding: %s", encoding)
	}

	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}

	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}

	return uniqueStrings(values), nil
}

func storeListSnapshot(tx *gorm.DB, objectID uint, checksum string, values []string) error {
	normalized := uniqueStrings(values)
	payload, err := encodeListSnapshotPayload(normalized)
	if err != nil {
		return err
	}

	row := networkModels.ObjectListSnapshot{
		ObjectID:   objectID,
		Checksum:   strings.TrimSpace(checksum),
		ValueCount: uint(len(normalized)),
		Encoding:   listSnapshotEncodingGzipJSONV1,
		Payload:    payload,
	}

	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "object_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"checksum", "value_count", "encoding", "payload", "updated_at"}),
	}).Create(&row).Error
}

func (s *Service) loadListSnapshotValues(objectID uint) ([]string, error) {
	var row networkModels.ObjectListSnapshot
	if err := s.DB.First(&row, "object_id = ?", objectID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	values, err := decodeListSnapshotPayload(row.Payload, row.Encoding)
	if err != nil {
		return nil, err
	}

	return values, nil
}

func (s *Service) loadDynamicValuesForObjects(objects map[uint]*networkModels.Object) (map[uint][]string, error) {
	valuesByObject := make(map[uint][]string, len(objects))
	fqdnIDs := make([]uint, 0)
	listIDs := make([]uint, 0)

	for id, obj := range objects {
		if obj == nil {
			continue
		}
		switch obj.Type {
		case "FQDN":
			fqdnIDs = append(fqdnIDs, id)
		case "List":
			listIDs = append(listIDs, id)
		}
	}

	if len(fqdnIDs) > 0 {
		var rows []networkModels.ObjectResolution
		if err := s.DB.
			Select("object_id", "resolved_value").
			Where("object_id IN ?", fqdnIDs).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			v := strings.TrimSpace(row.ResolvedValue)
			if v == "" {
				continue
			}
			valuesByObject[row.ObjectID] = append(valuesByObject[row.ObjectID], v)
		}
	}

	if len(listIDs) > 0 {
		var snapshots []networkModels.ObjectListSnapshot
		if err := s.DB.Where("object_id IN ?", listIDs).Find(&snapshots).Error; err != nil {
			return nil, err
		}

		loaded := map[uint]struct{}{}
		for _, snapshot := range snapshots {
			decoded, err := decodeListSnapshotPayload(snapshot.Payload, snapshot.Encoding)
			if err != nil {
				return nil, err
			}
			valuesByObject[snapshot.ObjectID] = append(valuesByObject[snapshot.ObjectID], decoded...)
			loaded[snapshot.ObjectID] = struct{}{}
		}

		// Backward compatibility fallback for pre-migration rows.
		missingIDs := make([]uint, 0)
		for _, objectID := range listIDs {
			if _, ok := loaded[objectID]; !ok {
				missingIDs = append(missingIDs, objectID)
			}
		}
		if len(missingIDs) > 0 {
			var rows []networkModels.ObjectResolution
			if err := s.DB.
				Select("object_id", "resolved_value").
				Where("object_id IN ?", missingIDs).
				Find(&rows).Error; err != nil {
				return nil, err
			}
			for _, row := range rows {
				v := strings.TrimSpace(row.ResolvedValue)
				if v == "" {
					continue
				}
				valuesByObject[row.ObjectID] = append(valuesByObject[row.ObjectID], v)
			}
		}
	}

	for id, vals := range valuesByObject {
		valuesByObject[id] = uniqueStrings(vals)
	}

	return valuesByObject, nil
}

func (s *Service) ensureListSnapshotMigration() {
	s.listSnapshotMigrationOnce.Do(func() {
		if err := s.migrateListResolutionsToSnapshots(); err != nil {
			logger.L.Error().Err(err).Msg("failed to migrate list object resolutions to snapshots")
		}
	})
}

func (s *Service) migrateListResolutionsToSnapshots() error {
	var count int64
	if err := s.DB.
		Table("migrations").
		Where("name = ?", listSnapshotMigrationName).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	if !s.DB.Migrator().HasTable(&networkModels.Object{}) ||
		!s.DB.Migrator().HasTable(&networkModels.ObjectResolution{}) {
		return s.DB.Table("migrations").Create(map[string]any{"name": listSnapshotMigrationName}).Error
	}

	var listObjectIDs []uint
	if err := s.DB.
		Table("objects AS o").
		Select("DISTINCT o.id").
		Joins("JOIN object_resolutions AS r ON r.object_id = o.id").
		Where("o.type = ?", "List").
		Pluck("o.id", &listObjectIDs).Error; err != nil {
		return err
	}

	for _, objectID := range listObjectIDs {
		values := []string{}
		if err := s.DB.
			Model(&networkModels.ObjectResolution{}).
			Where("object_id = ?", objectID).
			Pluck("resolved_value", &values).Error; err != nil {
			return err
		}

		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			normalized := uniqueStrings(values)
			checksum := objectValuesChecksum(normalized)

			if len(normalized) > 0 {
				if err := storeListSnapshot(tx, objectID, checksum, normalized); err != nil {
					return err
				}
			}

			if err := tx.Where("id = ? AND (resolution_checksum IS NULL OR TRIM(resolution_checksum) = '')", objectID).
				Model(&networkModels.Object{}).
				Update("resolution_checksum", checksum).Error; err != nil {
				return err
			}

			if err := tx.Where("object_id = ?", objectID).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				return err
			}

			return nil
		}); err != nil {
			return err
		}
	}

	return s.DB.Table("migrations").Create(map[string]any{"name": listSnapshotMigrationName}).Error
}
