// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"runtime"
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

// createSpec is the libpod create request, holding what podman create's flags
// would. The image is looked up locally; ensureImage has already pulled it.
type createSpec struct {
	Name          string                   `json:"name"`
	Labels        map[string]string        `json:"labels"`
	Image         string                   `json:"image"`
	ImageOS       string                   `json:"image_os"`
	ImageArch     string                   `json:"image_arch"`
	StopTimeout   uint                     `json:"stop_timeout"`
	NetNS         createNamespace          `json:"netns"`
	Networks      map[string]createNetwork `json:"Networks,omitempty"`
	Hostname      string                   `json:"hostname,omitempty"`
	Env           map[string]string        `json:"env,omitempty"`
	Annotations   map[string]string        `json:"annotations,omitempty"`
	HostAdd       []string                 `json:"hostadd,omitempty"`
	ShmSize       *int64                   `json:"shm_size,omitempty"`
	PortMappings  []createPort             `json:"portmappings,omitempty"`
	Mounts        []createMount            `json:"mounts,omitempty"`
	WorkDir       string                   `json:"work_dir,omitempty"`
	User          string                   `json:"user,omitempty"`
	Umask         string                   `json:"umask"`
	RestartPolicy string                   `json:"restart_policy"`
	RestartTries  *uint                    `json:"restart_tries,omitempty"`
	Entrypoint    []string                 `json:"entrypoint,omitempty"`
	Command       []string                 `json:"command,omitempty"`
}

type createNamespace struct {
	Mode string `json:"nsmode"`
}

type createNetwork struct {
	StaticIPs []string `json:"static_ips,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
}

type createPort struct {
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      uint16 `json:"host_port"`
	ContainerPort uint16 `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type createMount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options,omitempty"`
}

func newCreateSpec(record containerModels.Container, sources []string) createSpec {
	spec := createSpec{Name: record.RuntimeName(),
		Labels: map[string]string{ocidLabel: strconv.FormatUint(uint64(record.OCID), 10)},
		Image:  record.Image, ImageOS: record.OS, ImageArch: runtime.GOARCH, StopTimeout: record.StopTimeout,
		NetNS: createNamespace{Mode: "host"}, Env: map[string]string{}, Annotations: record.Annotations,
		HostAdd: record.ExtraHosts, WorkDir: record.WorkDir, User: record.User, Umask: "0022",
		RestartPolicy: record.Restart, Entrypoint: record.Entrypoint, Command: record.Command}

	if record.Network != nil {
		network := createNetwork{}
		if record.IP != "" {
			network.StaticIPs = []string{record.IP}
		}
		if record.Network.SwitchID == nil {
			network.Aliases = []string{strings.ToLower(record.Name)}
			// Stack members reach each other by service name, as in compose.
			if record.Service != "" && record.Network.Name == record.Stack && record.Service != network.Aliases[0] {
				network.Aliases = append(network.Aliases, record.Service)
			}
		}
		spec.NetNS.Mode = "bridge"
		spec.Networks = map[string]createNetwork{record.Network.PodmanName(): network}
		spec.Hostname = cmp.Or(record.Hostname, record.Name)
	}
	for _, variable := range record.Env {
		spec.Env[variable.Name] = variable.Value
	}
	if record.ShmSize != "" {
		size := shmBytes(record.ShmSize)
		spec.ShmSize = &size
	}
	for _, port := range record.Ports {
		spec.PortMappings = append(spec.PortMappings, createPort{HostIP: port.HostIP, HostPort: port.HostPort,
			ContainerPort: port.ContainerPort, Protocol: port.Protocol})
	}
	for i, mount := range record.Mounts {
		// nullfs is podman's bind mount on FreeBSD.
		bind := createMount{Destination: mount.Target, Type: "nullfs", Source: sources[i]}
		if mount.ReadOnly {
			bind.Options = []string{"ro"}
		}
		spec.Mounts = append(spec.Mounts, bind)
	}
	if record.Restart == "on-failure" && record.RestartRetries > 0 {
		spec.RestartTries = &record.RestartRetries
	}
	return spec
}

// shmBytes reads a size that sizeRegex accepted, such as 64m or 1gb, in the
// binary units podman uses.
func shmBytes(size string) int64 {
	size = strings.TrimSuffix(strings.ToLower(size), "b")
	shift := 10 * (strings.IndexByte("kmg", size[len(size)-1]) + 1)
	if shift > 0 {
		size = size[:len(size)-1]
	}
	value, _ := strconv.ParseInt(size, 10, 64)
	return value << shift
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
