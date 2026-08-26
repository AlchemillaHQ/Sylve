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
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db"
	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/logger"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
	"github.com/alchemillahq/sylve/pkg/utils"

	"github.com/cavaliergopher/grab/v3"
	"github.com/cenkalti/rain/v2/torrent"
	"gorm.io/gorm"
)

var (
	ErrDownloadInvalid          = errors.New("invalid_download_request")
	ErrDownloadUnprocessable    = errors.New("download_request_unprocessable")
	ErrDownloadConflict         = errors.New("download_conflict")
	ErrDownloadNotFound         = errors.New("download_not_found")
	ErrDownloadActive           = errors.New("download_active")
	ErrDownloadInUse            = errors.New("download_in_use")
	ErrDownloadCleanup          = errors.New("download_cleanup_failed")
	ErrDownloadQueueUnavailable = errors.New("download_queue_unavailable")
	ErrUtilitiesNotReady        = errors.New("utilities_not_ready")
)

func (s *Service) ListDownloads() ([]utilitiesModels.Downloads, error) {
	downloads := make([]utilitiesModels.Downloads, 0)

	if err := s.DB.Preload("Files").Order("id ASC").Find(&downloads).Error; err != nil {
		logger.L.Error().Msgf("Failed to list downloads: %v", err)
		return nil, err
	}

	needsSync := false
	for i := range downloads {
		dl := &downloads[i]
		if dl.Files == nil {
			dl.Files = make([]utilitiesModels.DownloadedFile, 0)
		}
		if dl.Status == utilitiesModels.DownloadStatusPending ||
			dl.Status == utilitiesModels.DownloadStatusProcessing {
			needsSync = true
		}
	}

	if needsSync {
		s.maybeEnqueueDownloadSync()
	}

	return downloads, nil
}

func (s *Service) ListDownloadsByUType() ([]utilitiesServiceInterfaces.UTypeGroupedDownload, error) {
	downloads := make([]utilitiesModels.Downloads, 0)
	grouped := make([]utilitiesServiceInterfaces.UTypeGroupedDownload, 0)

	if err := s.DB.Order("id ASC").Find(&downloads).Error; err != nil {
		logger.L.Error().Err(err).Msg("Failed to list completed download choices")
		return grouped, err
	}

	for _, dl := range downloads {
		if dl.Status != utilitiesModels.DownloadStatusDone {
			continue
		}

		label := dl.Name

		if dl.ExtractedPath != "" {
			info, err := os.Stat(dl.ExtractedPath)
			if err == nil {
				var targetFile string
				if info.IsDir() {
					files, err := os.ReadDir(dl.ExtractedPath)
					if err == nil && len(files) == 1 {
						targetFile = files[0].Name()
					}
				} else {
					targetFile = filepath.Base(dl.ExtractedPath)
				}
				if targetFile != "" &&
					(strings.HasSuffix(targetFile, ".raw") ||
						strings.HasSuffix(targetFile, ".img") ||
						strings.HasSuffix(targetFile, ".disk") ||
						strings.HasSuffix(targetFile, ".iso")) {
					label = fmt.Sprintf("%s@@@%s", dl.Name, targetFile)
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
	if download.Files == nil {
		download.Files = make([]utilitiesModels.DownloadedFile, 0)
	}

	return &download, nil
}

func (s *Service) GetDownloadByID(id uint) (*utilitiesModels.Downloads, error) {
	var download utilitiesModels.Downloads
	if err := s.DB.Preload("Files").Where("id = ?", id).First(&download).Error; err != nil {
		logger.L.Error().Msgf("Failed to get download by ID: %v", err)
		return nil, err
	}
	if download.Files == nil {
		download.Files = make([]utilitiesModels.DownloadedFile, 0)
	}

	return &download, nil
}

func (s *Service) DownloadFile(req utilitiesServiceInterfaces.DownloadFileRequest) (uint, error) {
	source := strings.TrimSpace(req.URL)
	if source == "" {
		return 0, fmt.Errorf("%w: source is required", ErrDownloadInvalid)
	}
	if !validDownloaderUploadType(req.DownloadType) {
		return 0, fmt.Errorf("%w: unsupported download type", ErrDownloadUnprocessable)
	}

	requestedName := ""
	if req.Filename != nil {
		requestedName = strings.TrimSpace(*req.Filename)
		if requestedName == "" {
			return 0, fmt.Errorf("%w: filename cannot be blank", ErrDownloadUnprocessable)
		}
		if err := utils.IsValidFilename(requestedName); err != nil {
			return 0, fmt.Errorf("%w: invalid filename", ErrDownloadUnprocessable)
		}
	}

	automaticExtraction := req.AutomaticExtraction != nil && *req.AutomaticExtraction
	automaticRawConversion := req.AutomaticRawConversion != nil && *req.AutomaticRawConversion
	ignoreTLS := req.IgnoreTLS != nil && *req.IgnoreTLS

	download := utilitiesModels.Downloads{
		URL:                    source,
		UUID:                   utils.GenerateDeterministicUUID(source),
		Name:                   requestedName,
		Progress:               0,
		Size:                   0,
		Files:                  make([]utilitiesModels.DownloadedFile, 0),
		UType:                  req.DownloadType,
		Status:                 utilitiesModels.DownloadStatusPending,
		AutomaticExtraction:    automaticExtraction,
		AutomaticRawConversion: automaticRawConversion,
		IgnoreTLS:              ignoreTLS,
	}

	switch {
	case utils.IsMagnetURI(source):
		if _, err := s.activeTorrentClient(); err != nil {
			return 0, err
		}
		if automaticExtraction || automaticRawConversion {
			return 0, fmt.Errorf("%w: torrent post-processing is not supported", ErrDownloadUnprocessable)
		}
		download.Type = utilitiesModels.DownloadTypeTorrent
		download.Path = filepath.Join("/non-existent", download.UUID)
		download.IgnoreTLS = false

	case isHTTPDownloadSource(source):
		parsed, err := url.Parse(source)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid HTTP URL", ErrDownloadUnprocessable)
		}
		if download.Name == "" {
			download.Name, err = filenameFromDownloadURL(parsed)
			if err != nil {
				return 0, err
			}
		}
		download.Type = utilitiesModels.DownloadTypeHTTP
		download.Path = filepath.Clean(filepath.Join(config.GetDownloadsPath("http"), download.Name))

	case filepath.IsAbs(source):
		sourcePath := filepath.Clean(source)
		info, err := os.Stat(sourcePath)
		if err != nil {
			return 0, fmt.Errorf("%w: source file is unavailable", ErrDownloadUnprocessable)
		}
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("%w: source is not a regular file", ErrDownloadUnprocessable)
		}
		file, err := os.Open(sourcePath)
		if err != nil {
			return 0, fmt.Errorf("%w: source file is not readable", ErrDownloadUnprocessable)
		}
		if err := file.Close(); err != nil {
			return 0, fmt.Errorf("inspect source file: %w", err)
		}
		if download.Name == "" {
			download.Name = filepath.Base(sourcePath)
			if err := utils.IsValidFilename(download.Name); err != nil {
				return 0, fmt.Errorf("%w: invalid inferred filename", ErrDownloadUnprocessable)
			}
		}
		download.URL = sourcePath
		download.Type = utilitiesModels.DownloadTypePath
		download.Path = filepath.Clean(filepath.Join(config.GetDownloadsPath("path"), download.Name))
		download.IgnoreTLS = false

	default:
		return 0, fmt.Errorf("%w: use a magnet URI, HTTP(S) URL, or absolute path", ErrDownloadUnprocessable)
	}

	if err := ValidateDownloaderPostProcessOptions(
		filepath.Base(download.Path),
		download.AutomaticExtraction,
		download.AutomaticRawConversion,
	); err != nil {
		return 0, err
	}
	if download.Type != utilitiesModels.DownloadTypeTorrent && download.URL != download.Path {
		if err := ensureDownloadDestinationAvailable(download.Path); err != nil {
			return 0, err
		}
	}

	var collisionCount int64
	if err := s.DB.Model(&utilitiesModels.Downloads{}).
		Where("url = ? OR path = ?", download.URL, download.Path).
		Count(&collisionCount).Error; err != nil {
		return 0, fmt.Errorf("check download identity: %w", err)
	}
	if collisionCount > 0 {
		return 0, ErrDownloadConflict
	}

	if err := s.DB.Create(&download).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return 0, ErrDownloadConflict
		}
		return 0, fmt.Errorf("create download: %w", err)
	}

	if err := s.enqueueDownloadStartOnce(context.Background(), utilitiesServiceInterfaces.DownloadStartPayload{
		ID: download.ID,
	}); err != nil {
		logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to enqueue download start job")
		if updateErr := s.DB.Model(&download).UpdateColumn("error", ErrDownloadQueueUnavailable.Error()).Error; updateErr != nil {
			logger.L.Error().Uint("download_id", download.ID).Err(updateErr).Msg("Failed to persist queue error")
		}
		return download.ID, fmt.Errorf("%w: %v", ErrDownloadQueueUnavailable, err)
	}

	return download.ID, nil
}

func isHTTPDownloadSource(source string) bool {
	parsed, err := url.ParseRequestURI(source)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func filenameFromDownloadURL(parsed *url.URL) (string, error) {
	if parsed == nil {
		return "", fmt.Errorf("%w: invalid HTTP URL", ErrDownloadUnprocessable)
	}
	name, err := url.PathUnescape(path.Base(parsed.EscapedPath()))
	if err != nil {
		return "", fmt.Errorf("%w: invalid URL filename", ErrDownloadUnprocessable)
	}
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "_")
	if err := utils.IsValidFilename(name); err != nil {
		return "", fmt.Errorf("%w: URL requires an explicit filename", ErrDownloadUnprocessable)
	}
	return name, nil
}

func ensureDownloadDestinationAvailable(destination string) error {
	_, err := os.Lstat(destination)
	switch {
	case err == nil:
		return ErrDownloadConflict
	case os.IsNotExist(err):
		return nil
	default:
		return fmt.Errorf("inspect download destination: %w", err)
	}
}

func (s *Service) StartDownload(id *uint) error {
	if id == nil {
		return fmt.Errorf("download_is_nil")
	}
	if !s.beginDownloadStart(*id) {
		return nil
	}
	defer s.endDownloadStart(*id)

	download, err := s.GetDownloadByID(*id)
	if err != nil {
		logger.L.Error().Uint("download_id", *id).Err(err).Msg("GetDownloadByID failed")
		return err
	}
	if download.Status == utilitiesModels.DownloadStatusDone ||
		download.Status == utilitiesModels.DownloadStatusFailed {
		return nil
	}
	if download.Status == utilitiesModels.DownloadStatusProcessing &&
		(download.Type == utilitiesModels.DownloadTypeHTTP || download.Type == utilitiesModels.DownloadTypePath) {
		info, err := os.Stat(download.Path)
		if err != nil || !info.Mode().IsRegular() {
			return s.failDownload(download, fmt.Errorf("download_payload_unavailable"))
		}
		if !download.AutomaticExtraction && !download.AutomaticRawConversion {
			return s.finishDownload(download, download.ExtractedPath)
		}
		return s.enqueuePostProcOnce(download.ID)
	}

	switch download.Type {
	case utilitiesModels.DownloadTypeTorrent:
		client, err := s.activeTorrentClient()
		if err != nil {
			return err
		}
		if !utils.IsMagnetURI(download.URL) {
			return s.failDownload(download, fmt.Errorf("invalid_torrent_source"))
		}
		torrentOpts := torrent.AddTorrentOptions{
			ID:                utils.GenerateDeterministicUUID(download.URL),
			StopAfterDownload: false,
		}

		t, err := client.AddURI(download.URL, &torrentOpts)
		if err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("Failed to add torrent")
			download.Status = utilitiesModels.DownloadStatusFailed
			download.Error = err.Error()
			if saveErr := s.DB.Save(download).Error; saveErr != nil {
				logger.L.Error().Uint("download_id", *id).Err(saveErr).Msg("Failed to persist failed status")
			}
			if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", err.Error(), map[string]any{
					"downloadId": download.ID,
					"status":     "failed",
					"error":      err.Error(),
				})
			}
			return err
		}

		download.UUID = t.ID()
		download.Path = t.Dir()
		download.Name = t.Name()
		download.Error = ""

		if err := s.DB.Save(download).Error; err != nil {
			logger.L.Error().Uint("download_id", *id).Err(err).Msg("failed_to_update_download_record")
			return fmt.Errorf("failed_to_update_download_record: %w", err)
		}
	case utilitiesModels.DownloadTypeHTTP:
		if !isHTTPDownloadSource(download.URL) {
			return s.failDownload(download, fmt.Errorf("invalid_http_source"))
		}
		if download.Error != "" {
			if err := s.DB.Model(download).UpdateColumn("error", "").Error; err != nil {
				return fmt.Errorf("clear_download_error: %w", err)
			}
			download.Error = ""
		}
		if download.Progress == 0 {
			if err := ensureDownloadDestinationAvailable(download.Path); err != nil {
				return s.failDownload(download, err)
			}
		}
		req, err := grab.NewRequest(download.Path, download.URL)
		if err != nil {
			return s.failDownload(download, fmt.Errorf("create_http_request: %w", err))
		}

		var resp *grab.Response

		if download.IgnoreTLS {
			resp = s.GrabInsecure.Do(req)
		} else {
			resp = s.GrabClient.Do(req)
		}

		s.httpRspMu.Lock()
		s.httpResponses[download.UUID] = resp
		s.httpRspMu.Unlock()

	case utilitiesModels.DownloadTypePath:
		destPath := filepath.Clean(download.Path)
		sourcePath := filepath.Clean(download.URL)
		if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(destPath) {
			return s.failDownload(download, fmt.Errorf("invalid_path_source"))
		}

		// If source is already the final destination, avoid self-copy/truncation.
		if sourcePath != destPath {
			publishedUpload, err := s.publishCompletedDownloaderUpload(sourcePath, destPath)
			if err == nil && !publishedUpload {
				err = copyFileNoReplace(sourcePath, destPath)
			}
			if err != nil {
				logger.L.Error().Uint("download_id", *id).Err(err).Msg("path_publish_failed")
				download.Status = utilitiesModels.DownloadStatusFailed
				download.Error = err.Error()
				s.DB.Model(download).Select("Status", "Error").Updates(map[string]any{
					"status": download.Status,
					"error":  download.Error,
				})
				if s.TelemetryDB != nil {
					db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", err.Error(), map[string]any{
						"downloadId": download.ID,
						"status":     "failed",
						"error":      err.Error(),
					})
				}
				return fmt.Errorf("path_publish_failed: %w", err)
			}
		} else {
			if _, err := os.Stat(destPath); err != nil {
				logger.L.Error().Uint("download_id", *id).Err(err).Msg("path_source_missing")
				download.Status = utilitiesModels.DownloadStatusFailed
				download.Error = err.Error()
				s.DB.Model(download).Select("Status", "Error").Updates(map[string]any{
					"status": download.Status,
					"error":  download.Error,
				})
				if s.TelemetryDB != nil {
					db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", err.Error(), map[string]any{
						"downloadId": download.ID,
						"status":     "failed",
						"error":      err.Error(),
					})
				}
				return fmt.Errorf("path_source_missing: %w", err)
			}
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
			if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", err.Error(), map[string]any{
					"downloadId": download.ID,
					"status":     "failed",
					"error":      err.Error(),
				})
			}
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

		if sourcePath != destPath {
			if err := s.removeCompletedDownloaderUploadSource(sourcePath); err != nil {
				logger.L.Warn().Uint("download_id", *id).Err(err).Msg("failed_to_remove_completed_upload_source")
			}
		}

		if download.Status == utilitiesModels.DownloadStatusDone && s.TelemetryDB != nil {
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "success", "", map[string]any{
				"downloadId": download.ID,
				"status":     "success",
			})
		}

		if needPostProc {
			if err := s.enqueuePostProcOnce(download.ID); err != nil {
				logger.L.Error().Uint("download_id", *id).Err(err).Msg("failed_to_enqueue_postproc")
				return fmt.Errorf("failed_to_enqueue_postproc: %w", err)
			}
		}

	default:
		return s.failDownload(download, fmt.Errorf("unsupported_download_type"))
	}

	return nil
}

func (s *Service) StartPostProcess(id *uint) (resultErr error) {
	if id == nil {
		return fmt.Errorf("download_is_nil")
	}

	defer func() {
		if resultErr != nil {
			return
		}
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
	if err := ValidateDownloaderPostProcessOptions(
		filepath.Base(d.Path),
		d.AutomaticExtraction,
		d.AutomaticRawConversion,
	); err != nil {
		return s.failDownload(&d, err)
	}

	var extractedPath string

	if d.AutomaticExtraction {
		extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), d.UUID)
		if err := utils.ResetDir(extractsPath); err != nil {
			return s.failDownload(&d, fmt.Errorf("reset extracts: %w", err))
		}

		mime, kind, err := utils.SniffMIME(d.Path)
		logger.L.Debug().Msgf("postproc sniff id=%d mime=%s ext=%s kind=%+v err=%v", d.ID, mime, kind.Extension, kind, err)
		if err != nil {
			logger.L.Warn().Msgf("sniff failed (%s): %v", d.Path, err)
			return s.failDownload(&d, fmt.Errorf("%w: %v", ErrDownloaderExtractionFormat, err))
		}

		if mime == "application/x-tar" || utils.IsTarLike(d.Path, mime) {
			if d.AutomaticRawConversion {
				return s.failDownload(&d, ErrDownloaderPostProcessOptions)
			}

			// We're using --no-xattrs to handle cross-platform rootfs extraction (e.g., Linux rootfs on FreeBSD)
			if out, err := utils.RunCommand("/usr/bin/tar", "--no-xattrs", "-xf", d.Path, "-C", extractsPath); err != nil {
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

		if files, _ := os.ReadDir(extractsPath); len(files) == 1 {
			d.ExtractedPath = filepath.Join(extractsPath, files[0].Name())
		} else {
			d.ExtractedPath = extractsPath
		}

		extractedPath = d.ExtractedPath
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

func (s *Service) enqueuePostProcOnce(id uint) error {
	s.downloadStartRunMu.Lock()
	if _, deleting := s.downloadDeleting[id]; deleting {
		s.downloadStartRunMu.Unlock()
		return ErrDownloadActive
	}
	s.inflightMu.Lock()
	if _, ok := s.inflight[id]; ok {
		s.inflightMu.Unlock()
		s.downloadStartRunMu.Unlock()
		return nil
	}
	s.inflight[id] = struct{}{}
	s.inflightMu.Unlock()
	s.downloadStartRunMu.Unlock()

	err := db.EnqueueJSON(context.Background(), "utils-download-postproc", &utilitiesServiceInterfaces.DownloadPostProcPayload{
		ID: id,
	})
	if err != nil {
		s.inflightMu.Lock()
		delete(s.inflight, id)
		s.inflightMu.Unlock()
		return err
	}

	return nil
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

	err := s.DB.Model(d).Select("Status", "Progress", "ExtractedPath", "Name", "Path").
		Updates(map[string]any{
			"name":           d.Name,
			"path":           d.Path,
			"status":         utilitiesModels.DownloadStatusDone,
			"progress":       d.Progress,
			"extracted_path": d.ExtractedPath,
		}).Error

	if err == nil && s.TelemetryDB != nil {
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", d.ID, "success", "", map[string]any{
			"downloadId": d.ID,
			"status":     "success",
		})
	}

	return err
}

func (s *Service) failDownload(d *utilitiesModels.Downloads, cause error) error {
	d.Status = "failed"
	d.Error = cause.Error()
	err := s.DB.Model(d).Select("Status", "Error").
		Updates(map[string]any{"status": d.Status, "error": d.Error}).Error

	if err == nil && s.TelemetryDB != nil {
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", d.ID, "failed", cause.Error(), map[string]any{
			"downloadId": d.ID,
			"status":     "failed",
			"error":      cause.Error(),
		})
	}

	return err
}

func (s *Service) UpdateDownload(
	id uint,
	req utilitiesServiceInterfaces.UpdateDownloadRequest,
) (*utilitiesModels.Downloads, error) {
	if id == 0 {
		return nil, ErrDownloadInvalid
	}
	if req.Name == nil && req.UType == nil && req.AutomaticExtraction == nil &&
		req.AutomaticRawConversion == nil {
		return nil, fmt.Errorf("%w: at least one field is required", ErrDownloadInvalid)
	}

	var d utilitiesModels.Downloads
	if err := s.DB.First(&d, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDownloadNotFound
		}
		return nil, fmt.Errorf("load download: %w", err)
	}
	if d.Status == utilitiesModels.DownloadStatusPending ||
		d.Status == utilitiesModels.DownloadStatusProcessing {
		return nil, ErrDownloadActive
	}

	effectiveName := d.Name
	if req.Name != nil {
		effectiveName = strings.TrimSpace(*req.Name)
		if err := utils.IsValidFilename(effectiveName); err != nil {
			return nil, fmt.Errorf("%w: invalid filename", ErrDownloadUnprocessable)
		}
	}
	if req.UType != nil && !validDownloaderUploadType(*req.UType) {
		return nil, fmt.Errorf("%w: unsupported download type", ErrDownloadUnprocessable)
	}
	effectiveExtraction := d.AutomaticExtraction
	if req.AutomaticExtraction != nil {
		effectiveExtraction = *req.AutomaticExtraction
	}
	effectiveRawConversion := d.AutomaticRawConversion
	if req.AutomaticRawConversion != nil {
		effectiveRawConversion = *req.AutomaticRawConversion
	}
	if d.Type == utilitiesModels.DownloadTypeTorrent &&
		(effectiveExtraction || effectiveRawConversion) {
		return nil, fmt.Errorf("%w: torrent post-processing is not supported", ErrDownloadUnprocessable)
	}
	if err := ValidateDownloaderPostProcessOptions(
		filepath.Base(d.Path),
		effectiveExtraction,
		effectiveRawConversion,
	); err != nil {
		return nil, err
	}

	updates := map[string]any{}

	if req.Name != nil {
		updates["name"] = effectiveName
	}
	if req.UType != nil {
		updates["u_type"] = *req.UType
	}

	needPostProc := d.ExtractedPath == "" && ((req.AutomaticExtraction != nil && *req.AutomaticExtraction && !d.AutomaticExtraction) ||
		(req.AutomaticRawConversion != nil && *req.AutomaticRawConversion && !d.AutomaticRawConversion))

	if req.AutomaticExtraction != nil {
		updates["automatic_extraction"] = *req.AutomaticExtraction
	}
	if req.AutomaticRawConversion != nil {
		updates["automatic_raw_conversion"] = *req.AutomaticRawConversion
	}

	if needPostProc && d.Status != utilitiesModels.DownloadStatusDone {
		return nil, fmt.Errorf("%w: post-processing requires a completed download", ErrDownloadActive)
	}
	if needPostProc {
		updates["status"] = utilitiesModels.DownloadStatusProcessing
		updates["error"] = ""
	}

	if err := s.DB.Model(&d).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update download: %w", err)
	}

	if needPostProc {
		if err := s.enqueuePostProcOnce(d.ID); err != nil {
			compensation := map[string]any{
				"name":                     d.Name,
				"u_type":                   d.UType,
				"automatic_extraction":     d.AutomaticExtraction,
				"automatic_raw_conversion": d.AutomaticRawConversion,
				"status":                   d.Status,
				"error":                    d.Error,
			}
			if restoreErr := s.DB.Model(&d).Updates(compensation).Error; restoreErr != nil {
				logger.L.Error().Uint("download_id", d.ID).Err(restoreErr).Msg("Failed to compensate download update")
			}
			return nil, fmt.Errorf("%w: %v", ErrDownloadQueueUnavailable, err)
		}
	}

	var updated utilitiesModels.Downloads
	if err := s.DB.Preload("Files").First(&updated, id).Error; err != nil {
		return nil, fmt.Errorf("reload download: %w", err)
	}
	if updated.Files == nil {
		updated.Files = make([]utilitiesModels.DownloadedFile, 0)
	}
	return &updated, nil
}

func (s *Service) SyncDownloadProgress() error {
	s.downloadSyncRunMu.Lock()
	defer s.downloadSyncRunMu.Unlock()

	var downloads []utilitiesModels.Downloads
	if err := s.DB.
		Where("progress < 100 OR status IN (?, ?)",
			utilitiesModels.DownloadStatusPending, utilitiesModels.DownloadStatusProcessing).
		Find(&downloads).Error; err != nil {
		return err
	}

	for _, d := range downloads {
		if s.isDownloadDeleting(d.ID) {
			continue
		}
		switch d.Type {
		case utilitiesModels.DownloadTypeTorrent:
			client, err := s.activeTorrentClient()
			if err != nil {
				return err
			}
			s.syncTorrent(client, &d)
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

func (s *Service) syncTorrent(client torrentRuntime, download *utilitiesModels.Downloads) {
	if download.Status == utilitiesModels.DownloadStatusDone {
		return
	}

	t := client.GetTorrent(download.UUID)
	if t == nil {
		if download.Status == utilitiesModels.DownloadStatusPending ||
			download.Status == utilitiesModels.DownloadStatusProcessing {
			s.maybeRecoverDownloadStart(download)
		}
		return
	}

	st := t.Stats()
	have, total := st.Pieces.Have, st.Pieces.Total

	if total == 0 {
		staleWindow := time.Now().Add(-15 * time.Minute)
		if download.UpdatedAt.Before(staleWindow) {
			download.Error = "magnet_metadata_timeout"
			download.Status = utilitiesModels.DownloadStatusFailed
			s.DB.Model(download).Select("Error", "Status").Updates(download)
			if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", download.Error, map[string]any{
					"downloadId": download.ID,
					"status":     "failed",
					"error":      download.Error,
				})
			}
			client.RemoveTorrent(download.UUID, true)
			return
		}
		download.Progress = 0
	} else {
		download.Progress = int((have * 100) / total)
	}
	download.Size = st.Bytes.Total
	download.Name = st.Name

	isFinished := total > 0 && have == total

	if isFinished {
		if err := s.persistCompletedTorrent(download, t); err != nil {
			logger.L.Error().Err(err).Msgf("Failed to persist completed torrent %s", download.UUID)
			return
		}
		if err := client.RemoveTorrent(download.UUID, true); err != nil {
			logger.L.Error().Err(err).Msgf("Failed to remove completed torrent %s", download.UUID)
		}
		if s.TelemetryDB != nil {
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "success", "", map[string]any{
				"downloadId": download.ID,
				"status":     "success",
			})
		}
		return
	}

	download.Status = utilitiesModels.DownloadStatusProcessing
	err := s.DB.Model(download).
		Select("Progress", "Size", "Name", "Status").
		Updates(download).Error

	if err != nil {
		logger.L.Error().Err(err).Msgf("Failed to update database for download %s", download.UUID)
	}
}

func (s *Service) persistCompletedTorrent(
	download *utilitiesModels.Downloads,
	t *torrent.Torrent,
) error {
	if download == nil || t == nil {
		return errors.New("completed torrent is unavailable")
	}

	files, err := t.Files()
	if err != nil {
		return fmt.Errorf("read completed torrent files: %w", err)
	}
	catalog := make([]completedTorrentFile, 0, len(files))
	for _, file := range files {
		catalog = append(catalog, completedTorrentFile{
			Path: file.Path(),
			Size: file.Length(),
		})
	}
	return s.persistCompletedTorrentFiles(download, catalog)
}

type completedTorrentFile struct {
	Path string
	Size int64
}

func (s *Service) persistCompletedTorrentFiles(
	download *utilitiesModels.Downloads,
	files []completedTorrentFile,
) error {
	if download == nil {
		return errors.New("completed torrent is unavailable")
	}
	if len(files) == 0 {
		return errors.New("completed torrent contains no files")
	}

	torrentRoot := filepath.Clean(config.GetDownloadsPath("torrents"))
	downloadRoot := filepath.Clean(filepath.Join(torrentRoot, download.UUID))
	if !managedDescendant(torrentRoot, downloadRoot) || filepath.Clean(download.Path) != downloadRoot {
		return errors.New("completed torrent has an unsafe path")
	}

	rows := make([]utilitiesModels.DownloadedFile, 0, len(files))
	for _, file := range files {
		relativePath := filepath.Clean(file.Path)
		if relativePath == "." || relativePath == ".." || filepath.IsAbs(relativePath) ||
			strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return errors.New("completed torrent contains an unsafe file path")
		}
		candidate := filepath.Clean(filepath.Join(downloadRoot, relativePath))
		if !managedDescendant(downloadRoot, candidate) {
			return errors.New("completed torrent file escapes its managed root")
		}
		resolvedCandidate, err := resolveSignedDownloadPath(downloadRoot, candidate)
		if err != nil {
			return fmt.Errorf("inspect completed torrent file: %w", err)
		}
		info, err := os.Stat(resolvedCandidate)
		if err != nil {
			return fmt.Errorf("stat completed torrent file: %w", err)
		}

		rows = append(rows, utilitiesModels.DownloadedFile{
			DownloadID: int(download.ID),
			Name:       relativePath,
			Size:       info.Size(),
		})
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("download_id = ?", download.ID).
			Delete(&utilitiesModels.DownloadedFile{}).Error; err != nil {
			return fmt.Errorf("replace completed torrent file catalog: %w", err)
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("persist completed torrent file catalog: %w", err)
		}

		result := tx.Model(&utilitiesModels.Downloads{}).
			Where("id = ? AND status IN (?, ?)",
				download.ID,
				utilitiesModels.DownloadStatusPending,
				utilitiesModels.DownloadStatusProcessing,
			).
			Updates(map[string]any{
				"error":    "",
				"name":     download.Name,
				"progress": 100,
				"size":     download.Size,
				"status":   utilitiesModels.DownloadStatusDone,
			})
		if result.Error != nil {
			return fmt.Errorf("persist completed torrent state: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return errors.New("completed torrent state changed")
		}
		return nil
	})
	if err != nil {
		return err
	}

	download.Error = ""
	download.Progress = 100
	download.Status = utilitiesModels.DownloadStatusDone
	return nil
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
			} else if download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending ||
				download.Status == utilitiesModels.DownloadStatusProcessing {
				if download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending {
					if s.flipToProcessing(download.ID) {
						logger.L.Debug().Msgf("syncHTTP: flipped to processing for download ID=%d", download.ID)
					} else {
						logger.L.Debug().Msgf("syncHTTP: flipToProcessing failed for download ID=%d", download.ID)
					}
				}

				if err := s.enqueuePostProcOnce(download.ID); err != nil {
					logger.L.Error().Msgf("syncHTTP: failed to enqueue postproc job for download ID=%d: %v", download.ID, err)
				}
			}

			s.httpRspMu.Lock()
			delete(s.httpResponses, download.UUID)
			s.httpRspMu.Unlock()
		}

		if failed {
			s.DB.Model(download).Select("Progress", "Size", "Error", "Status").Updates(download)
			if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "failed", download.Error, map[string]any{
					"downloadId": download.ID,
					"status":     "failed",
					"error":      download.Error,
				})
			}
		} else {
			s.DB.Model(download).Select("Progress", "Size", "Error").Updates(download)
		}

		return
	}

	if download.Status == utilitiesModels.DownloadStatusPending {
		s.maybeRecoverDownloadStart(download)
		return
	}

	// Recover processing records that lost in-memory HTTP response state (e.g. after restart).
	if download.Status == utilitiesModels.DownloadStatusProcessing {
		info, err := os.Stat(download.Path)
		if err != nil || !info.Mode().IsRegular() {
			if failErr := s.failDownload(download, fmt.Errorf("download_payload_unavailable")); failErr != nil {
				logger.L.Error().Uint("download_id", download.ID).Err(failErr).Msg("Failed to mark missing HTTP payload")
			}
			return
		}
		download.Size = info.Size()

		if !download.AutomaticExtraction && !download.AutomaticRawConversion {
			download.Progress = 100
			download.Status = utilitiesModels.DownloadStatusDone
			if err := s.DB.Model(download).Select("Progress", "Size", "Status").Updates(download).Error; err != nil {
				logger.L.Error().Msgf("syncHTTP: failed to finalize processing download ID=%d: %v", download.ID, err)
			}
			if s.TelemetryDB != nil {
				db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "success", "", map[string]any{
					"downloadId": download.ID,
					"status":     "success",
				})
			}
			return
		}

		if err := s.enqueuePostProcOnce(download.ID); err != nil {
			logger.L.Error().Msgf("syncHTTP: failed to recover postproc enqueue for download ID=%d: %v", download.ID, err)
		}
	}
}

func (s *Service) syncPath(download *utilitiesModels.Downloads) {
	if download == nil {
		logger.L.Error().Msg("syncPath: download is nil")
		return
	}

	if download.Status == utilitiesModels.DownloadStatusPending {
		s.maybeRecoverDownloadStart(download)
		return
	}

	if download.Status == utilitiesModels.DownloadStatusProcessing {
		info, err := os.Stat(download.Path)
		if err != nil || !info.Mode().IsRegular() {
			if failErr := s.failDownload(download, fmt.Errorf("download_payload_unavailable")); failErr != nil {
				logger.L.Error().Uint("download_id", download.ID).Err(failErr).Msg("Failed to mark missing path payload")
			}
			return
		}
		if !download.AutomaticExtraction && !download.AutomaticRawConversion {
			if err := s.finishDownload(download, download.ExtractedPath); err != nil {
				logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to finish path download")
			}
			return
		}
		if err := s.enqueuePostProcOnce(download.ID); err != nil {
			logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to recover path post-processing")
		}
	}
}

func (s *Service) maybeRecoverDownloadStart(download *utilitiesModels.Downloads) {
	if download == nil || s.isDownloadStartRunning(download.ID) || s.isDownloadDeleting(download.ID) {
		return
	}

	lastChange := download.UpdatedAt
	if lastChange.IsZero() {
		lastChange = download.CreatedAt
	}
	queueFailed := download.Error == ErrDownloadQueueUnavailable.Error()
	if !queueFailed && lastChange.After(time.Now().Add(-30*time.Second)) {
		return
	}

	if err := s.enqueueDownloadStartOnce(context.Background(), utilitiesServiceInterfaces.DownloadStartPayload{
		ID: download.ID,
	}); err != nil {
		logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to recover download start job")
		download.Error = ErrDownloadQueueUnavailable.Error()
		if updateErr := s.DB.Model(download).UpdateColumn("error", download.Error).Error; updateErr != nil {
			logger.L.Error().Uint("download_id", download.ID).Err(updateErr).Msg("Failed to persist download recovery error")
		}
		return
	}

	if download.Error != "" {
		download.Error = ""
		if err := s.DB.Model(download).UpdateColumn("error", "").Error; err != nil {
			logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to clear download recovery error")
		}
	}
}

type downloadCleanupPath struct {
	Path       string
	Recursive  bool
	ClearFlags bool
}

type downloadCleanupPlan struct {
	Paths []downloadCleanupPath
}

type downloadVMReferenceRow struct {
	DownloadUUID string `gorm:"column:download_uuid"`
	StorageID    uint   `gorm:"column:storage_id"`
	VMID         uint   `gorm:"column:vm_id"`
	VMRID        uint   `gorm:"column:vm_rid"`
	VMName       string `gorm:"column:vm_name"`
}

func newDownloadDeleteResult() utilitiesServiceInterfaces.DownloadDeleteResult {
	return utilitiesServiceInterfaces.DownloadDeleteResult{
		Deleted: make([]utilitiesServiceInterfaces.DownloadDeleteItem, 0),
		Failed:  make([]utilitiesServiceInterfaces.DownloadDeleteFailure, 0),
	}
}

func downloadDeleteItem(download utilitiesModels.Downloads) utilitiesServiceInterfaces.DownloadDeleteItem {
	return utilitiesServiceInterfaces.DownloadDeleteItem{
		ID:   download.ID,
		UUID: download.UUID,
		Name: download.Name,
		Type: download.Type,
	}
}

func downloadDeleteFailure(
	download *utilitiesModels.Downloads,
	id uint,
	code string,
) utilitiesServiceInterfaces.DownloadDeleteFailure {
	failure := utilitiesServiceInterfaces.DownloadDeleteFailure{
		ID:            id,
		Code:          code,
		RetainedPaths: make([]string, 0),
		VMReferences:  make([]utilitiesServiceInterfaces.DownloadDeleteVMReference, 0),
	}
	if download != nil {
		failure.ID = download.ID
		failure.UUID = download.UUID
		failure.Name = download.Name
		failure.Type = download.Type
	}
	return failure
}

func managedDescendant(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func managedPathAtOrBelow(root, candidate string) bool {
	return filepath.Clean(root) == filepath.Clean(candidate) || managedDescendant(root, candidate)
}

func addDownloadCleanupPath(
	plan *downloadCleanupPlan,
	path string,
	recursive bool,
	clearFlags bool,
) error {
	path = filepath.Clean(path)
	for _, existing := range plan.Paths {
		if existing.Path == path || (existing.Recursive && managedDescendant(existing.Path, path)) {
			return nil
		}
	}

	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect managed cleanup path: %w", err)
	}
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed cleanup path is a symbolic link")
		}
		if recursive && !info.IsDir() {
			return fmt.Errorf("managed cleanup path is not a directory")
		}
		if !recursive && !info.Mode().IsRegular() {
			return fmt.Errorf("managed cleanup path is not a regular file")
		}
	}

	if recursive {
		filtered := make([]downloadCleanupPath, 0, len(plan.Paths)+1)
		for _, existing := range plan.Paths {
			if managedDescendant(path, existing.Path) {
				continue
			}
			filtered = append(filtered, existing)
		}
		plan.Paths = filtered
	}
	plan.Paths = append(plan.Paths, downloadCleanupPath{
		Path:       path,
		Recursive:  recursive,
		ClearFlags: clearFlags,
	})
	return nil
}

func buildDownloadCleanupPlan(download utilitiesModels.Downloads) (downloadCleanupPlan, error) {
	plan := downloadCleanupPlan{Paths: make([]downloadCleanupPath, 0, 2)}
	extractedBase := filepath.Clean(config.GetDownloadsPath("extracted"))
	extractedRoot := filepath.Clean(filepath.Join(extractedBase, download.UUID))
	if !managedDescendant(extractedBase, extractedRoot) {
		return plan, fmt.Errorf("unsafe extracted download path")
	}
	if err := addDownloadCleanupPath(&plan, extractedRoot, true, true); err != nil {
		return plan, err
	}

	switch download.Type {
	case utilitiesModels.DownloadTypeHTTP, utilitiesModels.DownloadTypePath:
		managedRootName := "http"
		if download.Type == utilitiesModels.DownloadTypePath {
			managedRootName = "path"
		}
		managedRoot := filepath.Clean(config.GetDownloadsPath(managedRootName))
		primaryPath := filepath.Clean(download.Path)
		if !managedDescendant(managedRoot, primaryPath) && !managedDescendant(extractedRoot, primaryPath) {
			return plan, fmt.Errorf("unsafe managed download path")
		}
		if err := addDownloadCleanupPath(&plan, primaryPath, false, false); err != nil {
			return plan, err
		}

		if download.ExtractedPath != "" {
			extractedPath := filepath.Clean(download.ExtractedPath)
			if extractedPath != primaryPath && !managedPathAtOrBelow(extractedRoot, extractedPath) {
				return plan, fmt.Errorf("unsafe stored extracted path")
			}
		}

	case utilitiesModels.DownloadTypeTorrent:
		torrentBase := filepath.Clean(config.GetDownloadsPath("torrents"))
		expectedPath := filepath.Clean(filepath.Join(torrentBase, download.UUID))
		if !managedDescendant(torrentBase, expectedPath) {
			return plan, fmt.Errorf("unsafe torrent download path")
		}
		placeholder := filepath.Clean(filepath.Join("/non-existent", download.UUID))
		storedPath := filepath.Clean(download.Path)
		if storedPath != placeholder {
			if storedPath != expectedPath {
				return plan, fmt.Errorf("unexpected torrent download path")
			}
			if err := addDownloadCleanupPath(&plan, storedPath, true, false); err != nil {
				return plan, err
			}
		}

	default:
		return plan, fmt.Errorf("unsupported download type")
	}

	return plan, nil
}

func retainedDownloadPaths(plan downloadCleanupPlan) []string {
	retained := make([]string, 0)
	for _, target := range plan.Paths {
		if _, err := os.Lstat(target.Path); err == nil || !errors.Is(err, os.ErrNotExist) {
			retained = append(retained, target.Path)
		}
	}
	return retained
}

func removeDownloadCleanupPath(target downloadCleanupPath) error {
	info, err := os.Lstat(target.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect cleanup target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cleanup target changed to a symbolic link")
	}

	if target.Recursive {
		if !info.IsDir() {
			return fmt.Errorf("cleanup target changed to a non-directory")
		}
		if target.ClearFlags {
			if _, statErr := os.Stat("/bin/chflags"); statErr == nil {
				if _, err := utils.RunCommand("/bin/chflags", "-R", "noschg", target.Path); err != nil {
					return fmt.Errorf("clear cleanup target flags: %w", err)
				}
			}
		}
		if err := os.RemoveAll(target.Path); err != nil {
			return fmt.Errorf("remove cleanup directory: %w", err)
		}
		return nil
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("cleanup target changed to a non-regular file")
	}
	if err := os.Remove(target.Path); err != nil {
		return fmt.Errorf("remove cleanup file: %w", err)
	}
	return nil
}

func (s *Service) findDownloadVMReferences(
	downloads []utilitiesModels.Downloads,
) (map[string][]utilitiesServiceInterfaces.DownloadDeleteVMReference, error) {
	references := make(map[string][]utilitiesServiceInterfaces.DownloadDeleteVMReference)
	uuids := make([]string, 0, len(downloads))
	for _, download := range downloads {
		if strings.TrimSpace(download.UUID) != "" {
			uuids = append(uuids, download.UUID)
		}
	}
	if len(uuids) == 0 {
		return references, nil
	}

	var rows []downloadVMReferenceRow
	if err := s.DB.Model(&vmModels.Storage{}).
		Select(
			"vm_storages.download_uuid AS download_uuid, vm_storages.id AS storage_id, "+
				"vm_storages.vm_id AS vm_id, COALESCE(vms.rid, 0) AS vm_rid, "+
				"COALESCE(vms.name, '') AS vm_name",
		).
		Joins("LEFT JOIN vms ON vms.id = vm_storages.vm_id").
		Where("vm_storages.download_uuid IN ?", uuids).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("find download VM references: %w", err)
	}

	for _, row := range rows {
		references[row.DownloadUUID] = append(
			references[row.DownloadUUID],
			utilitiesServiceInterfaces.DownloadDeleteVMReference{
				StorageID: row.StorageID,
				VMID:      row.VMID,
				VMRID:     row.VMRID,
				VMName:    row.VMName,
			},
		)
	}
	return references, nil
}

func appendDownloadReferenceFailures(
	result *utilitiesServiceInterfaces.DownloadDeleteResult,
	downloads []utilitiesModels.Downloads,
	references map[string][]utilitiesServiceInterfaces.DownloadDeleteVMReference,
) bool {
	found := false
	for i := range downloads {
		download := &downloads[i]
		vmReferences := references[download.UUID]
		if len(vmReferences) == 0 {
			continue
		}
		failure := downloadDeleteFailure(download, download.ID, "download_in_use")
		failure.VMReferences = vmReferences
		result.Failed = append(result.Failed, failure)
		found = true
	}
	return found
}

func (s *Service) stopDownloadActivity(download utilitiesModels.Downloads) error {
	if download.Type == utilitiesModels.DownloadTypeHTTP {
		s.httpRspMu.Lock()
		response := s.httpResponses[download.UUID]
		delete(s.httpResponses, download.UUID)
		s.httpRspMu.Unlock()
		if response != nil {
			if err := response.Cancel(); err != nil && !errors.Is(err, context.Canceled) {
				logger.L.Debug().Uint("download_id", download.ID).Err(err).Msg("HTTP download stopped before deletion")
			}
		}
	}

	if download.Type == utilitiesModels.DownloadTypeTorrent {
		client, err := s.activeTorrentClient()
		if errors.Is(err, ErrUtilitiesNotReady) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := client.RemoveTorrent(download.UUID, false); err != nil {
			return fmt.Errorf("stop torrent before deletion: %w", err)
		}
	}
	return nil
}

func (s *Service) deletePreparedDownload(
	download utilitiesModels.Downloads,
	plan downloadCleanupPlan,
) error {
	if err := s.stopDownloadActivity(download); err != nil {
		return err
	}
	for _, target := range plan.Paths {
		if err := removeDownloadCleanupPath(target); err != nil {
			return err
		}
	}
	if download.Type == utilitiesModels.DownloadTypePath {
		if err := s.removeCompletedDownloaderUploadSource(download.URL); err != nil {
			return fmt.Errorf("remove completed upload source: %w", err)
		}
	}

	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("download_id = ?", download.ID).
			Delete(&utilitiesModels.DownloadedFile{}).Error; err != nil {
			return fmt.Errorf("delete downloaded file rows: %w", err)
		}
		if download.Type == utilitiesModels.DownloadTypePath {
			if err := tx.Where(
				"path = ? AND scope = ? AND status = ?",
				download.URL,
				utilitiesModels.UploadScopeDownloader,
				utilitiesModels.UploadStatusCompleted,
			).Delete(&utilitiesModels.Upload{}).Error; err != nil {
				return fmt.Errorf("delete completed upload identity: %w", err)
			}
		}
		deleted := tx.Where("id = ?", download.ID).Delete(&utilitiesModels.Downloads{})
		if deleted.Error != nil {
			return fmt.Errorf("delete download row: %w", deleted.Error)
		}
		if deleted.RowsAffected != 1 {
			return ErrDownloadNotFound
		}
		return nil
	}); err != nil {
		return err
	}

	s.downloadStartQueueMu.Lock()
	delete(s.downloadStartQueued, download.ID)
	s.downloadStartQueueMu.Unlock()
	s.inflightMu.Lock()
	delete(s.inflight, download.ID)
	s.inflightMu.Unlock()

	if s.TelemetryDB != nil {
		db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", download.ID, "cancelled", "deleted_by_user", map[string]any{
			"downloadId": download.ID,
			"status":     "cancelled",
		})
	}
	return nil
}

func (s *Service) markDownloadDeleteCleanupFailed(download utilitiesModels.Downloads) {
	if err := s.DB.Model(&utilitiesModels.Downloads{}).
		Where("id = ?", download.ID).
		Updates(map[string]any{
			"status": utilitiesModels.DownloadStatusFailed,
			"error":  ErrDownloadCleanup.Error(),
		}).Error; err != nil {
		logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to persist download cleanup failure")
	}
}

func (s *Service) DeleteDownloads(
	ids []int,
) (utilitiesServiceInterfaces.DownloadDeleteResult, error) {
	result := newDownloadDeleteResult()
	if s == nil || s.DB == nil || len(ids) == 0 {
		return result, ErrDownloadInvalid
	}

	requested := make([]uint, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return result, ErrDownloadInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		requested = append(requested, uint(id))
	}
	if len(requested) == 0 {
		return result, ErrDownloadInvalid
	}

	var found []utilitiesModels.Downloads
	if err := s.DB.Where("id IN ?", requested).Find(&found).Error; err != nil {
		return result, fmt.Errorf("load downloads for deletion: %w", err)
	}
	byID := make(map[uint]utilitiesModels.Downloads, len(found))
	for _, download := range found {
		byID[download.ID] = download
	}

	downloads := make([]utilitiesModels.Downloads, 0, len(requested))
	missing := false
	for _, id := range requested {
		download, ok := byID[id]
		if !ok {
			result.Failed = append(result.Failed, downloadDeleteFailure(nil, id, "download_not_found"))
			missing = true
			continue
		}
		downloads = append(downloads, download)
	}
	if missing {
		return result, ErrDownloadNotFound
	}

	plans := make(map[uint]downloadCleanupPlan, len(downloads))
	unsafeCleanup := false
	for i := range downloads {
		download := &downloads[i]
		plan, err := buildDownloadCleanupPlan(*download)
		if err != nil {
			logger.L.Warn().Uint("download_id", download.ID).Err(err).Msg("Rejected unsafe download cleanup plan")
			failure := downloadDeleteFailure(download, download.ID, "download_cleanup_unsafe")
			for _, candidate := range []string{download.Path, download.ExtractedPath} {
				candidate = strings.TrimSpace(candidate)
				if candidate != "" && !utils.Contains(failure.RetainedPaths, candidate) {
					failure.RetainedPaths = append(failure.RetainedPaths, candidate)
				}
			}
			result.Failed = append(result.Failed, failure)
			unsafeCleanup = true
			continue
		}
		plans[download.ID] = plan
	}
	if unsafeCleanup {
		return result, ErrDownloadConflict
	}

	references, err := s.findDownloadVMReferences(downloads)
	if err != nil {
		return result, err
	}
	if appendDownloadReferenceFailures(&result, downloads, references) {
		return result, ErrDownloadInUse
	}

	acquired := make([]uint, 0, len(downloads))
	active := false
	for i := range downloads {
		download := &downloads[i]
		if !s.beginDownloadDeletion(download.ID) {
			result.Failed = append(result.Failed, downloadDeleteFailure(download, download.ID, "download_active"))
			active = true
			continue
		}
		acquired = append(acquired, download.ID)
	}
	if active {
		for _, id := range acquired {
			s.endDownloadDeletion(id)
		}
		return result, ErrDownloadActive
	}
	defer func() {
		for _, id := range acquired {
			s.endDownloadDeletion(id)
		}
	}()
	s.downloadSyncRunMu.Lock()
	defer s.downloadSyncRunMu.Unlock()

	// Narrow the window in which a VM attachment could appear after the first
	// preflight. There is intentionally no Jail guard: jail creation copies the
	// base contents and does not persist the download UUID.
	references, err = s.findDownloadVMReferences(downloads)
	if err != nil {
		return result, err
	}
	if appendDownloadReferenceFailures(&result, downloads, references) {
		return result, ErrDownloadInUse
	}

	for _, download := range downloads {
		plan := plans[download.ID]
		if err := s.deletePreparedDownload(download, plan); err != nil {
			logger.L.Error().Uint("download_id", download.ID).Err(err).Msg("Failed to delete download cleanly")
			s.markDownloadDeleteCleanupFailed(download)
			failure := downloadDeleteFailure(&download, download.ID, "download_cleanup_failed")
			failure.RetainedPaths = retainedDownloadPaths(plan)
			result.Failed = append(result.Failed, failure)
			continue
		}
		result.Deleted = append(result.Deleted, downloadDeleteItem(download))
	}
	if len(result.Failed) > 0 {
		return result, ErrDownloadCleanup
	}
	return result, nil
}

func (s *Service) DeleteDownload(id int) error {
	_, err := s.DeleteDownloads([]int{id})
	return err
}

func (s *Service) BulkDeleteDownload(ids []int) error {
	_, err := s.DeleteDownloads(ids)
	return err
}

func (s *Service) CleanupStaleAuditRecords() {
	if s.TelemetryDB == nil {
		return
	}

	var orphans []infoModels.AuditRecord
	s.TelemetryDB.
		Where("async_job_type = ? AND status = ?", "file_download", "pending").
		Find(&orphans)

	for _, rec := range orphans {
		if rec.AsyncJobID == nil {
			continue
		}
		var d utilitiesModels.Downloads
		if err := s.DB.First(&d, *rec.AsyncJobID).Error; err != nil {
			db.FinalizeAsyncAuditRecord(s.TelemetryDB, "file_download", *rec.AsyncJobID, "cancelled", "orphaned_audit_record", map[string]any{
				"downloadId": *rec.AsyncJobID,
				"status":     "cancelled",
			})
		}
	}
}
