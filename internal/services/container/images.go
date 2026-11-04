// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"gorm.io/gorm/clause"
)

func (s *Service) requireUsablePool(ctx context.Context, pool string) error {
	var basic models.BasicSettings
	if err := s.DB.WithContext(ctx).First(&basic).Error; err != nil {
		return err
	}
	if !slices.Contains(basic.Pools, pool) {
		return fmt.Errorf("pool_not_usable: %s", pool)
	}
	return nil
}

func (s *Service) ListImages(ctx context.Context, pool string) ([]containerServiceInterfaces.Image, error) {
	if err := s.requireUsablePool(ctx, pool); err != nil {
		return nil, err
	}
	images := []containerServiceInterfaces.Image{}
	exists, err := s.GZFS.ZFS.Get(ctx, pool+"/"+containerModels.ImagesDataset, false)
	if err != nil || exists == nil {
		return images, nil
	}
	var entries []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		Size    int64    `json:"Size"`
		Created int64    `json:"Created"`
	}
	if err := s.api(ctx, pool, "images", http.MethodGet, "/images/json", nil, &entries); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		images = append(images, containerServiceInterfaces.Image{ID: entry.ID, Names: entry.Names, Size: entry.Size, Created: time.Unix(entry.Created, 0)})
	}
	return images, nil
}

func (s *Service) PullImage(ctx context.Context, pool, image, os string) error {
	if err := s.requireUsablePool(ctx, pool); err != nil {
		return err
	}
	if strings.TrimSpace(image) == "" || (os != "" && os != "freebsd" && os != "linux") {
		return fmt.Errorf("invalid_image")
	}
	if err := s.ensurePool(ctx, pool); err != nil {
		return err
	}
	return s.pullImage(ctx, pool, image, os)
}

func (s *Service) RemoveImage(ctx context.Context, pool, id string) error {
	if err := s.requireUsablePool(ctx, pool); err != nil {
		return err
	}
	return s.api(ctx, pool, "rmi", http.MethodDelete, "/images/"+url.PathEscape(id), nil, nil)
}

func (s *Service) PruneImages(ctx context.Context, pool string) error {
	if err := s.requireUsablePool(ctx, pool); err != nil {
		return err
	}
	_, err := s.podman(ctx, pool, nil, "image", "prune", "--all", "--force")
	return err
}

func (s *Service) ListRegistryCredentials(ctx context.Context) ([]containerModels.RegistryCredential, error) {
	var credentials []containerModels.RegistryCredential
	err := s.DB.WithContext(ctx).Order("registry").Find(&credentials).Error
	return credentials, err
}

func (s *Service) SaveRegistryCredential(ctx context.Context, req containerServiceInterfaces.RegistryCredentialRequest) (containerModels.RegistryCredential, error) {
	credential := containerModels.RegistryCredential{Registry: strings.TrimSpace(req.Registry), Username: req.Username, Password: req.Password}
	if credential.Registry == "" || strings.ContainsAny(credential.Registry, " /") || credential.Username == "" || credential.Password == "" {
		return credential, fmt.Errorf("invalid_registry_credential")
	}
	err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "registry"}},
		DoUpdates: clause.AssignmentColumns([]string{"username", "password", "updated_at"}),
	}).Create(&credential).Error
	if err != nil {
		return credential, err
	}
	return credential, s.writeAuthFile(ctx)
}

func (s *Service) DeleteRegistryCredential(ctx context.Context, id uint) error {
	if err := s.DB.WithContext(ctx).Delete(&containerModels.RegistryCredential{}, id).Error; err != nil {
		return err
	}
	return s.writeAuthFile(ctx)
}
