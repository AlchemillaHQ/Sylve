// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	storagezfs "github.com/alchemillahq/sylve/pkg/storage/zfs"
)

const ocidLabel = "io.sylve.ocid"

var (
	nameRegex       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
	envRegex        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	volumeRegex     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	ownerRegex      = regexp.MustCompile(`^[0-9]+:[0-9]+$`)
	modeRegex       = regexp.MustCompile(`^[0-7]{3,4}$`)
	annotationRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	hostRegex       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	sizeRegex       = regexp.MustCompile(`^[0-9]+[KkMmGg]?[Bb]?$`)
)

func (s *Service) validateName(ctx context.Context, name string, exceptID uint) error {
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("invalid_container_name")
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("name = ? AND id <> ?", name, exceptID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("container_name_already_in_use")
	}
	return nil
}

func (s *Service) ValidateCreate(ctx context.Context, req containerServiceInterfaces.CreateRequest) error {
	if enabled, err := s.enabled(ctx); err != nil || !enabled {
		return fmt.Errorf("container_service_not_enabled")
	}
	if req.OCID < 1 || req.OCID > clusterModels.GuestIdentityMaxID {
		return fmt.Errorf("invalid_container_id")
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("oc_id = ?", req.OCID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("container_id_already_in_use")
	}
	if err := s.validateName(ctx, req.Name, 0); err != nil {
		return err
	}
	if err := s.requireUsablePool(ctx, req.Pool); err != nil {
		return err
	}
	if req.StartOrder < 0 {
		return fmt.Errorf("start_order_must_be_greater_than_or_equal_to_0")
	}
	if err := validateLimits(req.Cores, req.Memory); err != nil {
		return err
	}
	if _, _, err := s.normalizeSpec(ctx, req.Spec); err != nil {
		return err
	}
	if s.identity != nil {
		return s.identity.RequireGuestIDAvailable(ctx, req.OCID)
	}
	return nil
}

func (s *Service) normalizeSpec(ctx context.Context, spec containerServiceInterfaces.Spec) (containerServiceInterfaces.Spec, *containerModels.Network, error) {
	spec.Image = strings.TrimSpace(spec.Image)
	if spec.Image == "" || strings.ContainsAny(spec.Image, " \t\n") {
		return spec, nil, fmt.Errorf("invalid_image")
	}
	if spec.OS != "" && spec.OS != "freebsd" && spec.OS != "linux" {
		return spec, nil, fmt.Errorf("invalid_os: must be freebsd, linux, or empty to detect")
	}
	if spec.WorkDir != "" && !path.IsAbs(spec.WorkDir) {
		return spec, nil, fmt.Errorf("invalid_work_dir")
	}
	if spec.Hostname != "" && !nameRegex.MatchString(spec.Hostname) {
		return spec, nil, fmt.Errorf("invalid_hostname")
	}

	seen := map[string]bool{}
	for _, env := range spec.Env {
		if !envRegex.MatchString(env.Name) || seen[env.Name] {
			return spec, nil, fmt.Errorf("invalid_env_name: %s", env.Name)
		}
		seen[env.Name] = true
	}

	switch spec.Restart {
	case "":
		spec.Restart = "no"
	case "no", "on-failure", "always", "unless-stopped":
	default:
		return spec, nil, fmt.Errorf("invalid_restart_policy")
	}
	if spec.Restart != "on-failure" {
		spec.RestartRetries = 0
	}
	if spec.StopTimeout == 0 {
		spec.StopTimeout = 10
	}

	var network *containerModels.Network
	if spec.Network == "" {
		spec.Network = containerServiceInterfaces.DefaultNetwork
	}
	if spec.Network == containerServiceInterfaces.HostNetwork {
		if spec.IP != "" || len(spec.Ports) > 0 {
			return spec, nil, fmt.Errorf("host_network_cannot_use_ip_or_ports")
		}
	} else {
		var err error
		if network, err = s.networkByName(ctx, spec.Network); err != nil {
			return spec, nil, err
		}
		if spec.IP != "" {
			_, subnet, _ := net.ParseCIDR(network.Subnet)
			ip := net.ParseIP(spec.IP)
			if ip == nil || ip.To4() == nil || !subnet.Contains(ip) || spec.IP == network.Gateway {
				return spec, nil, fmt.Errorf("invalid_ip: must be an unused address in %s", network.Subnet)
			}
		}
		if network.SwitchID != nil && len(spec.Ports) > 0 {
			return spec, nil, fmt.Errorf("switch_network_cannot_publish_ports")
		}
	}

	ports := map[string]bool{}
	for i, port := range spec.Ports {
		if port.Protocol == "" {
			spec.Ports[i].Protocol = "tcp"
		}
		if spec.Ports[i].Protocol != "tcp" && spec.Ports[i].Protocol != "udp" {
			return spec, nil, fmt.Errorf("invalid_port_protocol")
		}
		if port.HostPort == 0 || port.ContainerPort == 0 || (port.HostIP != "" && net.ParseIP(port.HostIP) == nil) {
			return spec, nil, fmt.Errorf("invalid_port_mapping")
		}
		key := spec.Ports[i].Protocol + "/" + strconv.Itoa(int(port.HostPort))
		if ports[key] {
			return spec, nil, fmt.Errorf("duplicate_host_port: %s", key)
		}
		ports[key] = true
	}

	for key, value := range spec.Annotations {
		if !annotationRegex.MatchString(key) || strings.ContainsAny(value, "\n\r") {
			return spec, nil, fmt.Errorf("invalid_annotation: %s", key)
		}
	}
	for _, entry := range spec.ExtraHosts {
		host, ip, _ := strings.Cut(entry, ":")
		if !hostRegex.MatchString(host) || net.ParseIP(ip) == nil {
			return spec, nil, fmt.Errorf("invalid_extra_host: %s", entry)
		}
	}
	if spec.ShmSize != "" && !sizeRegex.MatchString(spec.ShmSize) {
		return spec, nil, fmt.Errorf("invalid_shm_size: %s", spec.ShmSize)
	}

	targets := map[string]bool{}
	for i, mount := range spec.Mounts {
		target := path.Clean(mount.Target)
		if !path.IsAbs(mount.Target) || target == "/" || targets[target] {
			return spec, nil, fmt.Errorf("invalid_mount_target: %s", mount.Target)
		}
		targets[target] = true
		spec.Mounts[i].Target = target
		switch mount.Type {
		case containerModels.MountVolume:
			if !volumeRegex.MatchString(mount.Source) {
				return spec, nil, fmt.Errorf("invalid_volume_name: %s", mount.Source)
			}
			if mount.Owner != "" && !ownerRegex.MatchString(mount.Owner) || mount.Mode != "" && !modeRegex.MatchString(mount.Mode) {
				return spec, nil, fmt.Errorf("invalid_volume_permissions: %s", mount.Source)
			}
		case containerModels.MountDataset:
			if mount.Source == "" {
				return spec, nil, fmt.Errorf("invalid_mount_dataset")
			}
		case containerModels.MountPath:
			info, err := os.Stat(mount.Source)
			if !path.IsAbs(mount.Source) || err != nil || !info.IsDir() {
				return spec, nil, fmt.Errorf("invalid_mount_path: %s must be an existing directory", mount.Source)
			}
			if strings.HasPrefix(path.Clean(mount.Source)+"/", s.DataDir+"/") {
				return spec, nil, fmt.Errorf("invalid_mount_path: %s", mount.Source)
			}
		default:
			return spec, nil, fmt.Errorf("invalid_mount_type: %s", mount.Type)
		}
	}
	return spec, network, nil
}

func (s *Service) resolveMounts(ctx context.Context, record *containerModels.Container) ([]string, []string, error) {
	sources := make([]string, len(record.Mounts))
	var created []string
	for i, mount := range record.Mounts {
		switch mount.Type {
		case containerModels.MountPath:
			sources[i] = mount.Source
			continue
		case containerModels.MountVolume:
			name := containerModels.VolumesDataset(record.Pool, record.OCID) + "/" + mount.Source
			dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
			if err != nil && !storagezfs.DatasetDoesNotExist(err) {
				return nil, created, err
			}
			isNew := dataset == nil
			if isNew {
				if err := s.ensureDataset(ctx, containerModels.VolumesDataset(record.Pool, record.OCID)); err != nil {
					return nil, created, err
				}
				if dataset, err = s.GZFS.ZFS.CreateFilesystem(ctx, name, nil); err != nil {
					return nil, created, fmt.Errorf("failed_to_create_volume_%s: %w", mount.Source, err)
				}
				created = append(created, name)
			}
			if sources[i], err = storagezfs.FilesystemMountpoint(dataset); err != nil {
				return nil, created, err
			}
			if isNew {
				if err := setPermissions(sources[i], mount.Owner, mount.Mode); err != nil {
					return nil, created, fmt.Errorf("failed_to_set_volume_permissions_%s: %w", mount.Source, err)
				}
			}
			record.Mounts[i].GUID = dataset.GUID
		case containerModels.MountDataset:
			dataset, err := s.GZFS.ZFS.Get(ctx, mount.Source, false)
			if err != nil || dataset == nil {
				return nil, created, fmt.Errorf("dataset_not_found: %s", mount.Source)
			}
			if sources[i], err = storagezfs.FilesystemMountpoint(dataset); err != nil {
				return nil, created, err
			}
			record.Mounts[i].GUID = dataset.GUID
		}
	}
	return sources, created, nil
}

func setPermissions(path, owner, mode string) error {
	if owner != "" {
		uid, gid, _ := strings.Cut(owner, ":")
		u, _ := strconv.Atoi(uid)
		g, _ := strconv.Atoi(gid)
		if err := os.Chown(path, u, g); err != nil {
			return err
		}
	}
	if mode != "" {
		m, _ := strconv.ParseUint(mode, 8, 32)
		return os.Chmod(path, os.FileMode(m))
	}
	return nil
}

func (s *Service) ensureDataset(ctx context.Context, name string) error {
	dataset, err := s.GZFS.ZFS.Get(ctx, name, false)
	if err != nil && !storagezfs.DatasetDoesNotExist(err) {
		return err
	}
	if dataset != nil {
		return nil
	}
	_, err = s.GZFS.ZFS.CreateFilesystem(ctx, name, nil)
	return err
}

func createArgs(record containerModels.Container, sources []string) ([]string, []string) {
	args := []string{"create", "--name", record.RuntimeName(),
		"--label", ocidLabel + "=" + strconv.FormatUint(uint64(record.OCID), 10),
		"--platform", record.OS + "/" + runtime.GOARCH,
		"--stop-timeout", strconv.FormatUint(uint64(record.StopTimeout), 10)}

	hostname := record.Hostname
	if hostname == "" {
		hostname = record.Name
	}
	if record.Network == nil {
		args = append(args, "--network", "host")
	} else {
		network := record.Network.PodmanName()
		if record.IP != "" {
			network += ":ip=" + record.IP
		}
		args = append(args, "--network", network, "--hostname", hostname)
		if record.Network.SwitchID == nil {
			args = append(args, "--network-alias", strings.ToLower(record.Name))
		}
	}

	var env []string
	for _, variable := range record.Env {
		args = append(args, "--env", variable.Name)
		env = append(env, variable.Name+"="+variable.Value)
	}
	for _, key := range slices.Sorted(maps.Keys(record.Annotations)) {
		args = append(args, "--annotation", key+"="+record.Annotations[key])
	}
	for _, host := range record.ExtraHosts {
		args = append(args, "--add-host", host)
	}
	if record.ShmSize != "" {
		args = append(args, "--shm-size", record.ShmSize)
	}
	for _, port := range record.Ports {
		mapping := fmt.Sprintf("%d:%d/%s", port.HostPort, port.ContainerPort, port.Protocol)
		if port.HostIP != "" {
			mapping = net.JoinHostPort(port.HostIP, strconv.Itoa(int(port.HostPort))) + fmt.Sprintf(":%d/%s", port.ContainerPort, port.Protocol)
		}
		args = append(args, "--publish", mapping)
	}
	for i, mount := range record.Mounts {
		volume := sources[i] + ":" + mount.Target
		if mount.ReadOnly {
			volume += ":ro"
		}
		args = append(args, "--volume", volume)
	}
	if record.WorkDir != "" {
		args = append(args, "--workdir", record.WorkDir)
	}
	if record.User != "" {
		args = append(args, "--user", record.User)
	}
	restart := record.Restart
	if restart == "on-failure" && record.RestartRetries > 0 {
		restart += ":" + strconv.FormatUint(uint64(record.RestartRetries), 10)
	}
	args = append(args, "--restart", restart)
	if len(record.Entrypoint) > 0 {
		entrypoint, _ := json.Marshal(record.Entrypoint)
		args = append(args, "--entrypoint", string(entrypoint))
	}
	args = append(args, "--", record.Image)
	args = append(args, record.Command...)
	return args, env
}

func applySpec(record *containerModels.Container, spec containerServiceInterfaces.Spec, network *containerModels.Network) {
	record.Image, record.OS = spec.Image, spec.OS
	record.Command, record.Entrypoint = spec.Command, spec.Entrypoint
	record.WorkDir, record.User, record.Hostname = spec.WorkDir, spec.User, spec.Hostname
	record.Env, record.IP, record.Ports, record.Mounts = spec.Env, spec.IP, spec.Ports, spec.Mounts
	record.Restart, record.RestartRetries = spec.Restart, spec.RestartRetries
	record.StopTimeout = spec.StopTimeout
	record.Annotations, record.ExtraHosts, record.ShmSize = spec.Annotations, spec.ExtraHosts, spec.ShmSize
	record.Network, record.NetworkID = network, nil
	if network != nil {
		record.NetworkID = &network.ID
	}
}
