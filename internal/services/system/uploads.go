// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

const (
	fileExplorerUploadIdentityTTL = 24 * time.Hour
	fileExplorerPartialPrefix     = ".sylve-upload-"
	fileExplorerPartialSuffix     = ".partial"
)

var fileExplorerProcessStart = time.Now()

func FileExplorerPartialPath(directory string) string {
	return filepath.Join(directory, fileExplorerPartialPrefix+utils.GenerateRandomUUID()+fileExplorerPartialSuffix)
}

func (s *Service) RegisterFileExplorerPartial(path, owner string) (string, error) {
	if s == nil || s.DB == nil {
		return "", nil
	}
	node, err := utils.GetSystemHostname()
	if err != nil || strings.TrimSpace(node) == "" {
		return "", fmt.Errorf("file_explorer_upload_node_failed: %v", err)
	}
	record := utilitiesModels.Upload{
		ID:        utils.GenerateRandomUUID(),
		Scope:     utilitiesModels.UploadScopeFileExplorer,
		Path:      path,
		Name:      filepath.Base(path),
		Owner:     owner,
		Node:      node,
		Status:    utilitiesModels.UploadStatusReceiving,
		CreatedAt: time.Now(),
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return "", fmt.Errorf("file_explorer_partial_register_failed: %w", err)
	}
	return record.ID, nil
}

func (s *Service) ReleaseFileExplorerPartial(id string) {
	if s == nil || s.DB == nil || id == "" {
		return
	}
	_ = s.DB.Where("id = ? AND status = ?", id, utilitiesModels.UploadStatusReceiving).
		Delete(&utilitiesModels.Upload{}).Error
}

func (s *Service) SweepFileExplorerPartials(ctx context.Context) (int, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	node, err := utils.GetSystemHostname()
	if err != nil {
		return 0, fmt.Errorf("file_explorer_upload_node_failed: %w", err)
	}
	var records []utilitiesModels.Upload
	if err := s.DB.WithContext(ctx).Where(
		"scope = ? AND status = ? AND node = ? AND created_at < ?",
		utilitiesModels.UploadScopeFileExplorer,
		utilitiesModels.UploadStatusReceiving,
		node,
		fileExplorerProcessStart,
	).Find(&records).Error; err != nil {
		return 0, fmt.Errorf("file_explorer_partial_lookup_failed: %w", err)
	}

	removed := 0
	var sweepErr error
	for index := range records {
		record := &records[index]
		name := filepath.Base(record.Path)
		if strings.HasPrefix(name, fileExplorerPartialPrefix) && strings.HasSuffix(name, fileExplorerPartialSuffix) {
			if info, err := os.Lstat(record.Path); err == nil && info.Mode().IsRegular() {
				if err := os.Remove(record.Path); err != nil && !os.IsNotExist(err) {
					sweepErr = errors.Join(sweepErr, err)
					continue
				}
				removed++
			}
		}
		if err := s.DB.WithContext(ctx).Delete(record).Error; err != nil {
			sweepErr = errors.Join(sweepErr, err)
		}
	}
	return removed, sweepErr
}

var (
	ErrFileExplorerUploadNotFound = errors.New("file_explorer_upload_not_found")
	ErrFileExplorerUploadConflict = errors.New("file_explorer_upload_conflict")
)

// RegisterFileExplorerUpload returns an opaque, short-lived identity that can
// be used to revert exactly the file created by a successful upload. The path
// remains server-side and is never accepted back from the client as deletion
// authority.
func (s *Service) RegisterFileExplorerUpload(path, owner string) (string, error) {
	if s == nil || s.DB == nil || strings.TrimSpace(path) == "" || owner == "" {
		return "", fmt.Errorf("invalid_file_explorer_upload")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_stat_failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ErrFileExplorerUploadConflict
	}

	identity, err := uploadCore.IdentityFromFileInfo(info)
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_identity_failed: %w", err)
	}
	node, err := utils.GetSystemHostname()
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_node_failed: %w", err)
	}
	if strings.TrimSpace(node) == "" {
		return "", errors.New("file_explorer_upload_node_failed: empty hostname")
	}
	canonicalPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_path_failed: %w", err)
	}
	canonicalPath, err = filepath.EvalSymlinks(canonicalPath)
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_path_failed: %w", err)
	}

	record := utilitiesModels.Upload{
		ID:        utils.GenerateRandomUUID(),
		Scope:     utilitiesModels.UploadScopeFileExplorer,
		Path:      canonicalPath,
		Name:      filepath.Base(canonicalPath),
		Size:      info.Size(),
		Owner:     owner,
		Node:      node,
		Status:    utilitiesModels.UploadStatusStaged,
		Device:    identity.Device,
		Inode:     identity.Inode,
		CreatedAt: time.Now(),
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return "", fmt.Errorf("file_explorer_upload_identity_failed: %w", err)
	}
	return record.ID, nil
}

// DeleteFileExplorerUpload consumes a server-issued upload identity and removes
// the associated regular file. Unknown, expired, and foreign-user identities
// are intentionally indistinguishable.
func (s *Service) DeleteFileExplorerUpload(uploadID, owner string) error {
	uploadID = strings.TrimSpace(uploadID)
	if s == nil || s.DB == nil || uploadID == "" || owner == "" {
		return ErrFileExplorerUploadNotFound
	}

	node, err := utils.GetSystemHostname()
	if err != nil {
		return fmt.Errorf("file_explorer_upload_node_failed: %w", err)
	}
	if strings.TrimSpace(node) == "" {
		return errors.New("file_explorer_upload_node_failed: empty hostname")
	}

	var record utilitiesModels.Upload
	err = s.DB.Where(
		"id = ? AND scope = ? AND owner = ? AND node = ?",
		uploadID,
		utilitiesModels.UploadScopeFileExplorer,
		owner,
		node,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrFileExplorerUploadNotFound
	}
	if err != nil {
		return fmt.Errorf("file_explorer_upload_lookup_failed: %w", err)
	}
	if record.CreatedAt.IsZero() ||
		!record.CreatedAt.Add(fileExplorerUploadIdentityTTL).After(time.Now()) {
		if err := s.DB.Delete(&record).Error; err != nil {
			return fmt.Errorf("file_explorer_upload_identity_delete_failed: %w", err)
		}
		return ErrFileExplorerUploadNotFound
	}
	if err := s.EnsureFileExplorerMutationAllowed(record.Path); err != nil {
		return err
	}

	_, err = uploadCore.RemoveIfSame(record.Path, uploadCore.FileIdentity{
		Device: record.Device,
		Inode:  record.Inode,
	})
	switch {
	case err == nil:
		// The original file was removed, or it was already absent. In either
		// case, the requested state has been reached and the identity is
		// consumed below.
	case errors.Is(err, os.ErrNotExist):
		// The file disappeared between identity validation and removal. Treat the
		// requested state as reached and consume the identity below.
	case errors.Is(err, uploadCore.ErrNotRegularFile),
		errors.Is(err, uploadCore.ErrFileReplaced):
		return ErrFileExplorerUploadConflict
	case err != nil:
		return wrapFileExplorerIOError(record.Path, err)
	}

	if err := s.DB.Delete(&record).Error; err != nil {
		return fmt.Errorf("file_explorer_upload_identity_delete_failed: %w", err)
	}
	return nil
}
