// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/internal/db/replicationguard"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
)

func fileExplorerPathOverlaps(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	for _, pair := range [][2]string{{path, root}, {root, path}} {
		rel, err := filepath.Rel(pair[1], pair[0])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveFileExplorerGuardPath resolves every existing path component while
// preserving a possibly non-existent final suffix. This lets the restore fence
// reason about the actual mutation target without preventing normal explorer
// use of symlinked directories.
func resolveFileExplorerGuardPath(path string) string {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		path = string(filepath.Separator) + path
	}

	current := path
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// EnsureFileExplorerMutationAllowed blocks writes that could alter a jail
// whose dataset is being replaced by a restore. Read-only explorer operations
// intentionally do not call this method.
func (s *Service) EnsureFileExplorerMutationAllowed(paths ...string) error {
	if s == nil || s.DB == nil || !replicationguard.GuestOperationSchemaReady(s.DB) {
		return nil
	}
	var operations []clusterModels.ReplicationGuestOperation
	if err := s.DB.Where("guest_type = ? AND operation = ?", clusterModels.ReplicationGuestTypeJail, clusterModels.ReplicationGuestOperationRestore).Find(&operations).Error; err != nil {
		return wrapFileExplorerError(ErrFileExplorerOperationFailed, "restore_fence_lookup", err)
	}
	if len(operations) == 0 {
		return nil
	}
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return wrapFileExplorerError(ErrFileExplorerOperationFailed, "restore_fence_jails_path", err)
	}
	for _, operation := range operations {
		jailRoot := resolveFileExplorerGuardPath(
			filepath.Join(jailsPath, strconv.FormatUint(uint64(operation.GuestID), 10)),
		)
		for _, path := range paths {
			if path == "" {
				continue
			}
			path = resolveFileExplorerGuardPath(path)
			if fileExplorerPathOverlaps(path, jailRoot) {
				return fmt.Errorf("%w: ctid=%d", ErrFileExplorerRestoreInProgress, operation.GuestID)
			}
		}
	}
	return nil
}

func normalizeFileExplorerPath(path string, defaultRoot bool) (string, error) {
	if path == "" {
		if defaultRoot {
			return string(filepath.Separator), nil
		}
		return "", wrapFileExplorerError(ErrFileExplorerInvalidPath, "path_required", nil)
	}
	if strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", wrapFileExplorerError(ErrFileExplorerInvalidPath, path, nil)
	}

	return filepath.Clean(path), nil
}

func validateFileExplorerName(name string) error {
	if name == "" || strings.IndexByte(name, 0) >= 0 || strings.ContainsRune(name, filepath.Separator) || name == "." || name == ".." || filepath.Base(name) != name {
		return wrapFileExplorerError(ErrFileExplorerInvalidName, name, nil)
	}
	return nil
}

func normalizeFileExplorerDeletePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, wrapFileExplorerError(ErrFileExplorerInvalidPath, "paths_required", nil)
	}
	if len(paths) > MaxFileExplorerBatchItems {
		return nil, fmt.Errorf("%w: maximum=%d", ErrFileExplorerBatchTooLarge, MaxFileExplorerBatchItems)
	}

	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		cleanPath, err := normalizeFileExplorerPath(path, false)
		if err != nil {
			return nil, err
		}
		if cleanPath == string(filepath.Separator) {
			return nil, wrapFileExplorerError(ErrFileExplorerRootMutation, cleanPath, nil)
		}
		if _, exists := seen[cleanPath]; exists {
			continue
		}
		seen[cleanPath] = struct{}{}
		normalized = append(normalized, cleanPath)
	}

	return normalized, nil
}

func (s *Service) Traverse(path string) ([]systemServiceInterfaces.FileNode, error) {
	cleanPath, err := normalizeFileExplorerPath(path, true)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		return nil, wrapFileExplorerIOError(cleanPath, err)
	}

	nodes := make([]systemServiceInterfaces.FileNode, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(cleanPath, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}

		node := systemServiceInterfaces.FileNode{
			ID:   full,
			Date: info.ModTime(),
		}
		if info.IsDir() {
			node.Type = "folder"
			node.Lazy = true
		} else {
			node.Type = "file"
			node.Size = info.Size()
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

func (s *Service) AddFileOrFolder(path string, name string, isFolder bool) error {
	cleanPath, err := normalizeFileExplorerPath(path, false)
	if err != nil {
		return err
	}
	if err := validateFileExplorerName(name); err != nil {
		return err
	}

	fullPath := filepath.Join(cleanPath, name)
	if err := s.EnsureFileExplorerMutationAllowed(fullPath); err != nil {
		return err
	}

	if _, err := os.Lstat(fullPath); err == nil {
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, fullPath, nil)
	} else if !os.IsNotExist(err) {
		return wrapFileExplorerIOError(fullPath, err)
	}

	if isFolder {
		if err := os.Mkdir(fullPath, 0o755); err != nil {
			return wrapFileExplorerIOError(fullPath, err)
		}
		return nil
	}

	file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return wrapFileExplorerIOError(fullPath, err)
	}
	if err := file.Close(); err != nil {
		return wrapFileExplorerIOError(fullPath, err)
	}
	return nil
}

func (s *Service) DeleteFilesOrFolders(paths []string) error {
	normalized, err := normalizeFileExplorerDeletePaths(paths)
	if err != nil {
		return err
	}

	for _, path := range normalized {
		if _, err := os.Lstat(path); err != nil {
			return wrapFileExplorerIOError(path, err)
		}
	}
	if err := s.EnsureFileExplorerMutationAllowed(normalized...); err != nil {
		return err
	}

	for _, path := range normalized {
		if err := os.RemoveAll(path); err != nil {
			return wrapFileExplorerIOError(path, err)
		}
	}

	return nil
}

func (s *Service) RenameFileOrFolder(oldPath string, newName string) error {
	cleanOldPath, err := normalizeFileExplorerPath(oldPath, false)
	if err != nil {
		return err
	}
	if cleanOldPath == string(filepath.Separator) {
		return wrapFileExplorerError(ErrFileExplorerRootMutation, cleanOldPath, nil)
	}
	if err := validateFileExplorerName(newName); err != nil {
		return err
	}

	if _, err := os.Lstat(cleanOldPath); err != nil {
		return wrapFileExplorerIOError(cleanOldPath, err)
	}

	newPath := filepath.Join(filepath.Dir(cleanOldPath), newName)
	if newPath == cleanOldPath {
		return nil
	}
	if err := s.EnsureFileExplorerMutationAllowed(cleanOldPath, newPath); err != nil {
		return err
	}

	if _, err := os.Lstat(newPath); err == nil {
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, newPath, nil)
	} else if !os.IsNotExist(err) {
		return wrapFileExplorerIOError(newPath, err)
	}

	if err := os.Rename(cleanOldPath, newPath); err != nil {
		return wrapFileExplorerIOError(cleanOldPath, err)
	}
	return nil
}

func (s *Service) DownloadFile(id string) (*systemServiceInterfaces.FileDownload, error) {
	cleanPath, err := normalizeFileExplorerPath(id, false)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, wrapFileExplorerIOError(cleanPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, wrapFileExplorerError(ErrFileExplorerUnsupportedType, cleanPath, nil)
	}

	// Validate the opened handle as well as the pathname. O_NONBLOCK prevents a
	// path swapped to a FIFO between Stat and OpenFile from blocking the daemon.
	file, err := os.OpenFile(cleanPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, wrapFileExplorerIOError(cleanPath, err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, wrapFileExplorerIOError(cleanPath, err)
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, wrapFileExplorerError(ErrFileExplorerUnsupportedType, cleanPath, nil)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, wrapFileExplorerError(ErrFileExplorerUnsupportedType, cleanPath, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, wrapFileExplorerError(ErrFileExplorerUnsupportedType, cleanPath, err)
	}

	return &systemServiceInterfaces.FileDownload{
		Reader:  file,
		Name:    filepath.Base(cleanPath),
		ModTime: openedInfo.ModTime(),
	}, nil
}

const fileExplorerCopyBufferSize = 128 * 1024

type preparedFileExplorerTransfer struct {
	source            string
	target            string
	resolvedSource    string
	resolvedTarget    string
	sourceIsDirectory bool
}

func fileExplorerPathIsWithin(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolveFileExplorerEntryPath(path string) string {
	return filepath.Join(resolveFileExplorerGuardPath(filepath.Dir(path)), filepath.Base(path))
}

func validateFileExplorerCopySource(source string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return wrapFileExplorerIOError(path, walkErr)
		}

		info, err := entry.Info()
		if err != nil {
			return wrapFileExplorerIOError(path, err)
		}
		mode := info.Mode()
		if mode.IsRegular() || mode.IsDir() || mode&fs.ModeSymlink != 0 {
			return nil
		}

		return wrapFileExplorerError(ErrFileExplorerUnsupportedType, path, nil)
	})
}

func (s *Service) prepareFileExplorerTransfers(
	items []systemServiceInterfaces.FileTransferItem,
	move bool,
) ([]preparedFileExplorerTransfer, error) {
	if len(items) == 0 {
		return nil, wrapFileExplorerError(ErrFileExplorerInvalidOperation, "items_required", nil)
	}
	if len(items) > MaxFileExplorerBatchItems {
		return nil, fmt.Errorf("%w: maximum=%d", ErrFileExplorerBatchTooLarge, MaxFileExplorerBatchItems)
	}

	prepared := make([]preparedFileExplorerTransfer, 0, len(items))
	seenSources := make(map[string]struct{}, len(items))
	seenTargets := make(map[string]struct{}, len(items))

	for index, item := range items {
		source, err := normalizeFileExplorerPath(item.Source, false)
		if err != nil {
			return nil, err
		}
		destination, err := normalizeFileExplorerPath(item.Destination, false)
		if err != nil {
			return nil, err
		}
		if source == string(filepath.Separator) {
			return nil, wrapFileExplorerError(ErrFileExplorerRootMutation, source, nil)
		}

		sourceInfo, err := os.Lstat(source)
		if err != nil {
			return nil, wrapFileExplorerIOError(source, err)
		}

		target := destination
		destinationInfo, err := os.Stat(destination)
		switch {
		case err == nil && destinationInfo.IsDir():
			target = filepath.Join(destination, filepath.Base(source))
		case err == nil:
		case os.IsNotExist(err):
		default:
			return nil, wrapFileExplorerIOError(destination, err)
		}
		target = filepath.Clean(target)

		resolvedSource := resolveFileExplorerEntryPath(source)
		resolvedTarget := resolveFileExplorerEntryPath(target)
		if resolvedSource == resolvedTarget || (sourceInfo.IsDir() && fileExplorerPathIsWithin(resolvedTarget, resolvedSource)) {
			return nil, wrapFileExplorerError(
				ErrFileExplorerInvalidOperation,
				fmt.Sprintf("item=%d source=%s target=%s", index, source, target),
				nil,
			)
		}

		if _, err := os.Lstat(target); err == nil {
			return nil, wrapFileExplorerError(ErrFileExplorerAlreadyExists, target, nil)
		} else if !os.IsNotExist(err) {
			return nil, wrapFileExplorerIOError(target, err)
		}

		parent := filepath.Dir(target)
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return nil, wrapFileExplorerIOError(parent, err)
		}
		if !parentInfo.IsDir() {
			return nil, wrapFileExplorerError(ErrFileExplorerNotDirectory, parent, nil)
		}

		if _, duplicate := seenSources[resolvedSource]; duplicate {
			return nil, wrapFileExplorerError(ErrFileExplorerBatchConflict, "duplicate_source", nil)
		}
		if _, duplicate := seenTargets[resolvedTarget]; duplicate {
			return nil, wrapFileExplorerError(ErrFileExplorerBatchConflict, "duplicate_target", nil)
		}
		seenSources[resolvedSource] = struct{}{}
		seenTargets[resolvedTarget] = struct{}{}

		prepared = append(prepared, preparedFileExplorerTransfer{
			source:            source,
			target:            target,
			resolvedSource:    resolvedSource,
			resolvedTarget:    resolvedTarget,
			sourceIsDirectory: sourceInfo.IsDir(),
		})
	}

	for first := 0; first < len(prepared); first++ {
		for second := first + 1; second < len(prepared); second++ {
			left := prepared[first]
			right := prepared[second]
			if fileExplorerPathOverlaps(left.resolvedSource, right.resolvedSource) ||
				fileExplorerPathOverlaps(left.resolvedTarget, right.resolvedTarget) ||
				fileExplorerPathOverlaps(left.resolvedTarget, right.resolvedSource) ||
				fileExplorerPathOverlaps(right.resolvedTarget, left.resolvedSource) {
				return nil, wrapFileExplorerError(
					ErrFileExplorerBatchConflict,
					fmt.Sprintf("items=%d,%d", first, second),
					nil,
				)
			}
		}
	}

	guardedPaths := make([]string, 0, len(prepared)*2)
	for _, item := range prepared {
		guardedPaths = append(guardedPaths, item.target)
		if move {
			guardedPaths = append(guardedPaths, item.source)
		}
	}
	if err := s.EnsureFileExplorerMutationAllowed(guardedPaths...); err != nil {
		return nil, err
	}

	for _, item := range prepared {
		if err := validateFileExplorerCopySource(item.source); err != nil {
			return nil, err
		}
	}

	return prepared, nil
}

func copyFileExplorerRegularFile(source, target string, mode fs.FileMode) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return wrapFileExplorerIOError(source, err)
	}
	defer sourceFile.Close()

	targetFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrapFileExplorerIOError(target, err)
	}
	closed := false
	complete := false
	defer func() {
		if !closed {
			_ = targetFile.Close()
		}
		if !complete {
			_ = os.Remove(target)
		}
	}()

	buffer := make([]byte, fileExplorerCopyBufferSize)
	if _, err := io.CopyBuffer(targetFile, sourceFile, buffer); err != nil {
		return wrapFileExplorerIOError(target, err)
	}
	if err := targetFile.Chmod(mode.Perm()); err != nil {
		return wrapFileExplorerIOError(target, err)
	}
	if err := targetFile.Close(); err != nil {
		closed = true
		return wrapFileExplorerIOError(target, err)
	}
	closed = true
	complete = true
	return nil
}

func copyFileExplorerEntry(source, target string) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return wrapFileExplorerIOError(source, err)
	}

	switch mode := info.Mode(); {
	case mode.IsRegular():
		return copyFileExplorerRegularFile(source, target, mode)
	case mode&fs.ModeSymlink != 0:
		linkTarget, err := os.Readlink(source)
		if err != nil {
			return wrapFileExplorerIOError(source, err)
		}
		if err := os.Symlink(linkTarget, target); err != nil {
			return wrapFileExplorerIOError(target, err)
		}
		return nil
	case mode.IsDir():
		if err := os.Mkdir(target, 0o700); err != nil {
			return wrapFileExplorerIOError(target, err)
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.RemoveAll(target)
			}
		}()

		entries, err := os.ReadDir(source)
		if err != nil {
			return wrapFileExplorerIOError(source, err)
		}
		for _, entry := range entries {
			if err := copyFileExplorerEntry(
				filepath.Join(source, entry.Name()),
				filepath.Join(target, entry.Name()),
			); err != nil {
				return err
			}
		}
		if err := os.Chmod(target, mode.Perm()); err != nil {
			return wrapFileExplorerIOError(target, err)
		}
		complete = true
		return nil
	default:
		return wrapFileExplorerError(ErrFileExplorerUnsupportedType, source, nil)
	}
}

func removeCopiedFileExplorerEntry(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func (s *Service) moveFileExplorerEntry(item preparedFileExplorerTransfer) error {
	if _, err := os.Lstat(item.target); err == nil {
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, item.target, nil)
	} else if !os.IsNotExist(err) {
		return wrapFileExplorerIOError(item.target, err)
	}

	rename := os.Rename
	if s.fileExplorerRename != nil {
		rename = s.fileExplorerRename
	}
	if err := rename(item.source, item.target); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return wrapFileExplorerIOError(item.target, err)
	}

	if err := copyFileExplorerEntry(item.source, item.target); err != nil {
		return err
	}

	var removeErr error
	if item.sourceIsDirectory {
		removeErr = os.RemoveAll(item.source)
	} else {
		removeErr = os.Remove(item.source)
	}
	if removeErr == nil {
		return nil
	}

	sourceErr := wrapFileExplorerIOError(item.source, removeErr)
	if cleanupErr := removeCopiedFileExplorerEntry(item.target); cleanupErr != nil {
		return fmt.Errorf("%w: destination cleanup failed: %v", sourceErr, cleanupErr)
	}
	return sourceErr
}

func (s *Service) CopyOrMoveFilesOrFolders(
	items []systemServiceInterfaces.FileTransferItem,
	move bool,
) error {
	s.fileExplorerMutationMutex.Lock()
	defer s.fileExplorerMutationMutex.Unlock()

	prepared, err := s.prepareFileExplorerTransfers(items, move)
	if err != nil {
		return err
	}

	for _, item := range prepared {
		if move {
			if err := s.moveFileExplorerEntry(item); err != nil {
				return err
			}
			continue
		}
		if err := copyFileExplorerEntry(item.source, item.target); err != nil {
			return err
		}
	}

	return nil
}
