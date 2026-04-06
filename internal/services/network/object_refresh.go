// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"
	"gorm.io/gorm"
)

const defaultObjectRefreshInterval = 5 * time.Minute
const objectResolutionInsertBatchSize = 100

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

func parseListPayloadToValues(payload string) ([]string, error) {
	out := []string{}
	scanner := bufio.NewScanner(strings.NewReader(payload))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		value := strings.Fields(line)[0]
		switch {
		case utils.IsValidIPv4(value), utils.IsValidIPv6(value), utils.IsValidIPv4CIDR(value), utils.IsValidIPv6CIDR(value):
			out = append(out, value)
		case utils.IsValidFQDN(value):
			resolved, err := resolveFQDNValues(value)
			if err != nil {
				return nil, err
			}
			out = append(out, resolved...)
		default:
			return nil, fmt.Errorf("unsupported_list_line: %s", value)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return uniqueStrings(out), nil
}

func fetchListPayload(entry string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(entry)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http_status_%d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func checksumString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func listSourceToken(entryURL, payload string) string {
	return strings.TrimSpace(entryURL) + "\x00" + checksumString(payload)
}

func listSourceChecksum(tokens []string) string {
	return objectValuesChecksum(tokens)
}

func objectValuesChecksum(values []string) string {
	normalized := uniqueStrings(values)
	if len(normalized) == 0 {
		return ""
	}

	digest := sha256.New()
	for i, value := range normalized {
		if i > 0 {
			_, _ = digest.Write([]byte{0})
		}
		_, _ = digest.Write([]byte(value))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *Service) loadStoredResolutionChecksum(objectID uint) (string, error) {
	values := []string{}
	if err := s.DB.Model(&networkModels.ObjectResolution{}).
		Where("object_id = ?", objectID).
		Pluck("resolved_value", &values).Error; err != nil {
		return "", err
	}

	// List snapshots may hold dynamic values without per-value rows.
	if len(values) == 0 {
		snapshotValues, err := s.loadListSnapshotValues(objectID)
		if err != nil {
			return "", err
		}
		values = snapshotValues
	}

	return objectValuesChecksum(values), nil
}

func buildResolutionRows(objectID uint, values []string) []networkModels.ObjectResolution {
	rows := make([]networkModels.ObjectResolution, 0, len(values))
	for _, value := range uniqueStrings(values) {
		row := networkModels.ObjectResolution{
			ObjectID:      objectID,
			ResolvedValue: value,
		}
		if utils.IsValidIPv4(value) || utils.IsValidIPv6(value) {
			row.ResolvedIP = value
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Service) refreshObjectResolutions(object *networkModels.Object) (bool, error) {
	if object.Type != "FQDN" && object.Type != "List" {
		return false, nil
	}

	values := []string{}
	incomingSourceChecksum := ""
	switch object.Type {
	case "FQDN":
		for _, entry := range object.Entries {
			fqdn := strings.TrimSpace(entry.Value)
			if fqdn == "" {
				continue
			}
			resolved, err := resolveFQDNValues(fqdn)
			if err != nil {
				return false, err
			}
			values = append(values, resolved...)
		}
	case "List":
		listPayloads := make([]string, 0, len(object.Entries))
		sourceTokens := make([]string, 0, len(object.Entries))
		for _, entry := range object.Entries {
			url := strings.TrimSpace(entry.Value)
			if url == "" {
				continue
			}
			payload, err := fetchListPayload(url)
			if err != nil {
				return false, err
			}

			listPayloads = append(listPayloads, payload)
			sourceTokens = append(sourceTokens, listSourceToken(url, payload))
		}

		incomingSourceChecksum = listSourceChecksum(sourceTokens)
		existingSourceChecksum := strings.TrimSpace(object.SourceChecksum)
		if existingSourceChecksum != "" && existingSourceChecksum == incomingSourceChecksum {
			now := time.Now().UTC()
			if err := s.DB.Model(&networkModels.Object{}).Where("id = ?", object.ID).Updates(map[string]any{
				"last_refresh_at":    &now,
				"last_refresh_error": "",
				"source_checksum":    incomingSourceChecksum,
			}).Error; err != nil {
				return false, err
			}
			return false, nil
		}

		for _, payload := range listPayloads {
			fetched, err := parseListPayloadToValues(payload)
			if err != nil {
				return false, err
			}
			values = append(values, fetched...)
		}
	}

	values = uniqueStrings(values)
	incomingChecksum := objectValuesChecksum(values)

	existingChecksum := strings.TrimSpace(object.ResolutionChecksum)
	if existingChecksum == "" {
		storedChecksum, err := s.loadStoredResolutionChecksum(object.ID)
		if err != nil {
			return false, err
		}
		existingChecksum = storedChecksum
	}

	if existingChecksum == incomingChecksum {
		now := time.Now().UTC()
		updates := map[string]any{
			"last_refresh_at":     &now,
			"last_refresh_error":  "",
			"resolution_checksum": incomingChecksum,
		}
		if object.Type == "List" {
			updates["source_checksum"] = incomingSourceChecksum

			// Ensure list snapshots exist even when checksums match.
			if err := s.DB.Transaction(func(tx *gorm.DB) error {
				if err := storeListSnapshot(tx, object.ID, incomingChecksum, values); err != nil {
					return err
				}
				return tx.Model(&networkModels.Object{}).Where("id = ?", object.ID).Updates(updates).Error
			}); err != nil {
				return false, err
			}
			return false, nil
		}
		if err := s.DB.Model(&networkModels.Object{}).Where("id = ?", object.ID).Updates(updates).Error; err != nil {
			return false, err
		}
		return false, nil
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if object.Type == "List" {
			if err := storeListSnapshot(tx, object.ID, incomingChecksum, values); err != nil {
				return err
			}

			// Explicitly purge legacy list rows after moving to snapshots.
			if err := tx.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Where("object_id = ?", object.ID).Delete(&networkModels.ObjectResolution{}).Error; err != nil {
				return err
			}
			if len(values) > 0 {
				rows := buildResolutionRows(object.ID, values)
				if err := tx.CreateInBatches(rows, objectResolutionInsertBatchSize).Error; err != nil {
					return err
				}
			}
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"last_refresh_at":     &now,
			"last_refresh_error":  "",
			"resolution_checksum": incomingChecksum,
		}
		if object.Type == "List" {
			updates["source_checksum"] = incomingSourceChecksum
		}
		if err := tx.Model(&networkModels.Object{}).Where("id = ?", object.ID).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return false, err
	}

	return true, nil
}

func (s *Service) RefreshDynamicObjects() (bool, error) {
	var objects []networkModels.Object
	if err := s.DB.
		Preload("Entries").
		Where("type IN ? AND auto_update = ?", []string{"FQDN", "List"}, true).
		Find(&objects).Error; err != nil {
		return false, err
	}

	changed := false
	now := time.Now().UTC()
	for i := range objects {
		intervalSeconds := objects[i].RefreshIntervalSeconds
		if intervalSeconds == 0 {
			intervalSeconds = uint(defaultObjectRefreshInterval / time.Second)
		}

		if objects[i].LastRefreshAt != nil {
			nextRefresh := objects[i].LastRefreshAt.Add(time.Duration(intervalSeconds) * time.Second)
			if now.Before(nextRefresh) {
				continue
			}
		}

		updated, err := s.refreshObjectResolutions(&objects[i])
		if err != nil {
			_ = s.DB.Model(&networkModels.Object{}).Where("id = ?", objects[i].ID).Update("last_refresh_error", err.Error()).Error
			continue
		}
		if updated {
			changed = true
		}
	}

	if changed {
		if err := s.ApplyFirewallIfEnabled(); err != nil {
			return true, err
		}
	}

	return changed, nil
}

func (s *Service) RefreshObjectByID(id uint) error {
	var object networkModels.Object
	if err := s.DB.Preload("Entries").First(&object, id).Error; err != nil {
		return err
	}

	changed, err := s.refreshObjectResolutions(&object)
	if err != nil {
		_ = s.DB.Model(&networkModels.Object{}).Where("id = ?", id).Update("last_refresh_error", err.Error()).Error
		return err
	}

	if changed {
		return s.ApplyFirewallIfEnabled()
	}

	return nil
}

func (s *Service) StartObjectRefreshWorker(ctx context.Context) {
	ticker := time.NewTicker(defaultObjectRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.L.Info().Msg("stopping_object_refresh_worker")
			return
		case <-ticker.C:
			if _, err := s.RefreshDynamicObjects(); err != nil {
				logger.L.Error().Err(err).Msg("object_refresh_worker_failed")
			}
		}
	}
}
