// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package samba

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/logger"
)

const (
	auditLogPath    = "/var/log/samba4/audit.log"
	auditLogDir     = "/var/log/samba4"
	auditBatchSize  = 500
	auditLogPrefix  = "sylve-smb-al|"
	auditLogPrefixN = 14
)

func (s *Service) ParseAuditLogs() error {
	s.auditFileMu.Lock()
	defer s.auditFileMu.Unlock()

	f, err := os.Open(auditLogPath)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat audit log: %w", err)
	}

	if info.Size() < s.auditFileOffset {
		s.auditFileOffset = 0
	}

	if s.auditFileOffset > 0 {
		if _, err := f.Seek(s.auditFileOffset, 0); err != nil {
			s.auditFileOffset = 0
			f.Seek(0, 0)
		}
	}

	newData, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read audit log: %w", err)
	}

	pos, err := f.Seek(0, 1)
	if err == nil {
		s.auditFileOffset = pos
	}

	if len(newData) == 0 {
		return nil
	}

	s.pruneStaleMkdirs()

	batch := make([]sambaModels.SambaAuditLog, 0, auditBatchSize)
	offset := 0

	for offset < len(newData) {
		nl := bytes.IndexByte(newData[offset:], '\n')
		if nl < 0 {
			break
		}
		line := newData[offset : offset+nl]
		offset += nl + 1

		entry, ok := s.parseAuditLine(line)
		if !ok {
			continue
		}

		if entry.Action == "create_file" {
			if t, exists := s.recentMkdirs[entry.Path]; exists && time.Since(t) < 5*time.Second {
				continue
			}
		}

		batch = append(batch, entry)

		if len(batch) >= auditBatchSize {
			s.auditInsertCh <- batch
			batch = make([]sambaModels.SambaAuditLog, 0, auditBatchSize)
		}
	}

	if len(batch) > 0 {
		s.auditInsertCh <- batch
	}

	return nil
}

func (s *Service) parseAuditLine(line []byte) (sambaModels.SambaAuditLog, bool) {
	idx := bytes.Index(line, []byte(": "))
	if idx < 0 {
		return sambaModels.SambaAuditLog{}, false
	}

	payload := line[idx+2:]
	if !bytes.HasPrefix(payload, []byte(auditLogPrefix)) {
		return sambaModels.SambaAuditLog{}, false
	}

	rest := payload[auditLogPrefixN:]

	userEnd := bytes.IndexByte(rest, '|')
	if userEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	entry := sambaModels.SambaAuditLog{User: string(rest[:userEnd])}
	rest = rest[userEnd+1:]

	ipEnd := bytes.IndexByte(rest, '|')
	if ipEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	entry.IP = string(rest[:ipEnd])
	rest = rest[ipEnd+1:]

	machEnd := bytes.IndexByte(rest, '|')
	if machEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	rest = rest[machEnd+1:]

	shareEnd := bytes.IndexByte(rest, '|')
	if shareEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	entry.Share = string(rest[:shareEnd])
	rest = rest[shareEnd+1:]

	pidEnd := bytes.IndexByte(rest, '|')
	if pidEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	rest = rest[pidEnd+1:]

	actionEnd := bytes.IndexByte(rest, '|')
	if actionEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	action := string(rest[:actionEnd])

	switch action {
	case "connect", "disconnect", "mkdirat", "unlinkat", "renameat", "create_file":
	default:
		return sambaModels.SambaAuditLog{}, false
	}
	entry.Action = action
	rest = rest[actionEnd+1:]

	resultEnd := bytes.IndexByte(rest, '|')
	if resultEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	entry.Result = string(rest[:resultEnd])
	rest = rest[resultEnd+1:]

	switch action {
	case "mkdirat":
		lastPipe := bytes.LastIndexByte(rest, '|')
		if lastPipe >= 0 {
			entry.Path = string(rest[lastPipe+1:])
		} else {
			entry.Path = string(rest)
		}
		entry.Folder = filepath.Base(entry.Path)
		s.recentMkdirs[entry.Path] = time.Now()

	case "unlinkat":
		lastPipe := bytes.LastIndexByte(rest, '|')
		if lastPipe >= 0 {
			entry.Path = string(rest[lastPipe+1:])
		} else {
			entry.Path = string(rest)
		}
		entry.Folder = filepath.Base(entry.Path)

	case "renameat":
		firstPipe := bytes.IndexByte(rest, '|')
		if firstPipe >= 0 {
			entry.Path = string(rest[:firstPipe])
			rest = rest[firstPipe+1:]
			secondPipe := bytes.IndexByte(rest, '|')
			if secondPipe >= 0 {
				entry.Target = string(rest[:secondPipe])
			}
			entry.Folder = filepath.Base(entry.Path)
		}

	case "create_file":
		createMarker := []byte("|create|")
		markerIdx := bytes.Index(rest, createMarker)
		if markerIdx >= 0 {
			pathBytes := rest[markerIdx+len(createMarker):]
			entry.Path = string(pathBytes)
			entry.Folder = filepath.Base(entry.Path)
		}
	}

	return entry, true
}

func (s *Service) pruneStaleMkdirs() {
	now := time.Now()
	for path, t := range s.recentMkdirs {
		if now.Sub(t) > 5*time.Second {
			delete(s.recentMkdirs, path)
		}
	}
}

func (s *Service) GetAuditLogs(
	page int,
	size int,
	sortField, sortDir string,
) (*sambaServiceInterfaces.AuditLogsResponse, error) {
	auditDB := s.auditDB()

	if size <= 0 {
		size = 100
	}
	if page <= 0 {
		page = 1
	}

	var total int64
	if err := auditDB.
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
	if err := auditDB.
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
	if err := os.MkdirAll(auditLogDir, 0755); err != nil {
		logger.L.Error().Msgf("Failed to create audit log directory: %v", err)
		return
	}

	if _, err := os.Stat(auditLogPath); os.IsNotExist(err) {
		if err := os.WriteFile(auditLogPath, []byte(""), 0600); err != nil {
			logger.L.Error().Msgf("Failed to initialize audit log file: %v", err)
			return
		}
	}

	go s.auditBatchWriter(ctx)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	logger.L.Info().Msg("Started watching Samba audit logs")

	for {
		select {
		case <-ctx.Done():
			logger.L.Debug().Msg("Stopped watching Samba audit logs")
			return
		case <-ticker.C:
			if err := s.ParseAuditLogs(); err != nil {
				logger.L.Error().Err(err).Msg("Failed to parse Samba audit logs")
			}
		}
	}
}
