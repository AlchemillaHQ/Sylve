// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
	"github.com/dustin/go-humanize"
)

var Actions = []string{"create", "create-stack", "start", "stop", "restart", "kill", "update", "delete"}

func (s *Service) ValidateTask(ctx context.Context, ocid uint, action, payload string) error {
	switch action {
	case "create":
		var req containerServiceInterfaces.CreateRequest
		if err := json.Unmarshal([]byte(payload), &req); err != nil || req.OCID != ocid {
			return fmt.Errorf("invalid_container_create_payload")
		}
		return s.ValidateCreate(ctx, req)
	case "create-stack":
		var req containerServiceInterfaces.StackRequest
		if err := json.Unmarshal([]byte(payload), &req); err != nil || len(req.Containers) == 0 || req.Containers[0].OCID != ocid {
			return fmt.Errorf("invalid_container_stack_payload")
		}
		for _, create := range req.Containers {
			if err := s.ValidateCreate(ctx, create); err != nil {
				return fmt.Errorf("%s: %w", create.Name, err)
			}
		}
		return nil
	case "update":
		var spec containerServiceInterfaces.Spec
		if err := json.Unmarshal([]byte(payload), &spec); err != nil {
			return fmt.Errorf("invalid_container_update_payload")
		}
		if _, _, err := s.normalizeSpec(ctx, spec); err != nil {
			return err
		}
	}
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	if record.CreationPending {
		return fmt.Errorf("container_creation_pending")
	}
	return nil
}

type progressKey struct{}

func progress(ctx context.Context, message string) {
	if report, ok := ctx.Value(progressKey{}).(func(string)); ok {
		report(message)
	}
}

func (s *Service) ExecuteTask(ctx context.Context, ocid uint, action, payload string, report func(string)) error {
	ctx = context.WithValue(ctx, progressKey{}, report)
	switch action {
	case "create":
		var req containerServiceInterfaces.CreateRequest
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return fmt.Errorf("invalid_container_create_payload: %w", err)
		}
		return s.Create(ctx, req)
	case "create-stack":
		var req containerServiceInterfaces.StackRequest
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return fmt.Errorf("invalid_container_stack_payload: %w", err)
		}
		return s.createStack(ctx, req)
	case "update":
		var spec containerServiceInterfaces.Spec
		if err := json.Unmarshal([]byte(payload), &spec); err != nil {
			return fmt.Errorf("invalid_container_update_payload: %w", err)
		}
		return s.Update(ctx, ocid, spec)
	case "delete":
		var req containerServiceInterfaces.DeleteRequest
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &req); err != nil {
				return fmt.Errorf("invalid_container_delete_payload: %w", err)
			}
		}
		return s.Delete(ctx, ocid, req.DeleteVolumes)
	default:
		return s.Action(ctx, ocid, action)
	}
}

func (s *Service) Create(ctx context.Context, req containerServiceInterfaces.CreateRequest) (err error) {
	if err := s.ValidateCreate(ctx, req); err != nil {
		return err
	}
	spec, network, err := s.normalizeSpec(ctx, req.Spec)
	if err != nil {
		return err
	}
	reservation, err := s.reserveIdentity(ctx, req.OCID)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil && reservation != nil {
			if releaseErr := s.identity.ReleaseGuestIdentities(ctx, *reservation); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("guest_identity_release_failed: %w", releaseErr))
			}
		}
	}()

	record := containerModels.Container{OCID: req.OCID, Name: req.Name, Description: req.Description, Pool: req.Pool,
		Cores: req.Cores, Memory: req.Memory, StartAtBoot: req.StartAtBoot, StartOrder: req.StartOrder,
		WebPort: req.WebPort, WebHTTPS: req.WebHTTPS, CatalogApp: req.CatalogApp, Stack: req.Stack, StackTitle: req.StackTitle,
		Service: req.Service, CreationPending: true}
	applySpec(&record, spec, network)
	if err := s.DB.WithContext(ctx).Omit("Network").Create(&record).Error; err != nil {
		return err
	}
	s.changed(record.OCID)

	var createdVolumes []string
	defer func() {
		if err == nil || !record.CreationPending {
			return
		}
		cleanup := context.WithoutCancel(ctx)
		_ = s.remove(cleanup, record)
		s.destroyDatasets(cleanup, createdVolumes)
		if deleteErr := s.DB.WithContext(cleanup).Delete(&record).Error; deleteErr != nil {
			err = errors.Join(err, deleteErr)
		}
		s.changed(record.OCID)
	}()

	if err = s.ensurePool(ctx, record.Pool); err != nil {
		return err
	}
	if err = s.ensureImage(ctx, &record, spec.PullImage); err != nil {
		return err
	}
	sources, created, err := s.resolveMounts(ctx, &record)
	createdVolumes = created
	if err != nil {
		return err
	}
	if record.RuntimeID, err = s.createRuntime(ctx, record, sources); err != nil {
		return err
	}
	if err = applyLimits(ctx, record); err != nil {
		return err
	}
	record.CreationPending = false
	if err = s.DB.WithContext(ctx).Omit("Network").Save(&record).Error; err != nil {
		record.CreationPending = true
		return err
	}
	if reservation != nil {
		if err := s.identity.FinalizeGuestIdentities(ctx, *reservation); err != nil {
			return fmt.Errorf("guest_identity_finalize_failed: %w", err)
		}
		reservation = nil
	}
	s.changed(record.OCID)

	if req.StartAfterCreate {
		err = s.start(ctx, record)
	}
	return err
}

func (s *Service) ensureImage(ctx context.Context, record *containerModels.Container, always bool) error {
	inspect := func() (string, error) {
		var image struct {
			OS string `json:"Os"`
		}
		err := s.api(ctx, record.Pool, "image", http.MethodGet, "/images/"+url.PathEscape(record.Image)+"/json", nil, &image)
		return image.OS, err
	}
	system, err := inspect()
	if always || err != nil || (record.OS != "" && system != record.OS) {
		if err = s.pullImage(ctx, record.Pool, record.Image, record.OS); err != nil {
			return err
		}
		if system, err = inspect(); err != nil {
			return err
		}
	}
	record.OS = system
	return nil
}

func (s *Service) pullImage(ctx context.Context, pool, image, system string) error {
	downloads, err := s.downloads(ctx, pool)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(downloads, 0o700); err != nil {
		return err
	}
	defer reportPull(ctx, image, downloads)()

	candidates := []string{system}
	if system == "" {
		candidates = []string{"freebsd", "linux"}
	}
	for _, candidate := range candidates {
		query := url.Values{"reference": {image}, "os": {candidate}, "arch": {runtime.GOARCH}, "policy": {"always"}}
		if err = s.pull(ctx, pool, query); err == nil {
			return nil
		}
	}
	return err
}

// pull goes through the pool's service: podman processes pulling side by side
// force-unmount and clean up layers the others are still writing.
func (s *Service) pull(ctx context.Context, pool string, query url.Values) error {
	response, err := s.request(ctx, pool, "pull", http.MethodPost, "/images/pull?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(response.Body)
	for {
		var report struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&report); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("podman_pull_failed: %w", err)
		}
		if report.Error != "" {
			return fmt.Errorf("podman_pull_failed: %s", report.Error)
		}
	}
}

// downloads is where the pool's service keeps the layers it's pulling: on the
// pool, as images outgrow /var/tmp.
func (s *Service) downloads(ctx context.Context, pool string) (string, error) {
	dataset, err := s.GZFS.ZFS.Get(ctx, pool+"/"+path.Dir(containerModels.ImagesDataset), false)
	if err != nil {
		return "", err
	}
	root, err := storagezfs.FilesystemMountpoint(dataset)
	return filepath.Join(root, ".pull-downloads"), err
}

func reportPull(ctx context.Context, image, downloads string) func() {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var downloaded uint64
		still := 0
		for {
			message := "Pulling " + image
			if size := directorySize(downloads); size > 0 {
				if size == downloaded {
					still++
				} else {
					downloaded, still = size, 0
				}
				message += ": " + humanize.IBytes(size) + " downloaded"
				if still >= 3 {
					message += ", unpacking"
				}
			}
			progress(ctx, message)
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func directorySize(dir string) uint64 {
	var size uint64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				size += uint64(info.Size())
			}
		}
		return nil
	})
	return size
}

func (s *Service) createRuntime(ctx context.Context, record containerModels.Container, sources []string) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	err := s.api(ctx, record.Pool, "create", http.MethodPost, "/containers/create", newCreateSpec(record, sources), &created)
	return created.ID, err
}

func (s *Service) destroyDatasets(ctx context.Context, names []string) {
	for i := len(names) - 1; i >= 0; i-- {
		dataset, err := s.GZFS.ZFS.Get(ctx, names[i], false)
		if err == nil && dataset != nil {
			err = dataset.Destroy(ctx, true, false)
		}
		if err != nil && !storagezfs.DatasetDoesNotExist(err) {
			logger.L.Warn().Err(err).Str("dataset", names[i]).Msg("container_dataset_cleanup_failed")
		}
	}
}

func (s *Service) Action(ctx context.Context, ocid uint, action string) error {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		err = s.start(ctx, record)
	case "stop":
		err = s.stop(ctx, record, record.StopTimeout)
	case "kill":
		err = s.stop(ctx, record, 0)
	case "restart":
		if err = s.stop(ctx, record, record.StopTimeout); err == nil {
			err = s.start(ctx, record)
		}
	default:
		return fmt.Errorf("invalid_container_action: %s", action)
	}
	s.changed(ocid)
	return err
}

func (s *Service) start(ctx context.Context, record containerModels.Container) error {
	return s.api(ctx, record.Pool, "start", http.MethodPost, "/containers/"+record.RuntimeName()+"/start", nil, nil)
}

func (s *Service) remove(ctx context.Context, record containerModels.Container) error {
	err := s.api(ctx, record.Pool, "rm", http.MethodDelete,
		fmt.Sprintf("/containers/%s?force=true&ignore=true&timeout=%d", record.RuntimeName(), record.StopTimeout), nil, nil)
	s.stateMu.Lock()
	delete(s.states, record.RuntimeID)
	s.stateMu.Unlock()
	return err
}

func (s *Service) stop(ctx context.Context, record containerModels.Container, timeout uint) error {
	err := s.api(ctx, record.Pool, "stop", http.MethodPost, fmt.Sprintf("/containers/%s/stop?timeout=%d", record.RuntimeName(), timeout), nil, nil)
	if exec.CommandContext(ctx, "/bin/pkill", "-KILL", "-j", record.RuntimeID).Run() != nil {
		return err
	}
	for range 50 {
		if mainPID(record) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, err = s.podman(ctx, record.Pool, nil, "container", "cleanup", record.RuntimeName())
	return err
}

func (s *Service) Update(ctx context.Context, ocid uint, spec containerServiceInterfaces.Spec) (err error) {
	previous, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	spec, network, err := s.normalizeSpec(ctx, spec)
	if err != nil {
		return err
	}
	updated := previous
	applySpec(&updated, spec, network)
	if err := s.ensurePool(ctx, updated.Pool); err != nil {
		return err
	}
	if err := s.ensureImage(ctx, &updated, spec.PullImage); err != nil {
		return err
	}
	sources, createdVolumes, err := s.resolveMounts(ctx, &updated)
	if err != nil {
		s.destroyDatasets(context.WithoutCancel(ctx), createdVolumes)
		return err
	}

	state, _ := s.runtimeState(ctx, previous)
	running := state != nil && state.State == "running"
	if err := s.remove(ctx, previous); err != nil {
		return err
	}
	if updated.RuntimeID, err = s.createRuntime(ctx, updated, sources); err != nil {
		restore := context.WithoutCancel(ctx)
		s.destroyDatasets(restore, createdVolumes)
		if oldSources, _, resolveErr := s.resolveMounts(restore, &previous); resolveErr == nil {
			if id, restoreErr := s.createRuntime(restore, previous, oldSources); restoreErr == nil {
				removeLimits(restore, previous.RuntimeID)
				previous.RuntimeID = id
				_ = s.DB.WithContext(restore).Model(&previous).Update("runtime_id", id).Error
				_ = applyLimits(restore, previous)
				if running {
					_ = s.start(restore, previous)
				}
			}
		}
		return err
	}
	removeLimits(ctx, previous.RuntimeID)
	if err := applyLimits(ctx, updated); err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Omit("Network").Save(&updated).Error; err != nil {
		return err
	}
	s.changed(ocid)
	if running {
		err = s.start(ctx, updated)
	}
	return err
}

func (s *Service) Delete(ctx context.Context, ocid uint, deleteVolumes bool) error {
	record, err := s.record(ctx, ocid)
	if err != nil {
		return err
	}
	var claim *clusterServiceInterfaces.GuestIdentityReservation
	if s.identity != nil {
		reservation, err := s.identity.GuestIdentityClaim(ctx, clusterModels.GuestIdentityKindContainer, ocid, "")
		if err != nil {
			return err
		}
		claim = &reservation
		defer s.identity.CancelGuestIdentityClaim(reservation)
	}

	if err := s.remove(ctx, record); err != nil {
		return err
	}
	removeLimits(ctx, record.RuntimeID)
	if deleteVolumes {
		dataset, err := s.GZFS.ZFS.Get(ctx, containerModels.VolumesDataset(record.Pool, ocid), false)
		if err != nil && !storagezfs.DatasetDoesNotExist(err) {
			return err
		}
		if dataset != nil {
			if err := dataset.Destroy(ctx, true, false); err != nil {
				return fmt.Errorf("failed_to_delete_container_volumes: %w", err)
			}
		}
	}
	if err := s.DB.WithContext(ctx).Delete(&record).Error; err != nil {
		return err
	}
	s.changed(ocid)
	if err := s.RemoveEmptyStack(ctx, record.Stack); err != nil {
		logger.L.Warn().Err(err).Str("stack", record.Stack).Msg("container_stack_network_not_removed")
	}
	if claim != nil {
		if err := s.identity.ReleaseGuestIdentities(ctx, *claim); err != nil {
			// The container is gone; the owner reconcile gives its ID back later.
			logger.L.Warn().Err(err).Uint("ocid", ocid).Msg("guest_identity_release_pending")
		}
	}
	return nil
}
