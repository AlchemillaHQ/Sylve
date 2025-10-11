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
	"strconv"
	"sync"
	"time"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/coolerfall/aria2go"
)

// Aria2Client represents a libaria2 client
type Aria2Client struct {
	client    *aria2go.Aria2
	downloads map[string]*DownloadInfo
	mutex     sync.RWMutex
	isRunning bool
	notifier  *Aria2Notifier
}

// DownloadInfo represents download information
type DownloadInfo struct {
	GID             string
	Status          string
	TotalLength     int64
	CompletedLength int64
	DownloadSpeed   int64
	Files           []string
	Directory       string
	Filename        string
	DownloadType    string
	URL             string
	StartTime       time.Time
}

// Aria2DownloadStatus represents the status of a download (for compatibility)
type Aria2DownloadStatus struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"`
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	DownloadSpeed   string      `json:"downloadSpeed"`
	Files           []Aria2File `json:"files"`
	Dir             string      `json:"dir"`
}

// Aria2File represents a file in aria2 (for compatibility)
type Aria2File struct {
	Index    string   `json:"index"`
	Path     string   `json:"path"`
	Length   string   `json:"length"`
	URI      []string `json:"uris"`
	Selected string   `json:"selected"`
}

// Aria2Notifier implements the aria2go.Notifier interface
type Aria2Notifier struct {
	client *Aria2Client
}

// NewAria2Notifier creates a new notifier
func NewAria2Notifier(client *Aria2Client) *Aria2Notifier {
	return &Aria2Notifier{client: client}
}

// OnStart is called when a download starts
func (n *Aria2Notifier) OnStart(gid string) {
	logger.L.Info().Msgf("Download started: %s", gid)
	n.client.updateDownloadStatus(gid, "active")
}

// OnPause is called when a download is paused
func (n *Aria2Notifier) OnPause(gid string) {
	logger.L.Info().Msgf("Download paused: %s", gid)
	n.client.updateDownloadStatus(gid, "paused")
}

// OnStop is called when a download is stopped
func (n *Aria2Notifier) OnStop(gid string) {
	logger.L.Info().Msgf("Download stopped: %s", gid)
	n.client.updateDownloadStatus(gid, "removed")
}

// OnComplete is called when a download completes
func (n *Aria2Notifier) OnComplete(gid string) {
	logger.L.Info().Msgf("Download completed: %s", gid)
	n.client.updateDownloadStatus(gid, "complete")
}

// OnError is called when a download encounters an error
func (n *Aria2Notifier) OnError(gid string) {
	logger.L.Error().Msgf("Download error: %s", gid)
	n.client.updateDownloadStatus(gid, "error")
}

// NewAria2Client creates a new libaria2 client
func NewAria2Client() (*Aria2Client, error) {
	// Create download directory
	downloadDir := config.GetDownloadsPath("isos")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create download directory: %w", err)
	}

	// Initialize aria2go client
	aria2Client := &Aria2Client{
		downloads: make(map[string]*DownloadInfo),
		isRunning: true,
		notifier:  nil,
	}

	// Create notifier
	notifier := NewAria2Notifier(aria2Client)
	aria2Client.notifier = notifier

	// Initialize aria2 with configuration
	client := aria2go.NewAria2(aria2go.Config{
		Options: aria2go.Options{
			"dir":                       downloadDir,
			"save-session":              filepath.Join(downloadDir, "aria2.session"),
			"max-connection-per-server": "16",
			"split":                     "16",
			"min-split-size":            "1M",
			"continue":                  "true",
			"auto-file-renaming":        "false",
		},
		Notifier: notifier,
	})

	aria2Client.client = client

	// Start aria2 in a goroutine
	go func() {
		client.Run()
	}()

	// Start monitoring downloads
	go aria2Client.monitorDownloads()

	logger.L.Info().Msg("Started aria2go client")
	return aria2Client, nil
}

// updateDownloadStatus updates the status of a download
func (c *Aria2Client) updateDownloadStatus(gid, status string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if info, exists := c.downloads[gid]; exists {
		info.Status = status

		// Get fresh status from aria2
		downloadInfo := c.client.GetDownloadInfo(gid)
		if downloadInfo.TotalLength > 0 {
			info.TotalLength = downloadInfo.TotalLength
			info.CompletedLength = downloadInfo.BytesCompleted
			info.DownloadSpeed = int64(downloadInfo.DownloadSpeed)
		}
	}
}

// monitorDownloads monitors the status of all downloads
func (c *Aria2Client) monitorDownloads() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for c.isRunning {
		select {
		case <-ticker.C:
			c.updateDownloadStatuses()
		}
	}
}

// updateDownloadStatuses updates the status of all downloads
func (c *Aria2Client) updateDownloadStatuses() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for gid, info := range c.downloads {
		if info.Status == "complete" || info.Status == "error" {
			continue
		}

		// Get download status from aria2go
		downloadInfo := c.client.GetDownloadInfo(gid)
		if downloadInfo.TotalLength > 0 {
			info.TotalLength = downloadInfo.TotalLength
			info.CompletedLength = downloadInfo.BytesCompleted
			info.DownloadSpeed = int64(downloadInfo.DownloadSpeed)

			// Update status based on aria2go status
			switch downloadInfo.Status {
			case 1: // active
				info.Status = "active"
			case 2: // paused
				info.Status = "paused"
			case 3: // error
				info.Status = "error"
			case 4: // complete
				info.Status = "complete"
			case 5: // removed
				info.Status = "removed"
			}
		}
	}
}

// DownloadFile downloads a file using aria2go
func (c *Aria2Client) DownloadFile(url, filename string, downloadType string) (string, error) {
	var downloadDir string

	switch downloadType {
	case "isos":
		downloadDir = config.GetDownloadsPath("isos")
	case "jail_templates":
		downloadDir = config.GetDownloadsPath("jail_templates")
	case "vm_templates":
		downloadDir = config.GetDownloadsPath("vm_templates")
	default:
		return "", fmt.Errorf("unsupported download type: %s", downloadType)
	}

	// Ensure download directory exists
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create download directory: %w", err)
	}

	// Set download options
	options := aria2go.Options{
		"dir":                       downloadDir,
		"out":                       filename,
		"auto-file-renaming":        "false",
		"continue":                  "true",
		"max-connection-per-server": "16",
		"split":                     "16",
		"min-split-size":            "1M",
	}

	// Add download
	gid, err := c.client.AddUri(url, options)
	if err != nil {
		return "", fmt.Errorf("failed to add download: %w", err)
	}

	// Store download info
	c.mutex.Lock()
	c.downloads[gid] = &DownloadInfo{
		GID:          gid,
		Status:       "active",
		Filename:     filename,
		Directory:    downloadDir,
		DownloadType: downloadType,
		URL:          url,
		StartTime:    time.Now(),
	}
	c.mutex.Unlock()

	logger.L.Info().Msgf("Started download %s with GID: %s", url, gid)
	return gid, nil
}

// GetDownloadStatus gets the status of a download
func (c *Aria2Client) GetDownloadStatus(gid string) (*Aria2DownloadStatus, error) {
	c.mutex.RLock()
	info, exists := c.downloads[gid]
	c.mutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("download not found: %s", gid)
	}

	// Get fresh status from aria2go
	downloadInfo := c.client.GetDownloadInfo(gid)
	if downloadInfo.TotalLength > 0 {
		info.TotalLength = downloadInfo.TotalLength
		info.CompletedLength = downloadInfo.BytesCompleted
		info.DownloadSpeed = int64(downloadInfo.DownloadSpeed)

		// Update status based on aria2go status
		switch downloadInfo.Status {
		case 1: // active
			info.Status = "active"
		case 2: // paused
			info.Status = "paused"
		case 3: // error
			info.Status = "error"
		case 4: // complete
			info.Status = "complete"
		case 5: // removed
			info.Status = "removed"
		}
	}

	// Convert to legacy format
	status := &Aria2DownloadStatus{
		GID:             info.GID,
		Status:          info.Status,
		TotalLength:     strconv.FormatInt(info.TotalLength, 10),
		CompletedLength: strconv.FormatInt(info.CompletedLength, 10),
		DownloadSpeed:   strconv.FormatInt(info.DownloadSpeed, 10),
		Dir:             info.Directory,
		Files: []Aria2File{
			{
				Index:    "1",
				Path:     filepath.Join(info.Directory, info.Filename),
				Length:   strconv.FormatInt(info.TotalLength, 10),
				URI:      []string{info.URL},
				Selected: "true",
			},
		},
	}

	return status, nil
}

// GetAllDownloads gets all downloads
func (c *Aria2Client) GetAllDownloads() ([]Aria2DownloadStatus, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	var allDownloads []Aria2DownloadStatus

	// Get downloads from our tracking
	for _, info := range c.downloads {
		status := &Aria2DownloadStatus{
			GID:             info.GID,
			Status:          info.Status,
			TotalLength:     strconv.FormatInt(info.TotalLength, 10),
			CompletedLength: strconv.FormatInt(info.CompletedLength, 10),
			DownloadSpeed:   strconv.FormatInt(info.DownloadSpeed, 10),
			Dir:             info.Directory,
			Files: []Aria2File{
				{
					Index:    "1",
					Path:     filepath.Join(info.Directory, info.Filename),
					Length:   strconv.FormatInt(info.TotalLength, 10),
					URI:      []string{info.URL},
					Selected: "true",
				},
			},
		}
		allDownloads = append(allDownloads, *status)
	}

	return allDownloads, nil
}

// RemoveDownload removes a download
func (c *Aria2Client) RemoveDownload(gid string) error {
	// Remove from aria2go
	if !c.client.Remove(gid) {
		logger.L.Warn().Msgf("Failed to remove download from aria2go: %s", gid)
	}

	// Remove from our tracking
	c.mutex.Lock()
	delete(c.downloads, gid)
	c.mutex.Unlock()

	return nil
}

// PauseDownload pauses a download
func (c *Aria2Client) PauseDownload(gid string) error {
	if !c.client.Pause(gid) {
		return fmt.Errorf("failed to pause download: %s", gid)
	}
	return nil
}

// ResumeDownload resumes a download
func (c *Aria2Client) ResumeDownload(gid string) error {
	if !c.client.Resume(gid) {
		return fmt.Errorf("failed to resume download: %s", gid)
	}
	return nil
}

// GetProgress returns the progress percentage of a download
func (c *Aria2Client) GetProgress(gid string) (int, error) {
	c.mutex.RLock()
	info, exists := c.downloads[gid]
	c.mutex.RUnlock()

	if !exists {
		return 0, fmt.Errorf("download not found: %s", gid)
	}

	if info.TotalLength == 0 {
		return 0, nil
	}

	progress := int((info.CompletedLength * 100) / info.TotalLength)
	return progress, nil
}

// Close stops the aria2c client
func (c *Aria2Client) Close() error {
	c.isRunning = false

	// Shutdown aria2go client
	if c.client != nil {
		c.client.Shutdown()
	}

	logger.L.Info().Msg("Closed aria2go client")
	return nil
}

// GetDownloadByFilename finds a download by its filename
func (c *Aria2Client) GetDownloadByFilename(filename string) (*Aria2DownloadStatus, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	for _, info := range c.downloads {
		if info.Filename == filename {
			return c.GetDownloadStatus(info.GID)
		}
	}

	return nil, fmt.Errorf("download not found with filename: %s", filename)
}

// RemoveDownloadByFilename removes a download by its filename
func (c *Aria2Client) RemoveDownloadByFilename(filename string) error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	for gid, info := range c.downloads {
		if info.Filename == filename {
			// Remove from aria2go
			if !c.client.Remove(gid) {
				logger.L.Warn().Msgf("Failed to remove download from aria2go: %s", gid)
			}

			// Remove from tracking
			delete(c.downloads, gid)
			return nil
		}
	}

	return fmt.Errorf("download not found with filename: %s", filename)
}
