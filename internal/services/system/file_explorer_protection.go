// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package system

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/pkg/utils"
	"golang.org/x/sys/unix"
)

var ErrFileExplorerProtectedPath = errors.New("file_explorer_protected_path")

const (
	FileExplorerProtectedRoot          = "root"
	FileExplorerProtectedSystemFolder  = "system_folder"
	FileExplorerProtectedBaseSystem    = "base_system"
	FileExplorerProtectedMountPoint    = "mount_point"
	FileExplorerProtectedContainsMount = "contains_mount_point"
	FileExplorerProtectedJailRoot      = "jail_root"
	FileExplorerProtectedSylveData     = "sylve_data"
	FileExplorerProtectedContainsSylve = "contains_sylve_data"
)

type FileExplorerProtectedError struct {
	Path   string
	Reason string
}

func (e *FileExplorerProtectedError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrFileExplorerProtectedPath, e.Reason, e.Path)
}

func (e *FileExplorerProtectedError) Unwrap() error {
	return ErrFileExplorerProtectedPath
}

type fileExplorerMount struct {
	Path   string
	Source string
	Type   string
}

type fileExplorerProtection struct {
	mounts     map[string]string
	jailRoots  map[string]struct{}
	sylveRoots []string
	enclosing  map[string]string
}

var fileExplorerJailDataset = regexp.MustCompile(`^[^/@]+/sylve/jails/[0-9]+$`)

func newFileExplorerProtection(mounts []fileExplorerMount, dataPath string) *fileExplorerProtection {
	protection := &fileExplorerProtection{
		mounts:    make(map[string]string, len(mounts)),
		jailRoots: map[string]struct{}{},
		enclosing: map[string]string{},
	}
	if dataPath = strings.TrimSpace(dataPath); filepath.IsAbs(dataPath) {
		protection.sylveRoots = append(protection.sylveRoots, filepath.Clean(dataPath))
	}
	for _, mount := range mounts {
		if !filepath.IsAbs(mount.Path) {
			continue
		}
		path := filepath.Clean(mount.Path)
		protection.mounts[path] = mount.Source
		if mount.Type == "zfs" && utils.IsSylveDataset(mount.Source) {
			protection.sylveRoots = append(protection.sylveRoots, path)
			if fileExplorerJailDataset.MatchString(mount.Source) {
				protection.jailRoots[path] = struct{}{}
			}
		}
	}

	enclose := func(path, reason string) {
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			if _, set := protection.enclosing[parent]; !set {
				protection.enclosing[parent] = reason
			}
			if parent == string(filepath.Separator) {
				return
			}
		}
	}
	for _, root := range protection.sylveRoots {
		enclose(root, FileExplorerProtectedContainsSylve)
	}
	for path := range protection.mounts {
		enclose(path, FileExplorerProtectedContainsMount)
	}
	return protection
}

func (p *fileExplorerProtection) reason(path string) string {
	path = filepath.Clean(path)
	if path == string(filepath.Separator) {
		return FileExplorerProtectedRoot
	}
	if _, ok := p.jailRoots[path]; ok {
		return FileExplorerProtectedJailRoot
	}
	if _, ok := p.mounts[path]; ok {
		return FileExplorerProtectedMountPoint
	}
	if filepath.Dir(path) == string(filepath.Separator) {
		return FileExplorerProtectedSystemFolder
	}

	insideJail := false
	for root := range p.jailRoots {
		if utils.CleanPathWithin(path, root) {
			if utils.CleanPathWithin(path, root+"/.sylve") {
				return FileExplorerProtectedSylveData
			}
			insideJail = true
			break
		}
	}
	if !insideJail {
		for _, root := range p.sylveRoots {
			if utils.CleanPathWithin(path, root) {
				return FileExplorerProtectedSylveData
			}
		}
	}
	if utils.IsBaseSystemPath(path) {
		return FileExplorerProtectedBaseSystem
	}
	return p.enclosing[path]
}

func (p *fileExplorerProtection) mountSource(path string) string {
	return p.mounts[filepath.Clean(path)]
}

func (p *fileExplorerProtection) require(paths ...string) error {
	for _, path := range paths {
		if reason := p.reason(resolveFileExplorerEntryPath(path)); reason != "" {
			return &FileExplorerProtectedError{Path: path, Reason: reason}
		}
	}
	return nil
}

func fileExplorerMounts() ([]fileExplorerMount, error) {
	for range 3 {
		count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		stats := make([]unix.Statfs_t, count+16)
		count, err = unix.Getfsstat(stats, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		if count >= len(stats) {
			continue
		}
		mounts := make([]fileExplorerMount, 0, count)
		for _, stat := range stats[:count] {
			mounts = append(mounts, fileExplorerMount{
				Path:   unix.ByteSliceToString(stat.Mntonname[:]),
				Source: unix.ByteSliceToString(stat.Mntfromname[:]),
				Type:   unix.ByteSliceToString(stat.Fstypename[:]),
			})
		}
		return mounts, nil
	}
	return nil, errors.New("mount table kept changing")
}

func fileExplorerDataPath() string {
	if config.ParsedConfig == nil {
		return ""
	}
	path := strings.TrimSpace(config.ParsedConfig.DataPath)
	if path == "" {
		return ""
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

func (s *Service) fileExplorerProtection() (*fileExplorerProtection, error) {
	if s != nil && s.fileExplorerProtectionLoader != nil {
		return s.fileExplorerProtectionLoader()
	}
	mounts, err := fileExplorerMounts()
	if err != nil {
		return nil, wrapFileExplorerError(ErrFileExplorerOperationFailed, "mount_table", err)
	}
	return newFileExplorerProtection(mounts, fileExplorerDataPath()), nil
}

func (s *Service) requireFileExplorerPathsUnprotected(paths ...string) error {
	protection, err := s.fileExplorerProtection()
	if err != nil {
		return err
	}
	return protection.require(paths...)
}
