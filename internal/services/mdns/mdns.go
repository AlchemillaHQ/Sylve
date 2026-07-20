// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package mdns

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/alchemillahq/sylve/internal/db/models"
	mdnsModels "github.com/alchemillahq/sylve/internal/db/models/mdns"
	sambaModels "github.com/alchemillahq/sylve/internal/db/models/samba"
	mdnsInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/mdns"
	"github.com/alchemillahq/sylve/internal/logger"

	"github.com/alchemillahq/sylve/pkg/network/mdns"
	"gorm.io/gorm"
)

var _ mdnsInterfaces.MdnsServiceInterface = (*Service)(nil)

var recordTypePattern = regexp.MustCompile(`^_[a-z0-9-]+\._(tcp|udp)$`)

type Service struct {
	DB               *gorm.DB
	mu               sync.Mutex
	responder        dnssd.Responder
	responderFactory func() (dnssd.Responder, error)
	handles          []dnssd.ServiceHandle
	cancelFunc       context.CancelFunc
	wg               sync.WaitGroup
}

func NewService(db *gorm.DB) mdnsInterfaces.MdnsServiceInterface {
	return &Service{DB: db}
}

func (s *Service) isEnabled() bool {
	var basic models.BasicSettings
	if err := s.DB.First(&basic).Error; err != nil {
		return false
	}
	for _, svc := range basic.Services {
		if svc == models.Mdns {
			return true
		}
	}
	return false
}

func (s *Service) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuildLocked()
}

func (s *Service) rebuildLocked() error {
	if !s.isEnabled() {
		return s.unpublishLocked()
	}

	records, err := s.gatherManagedRecords()
	if err != nil {
		return fmt.Errorf("failed to gather managed records: %w", err)
	}

	userRecords, err := s.userRecords()
	if err != nil {
		return fmt.Errorf("failed to load user records: %w", err)
	}
	records = append(records, userRecords...)

	settings, _ := s.GetSettings()

	if len(records) == 0 {
		return s.unpublishLocked()
	}

	return s.publishLocked(records, settings)
}

func (s *Service) gatherManagedRecords() ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	var records []mdnsInterfaces.MdnsRecordWithManaged
	var basicSettings models.BasicSettings
	if err := s.DB.First(&basicSettings).Error; err != nil {
		return nil, err
	}

	sambaEnabled := false
	for _, service := range basicSettings.Services {
		if service == models.SambaServer {
			sambaEnabled = true
			break
		}
	}
	if !sambaEnabled {
		return records, nil
	}

	host, _ := os.Hostname()

	var sambaSettings sambaModels.SambaSettings
	if err := s.DB.First(&sambaSettings).Error; err != nil {
		sambaSettings.AppleExtensions = false
	}

	var shares []sambaModels.SambaShare
	if err := s.DB.Find(&shares).Error; err != nil {
		return nil, err
	}

	hasShares := len(shares) > 0

	if hasShares {
		records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: mdnsModels.MdnsRecord{
				Name: host,
				Type: "_smb._tcp",
				Port: 445,
				Txt:  map[string]string{},
			},
			Managed: true,
			Source:  "samba",
		})
	}

	if sambaSettings.AppleExtensions {
		records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: mdnsModels.MdnsRecord{
				Name: host,
				Type: "_device-info._tcp",
				Port: 9,
				Txt:  map[string]string{"model": "RackMac"},
			},
			Managed: true,
			Source:  "samba",
		})

		var tmShares []string
		for _, share := range shares {
			if share.TimeMachine {
				tmShares = append(tmShares, share.Name)
			}
		}
		if len(tmShares) > 0 {
			txt := map[string]string{
				"sys": "waMa=0,adVF=0x100",
			}
			for i, name := range tmShares {
				txt[fmt.Sprintf("dk%d", i)] = fmt.Sprintf("adVN=%s,adVF=0x82", name)
			}
			records = append(records, mdnsInterfaces.MdnsRecordWithManaged{
				MdnsRecord: mdnsModels.MdnsRecord{
					Name: host,
					Type: "_adisk._tcp",
					Port: 9,
					Txt:  txt,
				},
				Managed: true,
				Source:  "samba",
			})
		}
	}

	return records, nil
}

func (s *Service) userRecords() ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	var rows []mdnsModels.MdnsRecord
	if err := s.DB.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]mdnsInterfaces.MdnsRecordWithManaged, len(rows))
	for i, r := range rows {
		out[i] = mdnsInterfaces.MdnsRecordWithManaged{
			MdnsRecord: r,
			Managed:    false,
			Source:     "user",
		}
	}
	return out, nil
}

func (s *Service) publishLocked(records []mdnsInterfaces.MdnsRecordWithManaged, settings mdnsModels.MdnsSettings) error {
	host, _ := os.Hostname()
	if settings.Hostname != "" {
		host = settings.Hostname
	}

	var ifaces []string
	if settings.Interfaces != "" {
		for _, iface := range strings.Split(settings.Interfaces, ",") {
			iface = strings.TrimSpace(iface)
			if iface != "" {
				ifaces = append(ifaces, iface)
			}
		}
	}

	seen := map[string]bool{}
	var configs []dnssd.Config
	for _, r := range records {
		key := fmt.Sprintf("%s|%s", r.Name, r.Type)
		if seen[key] {
			logger.L.Warn().Str("name", r.Name).Str("type", r.Type).Msg("duplicate mdns record, skipping")
			continue
		}
		seen[key] = true

		recordIfaces := ifaces
		if r.Interfaces != "" {
			recordIfaces = nil
			for _, ri := range strings.Split(r.Interfaces, ",") {
				ri = strings.TrimSpace(ri)
				if ri != "" {
					recordIfaces = append(recordIfaces, ri)
				}
			}
		}

		port := r.Port
		if port == 0 {
			port = 9
		}

		configs = append(configs, dnssd.Config{
			Name:   r.Name,
			Type:   r.Type,
			Domain: "local",
			Host:   host,
			Port:   port,
			Text:   r.Txt,
			Ifaces: recordIfaces,
		})
	}

	s.stopResponderLocked()

	factory := s.responderFactory
	if factory == nil {
		factory = dnssd.NewResponder
	}
	rp, err := factory()
	if err != nil {
		return fmt.Errorf("failed to create responder: %w", err)
	}
	s.responder = rp

	for _, cfg := range configs {
		sv, err := dnssd.NewService(cfg)
		if err != nil {
			logger.L.Warn().Err(err).Str("type", cfg.Type).Msg("failed to create mdns service")
			continue
		}
		hdl, err := s.responder.Add(sv)
		if err != nil {
			logger.L.Warn().Err(err).Str("type", cfg.Type).Msg("failed to add mdns service")
			continue
		}
		s.handles = append(s.handles, hdl)
	}
	if len(s.handles) == 0 {
		s.stopResponderLocked()
		return fmt.Errorf("failed to add any mdns records to the responder")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := rp.Respond(ctx); err != nil && ctx.Err() == nil {
			logger.L.Warn().Err(err).Msg("mdns responder exited with error")
		}
	}()

	return nil
}

func (s *Service) unpublishLocked() error {
	s.stopResponderLocked()
	return nil
}

func (s *Service) stopResponderLocked() {
	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
		s.wg.Wait()
	}

	if s.responder != nil {
		s.responder.Close()
		s.responder = nil
	}
	s.handles = nil
}

func (s *Service) GetSettings() (mdnsModels.MdnsSettings, error) {
	var settings mdnsModels.MdnsSettings
	if err := s.DB.First(&settings).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			settings = mdnsModels.MdnsSettings{}
			if createErr := s.DB.Create(&settings).Error; createErr != nil {
				return settings, fmt.Errorf("failed to seed mdns settings: %w", createErr)
			}
			return settings, nil
		}
		return settings, fmt.Errorf("failed to get mdns settings: %w", err)
	}
	return settings, nil
}

func (s *Service) SetSettings(interfaces, hostname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings, err := s.GetSettings()
	if err != nil {
		return err
	}
	settings.Interfaces = interfaces
	settings.Hostname = hostname
	if err := s.DB.Save(&settings).Error; err != nil {
		return fmt.Errorf("failed to save mdns settings: %w", err)
	}
	return s.rebuildLocked()
}

func (s *Service) GetRecords() ([]mdnsInterfaces.MdnsRecordWithManaged, error) {
	managed, err := s.gatherManagedRecords()
	if err != nil {
		return nil, err
	}
	user, err := s.userRecords()
	if err != nil {
		return nil, err
	}
	return append(managed, user...), nil
}

func (s *Service) CreateRecord(name, recordType string, port int, txt map[string]string, interfaces string) (mdnsModels.MdnsRecord, error) {
	if err := validateRecordInput(name, recordType, port); err != nil {
		return mdnsModels.MdnsRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record := mdnsModels.MdnsRecord{
		Name:       name,
		Type:       recordType,
		Port:       port,
		Txt:        txt,
		Interfaces: interfaces,
	}
	if err := s.DB.Create(&record).Error; err != nil {
		return mdnsModels.MdnsRecord{}, fmt.Errorf("failed to create mdns record: %w", err)
	}

	if err := s.rebuildLocked(); err != nil {
		return mdnsModels.MdnsRecord{}, err
	}
	return record, nil
}

func (s *Service) UpdateRecord(id uint, name, recordType string, port int, txt map[string]string, interfaces string) error {
	if err := validateRecordInput(name, recordType, port); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var record mdnsModels.MdnsRecord
	if err := s.DB.First(&record, id).Error; err != nil {
		return fmt.Errorf("mdns record not found: %w", err)
	}

	record.Name = name
	record.Type = recordType
	record.Port = port
	record.Txt = txt
	record.Interfaces = interfaces

	if err := s.DB.Save(&record).Error; err != nil {
		return fmt.Errorf("failed to update mdns record: %w", err)
	}
	return s.rebuildLocked()
}

func (s *Service) DeleteRecord(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var record mdnsModels.MdnsRecord
	if err := s.DB.First(&record, id).Error; err != nil {
		return fmt.Errorf("mdns record not found: %w", err)
	}
	if err := s.DB.Delete(&record).Error; err != nil {
		return fmt.Errorf("failed to delete mdns record: %w", err)
	}
	return s.rebuildLocked()
}

func validateRecordInput(name, recordType string, port int) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !recordTypePattern.MatchString(recordType) {
		return fmt.Errorf("invalid record type %q: must match _name._tcp or _name._udp", recordType)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}
