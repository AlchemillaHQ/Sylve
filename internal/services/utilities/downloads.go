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
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
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

func (s *Service) DownloadFile(url string, optFilename string, insecureOkay bool, automaticExtraction bool, downloadType utilitiesModels.DownloadUType) error {
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
			URL:                 url,
			UUID:                t.ID(),
			Path:                t.Dir(),
			Type:                utilitiesModels.DownloadTypeTorrent,
			Name:                t.Name(),
			Size:                0,
			Progress:            0,
			Files:               []utilitiesModels.DownloadedFile{},
			Status:              utilitiesModels.DownloadStatusPending,
			AutomaticExtraction: false,
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
				URL:                 url,
				UUID:                uuid,
				Path:                filePath,
				Type:                utilitiesModels.DownloadTypeHTTP,
				Name:                filename,
				Size:                size,
				Progress:            100,
				Files:               []utilitiesModels.DownloadedFile{},
				Status:              utilitiesModels.DownloadStatusDone,
				AutomaticExtraction: automaticExtraction,
				UType:               downloadType,
			}

			if err := s.DB.Create(&download).Error; err != nil {
				return fmt.Errorf("failed_to_create_download_record: %w", err)
			}

			s.startPostProcessors(1)
			s.enqueuePost(download.ID)

			return nil
		}

		download := utilitiesModels.Downloads{
			URL:                 url,
			UUID:                uuid,
			Path:                filePath,
			Type:                utilitiesModels.DownloadTypeHTTP,
			Name:                filename,
			Size:                0,
			Progress:            0,
			Files:               []utilitiesModels.DownloadedFile{},
			Status:              utilitiesModels.DownloadStatusPending,
			AutomaticExtraction: automaticExtraction,
			UType:               downloadType,
		}

		if err := s.DB.Create(&download).Error; err != nil {
			fmt.Printf("Failed to create download record: %+v\n", err)
			return err
		}

		req, _ := grab.NewRequest(path.Join(destDir, filename), url)

		var resp *grab.Response

		if insecureOkay {
			resp = s.GrabInsecure.Do(req)
		} else {
			resp = s.GrabClient.Do(req)
		}

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
			URL:                 url,
			UUID:                utils.GenerateDeterministicUUID(url),
			Path:                destPath,
			Type:                utilitiesModels.DownloadTypePath,
			Name:                filename,
			Size:                size,
			Progress:            100,
			Files:               []utilitiesModels.DownloadedFile{},
			Status:              utilitiesModels.DownloadStatusDone,
			AutomaticExtraction: automaticExtraction,
			UType:               downloadType,
		}

		if err := s.DB.Create(&download).Error; err != nil {
			return fmt.Errorf("failed_to_create_download_record: %w", err)
		}

		return nil
	}

	return fmt.Errorf("invalid_url")
}

func (s *Service) startPostProcessors(n int) {
	s.workerOnce.Do(func() {
		if n <= 0 {
			n = 2
		}
		s.postq = make(chan uint, 64)
		logger.L.Debug().Msgf("postproc: starting %d workers (chan cap=%d)", n, cap(s.postq))
		for i := 0; i < n; i++ {
			i := i
			go func() {
				logger.L.Debug().Msgf("postproc: worker-%d online", i)
				s.postWorker(i)
			}()
		}
	})
}

func (s *Service) enqueuePost(id uint) {
	if s.postq == nil {
		logger.L.Error().Msg("postproc: enqueue on nil channel")
		return
	}
	s.inflightMu.Lock()
	if _, ok := s.inflight[id]; ok {
		s.inflightMu.Unlock()
		logger.L.Debug().Msgf("postproc: skip duplicate enqueue id=%d", id)
		return
	}
	s.inflight[id] = struct{}{}
	s.inflightMu.Unlock()

	logger.L.Debug().Msgf("postproc: enqueue id=%d", id)
	s.postq <- id
}

func (s *Service) postWorker(idx int) {
	for id := range s.postq {
		if err := s.postProcessOne(id); err != nil {
			logger.L.Error().Msgf("Utilities: Downloader: postproc-%d] id=%d: %v", idx, id, err)
		}
	}
}

func (s *Service) postProcessOne(id uint) error {
	defer func() {
		s.inflightMu.Lock()
		delete(s.inflight, id)
		s.inflightMu.Unlock()
	}()

	logger.L.Debug().Msgf("postproc start id=%d", id)

	// Load fresh copy
	var d utilitiesModels.Downloads
	if err := s.DB.First(&d, "id = ?", id).Error; err != nil {
		return err
	}

	// Double-check state (idempotent)
	if d.Status != "processing" {
		return nil
	}

	if !d.AutomaticExtraction {
		return s.finishDownload(&d, "")
	}

	// Prepare extract dir
	extractsPath := filepath.Join(config.GetDownloadsPath("extracted"), d.UUID)
	if err := utils.ResetDir(extractsPath); err != nil {
		return s.failDownload(&d, fmt.Errorf("reset extracts: %w", err))
	}

	logger.L.Debug().Msgf("postproc start id=%d status=%s path=%s", d.ID, d.Status, d.Path)
	// Detect type using header-only sniffing
	mime, kind, err := utils.SniffMIME(d.Path)
	logger.L.Debug().Msgf("postproc sniff id=%d mime=%s ext=%s kind=%+v err=%v", d.ID, mime, kind.Extension, kind, err)
	if err != nil {
		// If unknown, still mark done; not extractable
		logger.L.Warn().Msgf("sniff failed (%s): %v", d.Path, err)
		return s.finishDownload(&d, extractsPath)
	}

	// Extract or decompress
	if mime == "application/x-tar" || utils.IsTarLike(d.Path, mime) {
		if out, err := utils.RunCommand("tar", "-xf", d.Path, "-C", extractsPath); err != nil {
			logger.L.Error().Msgf("tar extract failed: %v (%s)", err, out)
			return s.failDownload(&d, err)
		}

		d.ExtractedPath = extractsPath
		return s.finishDownload(&d, extractsPath)
	}

	// Single compressed file → stream to file
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

	isBase, err := utils.DoesPathHaveBase(extractsPath)
	if err != nil {
		logger.L.Error().Msgf("Failed to classify extracted file: %v", err)
	}

	if isBase {
		d.UType = "fbsd-base"
	}

	return s.finishDownload(&d, d.ExtractedPath)
}

func defaultOutName(src string, ext string) string {
	base := filepath.Base(src)
	// Remove only the last extension; keep name sane
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if ext == "" {
		return base
	}
	return base // keep extensionless; container decides
}

func (s *Service) finishDownload(d *utilitiesModels.Downloads, extractedPath string) error {
	d.Status = "done"
	d.Progress = 100
	d.ExtractedPath = extractedPath

	return s.DB.Model(d).Select("Status", "Progress", "ExtractedPath").
		Updates(map[string]any{
			"status":         d.Status,
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
	s.startPostProcessors(3)

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

	// (optional) discover files once; keep it lightweight (omitted here)
	// Finish detection: when 100 and not yet processed → flip latch & enqueue
	if total > 0 && have == total && (download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending) {
		// if s.flipToProcessing(download.ID) {
		// 	s.postq <- download.ID
		// }
		download.Status = utilitiesModels.DownloadStatusDone
		download.Progress = 100
	}

	s.DB.Model(download).Select("Progress", "Size", "Name", "Status").Updates(download)
}

func (s *Service) syncHTTP(download *utilitiesModels.Downloads) {
	s.httpRspMu.Lock()
	resp, ok := s.httpResponses[download.UUID]
	s.httpRspMu.Unlock()

	// if the active response isn't in memory, just trust the file/state we have
	if ok {
		download.Progress = int(100 * resp.Progress())
		if info, err := os.Stat(resp.Filename); err == nil {
			download.Size = info.Size()
		}
		if resp.IsComplete() {
			if err := resp.Err(); err != nil {
				download.Error = err.Error()
				download.Status = "failed"
			} else if download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending {
				// finished; try to flip to processing
				if s.flipToProcessing(download.ID) {
					// s.postq <- download.ID
					s.enqueuePost(download.ID)
				}
			}
			s.httpRspMu.Lock()
			delete(s.httpResponses, download.UUID)
			s.httpRspMu.Unlock()
		}
		s.DB.Model(download).Select("Progress", "Size", "Error", "Status").Updates(download)
		return
	}

	// No active response in memory: if file exists and we never processed it, try to process
	if (download.Status == "" || download.Status == utilitiesModels.DownloadStatusPending) && fileProbablyComplete(download.Path) {
		if s.flipToProcessing(download.ID) {
			s.enqueuePost(download.ID)
		}
	}

	s.DB.Model(download).Select("Progress").Updates(map[string]any{"progress": download.Progress})
}

func (s *Service) flipToProcessing(id uint) bool {
	res := s.DB.Model(&utilitiesModels.Downloads{}).
		Where("id = ? AND status = ?", id, utilitiesModels.DownloadStatusPending).
		Updates(map[string]any{
			"status":   utilitiesModels.DownloadStatusProcessing,
			"progress": 99,
		})
	if res.Error != nil {
		logger.L.Error().Msgf("flipToProcessing: id=%d err=%v", id, res.Error)
	} else {
		logger.L.Debug().Msgf("flipToProcessing: id=%d rows=%d", id, res.RowsAffected)
	}
	return res.Error == nil && res.RowsAffected == 1
}

func fileProbablyComplete(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
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
