// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	valid "github.com/asaskevich/govalidator"
)

// ListISOs lists all available ISO files from the filesystem
func (s *Service) ListISOs() ([]ISOFile, error) {
	if s.ISOScanner == nil {
		return nil, fmt.Errorf("ISO scanner not initialized")
	}
	return s.ISOScanner.ListISOs()
}

// ListDownloads maintains backward compatibility but now returns ISOs from filesystem
func (s *Service) ListDownloads() ([]utilitiesModels.Downloads, error) {
	// For backward compatibility, convert ISO files to Downloads format
	isoFiles, err := s.ListISOs()
	if err != nil {
		return nil, err
	}

	var downloads []utilitiesModels.Downloads
	for _, iso := range isoFiles {
		download := utilitiesModels.Downloads{
			ID:        0,                                         // No database ID
			UUID:      utils.GenerateDeterministicUUID(iso.Path), // Use path as UUID source
			Path:      iso.Path,
			Name:      iso.Name,
			Type:      iso.Type,
			URL:       iso.Source,
			Progress:  100, // All files are complete
			Size:      iso.Size,
			Files:     []utilitiesModels.DownloadedFile{},
			CreatedAt: iso.ModTime,
			UpdatedAt: iso.ModTime,
		}
		downloads = append(downloads, download)
	}

	return downloads, nil
}

// GetDownload gets a specific download by filename (instead of UUID)
func (s *Service) GetDownload(filename string) (*utilitiesModels.Downloads, error) {
	// Try to find ISO by filename
	isoFiles, err := s.ListISOs()
	if err != nil {
		return nil, err
	}

	for _, iso := range isoFiles {
		if iso.Name == filename {
			download := utilitiesModels.Downloads{
				ID:        0,
				UUID:      utils.GenerateDeterministicUUID(iso.Path),
				Path:      iso.Path,
				Name:      iso.Name,
				Type:      iso.Type,
				URL:       iso.Source,
				Progress:  100,
				Size:      iso.Size,
				Files:     []utilitiesModels.DownloadedFile{},
				CreatedAt: iso.ModTime,
				UpdatedAt: iso.ModTime,
			}
			return &download, nil
		}
	}

	return nil, fmt.Errorf("download_not_found: %s", filename)
}

// GetMagnetDownloadAndFile gets magnet download and file (deprecated functionality)
func (s *Service) GetMagnetDownloadAndFile(filename, name string) (*utilitiesModels.Downloads, *utilitiesModels.DownloadedFile, error) {
	// This functionality is deprecated in the new stateless approach
	return nil, nil, fmt.Errorf("magnet_downloads_deprecated")
}

// GetFilePathById gets file path by filename (instead of ID)
func (s *Service) GetFilePathById(filename string, id int) (string, error) {
	// In the new approach, we use filename directly
	iso, err := s.ISOScanner.FindISOByName(filename)
	if err != nil {
		// Try to find by path if it's a full path
		iso, err = s.ISOScanner.FindISOByPath(filename)
		if err != nil {
			return "", fmt.Errorf("iso_not_found: %s", filename)
		}
	}
	return iso.Path, nil
}

// DownloadFile downloads a file using libaria2
func (s *Service) DownloadFile(url string, optFilename string, downloadType string) error {
	if s.Aria2Client == nil {
		return fmt.Errorf("aria2 client not initialized")
	}

	// Check if it's a magnet URI
	if utils.IsMagnetURI(url) {
		return s.downloadTorrent(url, optFilename, downloadType)
	}

	// Check if it's a valid URL
	if valid.IsURL(url) {
		return s.downloadHTTP(url, optFilename, downloadType)
	}

	// Check if it's a local file path
	if utils.IsAbsPath(url) {
		return s.copyLocalFile(url, optFilename, downloadType)
	}

	return fmt.Errorf("invalid_url")
}

// downloadHTTP downloads a file via HTTP using libaria2
func (s *Service) downloadHTTP(url, optFilename, downloadType string) error {
	var filename string
	if optFilename != "" {
		if err := utils.IsValidFilename(optFilename); err != nil {
			return fmt.Errorf("invalid_filename: %w", err)
		}
		filename = optFilename
	} else {
		filename = path.Base(url)
		if idx := strings.Index(filename, "?"); idx != -1 {
			filename = filename[:idx]
		}
		filename = strings.ReplaceAll(filename, " ", "_")
		if filename == "" {
			return fmt.Errorf("invalid_filename")
		}
	}

	// Determine download directory based on type
	var downloadDir string
	switch downloadType {
	case "isos":
		downloadDir = config.GetDownloadsPath("isos")
	case "jail_templates":
		downloadDir = config.GetDownloadsPath("jail_templates")
	case "vm_templates":
		downloadDir = config.GetDownloadsPath("vm_templates")
	default:
		// Default to ISOs for backward compatibility
		downloadDir = config.GetDownloadsPath("isos")
		downloadType = "isos"
	}

	// Check if file already exists
	filePath := path.Join(downloadDir, filename)
	if _, err := os.Stat(filePath); err == nil {
		logger.L.Info().Msgf("File already exists: %s", filePath)
		return nil
	}

	// Start download with libaria2
	gid, err := s.Aria2Client.DownloadFile(url, filename, downloadType)
	if err != nil {
		return fmt.Errorf("failed_to_start_download: %w", err)
	}

	logger.L.Info().Msgf("Started HTTP download %s with GID: %s", url, gid)
	return nil
}

// downloadTorrent downloads a torrent/magnet using libaria2
func (s *Service) downloadTorrent(url, optFilename, downloadType string) error {
	// For torrents, filename might be determined by the torrent content
	filename := optFilename
	if filename == "" {
		filename = "downloaded_file"
	}

	// Start torrent download with libaria2
	gid, err := s.Aria2Client.DownloadFile(url, filename, downloadType)
	if err != nil {
		return fmt.Errorf("failed_to_start_torrent_download: %w", err)
	}

	logger.L.Info().Msgf("Started torrent download %s with GID: %s", url, gid)
	return nil
}

// copyLocalFile copies a local file to the downloads directory
func (s *Service) copyLocalFile(url, optFilename, downloadType string) error {
	if _, err := os.Stat(url); os.IsNotExist(err) {
		return fmt.Errorf("file_not_found")
	}

	var filename string
	if optFilename != "" {
		if err := utils.IsValidFilename(optFilename); err != nil {
			return fmt.Errorf("invalid_filename: %w", err)
		}
		filename = optFilename
	} else {
		filename = path.Base(url)
		if filename == "" {
			return fmt.Errorf("invalid_filename")
		}
	}

	// Determine download directory based on type
	var downloadDir string
	switch downloadType {
	case "isos":
		downloadDir = config.GetDownloadsPath("isos")
	case "jail_templates":
		downloadDir = config.GetDownloadsPath("jail_templates")
	case "vm_templates":
		downloadDir = config.GetDownloadsPath("vm_templates")
	default:
		// Default to ISOs for backward compatibility
		downloadDir = config.GetDownloadsPath("isos")
	}

	destPath := path.Join(downloadDir, filename)

	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		logger.L.Info().Msgf("File already exists: %s", destPath)
		return nil
	}

	err := utils.CopyFile(url, destPath)
	if err != nil {
		return fmt.Errorf("file_copy_failed: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("file_stat_failed: %w", err)
	}

	size := info.Size()
	logger.L.Info().Msgf("Copied file %s to %s (%d bytes)", url, destPath, size)
	return nil
}

// SyncDownloadProgress syncs download progress from libaria2
func (s *Service) SyncDownloadProgress() error {
	if s.Aria2Client == nil {
		return fmt.Errorf("aria2 client not initialized")
	}

	// Get all downloads from libaria2
	downloads, err := s.Aria2Client.GetAllDownloads()
	if err != nil {
		return fmt.Errorf("failed_to_get_downloads: %w", err)
	}

	for _, download := range downloads {
		progress, err := s.Aria2Client.GetProgress(download.GID)
		if err != nil {
			logger.L.Warn().Msgf("Failed to get progress for %s: %v", download.GID, err)
			continue
		}

		logger.L.Debug().Msgf("Download %s (%s): %d%%", download.GID, download.Status, progress)

		// Log completed downloads
		if download.Status == "complete" {
			logger.L.Info().Msgf("Download completed: %s", download.GID)
			for _, file := range download.Files {
				logger.L.Info().Msgf("Downloaded file: %s", file.Path)
			}
		}
	}

	return nil
}

// DeleteDownload deletes a download by filename
func (s *Service) DeleteDownload(filename string) error {
	// Try to find and delete the ISO file
	iso, err := s.ISOScanner.FindISOByName(filename)
	if err != nil {
		return fmt.Errorf("download_not_found: %s", filename)
	}

	return s.deleteISOFile(iso.Path)
}

// BulkDeleteDownload deletes multiple downloads by filenames
func (s *Service) BulkDeleteDownload(filenames []string) error {
	for _, filename := range filenames {
		if err := s.DeleteDownload(filename); err != nil {
			logger.L.Warn().Msgf("Failed to delete download %s: %v", filename, err)
		}
	}

	return nil
}

// deleteISOFile deletes an ISO file from the filesystem
func (s *Service) deleteISOFile(filePath string) error {
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed_to_delete_file: %w", err)
	}

	logger.L.Info().Msgf("Deleted ISO file: %s", filePath)
	return nil
}

// FindISOByName finds an ISO by name (new method)
func (s *Service) FindISOByName(name string) (*utilitiesServiceInterfaces.ISOFile, error) {
	if s.ISOScanner == nil {
		return nil, fmt.Errorf("ISO scanner not initialized")
	}
	localISO, err := s.ISOScanner.FindISOByName(name)
	if err != nil {
		return nil, err
	}
	// Convert from local ISOFile to interface ISOFile
	return &utilitiesServiceInterfaces.ISOFile{
		Name:    localISO.Name,
		Path:    localISO.Path,
		Size:    localISO.Size,
		ModTime: localISO.ModTime,
		Type:    localISO.Type,
		Source:  localISO.Source,
	}, nil
}

// FindISOByPath finds an ISO by path (new method)
func (s *Service) FindISOByPath(path string) (*utilitiesServiceInterfaces.ISOFile, error) {
	if s.ISOScanner == nil {
		return nil, fmt.Errorf("ISO scanner not initialized")
	}
	localISO, err := s.ISOScanner.FindISOByPath(path)
	if err != nil {
		return nil, err
	}
	// Convert from local ISOFile to interface ISOFile
	return &utilitiesServiceInterfaces.ISOFile{
		Name:    localISO.Name,
		Path:    localISO.Path,
		Size:    localISO.Size,
		ModTime: localISO.ModTime,
		Type:    localISO.Type,
		Source:  localISO.Source,
	}, nil
}

// GetDownloadProgress gets download progress for a specific GID
func (s *Service) GetDownloadProgress(gid string) (int, error) {
	if s.Aria2Client == nil {
		return 0, fmt.Errorf("aria2 client not initialized")
	}
	return s.Aria2Client.GetProgress(gid)
}

// PauseDownload pauses a download
func (s *Service) PauseDownload(gid string) error {
	if s.Aria2Client == nil {
		return fmt.Errorf("aria2 client not initialized")
	}
	return s.Aria2Client.PauseDownload(gid)
}

// ResumeDownload resumes a download
func (s *Service) ResumeDownload(gid string) error {
	if s.Aria2Client == nil {
		return fmt.Errorf("aria2 client not initialized")
	}
	return s.Aria2Client.ResumeDownload(gid)
}

// CancelDownload cancels a download
func (s *Service) CancelDownload(gid string) error {
	if s.Aria2Client == nil {
		return fmt.Errorf("aria2 client not initialized")
	}
	return s.Aria2Client.RemoveDownload(gid)
}

// GetFilePathByFilename gets file path by filename (new method)
func (s *Service) GetFilePathByFilename(filename string) (string, error) {
	iso, err := s.ISOScanner.FindISOByName(filename)
	if err != nil {
		return "", fmt.Errorf("iso_not_found: %s", filename)
	}
	return iso.Path, nil
}

// Close cleans up resources
func (s *Service) Close() error {
	var errs []string

	if s.ISOScanner != nil {
		if err := s.ISOScanner.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("ISO scanner: %v", err))
		}
	}

	if s.Aria2Client != nil {
		if err := s.Aria2Client.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("aria2 client: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}

	return nil
}
