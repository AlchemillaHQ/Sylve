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
	"path/filepath"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/pkg/utils"

	valid "github.com/asaskevich/govalidator"
	"github.com/cavaliergopher/grab/v3"
	"github.com/cenkalti/rain/v2/torrent"
)

func (s *Service) ListDownloads() ([]utilitiesModels.Downloads, error) {
	var downloads []utilitiesModels.Downloads

	if err := s.DB.Preload("Files").Find(&downloads).Error; err != nil {
		logger.L.Error().Msgf("Failed to list downloads: %v", err)
		return nil, err
	}

	return downloads, nil
}

func (s *Service) GetDownload(uuid string) (*utilitiesModels.Downloads, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.Preload("Files").Where("uuid = ?", uuid).First(&download).Error; err != nil {
		logger.L.Error().Msgf("Failed to get download: %v", err)
		return nil, err
	}

	return &download, nil
}

func (s *Service) GetMagnetDownloadAndFile(uuid, name string) (*utilitiesModels.Downloads, *utilitiesModels.DownloadedFile, error) {
	var download utilitiesModels.Downloads

	if err := s.DB.Preload("Files").Where("uuid = ?", uuid).First(&download).Error; err != nil {
		logger.L.Error().Msgf("Failed to get download by UUID: %v", err)
		return nil, nil, err
	}

	var file utilitiesModels.DownloadedFile

	if download.Type == "torrent" {
		for _, f := range download.Files {
			if f.Name == name {
				file = f
				break
			}
		}
	}

	return &download, &file, nil
}

func (s *Service) GetFilePathById(uuid string, id int) (string, error) {
	dl, err := s.GetDownload(uuid)
	if err != nil {
		logger.L.Error().Msgf("Failed to get download by UUID: %v", err)
		return "", err
	}

	if dl.Type == "torrent" {
		var file utilitiesModels.DownloadedFile
		if err := s.DB.Where("id = ?", id).First(&file).Error; err != nil {
			logger.L.Error().Msgf("Failed to get file by ID: %v", err)
			return "", err
		}

		var download utilitiesModels.Downloads
		if err := s.DB.Where("id = ?", file.DownloadID).First(&download).Error; err != nil {
			logger.L.Error().Msgf("Failed to get download by ID: %v", err)
			return "", err
		}

		fullPath := path.Join(download.Path, file.Name)

		return fullPath, nil
	} else if dl.Type == "http" {
		return path.Join(config.GetDownloadsPath("http"), dl.Name), nil
	}

	return "", fmt.Errorf("unsupported_download_type")
}

func (s *Service) DownloadFile(url string, optFilename string) error {
	var existing utilitiesModels.Downloads

	if s.DB.Where("url = ?", url).First(&existing).RowsAffected > 0 {
		logger.L.Info().Msgf("Download already exists: %s", url)
		return nil
	}

	if utils.IsMagnetURI(url) {
		torrentOpts := torrent.AddTorrentOptions{
			ID:                utils.GenerateDeterministicUUID(url),
			StopAfterDownload: false,
		}

		t, err := s.BTTClient.AddURI(url, &torrentOpts)

		if err != nil {
			logger.L.Error().Msgf("Failed to add torrent: %v", err)
			return err
		}

		download := utilitiesModels.Downloads{
			URL:      url,
			UUID:     t.ID(),
			Path:     t.Dir(),
			Type:     "torrent",
			Name:     t.Name(),
			Size:     0,
			Progress: 0,
			Files:    []utilitiesModels.DownloadedFile{},
		}

		if err := s.DB.Create(&download).Error; err != nil {
			logger.L.Error().Msgf("Failed to create download record: %v", err)
			return err
		}

		return nil
	} else if valid.IsURL(url) {
		uuid := utils.GenerateDeterministicUUID(url)
		destDir := config.GetDownloadsPath("http")

		var filename string

		if optFilename != "" {
			err := utils.IsValidFilename(optFilename)
			if err != nil {
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

		filePath := path.Join(destDir, filename)
		if _, err := os.Stat(filePath); err == nil {
			var found utilitiesModels.Downloads
			if s.DB.Where("path = ? AND name = ?", filePath, filename).First(&found).RowsAffected > 0 {
				return nil
			}

			size := int64(0)
			info, err := os.Stat(filePath)
			if err == nil {
				size = info.Size()
			}

			download := utilitiesModels.Downloads{
				URL:      url,
				UUID:     uuid,
				Path:     filePath,
				Type:     "http",
				Name:     filename,
				Size:     size,
				Progress: 100,
				Files:    []utilitiesModels.DownloadedFile{},
			}

			if err := s.DB.Create(&download).Error; err != nil {
				return fmt.Errorf("failed_to_create_download_record: %w", err)
			}

			return nil
		}

		download := utilitiesModels.Downloads{
			URL:      url,
			UUID:     uuid,
			Path:     filePath,
			Type:     "http",
			Name:     filename,
			Size:     0,
			Progress: 0,
			Files:    []utilitiesModels.DownloadedFile{},
		}

		if err := s.DB.Create(&download).Error; err != nil {
			fmt.Printf("Failed to create download record: %+v\n", err)
			return err
		}

		req, _ := grab.NewRequest(path.Join(destDir, filename), url)
		resp := s.GrabClient.Do(req)
		s.httpRspMu.Lock()
		s.httpResponses[uuid] = resp
		s.httpRspMu.Unlock()

		return nil
	} else if utils.IsAbsPath(url) {
		if _, err := os.Stat(url); os.IsNotExist(err) {
			return fmt.Errorf("file_not_found")
		}

		var filename string

		if optFilename != "" {
			err := utils.IsValidFilename(optFilename)
			if err != nil {
				return fmt.Errorf("invalid_filename: %w", err)
			}

			filename = optFilename
		} else {
			filename = path.Base(url)
			if filename == "" {
				return fmt.Errorf("invalid_filename")
			}
		}

		destDir := config.GetDownloadsPath("http")
		destPath := path.Join(destDir, filename)

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

		download := utilitiesModels.Downloads{
			URL:      url,
			UUID:     utils.GenerateDeterministicUUID(url),
			Path:     destPath,
			Type:     "http",
			Name:     filename,
			Size:     size,
			Progress: 100,
			Files:    []utilitiesModels.DownloadedFile{},
		}

		if err := s.DB.Create(&download).Error; err != nil {
			return fmt.Errorf("failed_to_create_download_record: %w", err)
		}

		return nil
	}

	return fmt.Errorf("invalid_url")
}

func (s *Service) SyncDownloadProgress() error {
	var downloads []utilitiesModels.Downloads
	if err := s.DB.Where("progress < 100").Find(&downloads).Error; err != nil {
		return err
	}

	for _, download := range downloads {
		if download.Type == "torrent" {
			torrent := s.BTTClient.GetTorrent(download.UUID)
			if torrent == nil {
				logger.L.Error().Msgf("Torrent %s not found", download.UUID)
				continue
			}

			piecesHave := torrent.Stats().Pieces.Have
			piecesTotal := torrent.Stats().Pieces.Total

			if piecesHave == 0 {
				download.Progress = 0
			} else {
				download.Progress = int((piecesHave * 100) / piecesTotal)
			}

			download.Size = torrent.Stats().Bytes.Total
			download.Name = torrent.Stats().Name

			files, err := torrent.Files()

			if err != nil {
				continue
			}

			fileList := make([]string, len(files))
			for i, file := range files {
				fileList[i] = file.Path()
				downloadedFile := utilitiesModels.DownloadedFile{
					DownloadID: download.ID,
					Download:   download,
					Name:       file.Path(),
					Size:       file.Length(),
				}

				var existingFile utilitiesModels.DownloadedFile
				if s.DB.Where("download_id = ? AND name = ?", download.ID, file.Path()).First(&existingFile).RowsAffected > 0 {
					continue
				}

				if err := s.DB.Create(&downloadedFile).Error; err != nil {
					logger.L.Error().Msgf("Failed to create downloaded file record: %v", err)
					continue
				}

				download.Files = append(download.Files, downloadedFile)
			}

			if err := s.DB.Save(&download).Error; err != nil {
				logger.L.Error().Msgf("Failed to update download record: %v", err)
				return err
			}
		} else if download.Type == "http" {
			s.httpRspMu.Lock()
			resp, ok := s.httpResponses[download.UUID]
			s.httpRspMu.Unlock()
			if !ok {
				logger.L.Debug().Msgf("No active HTTP download for %s", download.UUID)
				continue
			}

			download.Progress = int(100 * resp.Progress())
			if info, err := os.Stat(resp.Filename); err == nil {
				download.Size = info.Size()
			}

			if resp.IsComplete() {
				if err := resp.Err(); err != nil {
					logger.L.Error().Msgf("HTTP download %s failed: %v", download.UUID, err)
				}
				s.httpRspMu.Lock()
				delete(s.httpResponses, download.UUID)
				s.httpRspMu.Unlock()

				download.Progress = 99

				if strings.HasSuffix(download.Name, ".txz") {
					extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), download.UUID)
					if _, err := os.Stat(extractsPath); err == nil {
						if err := os.RemoveAll(extractsPath); err != nil {
							logger.L.Error().Msgf("Failed to remove extracts folder: %v", err)
						}
					}

					if err := os.MkdirAll(extractsPath, 0755); err != nil {
						logger.L.Error().Msgf("Failed to create extracts folder: %v", err)
					}

					output, err := utils.RunCommand("tar", "-xf", resp.Filename, "-C", extractsPath)
					if err != nil {
						logger.L.Error().Msgf("Failed to extract tar file: %v", err)
					} else {
						logger.L.Info().Msgf("Extracted tar file to %s: %s", extractsPath, output)
					}
				}

				download.Progress = 100
			}

			if err := s.DB.Save(&download).Error; err != nil {
				logger.L.Error().Msgf("Failed to update HTTP download record: %v", err)
			}
		}
	}

	return nil
}

func (s *Service) DeleteDownload(id int) error {
	var download utilitiesModels.Downloads
	if err := s.DB.Where("id = ?", id).First(&download).Error; err != nil {
		logger.L.Debug().Msgf("Failed to find download: %v", err)
		return err
	}

	if download.Type == "torrent" {
		torrent := s.BTTClient.GetTorrent(download.UUID)
		if torrent != nil {
			if err := s.BTTClient.RemoveTorrent(download.UUID, false); err != nil {
				logger.L.Debug().Msgf("Failed to remove torrent: %v", err)
				return err
			}
		}
	}

	if download.Type == "http" {
		if strings.HasSuffix(download.Name, ".txz") {
			extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), download.UUID)
			_, err := utils.RunCommand("chflags", "-R", "noschg", extractsPath)

			if err != nil {
				logger.L.Error().Msgf("Failed to change flags for extracts folder: %v", err)
			}

			if _, err := os.Stat(extractsPath); err == nil {
				if err := os.RemoveAll(extractsPath); err != nil {
					logger.L.Error().Msgf("Failed to remove extracts folder: %v", err)
				}
			}
		}

		err := utils.DeleteFile(path.Join(config.GetDownloadsPath(download.Type), download.Name))
		if err != nil {
			logger.L.Debug().Msgf("Failed to delete HTTP download file: %v", err)
			return err
		}
	}

	for _, file := range download.Files {
		if err := s.DB.Delete(&file).Error; err != nil {
			logger.L.Debug().Msgf("Failed to delete downloaded file: %v", err)
			return err
		}
	}

	if err := s.DB.Delete(&download).Error; err != nil {
		logger.L.Debug().Msgf("Failed to delete download: %v", err)
		return err
	}

	return nil
}

func (s *Service) BulkDeleteDownload(ids []int) error {
	var downloads []utilitiesModels.Downloads
	if err := s.DB.Where("id IN ?", ids).Find(&downloads).Error; err != nil {
		return err
	}

	for _, download := range downloads {
		if download.Type == "torrent" {
			torrent := s.BTTClient.GetTorrent(download.UUID)
			if torrent != nil {
				if err := s.BTTClient.RemoveTorrent(download.UUID, false); err != nil {
					logger.L.Debug().Msgf("Failed to remove torrent: %v", err)
					return err
				}
			}
		}

		for _, file := range download.Files {
			if err := s.DB.Delete(&file).Error; err != nil {
				logger.L.Debug().Msgf("Failed to delete downloaded file: %v", err)
				return err
			}
		}

		if err := s.DB.Delete(&download).Error; err != nil {
			logger.L.Debug().Msgf("Failed to delete download: %v", err)
			return err
		}
	}

	return nil
}
