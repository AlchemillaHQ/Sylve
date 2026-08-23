// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/alchemillahq/sylve/pkg/utils"

	"golang.org/x/sys/unix"
)

const (
	receiveBufferBytes = 128 << 10
	maxEmptyReads      = 100
)

func RequestLimit(maxFileBytes, overheadBytes int64) int64 {
	if maxFileBytes > math.MaxInt64-overheadBytes {
		return math.MaxInt64
	}
	return maxFileBytes + overheadBytes
}

type Failure struct {
	StatusCode int
	Code       string
	Err        error
	Retryable  bool
	RetryAfter string
	LimitBytes int64
}

func (f *Failure) Error() string {
	if f == nil {
		return ""
	}
	if f.Err == nil {
		return f.Code
	}
	return f.Err.Error()
}

func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

type StagedFile struct {
	Name        string
	PartialPath string
	FinalPath   string
	Bytes       int64
	FileInfo    os.FileInfo
}

type ReceiveOptions struct {
	Field         string
	MaxFileBytes  int64
	NormalizeName func(string) (string, error)
	Open          func(string) (*os.File, string, string, *Failure)
}

func OpenMultipartRequest(
	writer http.ResponseWriter,
	request *http.Request,
	maxRequestBytes int64,
) (*multipart.Reader, func(), *Failure) {
	if request == nil || request.Body == nil {
		return nil, nil, NewFailure(
			http.StatusBadRequest,
			"malformed_multipart",
			errors.New("request body is required"),
		)
	}
	if request.ContentLength > maxRequestBytes {
		return nil, nil, limitFailure("request_too_large", maxRequestBytes)
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	stopCancellationClose := context.AfterFunc(request.Context(), func() {
		_ = request.Body.Close()
	})
	cleanup := func() {
		stopCancellationClose()
		_ = request.Body.Close()
	}

	reader, err := request.MultipartReader()
	if err != nil {
		cleanup()
		return nil, nil, NewFailure(http.StatusBadRequest, "malformed_multipart", err)
	}
	return reader, cleanup, nil
}

func ReceiveSingle(
	ctx context.Context,
	reader *multipart.Reader,
	options ReceiveOptions,
) (*StagedFile, *Failure) {
	if reader == nil || options.Field == "" || options.MaxFileBytes <= 0 || options.Open == nil {
		return nil, NewFailure(
			http.StatusInternalServerError,
			"invalid_upload_configuration",
			errors.New("invalid upload receiver configuration"),
		)
	}

	normalizeName := options.NormalizeName
	if normalizeName == nil {
		normalizeName = func(name string) (string, error) { return name, nil }
	}

	var staged *StagedFile
	keepPartial := false
	defer func() {
		if !keepPartial && staged != nil && staged.PartialPath != "" {
			_ = os.Remove(staged.PartialPath)
		}
	}()

	buffer := make([]byte, receiveBufferBytes)
	for {
		if contextFailure := ContextFailure(ctx); contextFailure != nil {
			return nil, contextFailure
		}

		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ReadFailure(ctx, err)
		}

		field, rawName, hasFilename, err := multipartDisposition(part)
		if err != nil {
			_ = part.Close()
			return nil, NewFailure(http.StatusBadRequest, "malformed_multipart", err)
		}
		if !hasFilename || rawName == "" {
			if drainFailure := drainPart(ctx, part, buffer); drainFailure != nil {
				_ = part.Close()
				return nil, drainFailure
			}
			if err := part.Close(); err != nil {
				return nil, ReadFailure(ctx, err)
			}
			continue
		}
		if field != options.Field {
			_ = part.Close()
			return nil, NewFailure(
				http.StatusBadRequest,
				"unexpected_file_field",
				fmt.Errorf("file must use multipart field %q", options.Field),
			)
		}
		if staged != nil {
			_ = part.Close()
			return nil, NewFailure(
				http.StatusBadRequest,
				"too_many_files",
				errors.New("exactly one file is allowed per upload request"),
			)
		}

		name, err := normalizeName(rawName)
		if err != nil {
			_ = part.Close()
			return nil, NewFailure(http.StatusBadRequest, "invalid_filename", err)
		}
		file, partialPath, finalPath, openFailure := options.Open(name)
		if openFailure != nil {
			_ = part.Close()
			return nil, openFailure
		}
		staged = &StagedFile{
			Name:        name,
			PartialPath: partialPath,
			FinalPath:   finalPath,
		}

		accepted, streamFailure := streamPart(
			ctx,
			file,
			part,
			buffer,
			options.MaxFileBytes,
		)
		staged.Bytes = accepted
		if streamFailure != nil {
			_ = file.Close()
			_ = part.Close()
			return nil, streamFailure
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = part.Close()
			return nil, FilesystemFailure(err, "partial_sync_failed")
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			_ = part.Close()
			return nil, FilesystemFailure(err, "partial_stat_failed")
		}
		staged.FileInfo = info
		if err := file.Close(); err != nil {
			_ = part.Close()
			return nil, FilesystemFailure(err, "partial_close_failed")
		}
		if err := part.Close(); err != nil {
			return nil, ReadFailure(ctx, err)
		}
	}

	if staged == nil {
		return nil, NewFailure(
			http.StatusBadRequest,
			"missing_file",
			fmt.Errorf("no file found in %q field", options.Field),
		)
	}
	if contextFailure := ContextFailure(ctx); contextFailure != nil {
		return nil, contextFailure
	}

	keepPartial = true
	return staged, nil
}

func OpenExclusive(path string) (*os.File, error) {
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(path, flags, 0o600)
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		_ = os.Remove(path)
		return nil, errors.New("failed to wrap partial file descriptor")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return file, nil
}

func CreateRandomPartial(directory string) (*os.File, string, error) {
	path := filepath.Join(
		directory,
		".sylve-upload-"+utils.GenerateRandomUUID()+".partial",
	)
	file, err := OpenExclusive(path)
	return file, path, err
}

// PublishNoReplace links a fully flushed partial into its final name without
// replacing an existing destination. Both paths must be on the same filesystem.
func PublishNoReplace(partialPath, finalPath string) error {
	if err := os.Link(partialPath, finalPath); err != nil {
		return err
	}
	if err := os.Remove(partialPath); err != nil {
		if rollbackErr := os.Remove(finalPath); rollbackErr != nil {
			return fmt.Errorf(
				"remove partial link: %w; published file rollback failed: %v",
				err,
				rollbackErr,
			)
		}
		return fmt.Errorf("remove partial link: %w", err)
	}
	return nil
}

func ReadFailure(ctx context.Context, err error) *Failure {
	if contextFailure := ContextFailure(ctx); contextFailure != nil {
		return contextFailure
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return limitFailure("request_too_large", maxBytesError.Limit)
	}
	return NewFailure(http.StatusBadRequest, "malformed_multipart", err)
}

func ContextFailure(ctx context.Context) *Failure {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	code := "upload_cancelled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "upload_timed_out"
	}
	return NewFailure(http.StatusRequestTimeout, code, ctx.Err())
}

func multipartDisposition(part *multipart.Part) (string, string, bool, error) {
	disposition, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return "", "", false, fmt.Errorf("parse content disposition: %w", err)
	}
	if disposition != "form-data" {
		return "", "", false, fmt.Errorf("unexpected content disposition %q", disposition)
	}
	filename, hasFilename := parameters["filename"]
	return parameters["name"], filename, hasFilename, nil
}

func streamPart(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	buffer []byte,
	maxBytes int64,
) (int64, *Failure) {
	var accepted int64
	emptyReads := 0
	for {
		if contextFailure := ContextFailure(ctx); contextFailure != nil {
			return accepted, contextFailure
		}

		remaining := maxBytes - accepted
		readSize := len(buffer)
		if remaining < int64(readSize) {
			readSize = int(remaining) + 1
		}
		readBytes, readErr := source.Read(buffer[:readSize])
		if readBytes > 0 {
			emptyReads = 0
			if int64(readBytes) > remaining {
				return accepted, limitFailure("file_too_large", maxBytes)
			}
			if writeFailure := writeBytes(ctx, destination, buffer[:readBytes]); writeFailure != nil {
				return accepted, writeFailure
			}
			accepted += int64(readBytes)
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= maxEmptyReads {
				return accepted, NewFailure(http.StatusBadRequest, "upload_read_failed", io.ErrNoProgress)
			}
		}

		if errors.Is(readErr, io.EOF) {
			return accepted, nil
		}
		if readErr != nil {
			return accepted, ReadFailure(ctx, readErr)
		}
	}
}

func writeBytes(ctx context.Context, destination io.Writer, data []byte) *Failure {
	for len(data) > 0 {
		if contextFailure := ContextFailure(ctx); contextFailure != nil {
			return contextFailure
		}
		written, err := destination.Write(data)
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return FilesystemFailure(err, "upload_write_failed")
		}
		if written == 0 {
			return FilesystemFailure(io.ErrShortWrite, "upload_write_failed")
		}
	}
	return nil
}

func drainPart(ctx context.Context, part io.Reader, buffer []byte) *Failure {
	emptyReads := 0
	for {
		if contextFailure := ContextFailure(ctx); contextFailure != nil {
			return contextFailure
		}
		readBytes, err := part.Read(buffer)
		if readBytes == 0 && err == nil {
			emptyReads++
			if emptyReads >= maxEmptyReads {
				return NewFailure(http.StatusBadRequest, "upload_read_failed", io.ErrNoProgress)
			}
		} else {
			emptyReads = 0
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ReadFailure(ctx, err)
		}
	}
}

func limitFailure(code string, limit int64) *Failure {
	return &Failure{
		StatusCode: http.StatusRequestEntityTooLarge,
		Code:       code,
		Err:        fmt.Errorf("%s: limit is %d bytes", code, limit),
		LimitBytes: limit,
	}
}

func CapacityFailure() *Failure {
	return &Failure{
		StatusCode: http.StatusTooManyRequests,
		Code:       "upload_capacity_exhausted",
		Err:        errors.New("all upload slots are currently in use"),
		Retryable:  true,
		RetryAfter: "1",
	}
}

func FilesystemFailure(err error, fallbackCode string) *Failure {
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return NewFailure(http.StatusInsufficientStorage, "insufficient_storage", err)
	case errors.Is(err, syscall.EACCES),
		errors.Is(err, syscall.EPERM),
		errors.Is(err, syscall.EROFS):
		return NewFailure(http.StatusForbidden, "upload_permission_denied", err)
	default:
		return NewFailure(http.StatusInternalServerError, fallbackCode, err)
	}
}

func NewFailure(statusCode int, code string, err error) *Failure {
	return &Failure{
		StatusCode: statusCode,
		Code:       code,
		Err:        err,
	}
}
