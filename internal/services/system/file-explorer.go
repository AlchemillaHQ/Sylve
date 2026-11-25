// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

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

func ValidateFileExplorerName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsRune(name, filepath.Separator) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
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
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return s.fileExplorerNodes(cleanPath, names), nil
}

const (
	DefaultFileExplorerListLimit = 10000
	MaxFileExplorerListLimit     = 50000
)

func (s *Service) ListDirectory(path string, limit int, query string) (systemServiceInterfaces.FileListing, error) {
	cleanPath, err := normalizeFileExplorerPath(path, true)
	if err != nil {
		return systemServiceInterfaces.FileListing{}, err
	}
	if limit <= 0 {
		limit = DefaultFileExplorerListLimit
	}
	limit = min(limit, MaxFileExplorerListLimit)

	directory, err := os.Open(cleanPath)
	if err != nil {
		return systemServiceInterfaces.FileListing{}, wrapFileExplorerIOError(cleanPath, err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return systemServiceInterfaces.FileListing{}, wrapFileExplorerIOError(cleanPath, err)
	}

	type candidate struct {
		name   string
		folder bool
		key    string
	}
	query = strings.ToLower(strings.TrimSpace(query))
	matched := make([]candidate, 0, min(len(entries), limit))
	for _, entry := range entries {
		key := strings.ToLower(entry.Name())
		if query != "" && !strings.Contains(key, query) {
			continue
		}
		matched = append(matched, candidate{name: entry.Name(), folder: entry.IsDir(), key: key})
	}

	listing := systemServiceInterfaces.FileListing{
		Total:     len(entries),
		Matched:   len(matched),
		Truncated: len(matched) > limit,
	}
	if listing.Truncated {
		sort.Slice(matched, func(i, j int) bool {
			if matched[i].folder != matched[j].folder {
				return matched[i].folder
			}
			if matched[i].key != matched[j].key {
				return matched[i].key < matched[j].key
			}
			return matched[i].name < matched[j].name
		})
		matched = matched[:limit]
	}
	names := make([]string, len(matched))
	for index, entry := range matched {
		names[index] = entry.name
	}
	listing.Entries = s.fileExplorerNodes(cleanPath, names)
	return listing, nil
}

func (s *Service) fileExplorerNodes(directory string, names []string) []systemServiceInterfaces.FileNode {
	protection, err := s.fileExplorerProtection()
	if err != nil {
		protection = nil
	}
	resolvedDirectory := resolveFileExplorerGuardPath(directory)
	owners := newFileExplorerOwners()
	nodes := make([]systemServiceInterfaces.FileNode, 0, len(names))
	for _, name := range names {
		node, ok := fileExplorerNode(filepath.Join(directory, name), owners)
		if !ok {
			continue
		}
		if protection != nil {
			entry := filepath.Join(resolvedDirectory, name)
			node.Protected = protection.reason(entry)
			node.Mount = protection.mountSource(entry)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

type fileExplorerOwners struct {
	users  map[uint32]string
	groups map[uint32]string
}

func newFileExplorerOwners() *fileExplorerOwners {
	return &fileExplorerOwners{users: map[uint32]string{}, groups: map[uint32]string{}}
}

func (o *fileExplorerOwners) user(id uint32) string {
	if name, ok := o.users[id]; ok {
		return name
	}
	name := strconv.FormatUint(uint64(id), 10)
	if account, err := user.LookupId(name); err == nil {
		name = account.Username
	}
	o.users[id] = name
	return name
}

func (o *fileExplorerOwners) group(id uint32) string {
	if name, ok := o.groups[id]; ok {
		return name
	}
	name := strconv.FormatUint(uint64(id), 10)
	if group, err := user.LookupGroupId(name); err == nil {
		name = group.Name
	}
	o.groups[id] = name
	return name
}

func fileExplorerNode(path string, owners *fileExplorerOwners) (systemServiceInterfaces.FileNode, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return systemServiceInterfaces.FileNode{}, false
	}
	node := systemServiceInterfaces.FileNode{ID: path}
	if info.Mode()&fs.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil {
			node.Link = target
		}
		if resolved, err := os.Stat(path); err == nil {
			info = resolved
		} else {
			node.Broken = true
		}
	}

	node.Date = info.ModTime()
	mode := info.Mode()
	switch {
	case mode.IsDir():
		node.Type = "folder"
		node.Lazy = true
	default:
		node.Type = "file"
		switch {
		case mode.IsRegular():
			node.Size = info.Size()
		case mode&fs.ModeNamedPipe != 0:
			node.Special = "fifo"
		case mode&fs.ModeSocket != 0:
			node.Special = "socket"
		case mode&fs.ModeDevice != 0:
			node.Special = "device"
		}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		node.Mode = fmt.Sprintf("%04o", stat.Mode&0o7777)
		node.Owner = owners.user(stat.Uid)
		node.Group = owners.group(stat.Gid)
	}
	return node, true
}

func (s *Service) AddFileOrFolder(path string, name string, isFolder bool) error {
	cleanPath, err := normalizeFileExplorerPath(path, false)
	if err != nil {
		return err
	}
	if err := ValidateFileExplorerName(name); err != nil {
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

func (s *Service) DeleteFilesOrFolders(ctx context.Context, paths []string) error {
	normalized, err := normalizeFileExplorerDeletePaths(paths)
	if err != nil {
		return err
	}

	for _, path := range normalized {
		if _, err := os.Lstat(path); err != nil {
			return wrapFileExplorerIOError(path, err)
		}
	}
	if err := s.requireFileExplorerPathsUnprotected(normalized...); err != nil {
		return err
	}
	if err := s.EnsureFileExplorerMutationAllowed(normalized...); err != nil {
		return err
	}

	completed := make([]string, 0, len(normalized))
	for _, path := range normalized {
		if err := removeFileExplorerTree(ctx, path); err != nil {
			return batchFailure(completed, wrapFileExplorerIOError(path, err))
		}
		completed = append(completed, path)
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
	if err := ValidateFileExplorerName(newName); err != nil {
		return err
	}

	if _, err := os.Lstat(cleanOldPath); err != nil {
		return wrapFileExplorerIOError(cleanOldPath, err)
	}

	newPath := filepath.Join(filepath.Dir(cleanOldPath), newName)
	if newPath == cleanOldPath {
		return nil
	}
	if err := s.requireFileExplorerPathsUnprotected(cleanOldPath); err != nil {
		return err
	}
	if err := s.EnsureFileExplorerMutationAllowed(cleanOldPath, newPath); err != nil {
		return err
	}

	if _, err := os.Lstat(newPath); err == nil {
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, newPath, nil)
	} else if !os.IsNotExist(err) {
		return wrapFileExplorerIOError(newPath, err)
	}

	if err := renameNoReplace(cleanOldPath, newPath); err != nil {
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
	movedSources := make([]string, 0, len(prepared))
	for _, item := range prepared {
		guardedPaths = append(guardedPaths, item.target)
		if move {
			guardedPaths = append(guardedPaths, item.source)
			movedSources = append(movedSources, item.source)
		}
	}
	if len(movedSources) > 0 {
		if err := s.requireFileExplorerPathsUnprotected(movedSources...); err != nil {
			return nil, err
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

const fileExplorerModeBits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

type fileExplorerInode struct {
	device uint64
	inode  uint64
}

type fileExplorerCopier struct {
	ctx    context.Context
	buffer []byte
	links  map[fileExplorerInode]string
	flags  []fileExplorerPendingFlags
}

func newFileExplorerCopier(ctx context.Context) *fileExplorerCopier {
	return &fileExplorerCopier{
		ctx:    ctx,
		buffer: make([]byte, fileExplorerCopyBufferSize),
		links:  map[fileExplorerInode]string{},
	}
}

func copyFileExplorerRegularFile(ctx context.Context, source, target string, info fs.FileInfo) error {
	copier := newFileExplorerCopier(ctx)
	if err := copier.regular(source, target, info); err != nil {
		return err
	}
	copier.applyFlags()
	return nil
}

func copyFileExplorerEntry(ctx context.Context, source, target string) error {
	return newFileExplorerCopier(ctx).item(source, target)
}

func (c *fileExplorerCopier) item(source, target string) error {
	if err := c.entry(source, target); err != nil {
		c.discardFlags()
		return err
	}
	c.applyFlags()
	return nil
}

func (c *fileExplorerCopier) regular(source, target string, info fs.FileInfo) error {
	var identity *fileExplorerInode
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
		identity = &fileExplorerInode{device: uint64(stat.Dev), inode: stat.Ino}
		if first, seen := c.links[*identity]; seen {
			if err := os.Link(first, target); err == nil {
				return nil
			}
		}
	}

	sourceFile, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return wrapFileExplorerIOError(source, err)
	}
	defer sourceFile.Close()
	if opened, err := sourceFile.Stat(); err != nil {
		return wrapFileExplorerIOError(source, err)
	} else if !opened.Mode().IsRegular() {
		return wrapFileExplorerError(ErrFileExplorerUnsupportedType, source, nil)
	}

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

	if _, err := io.CopyBuffer(targetFile, contextReader{ctx: c.ctx, reader: sourceFile}, c.buffer); err != nil {
		return wrapFileExplorerIOError(target, err)
	}
	if err := targetFile.Close(); err != nil {
		closed = true
		return wrapFileExplorerIOError(target, err)
	}
	closed = true
	if err := applyFileExplorerMetadata(source, target, info); err != nil {
		return err
	}
	complete = true
	c.deferFlags(target, info)
	if identity != nil {
		c.links[*identity] = target
	}
	return nil
}

func (c *fileExplorerCopier) entry(source, target string) error {
	if err := c.ctx.Err(); err != nil {
		return wrapFileExplorerIOError(source, err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return wrapFileExplorerIOError(source, err)
	}

	switch mode := info.Mode(); {
	case mode.IsRegular():
		return c.regular(source, target, info)
	case mode&fs.ModeSymlink != 0:
		linkTarget, err := os.Readlink(source)
		if err != nil {
			return wrapFileExplorerIOError(source, err)
		}
		if err := os.Symlink(linkTarget, target); err != nil {
			return wrapFileExplorerIOError(target, err)
		}
		return applyFileExplorerMetadata(source, target, info)
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
			if err := c.entry(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		if err := applyFileExplorerMetadata(source, target, info); err != nil {
			return err
		}
		c.deferFlags(target, info)
		complete = true
		return nil
	default:
		return wrapFileExplorerError(ErrFileExplorerUnsupportedType, source, nil)
	}
}

func (s *Service) moveFileExplorerEntry(copier *fileExplorerCopier, item preparedFileExplorerTransfer) error {
	if _, err := os.Lstat(item.target); err == nil {
		return wrapFileExplorerError(ErrFileExplorerAlreadyExists, item.target, nil)
	} else if !os.IsNotExist(err) {
		return wrapFileExplorerIOError(item.target, err)
	}

	rename := renameNoReplace
	if s.fileExplorerRename != nil {
		rename = s.fileExplorerRename
	}
	if err := rename(item.source, item.target); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return wrapFileExplorerIOError(item.target, err)
	}

	if err := copier.item(item.source, item.target); err != nil {
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
	return wrapFileExplorerError(ErrFileExplorerMoveIncomplete, item.source, removeErr)
}

func (s *Service) acquireFileExplorerTransfer(ctx context.Context) (func(), error) {
	s.fileExplorerTransferOnce.Do(func() {
		s.fileExplorerTransferSlot = make(chan struct{}, 1)
	})
	if err := ctx.Err(); err != nil {
		return nil, wrapFileExplorerError(ErrFileExplorerCancelled, "waiting_for_transfer", err)
	}
	select {
	case s.fileExplorerTransferSlot <- struct{}{}:
		return func() { <-s.fileExplorerTransferSlot }, nil
	case <-ctx.Done():
		return nil, wrapFileExplorerError(ErrFileExplorerCancelled, "waiting_for_transfer", ctx.Err())
	}
}

func (s *Service) CopyOrMoveFilesOrFolders(
	ctx context.Context,
	items []systemServiceInterfaces.FileTransferItem,
	move bool,
) error {
	release, err := s.acquireFileExplorerTransfer(ctx)
	if err != nil {
		return err
	}
	defer release()

	prepared, err := s.prepareFileExplorerTransfers(items, move)
	if err != nil {
		return err
	}

	copier := newFileExplorerCopier(ctx)
	completed := make([]string, 0, len(prepared))
	for _, item := range prepared {
		if err := ctx.Err(); err != nil {
			return batchFailure(completed, wrapFileExplorerIOError(item.source, err))
		}
		if move {
			err = s.moveFileExplorerEntry(copier, item)
		} else {
			err = copier.item(item.source, item.target)
		}
		if err != nil {
			return batchFailure(completed, err)
		}
		completed = append(completed, item.source)
	}

	return nil
}
