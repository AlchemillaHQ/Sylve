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
	"fmt"
	"os"
	"path/filepath"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	"github.com/alchemillahq/sylve/internal/downloadstorage"
	"github.com/alchemillahq/sylve/internal/upload"
	qemuimg "github.com/alchemillahq/sylve/pkg/qemu-img"
)

func (s *Service) resolveDownloadStorage(download *utilitiesModels.Downloads, write bool) (downloadstorage.Layout, error) {
	layout, err := s.DownloadStorage.Resolve(context.Background(), download.DownloadStorage, write)
	if err != nil {
		return layout, err
	}
	return layout, layout.ValidateDownload(*download)
}

// Storage interruption retains both the record and partial payload. Recovery
// resolves the same binding again, without discarding cached torrent metadata.
func (s *Service) interruptDownloadStorage(download *utilitiesModels.Downloads, cause error) {
	if download.Type == utilitiesModels.DownloadTypeTorrent {
		if client, err := s.activeTorrentClient(); err == nil {
			_ = client.StopTorrent(download.UUID)
		}
	} else if download.Type == utilitiesModels.DownloadTypeHTTP {
		s.httpRspMu.Lock()
		if response := s.httpResponses[download.UUID]; response != nil {
			response.Cancel()
			delete(s.httpResponses, download.UUID)
		}
		s.httpRspMu.Unlock()
	}
	s.DB.Model(download).UpdateColumn("error", downloadstorage.ErrorCode(cause))
}

// Convert into a private target-local file, then publish without replacement.
// qemu-img opens its output by pathname; never let it truncate another payload.
func (s *Service) convertDownloadToRaw(download utilitiesModels.Downloads, layout downloadstorage.Layout, source, destination string) error {
	if err := ensureDownloadDestinationAvailable(destination); err != nil {
		return err
	}
	var count int64
	if err := s.DB.Model(&utilitiesModels.Downloads{}).Where("path = ? AND id <> ?", destination, download.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrDownloadConflict
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := s.DownloadStorage.Recheck(context.Background(), layout, true); err != nil {
		return err
	}
	if err := layout.ValidatePath(destination); err != nil {
		return err
	}
	file, partial, err := upload.CreateRandomPartial(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		if s.DownloadStorage.Recheck(context.Background(), layout, true) == nil && layout.ValidatePath(partial) == nil {
			_ = os.Remove(partial)
		}
	}()
	if err := file.Close(); err != nil {
		return err
	}
	if err := qemuimg.Convert(source, partial, qemuimg.FormatRaw); err != nil {
		return err
	}
	if err := s.DownloadStorage.Recheck(context.Background(), layout, true); err != nil {
		return err
	}
	if err := layout.ValidatePath(partial); err != nil {
		return err
	}
	if err := layout.ValidatePath(destination); err != nil {
		return err
	}
	if err := os.Chmod(partial, info.Mode().Perm()); err != nil {
		return err
	}
	if err := upload.PublishNoReplace(partial, destination); err != nil {
		return fmt.Errorf("publish RAW image: %w", err)
	}
	return nil
}
