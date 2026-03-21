// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/fsnotify/fsnotify"
)

func (s *Service) ParseAuditLogs() error {
	const logPath = "/var/log/samba4/audit.log"
	const batchSize = 500

	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer f.Close()

	var batch []sambaModels.SambaAuditLog
	batch = make([]sambaModels.SambaAuditLog, 0, batchSize)

	seenCreates := make(map[string]bool)
	recentMkdirs := make(map[string]time.Time)

	cutoff := time.Now().Add(-5 * time.Second)
	var recentDBMkdirs []sambaModels.SambaAuditLog
	if err := s.DB.Where("action = ? AND created_at >= ?", "mkdirat", cutoff).Find(&recentDBMkdirs).Error; err == nil {
		for _, m := range recentDBMkdirs {
			recentMkdirs[m.Path] = m.CreatedAt
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, ": ")
		if idx < 0 {
			continue
		}

		payload := line[idx+2:]
		if !strings.HasPrefix(payload, "sylve-smb-al|") {
			continue
		}

		parts := strings.Split(payload, "|")
		if len(parts) < 8 {
			continue
		}

		action := parts[6]

		switch action {
		case "connect", "disconnect", "mkdirat", "unlinkat", "renameat", "create_file":
		default:
			continue
		}

		entry := sambaModels.SambaAuditLog{
			User:   parts[1],
			IP:     parts[2],
			Share:  parts[4],
			Action: action,
			Result: parts[7],
		}
		args := parts[8:]

		switch action {
		case "mkdirat":
			entry.Path = args[len(args)-1]
			recentMkdirs[entry.Path] = time.Now()

		case "unlinkat":
			entry.Path = args[len(args)-1]

		case "renameat":
			if len(args) >= 2 {
				entry.Path = args[0]
				entry.Target = args[1]
			}

		case "create_file":
			if len(args) >= 2 && args[len(args)-2] == "create" {
				p := args[len(args)-1]
				if !seenCreates[p] {
					seenCreates[p] = true
					entry.Path = p
				}
			}
		}

		if entry.Path != "" {
			entry.Folder = filepath.Base(entry.Path)

			if action == "create_file" {
				if t, exists := recentMkdirs[entry.Path]; exists && time.Since(t) < 5*time.Second {
					continue
				}
			}

			batch = append(batch, entry)

			if len(batch) >= batchSize {
				if err := s.DB.CreateInBatches(&batch, len(batch)).Error; err != nil {
					return fmt.Errorf("failed to insert audit log batch: %w", err)
				}
				batch = batch[:0]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error scanning audit log: %w", err)
	}

	if len(batch) > 0 {
		if err := s.DB.CreateInBatches(&batch, len(batch)).Error; err != nil {
			return fmt.Errorf("failed to insert final audit log batch: %w", err)
		}
	}

	if err := os.Truncate(logPath, 0); err != nil {
		return fmt.Errorf("failed to clear audit log: %w", err)
	}

	return nil
}

func (s *Service) GetAuditLogs(
	page int,
	size int,
	sortField, sortDir string,
) (*sambaServiceInterfaces.AuditLogsResponse, error) {
	if size <= 0 {
		size = 100
	}
	if page <= 0 {
		page = 1
	}

	var total int64
	if err := s.DB.
		Model(&sambaModels.SambaAuditLog{}).
		Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count audit logs: %w", err)
	}

	lastPage := int(math.Ceil(float64(total) / float64(size)))
	allowed := map[string]bool{
		"id":         true,
		"action":     true,
		"share":      true,
		"path":       true,
		"created_at": true,
	}

	field := "id"
	direction := "DESC"
	normalized := strings.ToLower(sortField)

	if normalized == "createdat" {
		normalized = "created_at"
	}

	if allowed[normalized] {
		field = normalized
		dir := strings.ToUpper(sortDir)
		if dir == "ASC" || dir == "DESC" {
			direction = dir
		} else {
			direction = "ASC"
		}
	}

	orderExpr := fmt.Sprintf("%s %s", field, direction)
	offset := (page - 1) * size

	var logs []sambaModels.SambaAuditLog
	if err := s.DB.
		Order(orderExpr).
		Offset(offset).
		Limit(size).
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch audit logs: %w", err)
	}

	return &sambaServiceInterfaces.AuditLogsResponse{
		LastPage: lastPage,
		Data:     logs,
	}, nil
}

func (s *Service) WatchAuditLogs(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.L.Error().Msgf("Failed to create fsnotify watcher: %v", err)
		return
	}
	defer watcher.Close()

	const logPath = "/var/log/samba4/audit.log"

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		if err := os.WriteFile(logPath, []byte(""), 0600); err != nil {
			logger.L.Error().Msgf("Failed to initialize audit log file: %v", err)
			return
		}
	}

	if err := watcher.Add(logPath); err != nil {
		logger.L.Error().Msgf("Failed to watch audit log: %v", err)
		return
	}

	var debounceTimer *time.Timer
	debounceDuration := 1 * time.Second

	logger.L.Info().Msg("Started watching Samba audit logs")

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			logger.L.Debug().Msg("Stopped watching Samba audit logs")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Write) {
				if debounceTimer != nil {
					debounceTimer.Stop()
				}

				debounceTimer = time.AfterFunc(debounceDuration, func() {
					if err := s.ParseAuditLogs(); err != nil {
						logger.L.Error().Msgf("Failed to parse Samba audit logs: %v", err)
					}
				})
			}

			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				logger.L.Warn().Msgf("Audit log was removed or renamed. Attempting to re-watch...")
				watcher.Remove(logPath)

				go func() {
					for {
						time.Sleep(2 * time.Second)
						if _, err := os.Stat(logPath); err == nil {
							if err := watcher.Add(logPath); err == nil {
								logger.L.Info().Msg("Successfully re-attached to audit log")
								break
							}
						}
					}
				}()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.L.Error().Msgf("fsnotify error: %v", err)
		}
	}
}
