// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alchemillahq/gzfs"
	"github.com/alchemillahq/sylve/internal/db/models"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"gorm.io/gorm"
)

type Service struct {
	DB            *gorm.DB
	KerberosState func(context.Context, string) (string, error)

	mutationMu      sync.Mutex
	topologyMu      sync.Mutex
	listDatasets    func(context.Context) ([]*gzfs.Dataset, error)
	inspectDatasets func(context.Context, []string) ([]*gzfs.Dataset, error)
	verifyMount     func(string, string) error
	driver          runtimeDriver
	resolveMapping  func(context.Context, string, string) (string, error)
	revision        uint64
	observationMu   sync.RWMutex
	observation     runtimeObservation
}

func NewService(db *gorm.DB) *Service {
	driver := newNativeRuntime()
	return &Service{
		DB: db, verifyMount: verifyDatasetMount, driver: driver, resolveMapping: driver.mappingCredential,
		listDatasets:    func(ctx context.Context) ([]*gzfs.Dataset, error) { return driver.filesystems(ctx, nil) },
		inspectDatasets: driver.filesystems,
	}
}

type Status struct {
	Enabled          bool                           `json:"enabled"`
	RuntimeAvailable bool                           `json:"runtimeAvailable"`
	ApplyStatus      string                         `json:"applyStatus"`
	DesiredExports   int                            `json:"desiredExports"`
	Message          string                         `json:"message"`
	Managed          bool                           `json:"managed"`
	Running          *bool                          `json:"running"`
	AppliedExports   int                            `json:"appliedExports"`
	PolicyVerified   bool                           `json:"policyVerified"`
	LastError        string                         `json:"lastError"`
	CheckedAt        *time.Time                     `json:"checkedAt"`
	SharesVersion    string                         `json:"sharesVersion"`
	ConfigVersion    string                         `json:"configVersion"`
	Filesystems      []nfsModels.ExportedFilesystem `json:"filesystems"`
}

func (s *Service) GetStatus(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
		return Status{}, err
	}
	shares, err := s.GetShares(ctx)
	if err != nil {
		return Status{}, err
	}
	config, err := s.GetConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	count := 0
	for _, share := range shares {
		if share.Enabled {
			count++
		}
	}
	state, err := s.runtimeState(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Enabled: slices.Contains(basic.Services, models.NFS), RuntimeAvailable: s.driver.Available(),
		Managed: state.Managed, ApplyStatus: state.ApplyStatus, DesiredExports: count, LastError: state.LastError,
	}
	status.Filesystems = state.Filesystems
	data, _ := json.Marshal(shares)
	status.SharesVersion, status.ConfigVersion = exportsHash(string(data)), configurationHash(config)
	if !state.Managed {
		status.ApplyStatus = "stopped"
		status.Message = "NFS is disabled in Sylve. Host NFS configuration and services are left alone."
		if status.Enabled {
			status.ApplyStatus = "pending"
			status.Message = "Enabling NFS gives Sylve full control of NFS configuration and services."
		}
		if !status.RuntimeAvailable {
			status.Message = "Native NFS requires root privileges."
		}
		return status, nil
	}
	observation := s.observationFor(state)
	status.Running = observation.running
	if !observation.checkedAt.IsZero() {
		status.CheckedAt = &observation.checkedAt
	}
	if state.ApplyStatus == "applied" || state.ApplyStatus == "idle" {
		if !status.Enabled || state.PolicyHash != (savedPolicy{config: config, shares: shares}).hash() {
			status.ApplyStatus, status.LastError = "pending", "Saved NFS policy no longer matches the applied configuration."
		} else if observation.checkedAt.IsZero() {
			status.ApplyStatus, status.LastError = "pending", "NFS runtime has not been checked recently."
		} else {
			status.AppliedExports = len(state.Filesystems)
		}
	}
	status.Message = "NFS health is sampled by the runtime monitor; client access policy is not independently verified."
	if !status.RuntimeAvailable {
		status.Message = "Native NFS requires root privileges."
	}
	return status, nil
}

func (s *Service) GetConfig(ctx context.Context) (nfsModels.NFSConfig, error) {
	if err := ctx.Err(); err != nil {
		return nfsModels.NFSConfig{}, err
	}
	var settings nfsModels.NFSSettings
	err := s.DB.WithContext(ctx).First(&settings, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nfsModels.NFSConfig{Protocol: "v3", V4MaxVersion: "4.2", BindAddresses: []string{}}, nil
	}
	if settings.V4MaxVersion == "" {
		settings.V4MaxVersion = "4.2"
	}
	if settings.BindAddresses == nil {
		settings.BindAddresses = []string{}
	}
	return settings.NFSConfig, err
}

func (s *Service) SetConfig(ctx context.Context, config nfsModels.NFSConfig) error {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	config, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return err
	}
	if _, err := kerberosPolicy(config, policy.shares); err != nil {
		return err
	}
	if configurationHash(config) == configurationHash(policy.config) {
		return s.reconcilePolicy(ctx, policy, nil)
	}
	if err := s.DB.WithContext(ctx).Save(&nfsModels.NFSSettings{ID: 1, NFSConfig: config}).Error; err != nil {
		return err
	}
	policy.config = config
	return s.reconcilePolicy(ctx, policy, nil)
}

func (s *Service) GetShares(ctx context.Context) ([]nfsModels.NFSShare, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	shares := make([]nfsModels.NFSShare, 0)
	err := s.DB.WithContext(ctx).Order("id").Find(&shares).Error
	for i := range shares {
		if shares[i].Security == nil {
			shares[i].Security = []string{"sys"}
		}
	}
	return shares, err
}

func (s *Service) SaveShare(ctx context.Context, id uint, input nfsModels.NFSShareConfig) (nfsModels.NFSShare, error) {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return nfsModels.NFSShare{}, err
	}
	share, datasets, err := s.saveShare(ctx, id, input, &policy)
	if err != nil {
		return share, err
	}
	return share, s.reconcilePolicy(ctx, policy, datasets)
}

func (s *Service) saveShare(ctx context.Context, id uint, input nfsModels.NFSShareConfig, policy *savedPolicy) (nfsModels.NFSShare, []*gzfs.Dataset, error) {
	input, err := normalizeShare(input)
	if err != nil {
		return nfsModels.NFSShare{}, nil, err
	}
	shares := policy.shares
	share := nfsModels.NFSShare{ID: id}
	index := slices.IndexFunc(shares, func(share nfsModels.NFSShare) bool { return share.ID == id })
	if id != 0 {
		if index < 0 {
			return share, nil, ErrNotFound
		}
		share = shares[index]
	} else if len(shares) >= maxShares {
		return share, nil, conflict("dataset", "At most 256 NFS share rules can be saved.")
	}
	if id != 0 && reflect.DeepEqual(input, share.NFSShareConfig) {
		return share, nil, nil
	}
	policyChanged := id == 0 || !reflect.DeepEqual(sharePolicy(input), sharePolicy(share.NFSShareConfig))
	var datasets []*gzfs.Dataset
	// A missing/unmounted dataset must never prevent disabling an existing rule.
	if id == 0 || input.Enabled && policyChanged || input.Dataset != share.Dataset {
		datasets, err = s.discover(ctx)
		if err != nil {
			return share, nil, err
		}
		dataset := findDataset(datasets, input.Dataset)
		if err := s.checkDataset(dataset, policy.pools); err != nil {
			return share, nil, err
		}
		share.DatasetName, share.Mountpoint = dataset.Name, dataset.Mountpoint
	}
	if input.Enabled && policyChanged {
		if _, _, err := s.resolveMappings(ctx, []nfsModels.NFSShare{{NFSShareConfig: input}}); err != nil {
			return share, nil, err
		}
		if _, err := s.kerberosState(ctx, policy.config, []nfsModels.NFSShare{{NFSShareConfig: input}}); err != nil {
			return share, nil, err
		}
	}
	share.NFSShareConfig = input
	if index >= 0 {
		shares[index] = share
	} else {
		shares = append(shares, share)
	}
	if err := validateClientConflicts(shares); err != nil {
		return share, nil, err
	}
	err = s.DB.WithContext(ctx).Save(&share).Error
	if index >= 0 {
		shares[index] = share
	} else {
		shares[len(shares)-1] = share
	}
	policy.shares = shares
	return share, datasets, err
}

func (s *Service) SetShareEnabled(ctx context.Context, id uint, enabled bool) error {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if id == 0 {
		return ErrNotFound
	}
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(policy.shares, func(share nfsModels.NFSShare) bool { return share.ID == id })
	if index < 0 {
		return ErrNotFound
	}
	share := policy.shares[index]
	if !enabled {
		if !share.Enabled {
			return s.reconcilePolicy(ctx, policy, nil)
		}
		if err := s.DB.WithContext(ctx).Model(&share).Update("enabled", false).Error; err != nil {
			return err
		}
		policy.shares[index].Enabled = false
		return s.reconcilePolicy(ctx, policy, nil)
	}
	share.Enabled = true
	_, datasets, err := s.saveShare(ctx, id, share.NFSShareConfig, &policy)
	if err != nil {
		return err
	}
	return s.reconcilePolicy(ctx, policy, datasets)
}

func (s *Service) DeleteShare(ctx context.Context, id uint) error {
	s.topologyMu.Lock()
	defer s.topologyMu.Unlock()
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if id == 0 {
		return ErrNotFound
	}
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return err
	}
	result := s.DB.WithContext(ctx).Delete(&nfsModels.NFSShare{}, id)
	if result.Error == nil && result.RowsAffected == 0 {
		return ErrNotFound
	}
	if result.Error != nil {
		return result.Error
	}
	policy.shares = slices.DeleteFunc(policy.shares, func(share nfsModels.NFSShare) bool { return share.ID == id })
	return s.reconcilePolicy(ctx, policy, nil)
}

func (s *Service) DisableSharesForDatasets(ctx context.Context, guids []string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if len(guids) == 0 {
		return nil
	}
	result := s.DB.WithContext(ctx).Model(&nfsModels.NFSShare{}).Where("dataset IN ? AND enabled = ?", guids, true).Update("enabled", false)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	policy, err := s.readPolicy(ctx)
	if err != nil {
		return err
	}
	return s.reconcilePolicy(ctx, policy, nil)
}

func (s *Service) discover(ctx context.Context) ([]*gzfs.Dataset, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	datasets, err := s.listDatasets(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover NFS filesystems: %w", errors.Join(err, ctx.Err()))
	}
	guids := make(map[string]bool, len(datasets))
	for _, dataset := range datasets {
		if dataset == nil || dataset.Name == "" || !validGUID(dataset.GUID) || dataset.Type != gzfs.DatasetTypeFilesystem {
			return nil, errors.New("ZFS returned an incomplete filesystem identity")
		}
		if guids[dataset.GUID] {
			return nil, fmt.Errorf("ZFS returned multiple filesystems with GUID %s", dataset.GUID)
		}
		guids[dataset.GUID] = true
	}
	return datasets, nil
}

func findDataset(datasets []*gzfs.Dataset, guid string) *gzfs.Dataset {
	for _, dataset := range datasets {
		if dataset.GUID == guid {
			return dataset
		}
	}
	return nil
}

func (s *Service) checkDataset(dataset *gzfs.Dataset, pools []string) error {
	if err := datasetEligible(dataset, pools); err != nil {
		return err
	}
	if err := s.verifyMount(dataset.Mountpoint, dataset.Name); err != nil {
		return conflict("dataset", "The mountpoint is not mounted directly from the selected ZFS filesystem.")
	}
	return nil
}

type DatasetOption struct {
	GUID       string `json:"guid"`
	Name       string `json:"name"`
	Mountpoint string `json:"mountpoint"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason"`
}

func (s *Service) GetDatasets(ctx context.Context) ([]DatasetOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
		return nil, err
	}
	datasets, err := s.discover(ctx)
	if err != nil {
		return nil, err
	}
	shares, err := s.GetShares(ctx)
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	for _, share := range shares {
		referenced[share.Dataset] = true
	}
	options := make([]DatasetOption, 0)
	for _, dataset := range datasets {
		err := s.checkDataset(dataset, basic.Pools)
		if err != nil && !referenced[dataset.GUID] {
			continue
		}
		option := DatasetOption{GUID: dataset.GUID, Name: dataset.Name, Mountpoint: dataset.Mountpoint, Available: err == nil}
		if err != nil {
			option.Reason = err.Error()
		}
		options = append(options, option)
		delete(referenced, dataset.GUID)
	}
	for guid := range referenced {
		options = append(options, DatasetOption{GUID: guid, Name: guid, Reason: "The saved dataset is unavailable."})
	}
	slices.SortFunc(options, func(a, b DatasetOption) int { return strings.Compare(a.Name, b.Name) })
	return options, nil
}
