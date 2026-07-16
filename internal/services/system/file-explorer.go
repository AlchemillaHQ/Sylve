// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// EnsureFileExplorerMutationAllowed blocks writes that could alter a jail
// whose dataset is being replaced by a restore. Read-only explorer operations
// intentionally do not call this method.
func (s *Service) EnsureFileExplorerMutationAllowed(paths ...string) error {
	if s == nil || s.DB == nil || !replicationguard.GuestOperationSchemaReady(s.DB) {
		return nil
	}
	var operations []clusterModels.ReplicationGuestOperation
	if err := s.DB.Where("guest_type = ? AND operation = ?", clusterModels.ReplicationGuestTypeJail, clusterModels.ReplicationGuestOperationRestore).Find(&operations).Error; err != nil {
		return fmt.Errorf("restore_fence_lookup_failed: %w", err)
	}
	if len(operations) == 0 {
		return nil
	}
	jailsPath, err := config.GetJailsPath()
	if err != nil {
		return fmt.Errorf("restore_fence_jails_path_failed: %w", err)
	}
	for _, operation := range operations {
		jailRoot := filepath.Join(jailsPath, strconv.FormatUint(uint64(operation.GuestID), 10))
		for _, path := range paths {
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				path = "/" + path
			}
			if fileExplorerPathOverlaps(path, jailRoot) {
				return fmt.Errorf("restore_in_progress: ctid=%d", operation.GuestID)
			}
		}
	}
	return nil
}

func (s *Service) Traverse(path string) ([]systemServiceInterfaces.FileNode, error) {
	if path == "" {
		path = "/"
	}

	if !filepath.IsAbs(path) {
		path = "/" + path
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var nodes []systemServiceInterfaces.FileNode
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
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
	if path == "" {
		path = "/"
	}

	if !filepath.IsAbs(path) {
		path = "/" + path
	}

	if strings.Contains(name, "/") || name == "." || name == ".." {
		return fmt.Errorf("invalid name: %s", name)
	}

	fullPath := filepath.Join(path, name)
	if err := s.EnsureFileExplorerMutationAllowed(fullPath); err != nil {
		return err
	}

	if _, err := os.Stat(fullPath); err == nil {
		return fmt.Errorf("file or folder already exists: %s", fullPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	if isFolder {
		return os.Mkdir(fullPath, 0755)
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}

	defer file.Close()

	return nil
}

func (s *Service) DeleteFileOrFolder(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if !filepath.IsAbs(path) {
		path = "/" + path
	}
	if err := s.EnsureFileExplorerMutationAllowed(path); err != nil {
		return err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("file or folder does not exist: %s", path)
	}

	return os.RemoveAll(path)
}

func (s *Service) DeleteFilesOrFolders(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no paths provided")
	}

	for _, path := range paths {
		if path == "" {
			return fmt.Errorf("empty path provided")
		}

		absPath := path
		if !filepath.IsAbs(absPath) {
			absPath = "/" + absPath
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return fmt.Errorf("file or folder does not exist: %s", absPath)
		}
		if err := s.EnsureFileExplorerMutationAllowed(absPath); err != nil {
			return err
		}
	}

	for _, path := range paths {
		absPath := path
		if !filepath.IsAbs(absPath) {
			absPath = "/" + absPath
		}

		if err := os.RemoveAll(absPath); err != nil {
			return fmt.Errorf("failed to delete %s: %w", absPath, err)
		}
	}

	return nil
}

func (s *Service) RenameFileOrFolder(oldPath string, newName string) error {
	if oldPath == "" || newName == "" {
		return fmt.Errorf("old path and new name cannot be empty")
	}

	if !filepath.IsAbs(oldPath) {
		oldPath = filepath.Clean("/" + oldPath)
	}

	if strings.Contains(newName, "/") || newName == "." || newName == ".." {
		return fmt.Errorf("invalid new name: %s", newName)
	}

	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if err := s.EnsureFileExplorerMutationAllowed(oldPath, newPath); err != nil {
		return err
	}

	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("file or folder already exists: %s", newPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.Rename(oldPath, newPath)
}

func (s *Service) DownloadFile(id string) (string, error) {
	cleanPath := filepath.Clean(id)

	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("path must be absolute")
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("file not found: %w", err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("cannot download a directory")
	}

	return cleanPath, nil
}

func copyFile(source, destination string, perm fs.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}
	if err := os.WriteFile(destination, data, perm); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func copyDir(sourceDir, destDir string) error {
	return filepath.Walk(sourceDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		return copyFile(path, targetPath, info.Mode())
	})
}

func (s *Service) CopyOrMoveFileOrFolder(source, destination string, move bool) error {
	if source == "" || destination == "" {
		return fmt.Errorf("source and destination cannot be empty")
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return fmt.Errorf("both source and destination must be absolute paths")
	}
	guardedPaths := []string{destination}
	if move {
		guardedPaths = append(guardedPaths, source)
	}
	if err := s.EnsureFileExplorerMutationAllowed(guardedPaths...); err != nil {
		return err
	}

	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("source does not exist: %w", err)
	}

	if destInfo, err := os.Stat(destination); err == nil && destInfo.IsDir() {
		destination = filepath.Join(destination, filepath.Base(source))
	}

	if move {
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("failed to move: %w", err)
		}
		return nil
	}

	if info.IsDir() {
		return copyDir(source, destination)
	}

	return copyFile(source, destination, info.Mode())
}

func (s *Service) CopyOrMoveFilesOrFolders(pairs [][2]string, move bool) error {
	if len(pairs) == 0 {
		return fmt.Errorf("no source-destination pairs provided")
	}

	for _, pair := range pairs {
		source := pair[0]
		dest := pair[1]

		if source == "" || dest == "" {
			return fmt.Errorf("source and destination cannot be empty")
		}
		if !filepath.IsAbs(source) || !filepath.IsAbs(dest) {
			return fmt.Errorf("both source and destination must be absolute paths")
		}
		guardedPaths := []string{dest}
		if move {
			guardedPaths = append(guardedPaths, source)
		}
		if err := s.EnsureFileExplorerMutationAllowed(guardedPaths...); err != nil {
			return err
		}

		if _, err := os.Stat(source); os.IsNotExist(err) {
			return fmt.Errorf("source does not exist: %s", source)
		} else if err != nil {
			return fmt.Errorf("failed to stat source %s: %w", source, err)
		}
	}

	for _, pair := range pairs {
		source := pair[0]
		dest := pair[1]

		info, _ := os.Stat(source)

		target := dest
		if destInfo, err := os.Stat(dest); err == nil && destInfo.IsDir() {
			target = filepath.Join(dest, filepath.Base(source))
		}

		if move {
			if err := os.Rename(source, target); err != nil {
				return fmt.Errorf("failed to move %s to %s: %w", source, target, err)
			}
		} else {
			if info.IsDir() {
				if err := copyDir(source, target); err != nil {
					return fmt.Errorf("failed to copy directory %s to %s: %w", source, target, err)
				}
			} else {
				if err := copyFile(source, target, info.Mode()); err != nil {
					return fmt.Errorf("failed to copy file %s to %s: %w", source, target, err)
				}
			}
		}
	}

	return nil
}
