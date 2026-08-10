// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alchemillahq/sylve/internal/config"
	"github.com/alchemillahq/sylve/internal/db/models"
	utilitiesModels "github.com/alchemillahq/sylve/internal/db/models/utilities"
	utilitiesServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/utilities"
	"github.com/alchemillahq/sylve/pkg/crypto"
	"github.com/alchemillahq/sylve/pkg/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const downloadSigningSecretName = "DownloadSigningSecret"

const SignedDownloadURLValidity = 2 * time.Hour

var (
	ErrSignedDownloadInvalid  = errors.New("invalid_signed_download_request")
	ErrSignedDownloadNotFound = errors.New("signed_download_not_found")
	ErrSignedDownloadNotReady = errors.New("signed_download_not_ready")
	ErrSignedDownloadUnsafe   = errors.New("unsafe_signed_download_target")
)

func (s *Service) getCachedDownloadSigningSecret() string {
	if s == nil {
		return ""
	}

	s.signingSecretMu.RLock()
	defer s.signingSecretMu.RUnlock()
	return strings.TrimSpace(s.downloadSignSecret)
}

func (s *Service) setCachedDownloadSigningSecret(secret string) {
	if s == nil {
		return
	}

	secret = strings.TrimSpace(secret)
	if secret == "" {
		return
	}

	s.signingSecretMu.Lock()
	s.downloadSignSecret = secret
	s.signingSecretMu.Unlock()
}

func (s *Service) getOrCreateDownloadSigningSecret() (string, error) {
	if s == nil || s.DB == nil {
		return "", fmt.Errorf("db_unavailable")
	}

	if cached := s.getCachedDownloadSigningSecret(); cached != "" {
		return cached, nil
	}

	// Serializing the cache miss avoids competing first-use database writes and,
	// more importantly, prevents a stale in-memory value if an existing blank
	// secret row is initialized concurrently.
	s.signingSecretInitMu.Lock()
	defer s.signingSecretInitMu.Unlock()

	if cached := s.getCachedDownloadSigningSecret(); cached != "" {
		return cached, nil
	}

	var secret models.SystemSecrets
	err := s.DB.Where("name = ?", downloadSigningSecretName).First(&secret).Error
	if err == nil {
		if strings.TrimSpace(secret.Data) != "" {
			s.setCachedDownloadSigningSecret(secret.Data)
			return strings.TrimSpace(secret.Data), nil
		}

		fresh := utils.GenerateRandomString(64)
		if updateErr := s.DB.Model(&secret).Update("data", fresh).Error; updateErr != nil {
			return "", fmt.Errorf("download_signing_secret_update_failed: %w", updateErr)
		}
		s.setCachedDownloadSigningSecret(fresh)
		return fresh, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("download_signing_secret_lookup_failed: %w", err)
	}

	fresh := utils.GenerateRandomString(64)
	created := models.SystemSecrets{
		Name: downloadSigningSecretName,
		Data: fresh,
	}
	if createErr := s.DB.Create(&created).Error; createErr != nil {
		// Handle concurrent creation on startup races.
		if lookupErr := s.DB.Where("name = ?", downloadSigningSecretName).First(&secret).Error; lookupErr == nil && strings.TrimSpace(secret.Data) != "" {
			s.setCachedDownloadSigningSecret(secret.Data)
			return strings.TrimSpace(secret.Data), nil
		}
		return "", fmt.Errorf("download_signing_secret_create_failed: %w", createErr)
	}

	s.setCachedDownloadSigningSecret(fresh)
	return fresh, nil
}

func (s *Service) BuildDownloadSignature(input string, expires int64) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || expires <= 0 {
		return "", fmt.Errorf("invalid_signature_input")
	}

	secret, err := s.getOrCreateDownloadSigningSecret()
	if err != nil {
		return "", err
	}

	return crypto.GenerateSignature(input, expires, []byte(secret)), nil
}

func (s *Service) ValidateDownloadSignature(input string, expires int64, provided string) (bool, error) {
	expected, err := s.BuildDownloadSignature(input, expires)
	if err != nil {
		return false, err
	}

	provided = strings.TrimSpace(provided)
	if len(provided) != len(expected) {
		return false, nil
	}

	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1, nil
}

func signedDownloadInput(node, downloadUUID string, resourceID int) (string, error) {
	node = strings.TrimSpace(node)
	downloadUUID = strings.TrimSpace(downloadUUID)
	if !utils.IsValidHostname(node) || resourceID <= 0 {
		return "", ErrSignedDownloadInvalid
	}
	if _, err := uuid.Parse(downloadUUID); err != nil {
		return "", ErrSignedDownloadInvalid
	}

	return fmt.Sprintf("v1\n%s\n%s\n%d", node, downloadUUID, resourceID), nil
}

func (s *Service) BuildSignedDownloadSignature(
	node, downloadUUID string,
	resourceID int,
	expires int64,
) (string, error) {
	input, err := signedDownloadInput(node, downloadUUID, resourceID)
	if err != nil {
		return "", err
	}
	return s.BuildDownloadSignature(input, expires)
}

func (s *Service) ValidateSignedDownloadSignature(
	node, downloadUUID string,
	resourceID int,
	expires int64,
	provided string,
) (bool, error) {
	input, err := signedDownloadInput(node, downloadUUID, resourceID)
	if err != nil {
		return false, err
	}
	return s.ValidateDownloadSignature(input, expires, provided)
}

func validSignedDownloadName(name string) bool {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || len([]byte(name)) > 4096 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func signedDownloadAttachmentName(name string) (string, error) {
	name = filepath.Base(name)
	if name == "." || name == ".." || !validSignedDownloadName(name) || len([]byte(name)) > 255 {
		return "", ErrSignedDownloadUnsafe
	}
	return name, nil
}

func resolveSignedDownloadPath(root, candidate string) (string, error) {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if !managedDescendant(root, candidate) {
		return "", ErrSignedDownloadUnsafe
	}

	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrSignedDownloadNotFound
	}
	if err != nil {
		return "", fmt.Errorf("inspect signed download target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", ErrSignedDownloadUnsafe
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve signed download root: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrSignedDownloadNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve signed download target: %w", err)
	}
	if !managedDescendant(resolvedRoot, resolvedCandidate) {
		return "", ErrSignedDownloadUnsafe
	}

	return resolvedCandidate, nil
}

func (s *Service) getSignedDownload(downloadUUID string) (*utilitiesModels.Downloads, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("signed download database unavailable")
	}
	downloadUUID = strings.TrimSpace(downloadUUID)
	if _, err := uuid.Parse(downloadUUID); err != nil {
		return nil, ErrSignedDownloadInvalid
	}

	var download utilitiesModels.Downloads
	if err := s.DB.Preload("Files").Where("uuid = ?", downloadUUID).First(&download).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSignedDownloadNotFound
		}
		return nil, fmt.Errorf("lookup signed download: %w", err)
	}
	if download.Status != utilitiesModels.DownloadStatusDone {
		return nil, ErrSignedDownloadNotReady
	}
	return &download, nil
}

func resolveSignedDownloadTarget(
	download *utilitiesModels.Downloads,
	resourceID int,
) (utilitiesServiceInterfaces.SignedDownloadTarget, error) {
	if download == nil || resourceID <= 0 {
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadInvalid
	}

	target := utilitiesServiceInterfaces.SignedDownloadTarget{
		ResourceID: resourceID,
		UUID:       download.UUID,
		Type:       download.Type,
	}
	targetRoot := ""

	switch download.Type {
	case utilitiesModels.DownloadTypeHTTP, utilitiesModels.DownloadTypePath:
		if resourceID != int(download.ID) {
			return target, ErrSignedDownloadNotFound
		}

		rootName := "http"
		if download.Type == utilitiesModels.DownloadTypePath {
			rootName = "path"
		}
		managedRoot := filepath.Clean(config.GetDownloadsPath(rootName))
		extractedRoot := filepath.Clean(filepath.Join(config.GetDownloadsPath("extracted"), download.UUID))
		candidate := filepath.Clean(download.Path)
		switch {
		case managedDescendant(managedRoot, candidate):
			targetRoot = managedRoot
		case managedDescendant(extractedRoot, candidate):
			targetRoot = extractedRoot
		default:
			return target, ErrSignedDownloadUnsafe
		}

		name, err := signedDownloadAttachmentName(download.Name)
		if err != nil {
			return target, err
		}
		target.Name = name
		target.Path = candidate

	case utilitiesModels.DownloadTypeTorrent:
		torrentRoot := filepath.Clean(config.GetDownloadsPath("torrents"))
		downloadRoot := filepath.Clean(filepath.Join(torrentRoot, download.UUID))
		if !managedDescendant(torrentRoot, downloadRoot) || filepath.Clean(download.Path) != downloadRoot {
			return target, ErrSignedDownloadUnsafe
		}

		var selected *utilitiesModels.DownloadedFile
		for i := range download.Files {
			if download.Files[i].ID == resourceID && download.Files[i].DownloadID == int(download.ID) {
				selected = &download.Files[i]
				break
			}
		}
		if selected == nil {
			return target, ErrSignedDownloadNotFound
		}

		relativePath := filepath.Clean(selected.Name)
		if relativePath == "." || relativePath == ".." || filepath.IsAbs(relativePath) ||
			strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return target, ErrSignedDownloadUnsafe
		}
		candidate := filepath.Clean(filepath.Join(downloadRoot, relativePath))
		if !managedDescendant(downloadRoot, candidate) {
			return target, ErrSignedDownloadUnsafe
		}

		name, err := signedDownloadAttachmentName(relativePath)
		if err != nil {
			return target, err
		}
		target.Name = name
		target.Path = candidate
		targetRoot = downloadRoot

	default:
		return target, ErrSignedDownloadUnsafe
	}

	resolvedPath, err := resolveSignedDownloadPath(targetRoot, target.Path)
	if err != nil {
		return target, err
	}
	target.Path = resolvedPath
	return target, nil
}

func (s *Service) ResolveSignedDownloadTargetByID(
	downloadUUID string,
	resourceID int,
) (utilitiesServiceInterfaces.SignedDownloadTarget, error) {
	if resourceID <= 0 {
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadInvalid
	}
	download, err := s.getSignedDownload(downloadUUID)
	if err != nil {
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, err
	}
	return resolveSignedDownloadTarget(download, resourceID)
}

func (s *Service) ResolveSignedDownloadTargetByName(
	downloadUUID, name string,
) (utilitiesServiceInterfaces.SignedDownloadTarget, error) {
	if !validSignedDownloadName(name) {
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadInvalid
	}
	download, err := s.getSignedDownload(downloadUUID)
	if err != nil {
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, err
	}

	resourceID := int(download.ID)
	switch download.Type {
	case utilitiesModels.DownloadTypeHTTP, utilitiesModels.DownloadTypePath:
		if name != download.Name {
			return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadNotFound
		}
	case utilitiesModels.DownloadTypeTorrent:
		resourceID = 0
		for _, file := range download.Files {
			if file.Name == name {
				resourceID = file.ID
				break
			}
		}
		if resourceID <= 0 {
			return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadNotFound
		}
	default:
		return utilitiesServiceInterfaces.SignedDownloadTarget{}, ErrSignedDownloadUnsafe
	}

	return resolveSignedDownloadTarget(download, resourceID)
}

func (s *Service) signedDownloadNode() (string, error) {
	hostnameFn := s.uploadHostnameFn
	if hostnameFn == nil {
		hostnameFn = utils.GetSystemHostname
	}
	node, err := hostnameFn()
	if err != nil {
		return "", fmt.Errorf("resolve signed download node: %w", err)
	}
	node = strings.TrimSpace(node)
	if !utils.IsValidHostname(node) {
		return "", fmt.Errorf("resolve signed download node: invalid hostname")
	}
	return node, nil
}

func (s *Service) CreateSignedDownloadURL(
	downloadUUID, name string,
) (utilitiesServiceInterfaces.SignedDownloadURLResult, error) {
	target, err := s.ResolveSignedDownloadTargetByName(downloadUUID, name)
	if err != nil {
		return utilitiesServiceInterfaces.SignedDownloadURLResult{}, err
	}
	node, err := s.signedDownloadNode()
	if err != nil {
		return utilitiesServiceInterfaces.SignedDownloadURLResult{}, err
	}

	expiresAt := time.Now().UTC().Add(SignedDownloadURLValidity)
	signature, err := s.BuildSignedDownloadSignature(node, target.UUID, target.ResourceID, expiresAt.Unix())
	if err != nil {
		return utilitiesServiceInterfaces.SignedDownloadURLResult{}, err
	}

	query := url.Values{}
	query.Set("expires", strconv.FormatInt(expiresAt.Unix(), 10))
	query.Set("id", strconv.Itoa(target.ResourceID))
	query.Set("node", node)
	query.Set("sig", signature)

	return utilitiesServiceInterfaces.SignedDownloadURLResult{
		URL:       "/api/utilities/downloads/" + url.PathEscape(target.UUID) + "?" + query.Encode(),
		ExpiresAt: expiresAt,
	}, nil
}
