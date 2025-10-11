// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"fmt"
	"os"
	"strings"

	"github.com/alchemillahq/sylve/internal/config"
	utilitiesService "github.com/alchemillahq/sylve/internal/services/utilities"
)

// FindISOByPath finds an ISO file by path (new method replacing FindISOByUUID)
func (s *Service) FindISOByPath(isoPath string, includeImg bool) (string, error) {
	// Check if the path exists directly
	if _, err := os.Stat(isoPath); err == nil {
		// Verify it's an ISO or IMG file
		lowerPath := strings.ToLower(isoPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return isoPath, nil
		}
		return "", fmt.Errorf("not_an_iso_or_img_file: %s", isoPath)
	}

	// If direct path doesn't work, try to find by filename in new downloads directories
	filename := strings.TrimPrefix(isoPath, "/")
	filename = strings.TrimPrefix(filename, config.GetDownloadsPath("isos")+"/")
	filename = strings.TrimPrefix(filename, config.GetDownloadsPath("jail_templates")+"/")
	filename = strings.TrimPrefix(filename, config.GetDownloadsPath("vm_templates")+"/")

	// Try ISOs directory first (most likely for VMs)
	isosDir := config.GetDownloadsPath("isos")
	isoPath = fmt.Sprintf("%s/%s", isosDir, filename)
	if _, err := os.Stat(isoPath); err == nil {
		lowerPath := strings.ToLower(isoPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return isoPath, nil
		}
	}

	// Try VM templates directory
	vmTemplatesDir := config.GetDownloadsPath("vm_templates")
	vmTemplatePath := fmt.Sprintf("%s/%s", vmTemplatesDir, filename)
	if _, err := os.Stat(vmTemplatePath); err == nil {
		lowerPath := strings.ToLower(vmTemplatePath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return vmTemplatePath, nil
		}
	}

	// Fallback: try old directories for backward compatibility
	// Try HTTP downloads directory
	httpDir := config.GetDownloadsPath("http")
	httpPath := fmt.Sprintf("%s/%s", httpDir, filename)
	if _, err := os.Stat(httpPath); err == nil {
		lowerPath := strings.ToLower(httpPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return httpPath, nil
		}
	}

	// Try torrents directory
	torrentsDir := config.GetDownloadsPath("torrents")
	torrentPath := fmt.Sprintf("%s/%s", torrentsDir, filename)
	if _, err := os.Stat(torrentPath); err == nil {
		lowerPath := strings.ToLower(torrentPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return torrentPath, nil
		}
	}

	// Search in all torrent subdirectories
	if entries, err := os.ReadDir(torrentsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				subDirPath := fmt.Sprintf("%s/%s/%s", torrentsDir, entry.Name(), filename)
				if _, err := os.Stat(subDirPath); err == nil {
					lowerPath := strings.ToLower(subDirPath)
					if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
						return subDirPath, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("iso_not_found: %s", isoPath)
}

// FindISOByName finds an ISO file by name using the utilities service
func (s *Service) FindISOByName(name string, includeImg bool) (string, error) {
	// Check if it's an absolute path
	if strings.HasPrefix(name, "/") {
		return s.FindISOByPath(name, includeImg)
	}

	// Search in ISOs directory first (most likely for VMs)
	isosDir := config.GetDownloadsPath("isos")
	isoPath := fmt.Sprintf("%s/%s", isosDir, name)
	if _, err := os.Stat(isoPath); err == nil {
		lowerPath := strings.ToLower(isoPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return isoPath, nil
		}
	}

	// Search in VM templates directory
	vmTemplatesDir := config.GetDownloadsPath("vm_templates")
	vmTemplatePath := fmt.Sprintf("%s/%s", vmTemplatesDir, name)
	if _, err := os.Stat(vmTemplatePath); err == nil {
		lowerPath := strings.ToLower(vmTemplatePath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return vmTemplatePath, nil
		}
	}

	// Fallback: try old directories for backward compatibility
	// Search in HTTP downloads directory
	httpDir := config.GetDownloadsPath("http")
	httpPath := fmt.Sprintf("%s/%s", httpDir, name)
	if _, err := os.Stat(httpPath); err == nil {
		lowerPath := strings.ToLower(httpPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return httpPath, nil
		}
	}

	// Search in torrents directory
	torrentsDir := config.GetDownloadsPath("torrents")
	torrentPath := fmt.Sprintf("%s/%s", torrentsDir, name)
	if _, err := os.Stat(torrentPath); err == nil {
		lowerPath := strings.ToLower(torrentPath)
		if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
			return torrentPath, nil
		}
	}

	// Search in all torrent subdirectories
	if entries, err := os.ReadDir(torrentsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				subDirPath := fmt.Sprintf("%s/%s/%s", torrentsDir, entry.Name(), name)
				if _, err := os.Stat(subDirPath); err == nil {
					lowerPath := strings.ToLower(subDirPath)
					if strings.HasSuffix(lowerPath, ".iso") || (includeImg && strings.HasSuffix(lowerPath, ".img")) {
						return subDirPath, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("iso_not_found: %s", name)
}

// FindISOByUUID maintains backward compatibility but now treats UUID as filename/path
func (s *Service) FindISOByUUID(uuid string, includeImg bool) (string, error) {
	// First try to find by path (new behavior)
	return s.FindISOByPath(uuid, includeImg)
}

// ListAvailableISOs lists all available ISO files from the filesystem
func (s *Service) ListAvailableISOs(includeImg bool) ([]utilitiesService.ISOFile, error) {
	// This would typically use the utilities service, but for now
	// implement a basic filesystem search

	var isos []utilitiesService.ISOFile

	// Search ISOs directory first (primary location for VM ISOs)
	isosDir := config.GetDownloadsPath("isos")
	if entries, err := os.ReadDir(isosDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			lowerName := strings.ToLower(name)
			if strings.HasSuffix(lowerName, ".iso") || (includeImg && strings.HasSuffix(lowerName, ".img")) {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				iso := utilitiesService.ISOFile{
					Name:    name,
					Path:    fmt.Sprintf("%s/%s", isosDir, name),
					Size:    info.Size(),
					ModTime: info.ModTime(),
					Type:    "isos",
					Source:  "manual",
				}
				isos = append(isos, iso)
			}
		}
	}

	// Search VM templates directory
	vmTemplatesDir := config.GetDownloadsPath("vm_templates")
	if entries, err := os.ReadDir(vmTemplatesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			lowerName := strings.ToLower(name)
			if strings.HasSuffix(lowerName, ".iso") || (includeImg && strings.HasSuffix(lowerName, ".img")) {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				iso := utilitiesService.ISOFile{
					Name:    name,
					Path:    fmt.Sprintf("%s/%s", vmTemplatesDir, name),
					Size:    info.Size(),
					ModTime: info.ModTime(),
					Type:    "vm_templates",
					Source:  "manual",
				}
				isos = append(isos, iso)
			}
		}
	}

	// Fallback: search old directories for backward compatibility
	// Search HTTP downloads directory
	httpDir := config.GetDownloadsPath("http")
	if entries, err := os.ReadDir(httpDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			lowerName := strings.ToLower(name)
			if strings.HasSuffix(lowerName, ".iso") || (includeImg && strings.HasSuffix(lowerName, ".img")) {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				iso := utilitiesService.ISOFile{
					Name:    name,
					Path:    fmt.Sprintf("%s/%s", httpDir, name),
					Size:    info.Size(),
					ModTime: info.ModTime(),
					Type:    "http",
					Source:  "manual",
				}
				isos = append(isos, iso)
			}
		}
	}

	// Search torrents directory
	torrentsDir := config.GetDownloadsPath("torrents")
	if entries, err := os.ReadDir(torrentsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			torrentPath := fmt.Sprintf("%s/%s", torrentsDir, entry.Name())
			if subEntries, err := os.ReadDir(torrentPath); err == nil {
				for _, subEntry := range subEntries {
					if subEntry.IsDir() {
						continue
					}

					name := subEntry.Name()
					lowerName := strings.ToLower(name)
					if strings.HasSuffix(lowerName, ".iso") || (includeImg && strings.HasSuffix(lowerName, ".img")) {
						info, err := subEntry.Info()
						if err != nil {
							continue
						}

						iso := utilitiesService.ISOFile{
							Name:    name,
							Path:    fmt.Sprintf("%s/%s", torrentPath, name),
							Size:    info.Size(),
							ModTime: info.ModTime(),
							Type:    "torrent",
							Source:  entry.Name(),
						}
						isos = append(isos, iso)
					}
				}
			}
		}
	}

	return isos, nil
}
