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
	"strings"
	"sync"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	vmModels "github.com/alchemillahq/sylve/internal/db/models/vm"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	systemServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/system"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/digitalocean/go-libvirt"
	"gorm.io/gorm"
)

var _ libvirtServiceInterfaces.LibvirtServiceInterface = (*Service)(nil)

const minimumLibvirtVersion uint64 = 12_005_000

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

	guestIdentityAvailabilityChecker clusterServiceInterfaces.GuestIdentityAvailabilityChecker

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

func (s *Service) SetGuestIdentityAvailabilityChecker(
	checker clusterServiceInterfaces.GuestIdentityAvailabilityChecker,
) {
	s.guestIdentityAvailabilityChecker = checker
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
	_, err := s.ensureConnection()
	return err
}

func validateLibvirtVersion(version uint64) error {
	if version >= minimumLibvirtVersion {
		return nil
	}
	return fmt.Errorf(
		"unsupported_libvirt_version: requires >= %s, found %s",
		formatLibvirtVersion(minimumLibvirtVersion),
		formatLibvirtVersion(version),
	)
}

func formatLibvirtVersion(version uint64) string {
	return fmt.Sprintf("%d.%d.%d", version/1_000_000, (version/1_000)%1_000, version%1_000)
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
	if err := validateLibvirtVersion(version); err != nil {
		_ = conn.Disconnect()
		return nil, 0, err
	}

	return conn, version, nil
}

func (s *Service) ensureConnection() (*libvirt.Libvirt, error) {
	if s == nil {
		return nil, fmt.Errorf("libvirt_not_initialized")
	}

	conn := s.conn()
	if conn != nil {
		if version, err := conn.ConnectGetLibVersion(); err == nil {
			if err := validateLibvirtVersion(version); err != nil {
				return nil, err
			}
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
		if version, err := current.ConnectGetLibVersion(); err == nil {
			if err := validateLibvirtVersion(version); err != nil {
				return nil, err
			}
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

	for i := range vm.Storages {
		if vm.Storages[i].Type == vmModels.VMStorageTypeDiskImage {
			// Removable media is replicated as metadata inside vm.json, but a
			// legacy pool hint must never make it a ZFS output root.
			vm.Storages[i].Pool = ""
			vm.Storages[i].DatasetID = nil
			vm.Storages[i].Dataset = vmModels.VMStorageDataset{}
			continue
		}
		if vm.Storages[i].Dataset.Name == "" {
			continue
		}
		switch vm.Storages[i].Type {
		case vmModels.VMStorageTypeRaw:
			if id := storageIDFromDataset(vm.Storages[i].Dataset.Name, "raw"); id != 0 {
				vm.Storages[i].ID = uint(id)
			}
		case vmModels.VMStorageTypeZVol:
			if id := storageIDFromDataset(vm.Storages[i].Dataset.Name, "zvol"); id != 0 {
				vm.Storages[i].ID = uint(id)
			}
		}
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

	for _, pool := range vmJSONOutputPools(vm.Storages) {
		sylveDir := fmt.Sprintf("/%s/sylve/virtual-machines/%d/.sylve", pool, rid)
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

	}

	return nil
}

func vmJSONOutputPools(storages []vmModels.Storage) []string {
	seen := make(map[string]struct{})
	pools := make([]string, 0, len(storages))
	for _, storage := range storages {
		if storage.Type == vmModels.VMStorageTypeDiskImage {
			continue
		}
		pool := strings.TrimSpace(storage.Pool)
		if pool == "" {
			pool = strings.TrimSpace(storage.Dataset.Pool)
		}
		if pool == "" {
			datasetName := strings.TrimSpace(storage.Dataset.Name)
			if slash := strings.Index(datasetName, "/"); slash > 0 {
				pool = strings.TrimSpace(datasetName[:slash])
			}
		}
		if pool == "" {
			continue
		}
		if _, exists := seen[pool]; exists {
			continue
		}
		seen[pool] = struct{}{}
		pools = append(pools, pool)
	}
	return pools
}
