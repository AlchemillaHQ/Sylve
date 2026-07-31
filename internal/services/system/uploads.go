// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
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

const fileExplorerUploadIdentityTTL = 24 * time.Hour

var (
	ErrFileExplorerUploadNotFound = errors.New("file_explorer_upload_not_found")
	ErrFileExplorerUploadNotFile  = errors.New("file_explorer_upload_not_file")
)

// RegisterFileExplorerUpload returns an opaque, short-lived identity that can
// be used to revert exactly the file created by a successful upload. The path
// remains server-side and is never accepted back from the client as deletion
// authority.
func (s *Service) RegisterFileExplorerUpload(path string, userID uint) (string, error) {
	if s == nil || s.DB == nil || strings.TrimSpace(path) == "" || userID == 0 {
		return "", fmt.Errorf("invalid_file_explorer_upload")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("file_explorer_upload_stat_failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ErrFileExplorerUploadNotFile
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
		UserID:    userID,
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
func (s *Service) DeleteFileExplorerUpload(uploadID string, userID uint) error {
	uploadID = strings.TrimSpace(uploadID)
	if s == nil || s.DB == nil || uploadID == "" || userID == 0 {
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
		"id = ? AND scope = ? AND user_id = ? AND node = ?",
		uploadID,
		utilitiesModels.UploadScopeFileExplorer,
		userID,
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
	case errors.Is(err, uploadCore.ErrNotRegularFile):
		return ErrFileExplorerUploadNotFile
	case errors.Is(err, uploadCore.ErrFileReplaced):
		return ErrFileExplorerUploadNotFound
	case err != nil:
		return fmt.Errorf("file_explorer_upload_delete_failed: %w", err)
	}

	if err := s.DB.Delete(&record).Error; err != nil {
		return fmt.Errorf("file_explorer_upload_identity_delete_failed: %w", err)
	}
	return nil
}
