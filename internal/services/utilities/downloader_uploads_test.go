// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package utilities

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/internal/testutil"
	uploadCore "github.com/alchemillahq/sylve/internal/upload"
	"github.com/alchemillahq/sylve/pkg/utils"

	"gorm.io/gorm"
)

func newDownloaderUploadTestService(t *testing.T) (*Service, string) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("SYLVE_DATA_PATH", root)
	staging := filepath.Join(root, "downloads", "uploads")
	downloads := filepath.Join(root, "downloads", "path")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		t.Fatal(err)
	}

	database := testutil.NewSQLiteTestDB(
		t,
		&utilitiesModels.Upload{},
		&utilitiesModels.Downloads{},
		&utilitiesModels.DownloadedFile{},
	)
	service := &Service{
		DB:            database,
		activeUploads: make(map[string]struct{}),
		uploadHostnameFn: func() (string, error) {
			return "node-a", nil
		},
		uploadStagingDirFn: func() string {
			return staging
		},
	}
	return service, staging
}

func stageDownloaderUpload(
	t *testing.T,
	service *Service,
	staging string,
	userID uint,
	name string,
	content string,
) utilitiesModels.Upload {
	t.Helper()

	uploadID := utils.GenerateRandomUUID()
	path := filepath.Join(staging, DownloaderUploadFinalName(uploadID))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.RegisterDownloaderUpload(
		context.Background(),
		uploadID,
		path,
		name,
		info.Size(),
		userID,
		info,
	)
	if err != nil {
		t.Fatalf("register downloader upload: %v", err)
	}
	return record
}

func TestUploadIdentityPersistsSignedHighBitValues(t *testing.T) {
	service, _ := newDownloaderUploadTestService(t)
	record := utilitiesModels.Upload{
		ID:     utils.GenerateRandomUUID(),
		Scope:  utilitiesModels.UploadScopeDownloader,
		Path:   "/tmp/high-bit.upload",
		Name:   "high-bit.img",
		Size:   1,
		UserID: 7,
		Node:   "node-a",
		Status: utilitiesModels.UploadStatusStaged,
		Device: -1<<63 + 0x1234,
		Inode:  -1,
	}

	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatalf("persist high-bit file identity: %v", err)
	}
	var stored utilitiesModels.Upload
	if err := service.DB.First(&stored, "id = ?", record.ID).Error; err != nil {
		t.Fatalf("load high-bit file identity: %v", err)
	}
	if stored.Device != record.Device || stored.Inode != record.Inode {
		t.Fatalf("stored identity=(%d,%d), want (%d,%d)",
			stored.Device, stored.Inode, record.Device, record.Inode)
	}
}

func TestCompleteDownloaderUploadIsIdempotentAndPreservesOptions(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img.xz", "compressed image")

	queueCalls := 0
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		queueCalls++
		return nil
	}
	request := utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
		DownloadType:           utilitiesModels.DownloadUTypeBase,
		AutomaticExtraction:    true,
		AutomaticRawConversion: true,
	}

	first, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request)
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}
	second, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request)
	if err != nil {
		t.Fatalf("second completion: %v", err)
	}
	if first.DownloadID == 0 || second.DownloadID != first.DownloadID {
		t.Fatalf("download IDs first=%d second=%d", first.DownloadID, second.DownloadID)
	}
	if queueCalls != 1 {
		t.Fatalf("queue calls=%d want=1", queueCalls)
	}

	var count int64
	if err := service.DB.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("download count=%d want=1", count)
	}
	var download utilitiesModels.Downloads
	if err := service.DB.First(&download, first.DownloadID).Error; err != nil {
		t.Fatal(err)
	}
	if !download.AutomaticExtraction || !download.AutomaticRawConversion {
		t.Fatalf("post-processing options were not preserved: %+v", download)
	}
	if download.UType != utilitiesModels.DownloadUTypeBase ||
		download.Type != utilitiesModels.DownloadTypePath ||
		download.Status != utilitiesModels.DownloadStatusPending {
		t.Fatalf("unexpected downloader record: %+v", download)
	}

	var completed utilitiesModels.Upload
	if err := service.DB.First(&completed, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != utilitiesModels.UploadStatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("upload was not completed: %+v", completed)
	}

	alreadyCompleted, err := service.AbortDownloaderUpload(context.Background(), record.ID, 7)
	if err != nil {
		t.Fatalf("abort completed upload: %v", err)
	}
	if !alreadyCompleted {
		t.Fatal("completed upload was reported as abortable")
	}
	if _, err := os.Stat(record.Path); err != nil {
		t.Fatalf("abort removed completed downloader source: %v", err)
	}
}

func TestCompleteDownloaderUploadRejectsTarAndRawCombination(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "rootfs.txz", "archive")
	queueCalls := 0
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		queueCalls++
		return nil
	}

	_, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType:           utilitiesModels.DownloadUTypeBase,
			AutomaticExtraction:    true,
			AutomaticRawConversion: true,
		},
	)
	if !errors.Is(err, ErrDownloaderPostProcessOptions) {
		t.Fatalf("completion error=%v want incompatible post-processing options", err)
	}
	if queueCalls != 0 {
		t.Fatalf("invalid completion queued %d downloader jobs", queueCalls)
	}

	var stored utilitiesModels.Upload
	if err := service.DB.First(&stored, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != utilitiesModels.UploadStatusStaged || stored.CompletedAt != nil {
		t.Fatalf("invalid completion changed upload state: %+v", stored)
	}
	var count int64
	if err := service.DB.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid completion created %d downloader records", count)
	}
}

func TestCompleteDownloaderUploadKeepsCompletedRetryIdempotent(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "rootfs.txz", "archive")
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		return nil
	}

	first, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType:        utilitiesModels.DownloadUTypeBase,
			AutomaticExtraction: true,
		},
	)
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}

	retry, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType:           utilitiesModels.DownloadUTypeBase,
			AutomaticExtraction:    true,
			AutomaticRawConversion: true,
		},
	)
	if err != nil {
		t.Fatalf("idempotent retry revalidated ignored options: %v", err)
	}
	if retry.DownloadID != first.DownloadID {
		t.Fatalf("retry download ID=%d want=%d", retry.DownloadID, first.DownloadID)
	}
}

func TestCompleteDownloaderUploadRetriesQueueWithoutDuplicatingRecord(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img", "image")

	queueCalls := 0
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		queueCalls++
		if queueCalls == 1 {
			return errors.New("queue offline")
		}
		return nil
	}
	request := utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
		DownloadType: utilitiesModels.DownloadUType("uncategorized"),
	}

	first, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request)
	if !errors.Is(err, ErrDownloaderUploadQueue) {
		t.Fatalf("first completion error=%v want queue failure", err)
	}
	if first.DownloadID == 0 {
		t.Fatal("download record was not committed before queue failure")
	}

	second, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request)
	if err != nil {
		t.Fatalf("retry completion: %v", err)
	}
	if second.DownloadID != first.DownloadID {
		t.Fatalf("retry created another download: first=%d second=%d", first.DownloadID, second.DownloadID)
	}
	if queueCalls != 2 {
		t.Fatalf("queue calls=%d want=2", queueCalls)
	}

	var count int64
	if err := service.DB.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("download count=%d want=1", count)
	}
}

func TestCompleteDownloaderUploadRetriesAfterRecordCreationFailure(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img", "image")
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		return nil
	}

	const callbackName = "test:fail_downloader_upload_record_once"
	failCreate := true
	if err := service.DB.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			if failCreate && tx.Statement.Table == "downloads" {
				failCreate = false
				tx.AddError(errors.New("database temporarily unavailable"))
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.DB.Callback().Create().Remove(callbackName)
	})

	request := utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
		DownloadType: utilitiesModels.DownloadUType("uncategorized"),
	}
	if _, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request); !errors.Is(
		err,
		ErrDownloaderUploadPersistence,
	) {
		t.Fatalf("first completion error=%v want persistence failure", err)
	}

	var staged utilitiesModels.Upload
	if err := service.DB.First(&staged, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if staged.Status != utilitiesModels.UploadStatusStaged || staged.CompletedAt != nil {
		t.Fatalf("failed transaction changed upload state: %+v", staged)
	}
	var count int64
	if err := service.DB.Model(&utilitiesModels.Downloads{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed transaction left %d downloader records", count)
	}

	if err := service.DB.Callback().Create().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	result, err := service.CompleteDownloaderUpload(context.Background(), record.ID, 7, request)
	if err != nil {
		t.Fatalf("retry completion: %v", err)
	}
	if result.DownloadID == 0 {
		t.Fatal("retry did not create downloader record")
	}
}

func TestCompleteDownloaderUploadDoesNotOverwriteDownloaderDestination(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img", "new")
	destination := filepath.Join(filepath.Dir(filepath.Dir(staging)), "downloads", "path", "disk.img")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType: utilitiesModels.DownloadUType("uncategorized"),
		},
	)
	if !errors.Is(err, ErrDownloaderUploadDestinationExists) {
		t.Fatalf("completion error=%v want destination collision", err)
	}
	content, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "existing" {
		t.Fatalf("existing destination changed to %q", content)
	}
}

func TestAbortDownloaderUploadIsIdempotent(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img", "image")

	completed, err := service.AbortDownloaderUpload(context.Background(), record.ID, 7)
	if err != nil {
		t.Fatalf("first abort: %v", err)
	}
	if completed {
		t.Fatal("staged upload reported completed")
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists: %v", err)
	}

	completed, err = service.AbortDownloaderUpload(context.Background(), record.ID, 7)
	if err != nil || completed {
		t.Fatalf("second abort completed=%v err=%v", completed, err)
	}
}

func TestCompletedDownloaderUploadMovesThroughExistingPathDownloadLifecycle(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	record := stageDownloaderUpload(t, service, staging, 7, "disk.img", "image")
	service.enqueueDownloadStartFn = func(
		_ context.Context,
		_ utilitiesServiceInterfaces.DownloadStartPayload,
	) error {
		return nil
	}

	completion, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType: utilitiesModels.DownloadUType("uncategorized"),
		},
	)
	if err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	if err := service.StartDownload(&completion.DownloadID); err != nil {
		t.Fatalf("start existing path download: %v", err)
	}

	var download utilitiesModels.Downloads
	if err := service.DB.First(&download, completion.DownloadID).Error; err != nil {
		t.Fatal(err)
	}
	if download.Status != utilitiesModels.DownloadStatusDone || download.Progress != 100 {
		t.Fatalf("download was not finalized: %+v", download)
	}
	content, err := os.ReadFile(download.Path)
	if err != nil {
		t.Fatalf("read downloader destination: %v", err)
	}
	if string(content) != "image" {
		t.Fatalf("destination content=%q", content)
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatalf("completed staging source was not released: %v", err)
	}

	retry, err := service.CompleteDownloaderUpload(
		context.Background(),
		record.ID,
		7,
		utilitiesServiceInterfaces.CompleteDownloaderUploadRequest{
			DownloadType: utilitiesModels.DownloadUType("uncategorized"),
		},
	)
	if err != nil {
		t.Fatalf("idempotent completion after source release: %v", err)
	}
	if retry.DownloadID != completion.DownloadID {
		t.Fatalf("completion retry returned download %d want %d", retry.DownloadID, completion.DownloadID)
	}
}

func TestCleanupExpiredUploadsSkipsActiveAndCompleted(t *testing.T) {
	service, staging := newDownloaderUploadTestService(t)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	service.uploadNowFn = func() time.Time { return now }

	expired := stageDownloaderUpload(t, service, staging, 7, "expired.img", "expired")
	active := stageDownloaderUpload(t, service, staging, 7, "active.img", "active")
	completed := stageDownloaderUpload(t, service, staging, 7, "completed.img", "completed")
	old := now.Add(-DownloaderUploadTTL - time.Minute)
	for _, id := range []string{expired.ID, active.ID, completed.ID} {
		if err := service.DB.Model(&utilitiesModels.Upload{}).
			Where("id = ?", id).
			Update("created_at", old).Error; err != nil {
			t.Fatal(err)
		}
	}
	completedAt := old.Add(time.Minute)
	if err := service.DB.Model(&utilitiesModels.Upload{}).
		Where("id = ?", completed.ID).
		Updates(map[string]any{
			"status":       utilitiesModels.UploadStatusCompleted,
			"completed_at": &completedAt,
		}).Error; err != nil {
		t.Fatal(err)
	}

	endActive := service.BeginDownloaderUpload(active.ID)
	defer endActive()

	activePartial := filepath.Join(staging, DownloaderUploadPartialName(active.ID))
	if err := os.WriteFile(activePartial, []byte("active-partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(activePartial, old, old); err != nil {
		t.Fatal(err)
	}
	oldPartialID := utils.GenerateRandomUUID()
	oldPartial := filepath.Join(staging, DownloaderUploadPartialName(oldPartialID))
	if err := os.WriteFile(oldPartial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldPartial, old, old); err != nil {
		t.Fatal(err)
	}
	recentPartial := filepath.Join(staging, DownloaderUploadPartialName(utils.GenerateRandomUUID()))
	if err := os.WriteFile(recentPartial, []byte("recent"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(staging, DownloaderUploadFinalName(utils.GenerateRandomUUID()))
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	explorerFile := filepath.Join(t.TempDir(), "explorer.iso")
	if err := os.WriteFile(explorerFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	explorerInfo, err := os.Stat(explorerFile)
	if err != nil {
		t.Fatal(err)
	}
	explorerIdentity, err := uploadCore.IdentityFromFileInfo(explorerInfo)
	if err != nil {
		t.Fatal(err)
	}
	explorerRecord := utilitiesModels.Upload{
		ID:        utils.GenerateRandomUUID(),
		Scope:     utilitiesModels.UploadScopeFileExplorer,
		Path:      explorerFile,
		Name:      filepath.Base(explorerFile),
		Size:      explorerInfo.Size(),
		UserID:    7,
		Node:      "node-a",
		Status:    utilitiesModels.UploadStatusStaged,
		Device:    explorerIdentity.Device,
		Inode:     explorerIdentity.Inode,
		CreatedAt: old,
	}
	if err := service.DB.Create(&explorerRecord).Error; err != nil {
		t.Fatal(err)
	}

	if err := service.CleanupExpiredUploads(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := os.Stat(expired.Path); !os.IsNotExist(err) {
		t.Fatalf("expired staged upload remains: %v", err)
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatalf("active upload was removed: %v", err)
	}
	if _, err := os.Stat(activePartial); err != nil {
		t.Fatalf("active partial was removed: %v", err)
	}
	if _, err := os.Stat(completed.Path); err != nil {
		t.Fatalf("completed upload was removed: %v", err)
	}
	if _, err := os.Stat(oldPartial); !os.IsNotExist(err) {
		t.Fatalf("expired partial remains: %v", err)
	}
	if _, err := os.Stat(recentPartial); err != nil {
		t.Fatalf("recent partial was removed: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expired orphan remains: %v", err)
	}
	if _, err := os.Stat(explorerFile); err != nil {
		t.Fatalf("file explorer content was removed when identity expired: %v", err)
	}

	var explorerCount int64
	if err := service.DB.Model(&utilitiesModels.Upload{}).
		Where("id = ?", explorerRecord.ID).
		Count(&explorerCount).Error; err != nil {
		t.Fatal(err)
	}
	if explorerCount != 0 {
		t.Fatal("expired file explorer identity remains")
	}
}
