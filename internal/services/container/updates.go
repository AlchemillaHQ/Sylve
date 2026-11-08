// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	notifier "github.com/alchemillahq/sylve/internal/notifications"
)

const updateCheckEvery = 6 * time.Hour

var registryClient = &http.Client{Timeout: 30 * time.Second}

var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

func (s *Service) watchImageUpdates(ctx context.Context) {
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := s.CheckImageUpdates(ctx); err != nil {
			logger.L.Warn().Err(err).Msg("container_image_update_check_failed")
		}
		timer.Reset(updateCheckEvery)
	}
}

// CheckImageUpdates asks each image's registry which digest its tag points at
// and marks the containers whose image doesn't have it. A registry that can't
// be asked leaves its containers as they were.
func (s *Service) CheckImageUpdates(ctx context.Context) error {
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		return err
	}
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Where("creation_pending = ?", false).Order("oc_id").Find(&records).Error; err != nil {
		return err
	}
	remote := map[string]string{}
	local := map[[2]string][]string{}
	var found []containerModels.Container
	for _, record := range records {
		digest, asked := remote[record.Image]
		if !asked {
			var err error
			if digest, err = s.registryDigest(ctx, record.Image); err != nil {
				logger.L.Debug().Err(err).Str("image", record.Image).Msg("container_image_update_unknown")
			}
			remote[record.Image] = digest
		}
		if digest == "" {
			continue
		}
		key := [2]string{record.Pool, record.Image}
		digests, inspected := local[key]
		if !inspected {
			digests = s.localDigests(ctx, record.Pool, record.Image)
			local[key] = digests
		}
		update := ""
		if len(digests) > 0 && !slices.Contains(digests, digest) {
			update = digest
		}
		if update == record.UpdateDigest {
			continue
		}
		if err := s.DB.WithContext(ctx).Model(&record).Update("update_digest", update).Error; err != nil {
			return err
		}
		s.changed(record.OCID)
		if update != "" {
			found = append(found, record)
		}
	}
	notifyImageUpdates(ctx, found)
	return nil
}

// localDigests are the digests podman recorded for the image: its own and,
// pulled by a tag pointing at an index, the index's.
func (s *Service) localDigests(ctx context.Context, pool, image string) []string {
	var inspected struct {
		Digest      string   `json:"Digest"`
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := s.api(ctx, pool, "image", http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, &inspected); err != nil {
		return nil
	}
	digests := []string{inspected.Digest}
	for _, named := range inspected.RepoDigests {
		if _, digest, ok := strings.Cut(named, "@"); ok {
			digests = append(digests, digest)
		}
	}
	return digests
}

// registryReference splits an image as podman reads it into its registry, its
// repository and its tag. Images pinned to a digest never update.
func registryReference(image string) (registry, repository, tag string, ok bool) {
	if strings.Contains(image, "@") {
		return "", "", "", false
	}
	tag = "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image, tag = image[:i], image[i+1:]
	}
	registry, repository, qualified := strings.Cut(image, "/")
	if !qualified || !strings.ContainsAny(registry, ".:") && registry != "localhost" {
		registry, repository = "docker.io", image
	}
	if registry == "docker.io" && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	return registry, repository, tag, repository != ""
}

// registryDigest is the digest the image's tag points at in its registry,
// asked for without pulling anything.
func (s *Service) registryDigest(ctx context.Context, image string) (string, error) {
	registry, repository, tag, ok := registryReference(image)
	if !ok {
		return "", nil
	}
	host := registry
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	head := func(token string) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/v2/"+repository+"/manifests/"+tag, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", manifestTypes)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		return registryClient.Do(request)
	}
	response, err := head("")
	if err != nil {
		return "", err
	}
	response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		token, err := s.registryToken(ctx, registry, response.Header.Get("WWW-Authenticate"))
		if err != nil {
			return "", err
		}
		if response, err = head(token); err != nil {
			return "", err
		}
		response.Body.Close()
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry_answered_%d", response.StatusCode)
	}
	digest := response.Header.Get("Docker-Content-Digest")
	if !strings.HasPrefix(digest, "sha256:") {
		return "", errors.New("registry_digest_missing")
	}
	return digest, nil
}

// registryToken gets the bearer token a registry's challenge asks for, with
// the login saved for the registry if there is one.
func (s *Service) registryToken(ctx context.Context, registry, challenge string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("registry_auth_unsupported: %s", scheme)
	}
	query := url.Values{}
	realm := ""
	for _, param := range strings.Split(params, ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
		value = strings.Trim(value, `"`)
		if name == "realm" {
			realm = value
		} else if name == "service" || name == "scope" {
			query.Set(name, value)
		}
	}
	if realm == "" {
		return "", errors.New("registry_auth_realm_missing")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	var credential containerModels.RegistryCredential
	if s.DB.WithContext(ctx).Where("registry = ?", registry).Limit(1).Find(&credential).Error == nil && credential.ID != 0 {
		request.SetBasicAuth(credential.Username, credential.Password)
	}
	response, err := registryClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil {
		return "", fmt.Errorf("registry_auth_failed_%d", response.StatusCode)
	}
	return cmp.Or(body.Token, body.AccessToken), nil
}

func notifyImageUpdates(ctx context.Context, found []containerModels.Container) {
	if len(found) == 0 {
		return
	}
	hostname, _ := os.Hostname()
	lines, ocids := []string{}, []string{}
	fingerprint := sha256.New()
	for _, record := range found {
		lines = append(lines, fmt.Sprintf("%s (%d): %s", cmp.Or(record.Title, record.Name), record.OCID, record.Image))
		ocids = append(ocids, fmt.Sprint(record.OCID))
		fmt.Fprintf(fingerprint, "%d@%s\n", record.OCID, record.UpdateDigest)
	}
	title := "Container image update available"
	if len(found) > 1 {
		title = fmt.Sprintf("Container image updates available (%d)", len(found))
	}
	_, err := notifier.Emit(ctx, notifier.EventInput{Kind: notifier.ContainerImageUpdatesKind, Title: title,
		Body:     "A newer image is available for:\n" + strings.Join(lines, "\n"),
		Severity: "info", Source: "containers", Fingerprint: "container-image-updates:" + hex.EncodeToString(fingerprint.Sum(nil))[:16],
		Metadata: map[string]string{"node": hostname, "ocIds": strings.Join(ocids, ",")}})
	if err != nil && !errors.Is(err, notifier.ErrEmitterNotConfigured) {
		logger.L.Warn().Err(err).Msg("container_image_update_notification_failed")
	}
}

// updateImage recreates the container from its image pulled again.
func (s *Service) updateImage(ctx context.Context, ocid uint) error {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	spec := containerServiceInterfaces.SpecOf(record)
	spec.PullImage = true
	return s.Update(ctx, ocid, spec)
}

// upgradeStack upgrades the stack's members that have an update, in the order
// the stack was created in, so what others depend on goes first.
func (s *Service) upgradeStack(ctx context.Context, ocid uint) error {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	if record.Stack == "" {
		return fmt.Errorf("container_not_in_stack")
	}
	var members []containerModels.Container
	if err := s.DB.WithContext(ctx).Where("stack = ? AND update_digest <> ''", record.Stack).Order("oc_id").Find(&members).Error; err != nil {
		return err
	}
	for _, member := range members {
		progress(ctx, "Updating "+cmp.Or(member.Title, member.Name))
		if err := s.updateImage(ctx, member.OCID); err != nil {
			return fmt.Errorf("%s: %w", member.Name, err)
		}
	}
	return nil
}
