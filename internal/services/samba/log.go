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
	"syscall"
	"time"

	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	sambaServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/samba"
	"github.com/alchemillahq/sylve/internal/logger"
	"gorm.io/gorm"
)

var (
	auditLogPath        = "/var/log/samba4/audit.log"
	auditReadChunkBytes = 8 << 20
)

const (
	auditBatchSize            = 500
	auditLogPrefix            = "sylve-smb-al|"
	auditDefaultRetentionDays = sambaModels.DefaultAuditRetentionDays
	auditPruneBatchSize       = 5000
	maxAuditReadPasses        = 64
	MaxAuditLogPageSize       = 500
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
			if err := s.drainOpenAuditFile(); err != nil {
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
		file, err := os.Open(auditLogPath)
		if err != nil {
			return fmt.Errorf("failed to open audit log: %w", err)
		}
		offset := int64(0)
		if !s.auditCursorLoaded {
			info, err := file.Stat()
			if err == nil {
				offset, err = s.loadAuditCursor(info)
			}
			if err != nil {
				file.Close()
				return fmt.Errorf("failed to load audit log position: %w", err)
			}
			s.auditCursorLoaded = true
		}
		s.auditFile = file
		s.auditFileOffset = offset
	}

	return s.drainOpenAuditFile()
}

func (s *Service) drainOpenAuditFile() error {
	for range maxAuditReadPasses {
		more, err := s.parseOpenAuditFile()
		if err != nil || !more {
			return err
		}
	}
	return nil
}

func auditFileIdentity(info os.FileInfo) (int64, int64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(stat.Dev), int64(stat.Ino)
	}
	return 0, 0
}

func (s *Service) loadAuditCursor(info os.FileInfo) (int64, error) {
	db := s.auditDB()
	var cursor sambaModels.SambaAuditCursor
	if err := db.Limit(1).Find(&cursor).Error; err != nil {
		return 0, err
	}
	if cursor.ID != 0 {
		device, inode := auditFileIdentity(info)
		if cursor.Device == device && cursor.Inode == inode && cursor.Offset <= info.Size() {
			return cursor.Offset, nil
		}
		return 0, nil
	}
	var imported int64
	if err := db.Model(&sambaModels.SambaAuditLog{}).Limit(1).Count(&imported).Error; err != nil {
		return 0, err
	}
	if imported > 0 {
		return info.Size(), nil
	}
	return 0, nil
}

func (s *Service) parseOpenAuditFile() (bool, error) {
	info, err := s.auditFile.Stat()
	if err != nil {
		return false, fmt.Errorf("failed to stat audit log: %w", err)
	}
	if info.Size() < s.auditFileOffset {
		s.auditFileOffset = 0
	}

	if _, err := s.auditFile.Seek(s.auditFileOffset, io.SeekStart); err != nil {
		s.auditFileOffset = 0
		if _, seekErr := s.auditFile.Seek(0, io.SeekStart); seekErr != nil {
			return false, fmt.Errorf("failed to rewind audit log: %w", seekErr)
		}
	}

	startOffset, err := s.auditFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, fmt.Errorf("failed to determine audit log offset: %w", err)
	}

	newData, err := io.ReadAll(io.LimitReader(s.auditFile, int64(auditReadChunkBytes)))
	if err != nil {
		return false, fmt.Errorf("failed to read audit log: %w", err)
	}
	full := len(newData) == auditReadChunkBytes
	device, inode := auditFileIdentity(info)

	if len(newData) == 0 {
		return false, nil
	}
	completeEnd := bytes.LastIndexByte(newData, '\n') + 1
	if completeEnd == 0 {
		if !full {
			return false, nil
		}
		completeEnd = len(newData)
		if err := s.insertAuditEntries(nil, 0, 0, auditCursor(device, inode, startOffset+int64(completeEnd))); err != nil {
			_, _ = s.auditFile.Seek(startOffset, io.SeekStart)
			return false, err
		}
		s.auditFileOffset = startOffset + int64(completeEnd)
		return true, nil
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

	cursor := auditCursor(device, inode, startOffset+int64(completeEnd))
	if err := s.insertAuditEntries(entries, repeatedEntryID, previousRepeats, cursor); err != nil {
		_, _ = s.auditFile.Seek(startOffset, io.SeekStart)
		return false, err
	}
	if lastEntry >= 0 {
		s.lastAuditLogID = entries[lastEntry].ID
	} else {
		s.lastAuditLogID = previousEntryID
	}

	s.auditFileOffset = cursor.Offset
	return full, nil
}

func auditCursor(device, inode, offset int64) sambaModels.SambaAuditCursor {
	return sambaModels.SambaAuditCursor{ID: 1, Device: device, Inode: inode, Offset: offset}
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

func (s *Service) insertAuditEntries(
	entries []sambaModels.SambaAuditLog,
	repeatedEntryID int,
	repeated uint32,
	cursor sambaModels.SambaAuditCursor,
) error {
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
			if len(entries) > 0 {
				if err := tx.CreateInBatches(&entries, auditBatchSize).Error; err != nil {
					return err
				}
			}
			return tx.Save(&cursor).Error
		})
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return fmt.Errorf("failed to insert audit log batch after retries: %w", err)
}

func auditEntryKey(entry sambaModels.SambaAuditLog) string {
	return strings.Join([]string{strconv.FormatInt(entry.CreatedAt.Unix(), 10), strconv.FormatUint(uint64(entry.ShareID), 10), entry.Share, entry.User, entry.Client, entry.IP, entry.Action, entry.Result, entry.Path, entry.Target, entry.ObjectType, entry.Disposition}, "\x00")
}

func parseSyslogTime(line []byte, now time.Time) (time.Time, bool) {
	if len(line) >= 15 && line[0] >= 'A' && line[0] <= 'Z' {
		parsed, err := time.ParseInLocation("Jan _2 15:04:05", string(line[:15]), now.Location())
		if err != nil {
			return time.Time{}, false
		}
		stamp := time.Date(now.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, now.Location())
		if stamp.After(now.Add(24 * time.Hour)) {
			stamp = stamp.AddDate(-1, 0, 0)
		}
		return stamp, true
	}
	if space := bytes.IndexByte(line, ' '); space > 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, string(line[:space])); err == nil {
			return parsed.In(now.Location()), true
		}
	}
	return time.Time{}, false
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

	if stamp, ok := parseSyslogTime(line, time.Now()); ok {
		entry.CreatedAt = stamp
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

var auditSearchColumns = []string{"path", "target", "user", "share", "client", "ip"}

func auditLikePattern(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(value))
	return "%" + escaped + "%"
}

func filterAuditLogs(db *gorm.DB, query sambaServiceInterfaces.AuditLogQuery) *gorm.DB {
	if share := strings.TrimSpace(query.Share); share != "" {
		db = db.Where("share = ?", share)
	}
	if user := strings.TrimSpace(query.User); user != "" {
		db = db.Where(`LOWER(user) LIKE ? ESCAPE '\'`, auditLikePattern(user))
	}
	if len(query.Actions) > 0 {
		db = db.Where("action IN ?", query.Actions)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		pattern := auditLikePattern(search)
		clauses := make([]string, len(auditSearchColumns))
		args := make([]any, len(auditSearchColumns))
		for i, column := range auditSearchColumns {
			clauses[i] = "LOWER(" + column + `) LIKE ? ESCAPE '\'`
			args[i] = pattern
		}
		db = db.Where("("+strings.Join(clauses, " OR ")+")", args...)
	}
	if query.From != nil {
		db = db.Where("created_at >= ?", *query.From)
	}
	if query.To != nil {
		db = db.Where("created_at <= ?", *query.To)
	}
	return db
}

func (s *Service) GetAuditLogs(query sambaServiceInterfaces.AuditLogQuery) (*sambaServiceInterfaces.AuditLogsResponse, error) {
	auditDB := s.auditDB()

	page, size, sortField, sortDir := query.Page, query.Size, query.SortField, query.SortDir
	if size <= 0 {
		size = 100
	}
	size = min(size, MaxAuditLogPageSize)
	if page <= 0 {
		page = 1
	}

	var total int64
	if err := filterAuditLogs(auditDB.Model(&sambaModels.SambaAuditLog{}), query).
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
	if err := filterAuditLogs(auditDB, query).
		Order(orderExpr).
		Offset(offset).
		Limit(size).
		Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch audit logs: %w", err)
	}

	return &sambaServiceInterfaces.AuditLogsResponse{
		LastPage: lastPage,
		Total:    total,
		Data:     logs,
	}, nil
}

func ValidAuditOperation(operation string) bool {
	_, ok := allowedSambaAuditOperations[operation]
	return ok
}

func (s *Service) watchAuditLogs(ctx context.Context) {
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
