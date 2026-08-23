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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
const networkObjectRefreshTimeout = 60 * time.Second
const maxNetworkObjectRedirects = 3

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
	return parseListPayloadToValuesContext(context.Background(), payload)
}

func resolveNetworkObjectFQDN(ctx context.Context, name string) ([]string, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return nil, err
	}

	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, address.IP.String())
	}
	return values, nil
}

func parseListPayloadToValuesContext(ctx context.Context, payload string) ([]string, error) {
	out := []string{}
	scanner := bufio.NewScanner(strings.NewReader(payload))

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		value := strings.Fields(line)[0]
		switch {
		case utils.IsValidIPv4(value), utils.IsValidIPv6(value), utils.IsValidIPv4CIDR(value), utils.IsValidIPv6CIDR(value):
			out = append(out, value)
		case utils.IsValidFQDN(value):
			resolved, err := resolveNetworkObjectFQDN(ctx, value)
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

func validateNetworkObjectListURL(value string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	if err != nil || parsed == nil || parsed.Host == "" {
		return invalidNetworkObject("invalid_network_object_list_url", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return invalidNetworkObject("invalid_network_object_list_url_scheme", nil)
	}
	if parsed.User != nil {
		return invalidNetworkObject("network_object_list_credentials_not_allowed", nil)
	}
	return nil
}

func fetchListPayload(ctx context.Context, entry string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", networkObjectUpstream("network_object_source_too_large", nil)
	}
	entry = strings.TrimSpace(entry)
	if err := validateNetworkObjectListURL(entry); err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxNetworkObjectRedirects {
				return networkObjectUpstream("network_object_source_redirect_limit", nil)
			}
			if err := validateNetworkObjectListURL(req.URL.String()); err != nil {
				return networkObjectUpstream("network_object_source_redirect_invalid", nil)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry, nil)
	if err != nil {
		return "", invalidNetworkObject("invalid_network_object_list_url", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrNetworkObjectUpstream) {
			return "", err
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", networkObjectUpstream("network_object_refresh_timeout", err)
		}
		return "", networkObjectUpstream("network_object_source_unavailable", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", networkObjectUpstream("network_object_source_unavailable", fmt.Errorf("http_status_%d", resp.StatusCode))
	}
	if resp.ContentLength > maxBytes {
		return "", networkObjectUpstream("network_object_source_too_large", nil)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", networkObjectUpstream("network_object_refresh_timeout", err)
		}
		return "", networkObjectUpstream("network_object_source_unavailable", err)
	}
	if int64(len(body)) > maxBytes {
		return "", networkObjectUpstream("network_object_source_too_large", nil)
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

	ctx, cancel := context.WithTimeout(context.Background(), networkObjectRefreshTimeout)
	defer cancel()

	values := []string{}
	incomingSourceChecksum := ""
	switch object.Type {
	case "FQDN":
		for _, entry := range object.Entries {
			fqdn := strings.TrimSpace(entry.Value)
			if fqdn == "" {
				continue
			}
			resolved, err := resolveNetworkObjectFQDN(ctx, fqdn)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return false, networkObjectUpstream("network_object_refresh_timeout", err)
				}
				return false, networkObjectUpstream("network_object_source_unavailable", err)
			}
			values = append(values, resolved...)
		}
	case "List":
		listPayloads := make([]string, 0, len(object.Entries))
		sourceTokens := make([]string, 0, len(object.Entries))
		var totalPayloadBytes int64
		for _, entry := range object.Entries {
			entryURL := strings.TrimSpace(entry.Value)
			if entryURL == "" {
				continue
			}
			payload, err := fetchListPayload(ctx, entryURL, maxNetworkObjectListPayloadBytes-totalPayloadBytes)
			if err != nil {
				return false, err
			}
			totalPayloadBytes += int64(len(payload))

			listPayloads = append(listPayloads, payload)
			sourceTokens = append(sourceTokens, listSourceToken(entryURL, payload))
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
			fetched, err := parseListPayloadToValuesContext(ctx, payload)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return false, networkObjectUpstream("network_object_refresh_timeout", err)
				}
				return false, networkObjectUpstream("network_object_source_invalid", err)
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
