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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/fsnotify/fsnotify"
)

// ISOFile represents an ISO file found on the filesystem
type ISOFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Type    string    `json:"type"`   // "isos", "jail_templates", "vm_templates", "manual"
	Source  string    `json:"source"` // source URL or "manual"
}

// ISOScanner provides stateless ISO scanning functionality
type ISOScanner struct {
	watcher   *fsnotify.Watcher
	watchDirs []string
	mu        sync.RWMutex
	isoCache  map[string][]ISOFile
	cacheTime time.Time
	cacheTTL  time.Duration
}

// NewISOScanner creates a new ISO scanner
func NewISOScanner() (*ISOScanner, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	scanner := &ISOScanner{
		watcher:  watcher,
		isoCache: make(map[string][]ISOFile),
		cacheTTL: 5 * time.Minute, // Cache for 5 minutes
	}

	// Get download directories to watch
	isosDir := config.GetDownloadsPath("isos")
	jailTemplatesDir := config.GetDownloadsPath("jail_templates")
	vmTemplatesDir := config.GetDownloadsPath("vm_templates")

	scanner.watchDirs = []string{isosDir, jailTemplatesDir, vmTemplatesDir}

	// Start watching directories
	for _, dir := range scanner.watchDirs {
		if err := scanner.watchDirectory(dir); err != nil {
			logger.L.Warn().Msgf("Failed to watch directory %s: %v", dir, err)
		}
	}

	// Start the filesystem watcher
	go scanner.watchFilesystem()

	return scanner, nil
}

// watchDirectory adds a directory to the watcher
func (s *ISOScanner) watchDirectory(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return s.watcher.Add(dir)
}

// watchFilesystem monitors filesystem changes
func (s *ISOScanner) watchFilesystem() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}

			// Only invalidate cache for file changes
			if event.Op&fsnotify.Create == fsnotify.Create ||
				event.Op&fsnotify.Write == fsnotify.Write ||
				event.Op&fsnotify.Remove == fsnotify.Remove ||
				event.Op&fsnotify.Rename == fsnotify.Rename {

				// Check if it's an ISO file
				if strings.HasSuffix(strings.ToLower(event.Name), ".iso") ||
					strings.HasSuffix(strings.ToLower(event.Name), ".img") {
					s.invalidateCache()
					logger.L.Debug().Msgf("ISO file changed: %s", event.Name)
				}
			}

		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			logger.L.Error().Msgf("Filesystem watcher error: %v", err)
		}
	}
}

// invalidateCache clears the ISO cache
func (s *ISOScanner) invalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isoCache = make(map[string][]ISOFile)
	s.cacheTime = time.Time{}
}

// isCacheValid checks if the cache is still valid
func (s *ISOScanner) isCacheValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.cacheTime.IsZero() && time.Since(s.cacheTime) < s.cacheTTL
}

// scanForISOs scans all configured directories for ISO files
func (s *ISOScanner) scanForISOs() ([]ISOFile, error) {
	if s.isCacheValid() {
		s.mu.RLock()
		defer s.mu.RUnlock()

		var allISOs []ISOFile
		for _, isos := range s.isoCache {
			allISOs = append(allISOs, isos...)
		}
		return allISOs, nil
	}

	var allISOs []ISOFile
	isoMap := make(map[string][]ISOFile)

	// Scan ISOs directory
	isosDir := config.GetDownloadsPath("isos")
	if isoFiles, err := s.scanDirectory(isosDir, "isos", "manual"); err != nil {
		logger.L.Warn().Msgf("Failed to scan ISOs directory %s: %v", isosDir, err)
	} else {
		isoMap["isos"] = isoFiles
		allISOs = append(allISOs, isoFiles...)
	}

	// Scan jail templates directory
	jailTemplatesDir := config.GetDownloadsPath("jail_templates")
	if jailTemplateFiles, err := s.scanDirectory(jailTemplatesDir, "jail_templates", "manual"); err != nil {
		logger.L.Warn().Msgf("Failed to scan jail templates directory %s: %v", jailTemplatesDir, err)
	} else {
		isoMap["jail_templates"] = jailTemplateFiles
		allISOs = append(allISOs, jailTemplateFiles...)
	}

	// Scan VM templates directory
	vmTemplatesDir := config.GetDownloadsPath("vm_templates")
	if vmTemplateFiles, err := s.scanDirectory(vmTemplatesDir, "vm_templates", "manual"); err != nil {
		logger.L.Warn().Msgf("Failed to scan VM templates directory %s: %v", vmTemplatesDir, err)
	} else {
		isoMap["vm_templates"] = vmTemplateFiles
		allISOs = append(allISOs, vmTemplateFiles...)
	}

	// Update cache
	s.mu.Lock()
	s.isoCache = isoMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return allISOs, nil
}

// scanDirectory scans a single directory for ISO files
func (s *ISOScanner) scanDirectory(dir, scanType, source string) ([]ISOFile, error) {
	var isos []ISOFile

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return isos, nil
		}
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".iso") &&
			!strings.HasSuffix(strings.ToLower(name), ".img") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			logger.L.Warn().Msgf("Failed to get file info for %s: %v", name, err)
			continue
		}

		iso := ISOFile{
			Name:    name,
			Path:    filepath.Join(dir, name),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Type:    scanType,
			Source:  source,
		}

		isos = append(isos, iso)
	}

	// Sort by modification time (newest first)
	sort.Slice(isos, func(i, j int) bool {
		return isos[i].ModTime.After(isos[j].ModTime)
	})

	return isos, nil
}

// ListISOs returns all available ISO files
func (s *ISOScanner) ListISOs() ([]ISOFile, error) {
	return s.scanForISOs()
}

// FindISOByName finds an ISO file by name
func (s *ISOScanner) FindISOByName(name string) (*ISOFile, error) {
	isos, err := s.scanForISOs()
	if err != nil {
		return nil, err
	}

	for _, iso := range isos {
		if iso.Name == name {
			return &iso, nil
		}
	}

	return nil, fmt.Errorf("iso_not_found: %s", name)
}

// FindISOByPath finds an ISO file by full path
func (s *ISOScanner) FindISOByPath(path string) (*ISOFile, error) {
	isos, err := s.scanForISOs()
	if err != nil {
		return nil, err
	}

	for _, iso := range isos {
		if iso.Path == path {
			return &iso, nil
		}
	}

	return nil, fmt.Errorf("iso_not_found: %s", path)
}

// Close closes the filesystem watcher
func (s *ISOScanner) Close() error {
	if s.watcher != nil {
		return s.watcher.Close()
	}
	return nil
}
