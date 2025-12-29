// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
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

	var pendingCount int16
	for _, dl := range downloads {
		if dl.Status != utilitiesModels.DownloadStatusDone &&
			(dl.Status == utilitiesModels.DownloadStatusPending || dl.Status == utilitiesModels.DownloadStatusProcessing) &&
			dl.Progress < 100 {
			pendingCount++
		}
	}

	if pendingCount > 0 {
		_ = db.EnqueueNoPayload(context.Background(), "utils-download-sync")
	}

	return downloads, nil
}

func (s *Service) ListDownloadsByUType() ([]utilitiesServiceInterfaces.UTypeGroupedDownload, error) {
	var downloads []utilitiesModels.Downloads
	var grouped []utilitiesServiceInterfaces.UTypeGroupedDownload

	if err := s.DB.Find(&downloads).Error; err != nil {
		return grouped, err
	}

	for _, dl := range downloads {
		label := dl.Name

		if dl.ExtractedPath != "" {
			info, err := os.Stat(dl.ExtractedPath)
			if err == nil && info.IsDir() {
				files, err := os.ReadDir(dl.ExtractedPath)
				if err == nil && len(files) == 1 {
					if strings.HasSuffix(files[0].Name(), ".raw") ||
						strings.HasSuffix(files[0].Name(), ".img") ||
						strings.HasSuffix(files[0].Name(), ".disk") ||
						strings.HasSuffix(files[0].Name(), ".iso") {
						label = fmt.Sprintf("%s@@@%s", dl.Name, files[0].Name())
					}
				}
			}
		}

		grouped = append(grouped, utilitiesServiceInterfaces.UTypeGroupedDownload{
			UUID:  dl.UUID,
			Label: label,
			UType: dl.UType,
		})
	}

	return grouped, nil
}

func (s *Service) GetDownload(uuid string) (*utilitiesModels.Downloads, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.Preload("Files").Where("uuid = ?", uuid).First(&download).Error; err != nil {
		logger.L.Error().Msgf("Failed to get download: %v", err)
		return nil, err
	}

	return &download, nil
}

func (s *Service) GetDownloadByID(id uint) (*utilitiesModels.Downloads, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.Preload("Files").Where("id = ?", id).First(&download).Error; err != nil {
		logger.L.Error().Msgf("Failed to get download by ID: %v", err)
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

func (s *Service) DownloadFile(req utilitiesServiceInterfaces.DownloadFileRequest) error {
	var fileName string
	if req.Filename != nil && *req.Filename != "" {
		fileName = *req.Filename
	} else {
		fileName = ""
	}

	var ignoreTLS bool
	if req.IgnoreTLS != nil && *req.IgnoreTLS {
		ignoreTLS = true
	} else {
		ignoreTLS = false
	}

	var automaticExtraction bool
	if req.AutomaticExtraction != nil && *req.AutomaticExtraction {
		automaticExtraction = true
	} else {
		automaticExtraction = false
	}

	var automaticRawConversion bool
	if req.AutomaticRawConversion != nil && *req.AutomaticRawConversion {
		automaticRawConversion = true
	} else {
		automaticRawConversion = false
	}

	url := req.URL
	downloadType := req.DownloadType

	var existing utilitiesModels.Downloads

	if s.DB.Where("url = ?", url).First(&existing).RowsAffected > 0 {
		logger.L.Info().Msgf("Download already exists: %s", url)
		return fmt.Errorf("url_already_exists")
	}

	tmpUUID := utils.GenerateDeterministicUUID(url)

	if utils.IsMagnetURI(url) {
		download := utilitiesModels.Downloads{
			URL:                    url,
			UUID:                   tmpUUID,
			Path:                   fmt.Sprintf("/non-existent/%s", tmpUUID),
			Type:                   utilitiesModels.DownloadTypeTorrent,
			Name:                   fileName,
			Size:                   0,
			Progress:               0,
			Files:                  []utilitiesModels.DownloadedFile{},
			Status:                 utilitiesModels.DownloadStatusPending,
			AutomaticExtraction:    false,
			UType:                  downloadType,
			AutomaticRawConversion: false,
			IgnoreTLS:              true,
		}

		if err := s.DB.Create(&download).Error; err != nil {
			logger.L.Error().Msgf("Failed to create download record: %v", err)
			return err
		}

		err := db.EnqueueJSON(context.Background(), "utils-download-start", &utilitiesServiceInterfaces.DownloadStartPayload{
			ID: download.ID,
		})

		if err != nil {
			logger.L.Error().Msgf("Failed to enqueue download start job: %v", err)
			s.DB.Model(&download).Update("status", utilitiesModels.DownloadStatusFailed)
			return err
		}

		return nil
	} else if valid.IsURL(url) {
		uuid := utils.GenerateDeterministicUUID(url)
		destDir := config.GetDownloadsPath("http")

		var finalName string

		if fileName != "" {
			err := utils.IsValidFilename(fileName)
			if err != nil {
				return fmt.Errorf("invalid_filename: %w", err)
			}

			finalName = fileName
		} else {
			finalName = path.Base(url)

			if idx := strings.Index(finalName, "?"); idx != -1 {
				finalName = finalName[:idx]
			}

			finalName = strings.ReplaceAll(finalName, " ", "_")
			if finalName == "" {
				return fmt.Errorf("invalid_filename")
			}
		}

		filePath := path.Join(destDir, finalName)

		if _, err := os.Stat(filePath); err == nil {
			err := os.Remove(filePath)
			if err != nil {
				return fmt.Errorf("failed_to_remove_incomplete_file: %w", err)
			}
		}

		download := utilitiesModels.Downloads{
			URL:                    url,
			UUID:                   uuid,
			Path:                   filePath,
			Type:                   utilitiesModels.DownloadTypeHTTP,
			Name:                   finalName,
			Size:                   0,
			Progress:               0,
			Files:                  []utilitiesModels.DownloadedFile{},
			Status:                 utilitiesModels.DownloadStatusPending,
			UType:                  downloadType,
			AutomaticExtraction:    automaticExtraction,
			AutomaticRawConversion: automaticRawConversion,
			IgnoreTLS:              ignoreTLS,
		}

		if err := s.DB.Create(&download).Error; err != nil {
			fmt.Printf("Failed to create download record: %+v\n", err)
			return err
		}

		err := db.EnqueueJSON(context.Background(), "utils-download-start", &utilitiesServiceInterfaces.DownloadStartPayload{
			ID: download.ID,
		})

		if err != nil {
			logger.L.Error().Msgf("Failed to enqueue download start job: %v", err)
			s.DB.Model(&download).Update("status", utilitiesModels.DownloadStatusFailed)
			return err
		}

		return nil
	} else if utils.IsAbsPath(url) {
		if _, err := os.Stat(url); os.IsNotExist(err) {
			return fmt.Errorf("file_not_found")
		}

		var finalName string

		if fileName != "" {
			err := utils.IsValidFilename(fileName)
			if err != nil {
				return fmt.Errorf("invalid_filename: %w", err)
			}

			finalName = fileName
		} else {
			finalName = path.Base(url)
			if finalName == "" {
				return fmt.Errorf("invalid_filename")
			}
		}

		destDir := config.GetDownloadsPath("http")
		destPath := path.Join(destDir, finalName)

		if _, err := os.Stat(destPath); err == nil {
			err := os.Remove(destPath)
			if err != nil {
				return fmt.Errorf("failed_to_remove_existing_file: %w", err)
			}
		}

		download := utilitiesModels.Downloads{
			URL:                    url,
			UUID:                   utils.GenerateDeterministicUUID(url),
			Path:                   destPath,
			Type:                   utilitiesModels.DownloadTypePath,
			Name:                   finalName,
			Size:                   0,
			Progress:               0,
			Files:                  []utilitiesModels.DownloadedFile{},
			Status:                 utilitiesModels.DownloadStatusPending,
			AutomaticExtraction:    automaticExtraction,
			AutomaticRawConversion: automaticRawConversion,
			UType:                  downloadType,
			IgnoreTLS:              ignoreTLS,
		}

		if err := s.DB.Create(&download).Error; err != nil {
			return fmt.Errorf("failed_to_create_download_record: %w", err)
		}

		err := db.EnqueueJSON(context.Background(), "utils-download-start", &utilitiesServiceInterfaces.DownloadStartPayload{
			ID: download.ID,
		})

		if err != nil {
			logger.L.Error().Msgf("Failed to enqueue download start job: %v", err)
			s.DB.Model(&download).Update("status", utilitiesModels.DownloadStatusFailed)
			return err
		}

		return nil
	}

	return fmt.Errorf("invalid_url")
}

func (s *Service) StartDownload(id *uint) error {
	if id == nil {
		return fmt.Errorf("download_is_nil")
	}

	download, err := s.GetDownloadByID(*id)
	if err != nil {
		logger.L.Error().Uint("download_id", *id).Err(err).Msg("GetDownloadByID failed")
		return err
	}

	if utils.IsMagnetURI(download.URL) {
		torrentOpts := torrent.AddTorrentOptions{
			ID:                utils.GenerateDeterministicUUID(download.URL),
			StopAfterDownload: false,
		}

		t, err := s.BTTClient.AddURI(download.URL, &torrentOpts)
		if err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("Failed to add torrent")
			download.Status = utilitiesModels.DownloadStatusFailed
			download.Error = err.Error()
			if saveErr := s.DB.Save(download).Error; saveErr != nil {
				logger.L.Error().Uint("download_id", *id).Err(saveErr).Msg("Failed to persist failed status")
			}
			return err
		}

		download.UUID = t.ID()
		download.Path = t.Dir()
		download.Name = t.Name()

		if err := s.DB.Save(download).Error; err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("failed_to_update_download_record")
			return fmt.Errorf("failed_to_update_download_record: %w", err)
		}
	} else if valid.IsURL(download.URL) {
		destDir := config.GetDownloadsPath("http")
		req, _ := grab.NewRequest(path.Join(destDir, download.Name), download.URL)

		var resp *grab.Response

		if download.IgnoreTLS {
			resp = s.GrabInsecure.Do(req)
		} else {
			resp = s.GrabClient.Do(req)
		}

		s.httpRspMu.Lock()
		s.httpResponses[download.UUID] = resp
		s.httpRspMu.Unlock()
	} else if utils.IsAbsPath(download.URL) {
		destDir := config.GetDownloadsPath("path")
		destPath := path.Join(destDir, download.Name)
		err := utils.CopyFile(download.URL, destPath)

		if err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("file_copy_failed")
			download.Status = utilitiesModels.DownloadStatusFailed
			download.Error = err.Error()
			s.DB.Model(download).Select("Status", "Error").Updates(map[string]any{
				"status": download.Status,
				"error":  download.Error,
			})
			return fmt.Errorf("file_copy_failed: %w", err)
		}

		info, err := os.Stat(destPath)
		if err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("file_stat_failed")
			download.Status = utilitiesModels.DownloadStatusFailed
			download.Error = err.Error()
			s.DB.Model(download).Select("Status", "Error").Updates(map[string]any{
				"status": download.Status,
				"error":  download.Error,
			})
			return fmt.Errorf("file_stat_failed: %w", err)
		}

		download.Size = info.Size()
		download.Progress = 100
		download.Path = destPath

		needPostProc := download.AutomaticExtraction || download.AutomaticRawConversion

		if needPostProc {
			download.Status = utilitiesModels.DownloadStatusProcessing
		} else {
			download.Status = utilitiesModels.DownloadStatusDone
		}

		if err := s.DB.Model(download).Select("Status", "Progress", "Size", "Path").Updates(map[string]any{
			"status":   download.Status,
			"progress": download.Progress,
			"size":     download.Size,
			"path":     download.Path,
		}).Error; err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("failed_to_update_download_record")
			return fmt.Errorf("failed_to_update_download_record: %w", err)
		}

		if needPostProc {
			err = db.EnqueueJSON(context.Background(), "utils-download-postproc", &utilitiesServiceInterfaces.DownloadPostProcPayload{
				ID: download.ID,
			})

			if err != nil {
				logger.L.Error().Uint("download_id", *id).Err(err).Msg("failed_to_enqueue_postproc")
				return fmt.Errorf("failed_to_enqueue_postproc: %w", err)
			}
		}
	}

	return nil
}

func (s *Service) StartPostProcess(id *uint) error {
	if id == nil {
		return fmt.Errorf("download_is_nil")
	}

	defer func() {
		s.inflightMu.Lock()
		delete(s.inflight, *id)
		s.inflightMu.Unlock()
	}()

	logger.L.Debug().Msgf("Post Process started for id=%d", *id)

	var d utilitiesModels.Downloads
	if err := s.DB.First(&d, "id = ?", *id).Error; err != nil {
		return err
	}

	if d.Status != utilitiesModels.DownloadStatusProcessing {
		return nil
	}

	if !d.AutomaticExtraction && !d.AutomaticRawConversion {
		return s.finishDownload(&d, "")
	}

	var extractedPath string

	if d.AutomaticExtraction {
		extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), d.UUID)
		if err := utils.ResetDir(extractsPath); err != nil {
			return s.failDownload(&d, fmt.Errorf("reset extracts: %w", err))
		}

		mime, kind, err := utils.SniffMIME(d.Path)
		sniffFailed := false
		logger.L.Debug().Msgf("postproc sniff id=%d mime=%s ext=%s kind=%+v err=%v", d.ID, mime, kind.Extension, kind, err)
		if err != nil {
			// If unknown, still mark done; not extractable
			logger.L.Warn().Msgf("sniff failed (%s): %v", d.Path, err)
			sniffFailed = true
		}

		if !sniffFailed {
			if mime == "application/x-tar" || utils.IsTarLike(d.Path, mime) {
				// We're using --no-xattrs to handle cross-platform rootfs extraction (e.g., Linux rootfs on FreeBSD)
				if out, err := utils.RunCommand("tar", "--no-xattrs", "-xf", d.Path, "-C", extractsPath); err != nil {
					logger.L.Error().Msgf("tar extract failed: %v (%s)", err, out)
					return s.failDownload(&d, err)
				}

				d.ExtractedPath = extractsPath
				return s.finishDownload(&d, extractsPath)
			}

			outName := defaultOutName(d.Path, kind.Extension)
			outFile := filepath.Join(extractsPath, outName)
			if err := utils.DecompressOne(mime, d.Path, outFile); err != nil {
				logger.L.Error().Msgf("decompress failed: %v", err)
				return s.failDownload(&d, err)
			}

			if err := utils.DecompressOne(mime, d.Path, outFile); err != nil {
				logger.L.Error().Msgf("decompress failed: %v", err)
				return s.failDownload(&d, err)
			}

			if files, _ := os.ReadDir(extractsPath); len(files) == 1 {
				d.ExtractedPath = filepath.Join(extractsPath, files[0].Name())
			} else {
				d.ExtractedPath = extractsPath
			}

			extractedPath = d.ExtractedPath
		}
	}

	if d.AutomaticRawConversion {
		srcPath := d.Path
		if extractedPath != "" {
			srcPath = extractedPath
		}

		dstPath := strings.TrimSuffix(srcPath, filepath.Ext(srcPath)) + ".raw"
		err := qemuimg.Convert(srcPath, dstPath, qemuimg.FormatRaw)

		if err != nil {
			logger.L.Error().Msgf("raw conversion failed: %v", err)
			return s.failDownload(&d, err)
		}

		if err := os.Remove(srcPath); err != nil {
			logger.L.Error().Msgf("Failed to remove source file: %v", err)
		}

		d.Name = filepath.Base(dstPath)
		d.Path = dstPath
		d.ExtractedPath = dstPath
		extractedPath = dstPath
	}

	return s.finishDownload(&d, extractedPath)
}

func (s *Service) flipToProcessing(id uint) bool {
	res := s.DB.Model(&utilitiesModels.Downloads{}).
		Where("id = ? AND status = ?", id, utilitiesModels.DownloadStatusPending).
		Updates(map[string]any{
			"status":   utilitiesModels.DownloadStatusProcessing,
			"progress": 99,
		})

	return res.Error == nil && res.RowsAffected == 1
}

func (s *Service) finishDownload(d *utilitiesModels.Downloads, extractedPath string) error {
	d.Progress = 100
	d.ExtractedPath = extractedPath

	return s.DB.Model(d).Select("Status", "Progress", "ExtractedPath", "Name", "Path").
		Updates(map[string]any{
			"name":           d.Name,
			"path":           d.Path,
			"status":         utilitiesModels.DownloadStatusDone,
			"progress":       d.Progress,
			"extracted_path": d.ExtractedPath,
		}).Error
}

func (s *Service) failDownload(d *utilitiesModels.Downloads, cause error) error {
	d.Status = "failed"
	d.Error = cause.Error()
	return s.DB.Model(d).Select("Status", "Error").
		Updates(map[string]any{"status": d.Status, "error": d.Error}).Error
}

func (s *Service) SyncDownloadProgress() error {
	var downloads []utilitiesModels.Downloads
	if err := s.DB.
		Where("progress < 100 OR status IN (?, ?)",
			utilitiesModels.DownloadStatusPending, utilitiesModels.DownloadStatusProcessing).
		Find(&downloads).Error; err != nil {
		return err
	}

	for _, d := range downloads {
		switch d.Type {
		case utilitiesModels.DownloadTypeTorrent:
			s.syncTorrent(&d)
		case utilitiesModels.DownloadTypeHTTP:
			s.syncHTTP(&d)
		case utilitiesModels.DownloadTypePath:
			s.syncPath(&d)
		default:
			logger.L.Warn().Msgf("Unknown download type: %s", d.Type)
		}
	}
	return nil
}

func (s *Service) syncTorrent(download *utilitiesModels.Downloads) {
	t := s.BTTClient.GetTorrent(download.UUID)
	if t == nil {
		logger.L.Error().Msgf("Torrent %s not found", download.UUID)
		return
	}

	st := t.Stats()
	have, total := st.Pieces.Have, st.Pieces.Total
	if total == 0 {
		download.Progress = 0
	} else {
		download.Progress = int((have * 100) / total)
	}
	download.Size = st.Bytes.Total
	download.Name = st.Name

	if total > 0 && have == total && (download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending) {
		download.Status = utilitiesModels.DownloadStatusDone
		download.Progress = 100
	}

	s.DB.Model(download).Select("Progress", "Size", "Name", "Status").Updates(download)
}

func (s *Service) syncHTTP(download *utilitiesModels.Downloads) {
	if download == nil {
		logger.L.Error().Msg("syncHTTP: download is nil")
		return
	}

	s.httpRspMu.Lock()
	resp, ok := s.httpResponses[download.UUID]
	s.httpRspMu.Unlock()

	if ok {
		download.Progress = int(100 * resp.Progress())
		if info, err := os.Stat(resp.Filename); err == nil {
			download.Size = info.Size()
		}

		failed := false

		if resp.IsComplete() {
			if err := resp.Err(); err != nil {
				download.Error = err.Error()
				download.Status = "failed"
				failed = true
			} else if download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending {
				if s.flipToProcessing(download.ID) {
					logger.L.Debug().Msgf("syncHTTP: queued postproc job for download ID=%d", download.ID)
				} else {
					logger.L.Debug().Msgf("syncHTTP: flipToProcessing failed for download ID=%d", download.ID)
				}

				if err := db.EnqueueJSON(context.Background(), "utils-download-postproc",
					&utilitiesServiceInterfaces.DownloadPostProcPayload{ID: download.ID},
				); err != nil {
					logger.L.Error().Msgf("syncHTTP: failed to enqueue postproc job for download ID=%d: %v", download.ID, err)
				}
			}

			s.httpRspMu.Lock()
			delete(s.httpResponses, download.UUID)
			s.httpRspMu.Unlock()
		}

		if failed {
			s.DB.Model(download).Select("Progress", "Size", "Error", "Status").Updates(download)
		} else {
			s.DB.Model(download).Select("Progress", "Size", "Error").Updates(download)
		}

		return
	}

	freshWindow := time.Now().Add(-30 * time.Second)
	if download.CreatedAt.After(freshWindow) &&
		download.Status == utilitiesModels.DownloadStatusPending &&
		download.Progress == 0 {
		logger.L.Debug().Msgf(
			"syncHTTP: fresh pending HTTP download with no response yet, skipping (ID=%d)",
			download.ID,
		)
		return
	}

	staleWindow := time.Now().Add(-5 * time.Minute)
	if download.Status == utilitiesModels.DownloadStatusPending &&
		download.Progress == 0 &&
		download.CreatedAt.Before(staleWindow) {
		logger.L.Warn().Msgf(
			"syncHTTP: stale pending HTTP download with no response (ID=%d), marking failed",
			download.ID,
		)
		download.Error = "no_active_http_response"
		download.Status = "failed"
		s.DB.Model(download).Select("Error", "Status").Updates(download)
		return
	}
}

func (s *Service) syncPath(download *utilitiesModels.Downloads) {
	if download == nil {
		logger.L.Error().Msg("syncPath: download is nil")
		return
	}

	// Check if this is a stale pending path download that never got processed
	if download.Status == utilitiesModels.DownloadStatusPending {
		staleWindow := time.Now().Add(-2 * time.Minute)
		if download.CreatedAt.Before(staleWindow) {
			logger.L.Info().Msgf("syncPath: stale pending path download (ID=%d), enqueuing start job", download.ID)

			err := db.EnqueueJSON(context.Background(), "utils-download-start", &utilitiesServiceInterfaces.DownloadStartPayload{
				ID: download.ID,
			})

			if err != nil {
				logger.L.Error().Msgf("syncPath: failed to enqueue start job for download ID=%d: %v", download.ID, err)
				download.Error = "failed_to_enqueue_start_job"
				download.Status = utilitiesModels.DownloadStatusFailed
				s.DB.Model(download).Select("Error", "Status").Updates(download)
			}
		}
	}
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
		if download.UType == utilitiesModels.DownloadUTypeBase && download.ExtractedPath != "" {
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

		var dType string
		if download.Type == utilitiesModels.DownloadTypeHTTP {
			dType = "http"
		} else if download.Type == utilitiesModels.DownloadTypePath {
			dType = "path"
		} else if download.Type == utilitiesModels.DownloadTypeTorrent {
			dType = "torrent"
		}

		err := utils.DeleteFile(path.Join(config.GetDownloadsPath(dType), download.Name))
		if err != nil {
			logger.L.Debug().Msgf("Failed to delete HTTP download file: %v", err)
			return err
		}

		extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), download.UUID)
		if _, err := os.Stat(extractsPath); err == nil {
			if err := os.RemoveAll(extractsPath); err != nil {
				logger.L.Error().Msgf("Failed to remove extracts folder: %v", err)
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

	return nil
}

func (s *Service) BulkDeleteDownload(ids []int) error {
	var downloads []utilitiesModels.Downloads
	if err := s.DB.Where("id IN ?", ids).Find(&downloads).Error; err != nil {
		return err
	}

	for _, download := range downloads {
		if download.Type == "http" {
			err := utils.DeleteFile(path.Join(config.GetDownloadsPath("http"), download.Name))
			if err != nil {
				logger.L.Debug().Msgf("Failed to delete HTTP download file: %v", err)
			}

			extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), download.UUID)
			if _, err := os.Stat(extractsPath); err == nil {
				if err := os.RemoveAll(extractsPath); err != nil {
					logger.L.Error().Msgf("Failed to remove extracts folder: %v", err)
				}
			}
		}

		if download.Type == "path" {
			err := utils.DeleteFile(path.Join(config.GetDownloadsPath("path"), download.Name))
			if err != nil {
				logger.L.Debug().Msgf("Failed to delete Path download file: %v", err)
			}

			extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), download.UUID)
			if _, err := os.Stat(extractsPath); err == nil {
				if err := os.RemoveAll(extractsPath); err != nil {
					logger.L.Error().Msgf("Failed to remove extracts folder: %v", err)
				}
			}
		}

		if download.Type == "torrent" {
			err := utils.DeleteFile(download.Path)
			if err != nil {
				logger.L.Debug().Msgf("Failed to delete Torrent download file: %v", err)
			}
		}
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
