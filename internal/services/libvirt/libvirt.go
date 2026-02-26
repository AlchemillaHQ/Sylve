// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

var _ libvirtServiceInterfaces.LibvirtServiceInterface = (*Service)(nil)

type Service struct {
	DB *gorm.DB

	System systemServiceInterfaces.SystemServiceInterface

	Conn *libvirt.Libvirt

	actionMutex sync.Mutex
	crudMutex   sync.Mutex

	GZFS *gzfs.Client
}

func NewLibvirtService(db *gorm.DB, system systemServiceInterfaces.SystemServiceInterface, gzfs *gzfs.Client) libvirtServiceInterfaces.LibvirtServiceInterface {
	skeleton := &Service{
		DB:     db,
		System: system,
		Conn:   nil,
		GZFS:   gzfs,
	}

	var basicSettings models.BasicSettings

	err := db.First(&basicSettings).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return skeleton
		} else {
			logger.L.Fatal().Err(err).Msg("failed to check basic settings")
		}
	} else {
		if !slices.Contains(basicSettings.Services, models.Virtualization) {
			logger.L.Debug().Msg("Virtualization not enabled, skipping libvirt initialization")
			return skeleton
		}
	}

	uri, _ := url.Parse("bhyve:///system")
	l, err := libvirt.ConnectToURI(uri)
	if err != nil {
		logger.L.Fatal().Err(err).Msg("failed to connect to libvirt")
	}

	v, err := l.ConnectGetLibVersion()

	if err != nil {
		logger.L.Fatal().Err(err).Msg("failed to retrieve libvirt version")
	}

	logger.L.Info().Msgf("Libvirt version: %d", v)

	skeleton.Conn = l

	return skeleton
}

func (s *Service) CheckVersion() error {
	if err := s.requireConnection(); err != nil {
		return err
	}

	_, err := s.Conn.ConnectGetLibVersion()
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) IsVirtualizationEnabled() bool {
	var basicSettings models.BasicSettings
	err := s.DB.First(&basicSettings).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false
		} else {
			return false
		}
	} else {
		if !slices.Contains(basicSettings.Services, models.Virtualization) {
			return false
		}
	}

	return true
}

func (s *Service) requireConnection() error {
	if s == nil || s.Conn == nil {
		return fmt.Errorf("libvirt_not_initialized")
	}

	return nil
}

func (s *Service) WriteVMJson(rid uint) error {
	if rid == 0 {
		return fmt.Errorf("invalid_resource_id")
	}

	vm, err := s.GetVMByRID(rid)
	if err != nil {
		return err
	}

	vmJsonData, err := json.MarshalIndent(vm, "", "  ")
	if err != nil {
		return fmt.Errorf("failed_to_marshal_vm_to_json: %w", err)
	}

	configDir, err := s.GetVMConfigDirectory(rid)
	if err != nil {
		return fmt.Errorf("failed_to_get_vm_config_directory: %w", err)
	}

	filesToCopy := []string{
		fmt.Sprintf("%d_vars.fd", rid),
		fmt.Sprintf("%d_tpm.log", rid),
		fmt.Sprintf("%d_tpm.state", rid),
	}

	copyFile := func(src, dst string) error {
		srcFile, err := os.Open(src)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	}

	processedPools := make(map[string]bool)

	for _, storage := range vm.Storages {
		if storage.Pool == "" || processedPools[storage.Pool] {
			continue
		}

		sylveDir := fmt.Sprintf("/%s/sylve/virtual-machines/%d/.sylve", storage.Pool, rid)
		vmJsonPath := filepath.Join(sylveDir, "vm.json")

		if err := os.MkdirAll(sylveDir, 0755); err != nil {
			return fmt.Errorf("failed_to_create_directory_%s: %w", sylveDir, err)
		}

		if err := os.WriteFile(vmJsonPath, vmJsonData, 0644); err != nil {
			return fmt.Errorf("failed_to_write_vm_json_to_%s: %w", vmJsonPath, err)
		}

		for _, filename := range filesToCopy {
			srcPath := filepath.Join(configDir, filename)
			dstPath := filepath.Join(sylveDir, filename)

			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed_to_copy_%s: %w", filename, err)
			}
		}

		processedPools[storage.Pool] = true
	}

	return nil
}
