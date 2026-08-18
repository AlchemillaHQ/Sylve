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
	"strconv"
	"strings"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var auditLogPath = "/var/log/samba4/audit.log"

const (
	auditBatchSize            = 500
	auditLogPrefix            = "sylve-smb-al|"
	auditDefaultRetentionDays = sambaModels.DefaultAuditRetentionDays
	auditPruneBatchSize       = 5000
)

func (s *Service) ParseAuditLogs() error {
	s.auditFileMu.Lock()
	defer s.auditFileMu.Unlock()

	pathInfo, err := os.Stat(auditLogPath)
	if err != nil {
		return fmt.Errorf("failed to stat audit log: %w", err)
	}

	if s.auditFile != nil {
		openInfo, statErr := s.auditFile.Stat()
		if statErr != nil {
			return fmt.Errorf("failed to stat open audit log: %w", statErr)
		}
		if !os.SameFile(openInfo, pathInfo) {
			if err := s.parseOpenAuditFile(); err != nil {
				return err
			}
			if err := s.auditFile.Close(); err != nil {
				return fmt.Errorf("failed to close rotated audit log: %w", err)
			}
			s.auditFile = nil
			s.auditFileOffset = 0
		}
	}

	if s.auditFile == nil {
		s.auditFile, err = os.Open(auditLogPath)
		if err != nil {
			return fmt.Errorf("failed to open audit log: %w", err)
		}
	}

	return s.parseOpenAuditFile()
}

func (s *Service) parseOpenAuditFile() error {
	info, err := s.auditFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat audit log: %w", err)
	}
	if info.Size() < s.auditFileOffset {
		s.auditFileOffset = 0
	}

	if _, err := s.auditFile.Seek(s.auditFileOffset, io.SeekStart); err != nil {
		s.auditFileOffset = 0
		if _, seekErr := s.auditFile.Seek(0, io.SeekStart); seekErr != nil {
			return fmt.Errorf("failed to rewind audit log: %w", seekErr)
		}
	}

	startOffset, err := s.auditFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("failed to determine audit log offset: %w", err)
	}

	newData, err := io.ReadAll(s.auditFile)
	if err != nil {
		return fmt.Errorf("failed to read audit log: %w", err)
	}

	if len(newData) == 0 {
		return nil
	}
	completeEnd := bytes.LastIndexByte(newData, '\n') + 1
	if completeEnd == 0 {
		return nil
	}
	newData = newData[:completeEnd]

	s.pruneStaleMkdirs()
	shares := s.auditShareMetadata()
	entries := make([]sambaModels.SambaAuditLog, 0, auditBatchSize)
	entryIndex := make(map[string]int)
	lastEntry := -1
	previousEntryID := s.lastAuditLogID
	repeatedEntryID := 0
	previousRepeats := uint32(0)
	clearLastEntry := func() {
		lastEntry = -1
		previousEntryID = 0
	}
	offset := 0

	for offset < len(newData) {
		nl := bytes.IndexByte(newData[offset:], '\n')
		if nl < 0 {
			break
		}
		line := newData[offset : offset+nl]
		offset += nl + 1

		if repeated, ok := parseSyslogRepeat(line); ok {
			if lastEntry >= 0 {
				entries[lastEntry].Occurrences += uint32(repeated)
			} else if previousEntryID > 0 {
				repeatedEntryID = previousEntryID
				previousRepeats += uint32(repeated)
			}
			continue
		}

		entry, ok := s.parseAuditLine(line)
		if !ok {
			clearLastEntry()
			continue
		}
		entry.Occurrences = 1
		entry.RetentionDays = sambaModels.AuditRetentionDaysPointer(auditDefaultRetentionDays)
		if share, exists := shares[entry.Share]; exists {
			entry.ShareID = uint(share.ID)
			entry.RetentionDays = sambaModels.AuditRetentionDaysPointer(
				sambaModels.AuditRetentionDaysValue(share.AuditRetentionDays),
			)
		}

		// Successful open probes are extremely noisy and are already represented by
		// the dedicated open operations. Keep failures and every create disposition.
		if entry.Action == "create_file" && entry.Result == "ok" && entry.Disposition == "open" {
			clearLastEntry()
			continue
		}

		if entry.Action == "create_file" {
			if t, exists := s.recentMkdirs[entry.Path]; exists && time.Since(t) < 5*time.Second {
				clearLastEntry()
				continue
			}
		}

		key := auditEntryKey(entry)
		if index, exists := entryIndex[key]; exists {
			entries[index].Occurrences++
			lastEntry = index
			continue
		}
		entryIndex[key] = len(entries)
		entries = append(entries, entry)
		lastEntry = len(entries) - 1
	}

	if err := s.insertAuditEntries(entries, repeatedEntryID, previousRepeats); err != nil {
		_, _ = s.auditFile.Seek(startOffset, io.SeekStart)
		return err
	}
	if lastEntry >= 0 {
		s.lastAuditLogID = entries[lastEntry].ID
	} else {
		s.lastAuditLogID = previousEntryID
	}

	s.auditFileOffset = startOffset + int64(completeEnd)
	return nil
}

func (s *Service) auditShareMetadata() map[string]sambaModels.SambaShare {
	result := make(map[string]sambaModels.SambaShare)
	if s.DB == nil {
		return result
	}
	var shares []sambaModels.SambaShare
	if err := s.DB.Select("id", "name", "audit_retention_days").Find(&shares).Error; err != nil {
		logger.L.Warn().Err(err).Msg("failed to load Samba audit retention metadata")
		return result
	}
	for _, share := range shares {
		result[share.Name] = share
	}
	return result
}

func (s *Service) insertAuditEntries(entries []sambaModels.SambaAuditLog, repeatedEntryID int, repeated uint32) error {
	if len(entries) == 0 && (repeatedEntryID == 0 || repeated == 0) {
		return nil
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.auditDB().Transaction(func(tx *gorm.DB) error {
			if repeatedEntryID > 0 && repeated > 0 {
				if err := tx.Model(&sambaModels.SambaAuditLog{}).
					Where("id = ?", repeatedEntryID).
					UpdateColumn("occurrences", gorm.Expr("occurrences + ?", repeated)).Error; err != nil {
					return err
				}
			}
			if len(entries) == 0 {
				return nil
			}
			return tx.CreateInBatches(&entries, auditBatchSize).Error
		})
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return fmt.Errorf("failed to insert audit log batch after retries: %w", err)
}

func auditEntryKey(entry sambaModels.SambaAuditLog) string {
	return strings.Join([]string{strconv.FormatUint(uint64(entry.ShareID), 10), entry.Share, entry.User, entry.Client, entry.IP, entry.Action, entry.Result, entry.Path, entry.Target, entry.ObjectType, entry.Disposition}, "\x00")
}

func parseSyslogRepeat(line []byte) (int, bool) {
	const marker = "syslogd: last message repeated "
	index := bytes.Index(line, []byte(marker))
	if index < 0 {
		return 0, false
	}
	value := strings.TrimSuffix(string(line[index+len(marker):]), " times")
	count, err := strconv.Atoi(value)
	return count, err == nil && count > 0
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

	rest := payload[len(auditLogPrefix):]

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
	entry.Client = string(rest[:machEnd])
	rest = rest[machEnd+1:]

	shareEnd := bytes.IndexByte(rest, '|')
	if shareEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	entry.Share = string(rest[:shareEnd])
	rest = rest[shareEnd+1:]

	servicePathEnd := bytes.IndexByte(rest, '|')
	if servicePathEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	servicePath := string(rest[:servicePathEnd])
	rest = rest[servicePathEnd+1:]

	actionEnd := bytes.IndexByte(rest, '|')
	if actionEnd < 0 {
		return sambaModels.SambaAuditLog{}, false
	}
	action := string(rest[:actionEnd])

	switch action {
	case "connect", "disconnect", "mkdirat", "unlinkat", "renameat", "create_file", "openat", "close", "read", "write":
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
	case "connect", "disconnect":
		entry.Path = servicePath
		entry.Folder = filepath.Base(entry.Path)

	case "mkdirat":
		lastPipe := bytes.LastIndexByte(rest, '|')
		if lastPipe >= 0 {
			entry.Path = string(rest[lastPipe+1:])
		} else {
			entry.Path = string(rest)
		}
		entry.Folder = filepath.Base(entry.Path)
		s.recentMkdirs[entry.Path] = time.Now()

	case "unlinkat", "openat", "close", "read", "write":
		lastPipe := bytes.LastIndexByte(rest, '|')
		if lastPipe >= 0 {
			entry.Path = string(rest[lastPipe+1:])
		} else {
			entry.Path = string(rest)
		}
		entry.Folder = filepath.Base(entry.Path)

	case "renameat":
		parts := bytes.Split(rest, []byte{'|'})
		if len(parts) >= 2 {
			entry.Path = string(parts[0])
			entry.Target = string(parts[len(parts)-1])
			entry.Folder = filepath.Base(entry.Path)
		}

	case "create_file":
		parts := bytes.Split(rest, []byte{'|'})
		if len(parts) >= 4 {
			entry.ObjectType = string(parts[len(parts)-3])
			entry.Disposition = string(parts[len(parts)-2])
		}
		entry.Path = string(parts[len(parts)-1])
		entry.Folder = filepath.Base(entry.Path)
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
		"id":          true,
		"action":      true,
		"share":       true,
		"user":        true,
		"client":      true,
		"ip":          true,
		"path":        true,
		"target":      true,
		"occurrences": true,
		"created_at":  true,
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
	auditLogDir := filepath.Dir(auditLogPath)
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
	if err := os.Chmod(auditLogPath, 0600); err != nil {
		logger.L.Error().Msgf("Failed to secure audit log file: %v", err)
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(6 * time.Hour)
	defer pruneTicker.Stop()
	defer func() {
		s.auditFileMu.Lock()
		if s.auditFile != nil {
			_ = s.auditFile.Close()
			s.auditFile = nil
		}
		s.auditFileMu.Unlock()
	}()

	if err := s.PruneAuditLogs(time.Now()); err != nil {
		logger.L.Error().Err(err).Msg("Failed to prune Samba audit logs")
	}

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
		case <-pruneTicker.C:
			if err := s.PruneAuditLogs(time.Now()); err != nil {
				logger.L.Error().Err(err).Msg("Failed to prune Samba audit logs")
			}
		}
	}
}

func (s *Service) PruneAuditLogs(now time.Time) error {
	var retentionValues []uint32
	if err := s.auditDB().Model(&sambaModels.SambaAuditLog{}).
		Where("retention_days > 0").Distinct().Pluck("retention_days", &retentionValues).Error; err != nil {
		return fmt.Errorf("failed to list Samba audit retention periods: %w", err)
	}
	for _, days := range retentionValues {
		cutoff := now.AddDate(0, 0, -int(days))
		for {
			var ids []int
			if err := s.auditDB().Model(&sambaModels.SambaAuditLog{}).
				Where("retention_days = ? AND created_at < ?", days, cutoff).
				Order("id ASC").Limit(auditPruneBatchSize).Pluck("id", &ids).Error; err != nil {
				return fmt.Errorf("failed to find expired Samba audit logs: %w", err)
			}
			if len(ids) == 0 {
				break
			}
			if err := s.auditDB().Where("id IN ?", ids).Delete(&sambaModels.SambaAuditLog{}).Error; err != nil {
				return fmt.Errorf("failed to delete expired Samba audit logs: %w", err)
			}
		}
	}
	return nil
}
