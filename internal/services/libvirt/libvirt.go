// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package libvirt

import (
	"context"
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
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
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

	connMu      sync.RWMutex
	reconnectMu sync.Mutex
	Conn        *libvirt.Libvirt
	uri         string

	actionMutex sync.Mutex
	crudMutex   sync.Mutex

	leftPanelRefreshEmitterMu sync.RWMutex
	leftPanelRefreshEmitter   func(reason string)

	preflightCreateVMTemplateFn func(
		ctx context.Context,
		templateID uint,
		req libvirtServiceInterfaces.CreateFromTemplateRequest,
	) (vmTemplateCreatePlan, error)
	createVMTemplateTargetFn func(
		ctx context.Context,
		template vmModels.VMTemplate,
		target vmTemplateCreateTarget,
		poolByStorageID map[uint]string,
		req libvirtServiceInterfaces.CreateFromTemplateRequest,
	) error

	GZFS *gzfs.Client
}

func NewLibvirtService(db *gorm.DB, system systemServiceInterfaces.SystemServiceInterface, gzfs *gzfs.Client) libvirtServiceInterfaces.LibvirtServiceInterface {
	skeleton := &Service{
		DB:     db,
		System: system,
		Conn:   nil,
		GZFS:   gzfs,
		uri:    "bhyve:///system",
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

	// Defer connection establishment until startup checks or first VM operation.
	// At this stage libvirtd may not have been onestart'ed yet.
	logger.L.Debug().Msg("Virtualization enabled, deferring libvirt connection initialization")

	return skeleton
}

func (s *Service) CheckVersion() error {
	conn, err := s.ensureConnection()
	if err != nil {
		return err
	}

	_, err = conn.ConnectGetLibVersion()
	return err
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
	_, err := s.ensureConnection()
	return err
}

func (s *Service) conn() *libvirt.Libvirt {
	if s == nil {
		return nil
	}

	s.connMu.RLock()
	defer s.connMu.RUnlock()

	return s.Conn
}

func (s *Service) setConn(conn *libvirt.Libvirt) {
	s.connMu.Lock()
	defer s.connMu.Unlock()

	s.Conn = conn
}

func (s *Service) connect() (*libvirt.Libvirt, uint64, error) {
	if s == nil {
		return nil, 0, fmt.Errorf("libvirt_not_initialized")
	}

	uri, err := url.Parse(s.uri)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid_libvirt_uri: %w", err)
	}

	conn, err := libvirt.ConnectToURI(uri)
	if err != nil {
		return nil, 0, err
	}

	version, err := conn.ConnectGetLibVersion()
	if err != nil {
		_ = conn.Disconnect()
		return nil, 0, fmt.Errorf("failed_to_retrieve_libvirt_version: %w", err)
	}

	return conn, version, nil
}

func (s *Service) ensureConnection() (*libvirt.Libvirt, error) {
	if s == nil {
		return nil, fmt.Errorf("libvirt_not_initialized")
	}

	conn := s.conn()
	if conn != nil {
		if _, err := conn.ConnectGetLibVersion(); err == nil {
			return conn, nil
		}
	}

	return s.reconnect()
}

func (s *Service) reconnect() (*libvirt.Libvirt, error) {
	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	current := s.conn()
	if current != nil {
		if _, err := current.ConnectGetLibVersion(); err == nil {
			return current, nil
		}
	}

	conn, version, err := s.connect()
	if err != nil {
		return nil, err
	}

	oldConn := s.conn()
	s.setConn(conn)

	if oldConn != nil && oldConn != conn {
		_ = oldConn.Disconnect()
	}

	logger.L.Info().Msgf("Reconnected to libvirt version: %d", version)

	return conn, nil
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
		csmROMFileName,
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
