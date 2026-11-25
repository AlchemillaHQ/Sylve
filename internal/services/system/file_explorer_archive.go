// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

const FileExplorerArchiveNoteName = "SYLVE-SKIPPED.txt"

var fileExplorerStoredExtensions = map[string]struct{}{
	".7z": {}, ".avi": {}, ".bz2": {}, ".flac": {}, ".gif": {}, ".gz": {}, ".jpeg": {}, ".jpg": {},
	".mkv": {}, ".mov": {}, ".mp3": {}, ".mp4": {}, ".ogg": {}, ".png": {}, ".qcow2": {}, ".rar": {},
	".tgz": {}, ".txz": {}, ".webm": {}, ".webp": {}, ".xz": {}, ".zip": {}, ".zst": {},
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrFileExplorerCancelled, err)
	}
	return r.reader.Read(buffer)
}

type FileExplorerArchiveItem struct {
	Path string
	Name string
}

type FileExplorerArchive struct {
	Name          string
	Items         []FileExplorerArchiveItem
	NoteDirectory string
}

func (s *Service) PrepareArchive(ids []string) (FileExplorerArchive, error) {
	switch {
	case len(ids) == 0:
		return FileExplorerArchive{}, wrapFileExplorerError(ErrFileExplorerInvalidPath, "paths_required", nil)
	case len(ids) == 1:
		return prepareFolderArchive(ids[0])
	case len(ids) > MaxFileExplorerBatchItems:
		return FileExplorerArchive{}, fmt.Errorf("%w: maximum=%d", ErrFileExplorerBatchTooLarge, MaxFileExplorerBatchItems)
	}

	var parent string
	seen := make(map[string]struct{}, len(ids))
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		cleanPath, err := normalizeFileExplorerPath(id, false)
		if err != nil {
			return FileExplorerArchive{}, err
		}
		if cleanPath == string(filepath.Separator) {
			return FileExplorerArchive{}, wrapFileExplorerError(ErrFileExplorerInvalidOperation, cleanPath, nil)
		}
		if parent == "" {
			parent = filepath.Dir(cleanPath)
		} else if filepath.Dir(cleanPath) != parent {
			return FileExplorerArchive{}, wrapFileExplorerError(ErrFileExplorerBatchConflict, "mixed_parents", nil)
		}
		if _, duplicate := seen[cleanPath]; duplicate {
			return FileExplorerArchive{}, wrapFileExplorerError(ErrFileExplorerBatchConflict, "duplicate_path", nil)
		}
		seen[cleanPath] = struct{}{}
		if _, err := os.Lstat(cleanPath); err != nil {
			return FileExplorerArchive{}, wrapFileExplorerIOError(cleanPath, err)
		}
		names = append(names, filepath.Base(cleanPath))
	}

	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return FileExplorerArchive{}, wrapFileExplorerIOError(parent, err)
	}
	archive := FileExplorerArchive{
		Name:  fmt.Sprintf("%s - %d items", fileExplorerArchiveBase(parent), len(names)),
		Items: make([]FileExplorerArchiveItem, 0, len(names)),
	}
	for _, name := range names {
		archive.Items = append(archive.Items, FileExplorerArchiveItem{Path: filepath.Join(resolvedParent, name), Name: name})
	}
	return archive, nil
}

func prepareFolderArchive(id string) (FileExplorerArchive, error) {
	cleanPath, err := normalizeFileExplorerPath(id, false)
	if err != nil {
		return FileExplorerArchive{}, err
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return FileExplorerArchive{}, wrapFileExplorerIOError(cleanPath, err)
	}
	if !info.IsDir() {
		return FileExplorerArchive{}, wrapFileExplorerError(ErrFileExplorerNotDirectory, cleanPath, nil)
	}
	root, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return FileExplorerArchive{}, wrapFileExplorerIOError(cleanPath, err)
	}
	name := fileExplorerArchiveBase(cleanPath)
	return FileExplorerArchive{
		Name:          name,
		Items:         []FileExplorerArchiveItem{{Path: root, Name: name}},
		NoteDirectory: name,
	}, nil
}

func fileExplorerArchiveBase(path string) string {
	name := filepath.Base(path)
	if name == string(filepath.Separator) {
		return "root"
	}
	return name
}

func archiveProblem(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

type fileExplorerArchiveWriter struct {
	ctx       context.Context
	zip       *zip.Writer
	buffer    []byte
	skipped   []string
	noteDir   string
	noteNames map[string]struct{}
}

func (w *fileExplorerArchiveWriter) skip(archivePath, problem string) {
	w.skipped = append(w.skipped, fmt.Sprintf("%s: %s", archivePath, problem))
}

func (w *fileExplorerArchiveWriter) record(archivePath string) {
	if path.Dir(archivePath) == w.noteDir {
		w.noteNames[path.Base(archivePath)] = struct{}{}
	}
}

func (w *fileExplorerArchiveWriter) notePath() string {
	stem := strings.TrimSuffix(FileExplorerArchiveNoteName, ".txt")
	name := FileExplorerArchiveNoteName
	for index := 2; ; index++ {
		if _, taken := w.noteNames[name]; !taken {
			break
		}
		name = fmt.Sprintf("%s (%d).txt", stem, index)
	}
	if w.noteDir == "." {
		return name
	}
	return w.noteDir + "/" + name
}

func WriteArchive(ctx context.Context, archive FileExplorerArchive, output io.Writer) error {
	w := &fileExplorerArchiveWriter{
		ctx:       ctx,
		zip:       zip.NewWriter(output),
		buffer:    make([]byte, fileExplorerCopyBufferSize),
		noteDir:   path.Clean(archive.NoteDirectory),
		noteNames: map[string]struct{}{},
	}
	w.zip.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestSpeed)
	})
	for _, item := range archive.Items {
		if err := w.item(item); err != nil {
			return err
		}
	}
	if len(w.skipped) > 0 {
		writer, err := w.zip.Create(w.notePath())
		if err != nil {
			return err
		}
		note := "These items could not be added to the archive:\n\n" + strings.Join(w.skipped, "\n") + "\n"
		if _, err := io.WriteString(writer, note); err != nil {
			return err
		}
	}
	return w.zip.Close()
}

func (w *fileExplorerArchiveWriter) item(item FileExplorerArchiveItem) error {
	return filepath.WalkDir(item.Path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if err := w.ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrFileExplorerCancelled, err)
		}
		relative, err := filepath.Rel(item.Path, entryPath)
		if err != nil {
			return err
		}
		archivePath := item.Name
		if relative != "." {
			archivePath = item.Name + "/" + filepath.ToSlash(relative)
		}
		if walkErr != nil {
			w.skip(archivePath, archiveProblem(walkErr))
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			w.skip(archivePath, archiveProblem(err))
			return nil
		}
		w.record(archivePath)

		mode := info.Mode()
		switch {
		case mode.IsDir():
			header := &zip.FileHeader{Name: archivePath + "/", Method: zip.Store, Modified: info.ModTime()}
			header.SetMode(mode)
			_, err := w.zip.CreateHeader(header)
			return err
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(entryPath)
			if err != nil {
				w.skip(archivePath, archiveProblem(err))
				return nil
			}
			header := &zip.FileHeader{Name: archivePath, Method: zip.Store, Modified: info.ModTime()}
			header.SetMode(mode)
			writer, err := w.zip.CreateHeader(header)
			if err != nil {
				return err
			}
			_, err = io.WriteString(writer, target)
			return err
		case mode.IsRegular():
			return addArchiveFile(w.ctx, w.zip, entryPath, archivePath, w.buffer, w.skip)
		default:
			w.skip(archivePath, "not a regular file, folder or symbolic link")
			return nil
		}
	})
}

func addArchiveFile(
	ctx context.Context,
	archive *zip.Writer,
	path, archivePath string,
	buffer []byte,
	skip func(string, string),
) error {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		skip(archivePath, archiveProblem(err))
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		skip(archivePath, archiveProblem(err))
		return nil
	}
	if !info.Mode().IsRegular() {
		skip(archivePath, "not a regular file, folder or symbolic link")
		return nil
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = archivePath
	header.Method = zip.Deflate
	if _, stored := fileExplorerStoredExtensions[strings.ToLower(filepath.Ext(path))]; stored {
		header.Method = zip.Store
	}
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(writer, contextReader{ctx: ctx, reader: file}, buffer)
	return err
}
