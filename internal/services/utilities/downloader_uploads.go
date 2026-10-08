// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DownloaderUploadTTL             = 24 * time.Hour
	downloaderUploadCleanupInterval = 15 * time.Minute
	downloaderUploadPartialPrefix   = ".sylve-upload-"
	downloaderUploadPartialSuffix   = ".partial"
	downloaderUploadFinalSuffix     = ".upload"
)

var (
	ErrDownloaderUploadNotFound          = errors.New("downloader_upload_not_found")
	ErrDownloaderUploadExpired           = errors.New("downloader_upload_expired")
	ErrDownloaderUploadInvalid           = errors.New("downloader_upload_invalid")
	ErrDownloaderUploadFileUnavailable   = errors.New("downloader_upload_file_unavailable")
	ErrDownloaderUploadDestinationExists = errors.New("downloader_upload_destination_exists")
	ErrDownloaderUploadPersistence       = errors.New("downloader_upload_persistence_failed")
	ErrDownloaderUploadQueue             = errors.New("downloader_upload_queue_failed")
	ErrDownloaderUploadActive            = errors.New("downloader_upload_active")
)

func DownloaderUploadPartialName(uploadID string) string {
	return downloaderUploadPartialPrefix + uploadID + downloaderUploadPartialSuffix
}

func DownloaderUploadFinalName(uploadID string) string {
	return uploadID + downloaderUploadFinalSuffix
}

func (s *Service) uploadNow() time.Time {
	if s.uploadNowFn != nil {
		return s.uploadNowFn()
	}
	return time.Now()
}

func (s *Service) uploadNode() (string, error) {
	hostnameFn := s.uploadHostnameFn
	if hostnameFn == nil {
		hostnameFn = utils.GetSystemHostname
	}
	node, err := hostnameFn()
	if err != nil {
		return "", fmt.Errorf("resolve upload node: %w", err)
	}
	node = strings.TrimSpace(node)
	if node == "" {
		return "", errors.New("resolve upload node: empty hostname")
	}
	return node, nil
}

func (s *Service) uploadStagingDir() string {
	if s.uploadStagingDirFn != nil {
		return s.uploadStagingDirFn()
	}
	return config.GetDownloadsPath("uploads")
}

func (s *Service) PrepareDownloaderUpload(ctx context.Context, pool string) (utilitiesModels.DownloadStorage, string, func(), error) {
	release := s.DownloadStorage.ReadLock(pool)
	ref, layout, err := s.DownloadStorage.Reserve(ctx, pool)
	if err != nil {
		release()
		return ref, "", nil, err
	}
	directory := layout.Dir("uploads")
	if pool == "" && s.uploadStagingDirFn != nil {
		directory = s.uploadStagingDir()
	}
	return ref, directory, release, nil
}

func (s *Service) validateDownloaderUpload(record utilitiesModels.Upload) (downloadstorage.Layout, error) {
	layout, err := s.DownloadStorage.Resolve(context.Background(), record.DownloadStorage, true)
	if err != nil {
		return layout, err
	}
	directory := layout.Dir("uploads")
	if record.StoragePool == "" && record.StorageRoot == "" {
		directory = s.uploadStagingDir()
	}
	if record.StoragePool != "" {
		if err := layout.ValidatePath(directory); err != nil {
			return layout, err
		}
	}
	directory, err = canonicalUploadDirectory(directory)
	if err != nil {
		return layout, fmt.Errorf("%w: upload staging unavailable", downloadstorage.ErrUnavailable)
	}
	if filepath.Clean(record.Path) != filepath.Join(directory, DownloaderUploadFinalName(record.ID)) {
		return layout, downloadstorage.ErrMismatch
	}
	if record.StoragePool != "" {
		if err := layout.ValidatePath(record.Path); err != nil {
			return layout, err
		}
	}
	return layout, nil
}

func (s *Service) ValidateDownloaderUploadPath(ctx context.Context, ref utilitiesModels.DownloadStorage, path string) error {
	layout, err := s.DownloadStorage.Resolve(ctx, ref, true)
	if err != nil {
		return err
	}
	if ref.StoragePool != "" {
		return layout.ValidatePath(path)
	}
	return nil
}

func canonicalUploadDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("upload staging path is not a directory")
	}
	return resolved, nil
}

func (s *Service) BeginDownloaderUpload(uploadID string) func() {
	s.uploadActiveMu.Lock()
	if s.activeUploads == nil {
		s.activeUploads = make(map[string]struct{})
	}
	s.activeUploads[uploadID] = struct{}{}
	s.uploadActiveMu.Unlock()

	return func() {
		s.uploadActiveMu.Lock()
		delete(s.activeUploads, uploadID)
		s.uploadActiveMu.Unlock()
	}
}

func (s *Service) isDownloaderUploadActive(uploadID string) bool {
	s.uploadActiveMu.Lock()
	_, active := s.activeUploads[uploadID]
	s.uploadActiveMu.Unlock()
	return active
}

func (s *Service) RegisterDownloaderUpload(
	ctx context.Context,
	uploadID string,
	path string,
	name string,
	size int64,
	userID uint,
	info os.FileInfo,
	storage utilitiesModels.DownloadStorage,
) (utilitiesModels.Upload, error) {
	if s == nil || s.DB == nil || userID == 0 || size < 0 {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: missing upload metadata", ErrDownloaderUploadInvalid)
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(uploadID))
	if err != nil || parsedID.String() != uploadID {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: invalid upload ID", ErrDownloaderUploadInvalid)
	}
	name = strings.TrimSpace(name)
	if err := utils.IsValidFilename(name); err != nil {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: %v", ErrDownloaderUploadInvalid, err)
	}

	layout, err := s.DownloadStorage.Resolve(ctx, storage, true)
	if err != nil {
		return utilitiesModels.Upload{}, err
	}
	stagingPath := layout.Dir("uploads")
	if storage.StorageRoot == "" && storage.StoragePool == "" {
		stagingPath = s.uploadStagingDir()
	}
	if storage.StoragePool != "" {
		if err := layout.ValidatePath(path); err != nil {
			return utilitiesModels.Upload{}, err
		}
	}
	stagingDir, err := canonicalUploadDirectory(stagingPath)
	if err != nil {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: resolve staging directory: %v", ErrDownloaderUploadPersistence, err)
	}
	canonicalPath := filepath.Join(stagingDir, DownloaderUploadFinalName(uploadID))
	if filepath.Clean(path) != canonicalPath {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: upload path is outside server staging", ErrDownloaderUploadInvalid)
	}

	currentInfo, err := os.Lstat(canonicalPath)
	if err != nil {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: %v", ErrDownloaderUploadFileUnavailable, err)
	}
	identity, err := uploadCore.IdentityFromFileInfo(currentInfo)
	if err != nil || !uploadCore.MatchesFileInfo(identity, info) || currentInfo.Size() != size {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: staged file identity changed", ErrDownloaderUploadFileUnavailable)
	}
	node, err := s.uploadNode()
	if err != nil {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: %v", ErrDownloaderUploadPersistence, err)
	}

	record := utilitiesModels.Upload{
		DownloadStorage: storage,
		ID:              uploadID,
		Scope:           utilitiesModels.UploadScopeDownloader,
		Path:            canonicalPath,
		Name:            name,
		Size:            size,
		UserID:          userID,
		Node:            node,
		Status:          utilitiesModels.UploadStatusStaged,
		Device:          identity.Device,
		Inode:           identity.Inode,
		CreatedAt:       s.uploadNow(),
	}
	if _, err := s.validateDownloaderUpload(record); err != nil {
		return utilitiesModels.Upload{}, err
	}

	s.uploadLifecycleMu.Lock()
	defer s.uploadLifecycleMu.Unlock()
	if err := s.DB.WithContext(ctx).Create(&record).Error; err != nil {
		return utilitiesModels.Upload{}, fmt.Errorf("%w: create upload identity: %v", ErrDownloaderUploadPersistence, err)
	}
	return record, nil
}

func validDownloaderUploadType(value utilitiesModels.DownloadUType) bool {
	switch value {
	case utilitiesModels.DownloadUTypeBase,
		utilitiesModels.DownloadUTypeCloudInit,
		utilitiesModels.DownloadUTypeOther:
		return true
	default:
		return false
	}
}

func (s *Service) CompleteDownloaderUpload(
	ctx context.Context,
	uploadID string,
	userID uint,
	req utilitiesServiceInterfaces.CompleteDownloaderUploadRequest,
) (utilitiesServiceInterfaces.DownloaderUploadCompletion, error) {
	result := utilitiesServiceInterfaces.DownloaderUploadCompletion{
		UploadID: uploadID,
		Status:   utilitiesModels.UploadStatusCompleted,
	}
	if s == nil || s.DB == nil || strings.TrimSpace(uploadID) == "" || userID == 0 {
		return result, ErrDownloaderUploadNotFound
	}
	if !validDownloaderUploadType(req.DownloadType) {
		return result, fmt.Errorf("%w: unsupported download type", ErrDownloaderUploadInvalid)
	}
	node, err := s.uploadNode()
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrDownloaderUploadPersistence, err)
	}

	s.uploadLifecycleMu.Lock()
	defer s.uploadLifecycleMu.Unlock()

	var record utilitiesModels.Upload
	err = s.DB.WithContext(ctx).Where(
		"id = ? AND scope = ? AND user_id = ? AND node = ?",
		uploadID,
		utilitiesModels.UploadScopeDownloader,
		userID,
		node,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, ErrDownloaderUploadNotFound
	}
	if err != nil {
		return result, fmt.Errorf("%w: lookup upload: %v", ErrDownloaderUploadPersistence, err)
	}
	defer s.DownloadStorage.ReadLock(record.StoragePool)()
	layout, err := s.validateDownloaderUpload(record)
	if err != nil {
		return result, err
	}
	if record.Status == utilitiesModels.UploadStatusStaged &&
		!record.CreatedAt.IsZero() &&
		!record.CreatedAt.Add(DownloaderUploadTTL).After(s.uploadNow()) {
		return result, ErrDownloaderUploadExpired
	}
	if record.Status == utilitiesModels.UploadStatusStaged {
		if err := ValidateDownloaderPostProcessOptions(
			record.Name,
			req.AutomaticExtraction,
			req.AutomaticRawConversion,
		); err != nil {
			return result, err
		}
		info, statErr := os.Lstat(record.Path)
		if statErr != nil || !uploadCore.MatchesFileInfo(uploadCore.FileIdentity{
			Device: record.Device,
			Inode:  record.Inode,
		}, info) || info.Size() != record.Size {
			return result, fmt.Errorf("%w: staged file is missing or changed", ErrDownloaderUploadFileUnavailable)
		}
	}

	var download utilitiesModels.Downloads
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current utilitiesModels.Upload
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND scope = ? AND user_id = ? AND node = ?",
			uploadID,
			utilitiesModels.UploadScopeDownloader,
			userID,
			node,
		).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDownloaderUploadNotFound
			}
			return fmt.Errorf("%w: reload upload: %v", ErrDownloaderUploadPersistence, err)
		}

		findErr := tx.Where("url = ?", current.Path).First(&download).Error
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: find downloader entry: %v", ErrDownloaderUploadPersistence, findErr)
		}

		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			if current.Status == utilitiesModels.UploadStatusCompleted {
				return fmt.Errorf("%w: completed upload has no downloader entry", ErrDownloaderUploadPersistence)
			}

			destinationPath := filepath.Join(layout.Dir("path"), current.Name)
			if err := layout.ValidatePath(destinationPath); err != nil {
				return err
			}
			if _, err := os.Lstat(destinationPath); err == nil {
				return ErrDownloaderUploadDestinationExists
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("%w: inspect downloader destination: %v", ErrDownloaderUploadPersistence, err)
			}
			var destinationCount int64
			if err := tx.Model(&utilitiesModels.Downloads{}).
				Where("path = ?", destinationPath).
				Count(&destinationCount).Error; err != nil {
				return fmt.Errorf("%w: check downloader destination: %v", ErrDownloaderUploadPersistence, err)
			}
			if destinationCount != 0 {
				return ErrDownloaderUploadDestinationExists
			}

			download = utilitiesModels.Downloads{
				DownloadStorage:        current.DownloadStorage,
				URL:                    current.Path,
				UUID:                   utils.GenerateDeterministicUUID(current.Path),
				Path:                   destinationPath,
				Type:                   utilitiesModels.DownloadTypePath,
				Name:                   current.Name,
				Size:                   current.Size,
				Progress:               0,
				Files:                  []utilitiesModels.DownloadedFile{},
				Status:                 utilitiesModels.DownloadStatusPending,
				AutomaticExtraction:    req.AutomaticExtraction,
				AutomaticRawConversion: req.AutomaticRawConversion,
				UType:                  req.DownloadType,
				IgnoreTLS:              false,
			}
			download.StorageRoot = layout.Root
			if err := tx.Create(&download).Error; err != nil {
				return fmt.Errorf("%w: create downloader entry: %v", ErrDownloaderUploadPersistence, err)
			}
		}

		if current.Status == utilitiesModels.UploadStatusStaged {
			completedAt := s.uploadNow()
			update := tx.Model(&utilitiesModels.Upload{}).
				Where("id = ? AND status = ?", current.ID, utilitiesModels.UploadStatusStaged).
				Updates(map[string]any{
					"status":       utilitiesModels.UploadStatusCompleted,
					"completed_at": &completedAt,
				})
			if update.Error != nil {
				return fmt.Errorf("%w: complete upload identity: %v", ErrDownloaderUploadPersistence, update.Error)
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf("%w: upload state changed", ErrDownloaderUploadPersistence)
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	result.DownloadID = download.ID
	if download.Status == utilitiesModels.DownloadStatusPending {
		queueCtx := context.WithoutCancel(ctx)
		if err := s.enqueueDownloadStartOnce(queueCtx, utilitiesServiceInterfaces.DownloadStartPayload{
			ID: download.ID,
		}); err != nil {
			return result, fmt.Errorf("%w: %v", ErrDownloaderUploadQueue, err)
		}
	}
	return result, nil
}

func (s *Service) removeCompletedDownloaderUploadSource(sourcePath string) error {
	if s == nil || s.DB == nil {
		return nil
	}

	s.uploadLifecycleMu.Lock()
	defer s.uploadLifecycleMu.Unlock()

	var record utilitiesModels.Upload
	err := s.DB.Where(
		"path = ? AND scope = ? AND status = ?",
		sourcePath,
		utilitiesModels.UploadScopeDownloader,
		utilitiesModels.UploadStatusCompleted,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup completed upload source: %w", err)
	}
	if _, err := s.validateDownloaderUpload(record); err != nil {
		return err
	}

	_, err = uploadCore.RemoveIfSame(record.Path, uploadCore.FileIdentity{
		Device: record.Device,
		Inode:  record.Inode,
	})
	if err != nil {
		return fmt.Errorf("remove completed upload source: %w", err)
	}
	return nil
}

// publishCompletedDownloaderUpload promotes a staged downloader upload into
// its reserved destination without copying it when both paths are on the same
// filesystem. It returns false when sourcePath is an ordinary path download
// rather than a completed downloader upload.
func (s *Service) publishCompletedDownloaderUpload(sourcePath, destinationPath string) (bool, error) {
	if s == nil || s.DB == nil {
		return false, nil
	}

	var record utilitiesModels.Upload
	err := s.DB.Where(
		"path = ? AND scope = ? AND status = ?",
		sourcePath,
		utilitiesModels.UploadScopeDownloader,
		utilitiesModels.UploadStatusCompleted,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("lookup completed upload source: %w", err)
	}
	layout, err := s.validateDownloaderUpload(record)
	if err != nil {
		return true, err
	}
	if filepath.Clean(destinationPath) != filepath.Join(layout.Dir("path"), record.Name) {
		return true, downloadstorage.ErrMismatch
	}
	if err := layout.ValidatePath(destinationPath); err != nil {
		return true, err
	}

	identity := uploadCore.FileIdentity{
		Device: record.Device,
		Inode:  record.Inode,
	}

	// A worker may have been interrupted after publishing but before updating
	// the download row. A same-inode destination proves that publication
	// completed and makes the retry idempotent.
	if destinationInfo, statErr := os.Lstat(destinationPath); statErr == nil {
		if destinationInfo.Size() != record.Size || !uploadCore.MatchesFileInfo(identity, destinationInfo) {
			return true, ErrDownloaderUploadDestinationExists
		}
		if _, removeErr := uploadCore.RemoveIfSame(sourcePath, identity); removeErr != nil {
			return true, fmt.Errorf("remove already-published upload source: %w", removeErr)
		}
		return true, nil
	} else if !os.IsNotExist(statErr) {
		return true, fmt.Errorf("inspect downloader destination: %w", statErr)
	}

	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return true, fmt.Errorf("inspect completed upload source: %w", err)
	}
	if sourceInfo.Size() != record.Size || !uploadCore.MatchesFileInfo(identity, sourceInfo) {
		return true, ErrDownloaderUploadFileUnavailable
	}

	if err := uploadCore.PublishNoReplace(sourcePath, destinationPath); err == nil {
		return true, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		// PublishNoReplace can be interrupted between linking and unlinking.
		// Reinspect before reporting failure so that state is recoverable.
		if destinationInfo, statErr := os.Lstat(destinationPath); statErr == nil &&
			destinationInfo.Size() == record.Size &&
			uploadCore.MatchesFileInfo(identity, destinationInfo) {
			if _, removeErr := uploadCore.RemoveIfSame(sourcePath, identity); removeErr != nil {
				return true, fmt.Errorf("remove published upload source: %w", removeErr)
			}
			return true, nil
		}
		return true, fmt.Errorf("publish completed upload: %w", err)
	}

	// A separately mounted uploads directory cannot use hard links. Preserve
	// compatibility with that layout, but publish via a private temporary file
	// so an interrupted copy never exposes a partial final destination.
	if err := copyFileNoReplace(sourcePath, destinationPath, func() error {
		if err := s.DownloadStorage.Recheck(context.Background(), layout, true); err != nil {
			return err
		}
		return layout.ValidatePath(destinationPath)
	}); err != nil {
		return true, fmt.Errorf("copy completed upload across filesystems: %w", err)
	}
	if _, err := uploadCore.RemoveIfSame(sourcePath, identity); err != nil {
		logger.L.Warn().Err(err).Msg("failed to remove cross-filesystem upload source")
	}
	return true, nil
}

func copyFileNoReplace(sourcePath, destinationPath string, beforePublish func() error) error {
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer source.Close()

	destination, partialPath, err := uploadCore.CreateRandomPartial(filepath.Dir(destinationPath))
	if err != nil {
		return fmt.Errorf("create destination partial: %w", err)
	}
	published := false
	defer func() {
		_ = destination.Close()
		if !published && (beforePublish == nil || beforePublish() == nil) {
			_ = os.Remove(partialPath)
		}
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy source: %w", err)
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("sync destination partial: %w", err)
	}
	if err := destination.Close(); err != nil {
		return fmt.Errorf("close destination partial: %w", err)
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if err := uploadCore.PublishNoReplace(partialPath, destinationPath); err != nil {
		return fmt.Errorf("publish destination: %w", err)
	}
	published = true
	return nil
}

// AbortDownloaderUpload is intentionally indistinguishable for missing and
// foreign identities. A completed identity is retained and its downloader
// source is never removed.
func (s *Service) AbortDownloaderUpload(
	ctx context.Context,
	uploadID string,
	userID uint,
) (bool, error) {
	if s == nil || s.DB == nil || strings.TrimSpace(uploadID) == "" || userID == 0 {
		return false, nil
	}
	node, err := s.uploadNode()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrDownloaderUploadPersistence, err)
	}

	s.uploadLifecycleMu.Lock()
	defer s.uploadLifecycleMu.Unlock()

	var record utilitiesModels.Upload
	err = s.DB.WithContext(ctx).Where(
		"id = ? AND scope = ? AND user_id = ? AND node = ?",
		uploadID,
		utilitiesModels.UploadScopeDownloader,
		userID,
		node,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: lookup upload: %v", ErrDownloaderUploadPersistence, err)
	}
	defer s.DownloadStorage.ReadLock(record.StoragePool)()
	if record.Status == utilitiesModels.UploadStatusCompleted {
		return true, nil
	}
	if s.isDownloaderUploadActive(uploadID) {
		return false, ErrDownloaderUploadActive
	}
	if _, err := s.validateDownloaderUpload(record); err != nil {
		return false, err
	}

	_, err = uploadCore.RemoveIfSame(record.Path, uploadCore.FileIdentity{
		Device: record.Device,
		Inode:  record.Inode,
	})
	if err != nil {
		return false, fmt.Errorf("%w: remove staged file: %v", ErrDownloaderUploadFileUnavailable, err)
	}
	if err := s.DB.WithContext(ctx).Delete(&record).Error; err != nil {
		return false, fmt.Errorf("%w: delete upload identity: %v", ErrDownloaderUploadPersistence, err)
	}
	return false, nil
}

func (s *Service) CleanupExpiredUploads(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	node, err := s.uploadNode()
	if err != nil {
		return err
	}
	now := s.uploadNow()
	cutoff := now.Add(-DownloaderUploadTTL)

	s.uploadLifecycleMu.Lock()
	defer s.uploadLifecycleMu.Unlock()

	var records []utilitiesModels.Upload
	if err := s.DB.WithContext(ctx).
		Where("node = ? AND status = ? AND created_at <= ?", node, utilitiesModels.UploadStatusStaged, cutoff).
		Find(&records).Error; err != nil {
		return fmt.Errorf("list expired uploads: %w", err)
	}

	var cleanupErr error
	for i := range records {
		record := &records[i]
		releaseStorage := s.DownloadStorage.ReadLock(record.StoragePool)
		if record.Scope == utilitiesModels.UploadScopeDownloader {
			if s.isDownloaderUploadActive(record.ID) {
				releaseStorage()
				continue
			}
			if _, err := s.validateDownloaderUpload(*record); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				releaseStorage()
				continue
			}
			_, removeErr := uploadCore.RemoveIfSame(record.Path, uploadCore.FileIdentity{
				Device: record.Device,
				Inode:  record.Inode,
			})
			if removeErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove expired upload %s: %w", record.ID, removeErr))
				releaseStorage()
				continue
			}
		}
		if err := s.DB.WithContext(ctx).Delete(record).Error; err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete expired upload %s: %w", record.ID, err))
		}
		releaseStorage()
	}

	layouts, err := s.DownloadStorage.CleanupLayouts(ctx)
	cleanupErr = errors.Join(cleanupErr, err)
	for _, layout := range layouts {
		directory := layout.Dir("uploads")
		if layout.Pool() == "" {
			directory = s.uploadStagingDir()
		}
		if err := s.cleanupDownloaderUploadDirectory(ctx, node, cutoff, directory, layout); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (s *Service) cleanupDownloaderUploadDirectory(
	ctx context.Context,
	node string,
	cutoff time.Time,
	stagingDirectory string,
	layout downloadstorage.Layout,
) error {
	defer s.DownloadStorage.ReadLock(layout.Pool())()
	if err := s.DownloadStorage.Recheck(ctx, layout, true); err != nil {
		return err
	}
	if layout.Pool() != "" {
		if err := layout.ValidatePath(stagingDirectory); err != nil {
			return err
		}
	}
	directory, err := canonicalUploadDirectory(stagingDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve upload cleanup directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read upload cleanup directory: %w", err)
	}

	var cleanupErr error
	for _, entry := range entries {
		uploadID, partial, recognized := parseDownloaderUploadStagingName(entry.Name())
		if !recognized || s.isDownloaderUploadActive(uploadID) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stat upload staging entry %s: %w", entry.Name(), err))
			continue
		}
		if !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}

		if !partial {
			var count int64
			if err := s.DB.WithContext(ctx).Model(&utilitiesModels.Upload{}).
				Where("id = ? AND scope = ? AND node = ?", uploadID, utilitiesModels.UploadScopeDownloader, node).
				Count(&count).Error; err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("lookup orphan upload %s: %w", uploadID, err))
				continue
			}
			if count != 0 {
				continue
			}
		}

		path := filepath.Join(directory, entry.Name())
		if err := s.DownloadStorage.Recheck(ctx, layout, true); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if layout.Pool() != "" {
			if err := layout.ValidatePath(path); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
				continue
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove upload staging entry %s: %w", entry.Name(), err))
		}
	}
	return cleanupErr
}

func parseDownloaderUploadStagingName(name string) (string, bool, bool) {
	partial := strings.HasPrefix(name, downloaderUploadPartialPrefix) &&
		strings.HasSuffix(name, downloaderUploadPartialSuffix)
	final := strings.HasSuffix(name, downloaderUploadFinalSuffix) && !strings.HasPrefix(name, ".")
	if !partial && !final {
		return "", false, false
	}

	var rawID string
	if partial {
		rawID = strings.TrimSuffix(strings.TrimPrefix(name, downloaderUploadPartialPrefix), downloaderUploadPartialSuffix)
	} else {
		rawID = strings.TrimSuffix(name, downloaderUploadFinalSuffix)
	}
	parsed, err := uuid.Parse(rawID)
	if err != nil || parsed.String() != rawID {
		return "", false, false
	}
	return rawID, partial, true
}

func (s *Service) StartUploadCleanupWorker(ctx context.Context) {
	run := func() {
		if err := s.CleanupExpiredUploads(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.L.Warn().Err(err).Msg("downloader upload cleanup failed")
		}
	}
	run()

	ticker := time.NewTicker(downloaderUploadCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
