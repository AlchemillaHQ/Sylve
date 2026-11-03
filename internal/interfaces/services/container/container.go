// SPDX-License-Identifier: BSD-2-Clause

package containerServiceInterfaces

import (
	"context"
	"os/exec"
	"time"

	"github.com/alchemillahq/sylve/internal/db"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
)

const (
	HostNetwork    = "host"
	DefaultNetwork = "default"
)

type Spec struct {
	Image          string                   `json:"image"`
	OS             string                   `json:"os"`
	Command        []string                 `json:"command"`
	Entrypoint     []string                 `json:"entrypoint"`
	WorkDir        string                   `json:"workDir"`
	User           string                   `json:"user"`
	Hostname       string                   `json:"hostname"`
	Env            []containerModels.EnvVar `json:"env"`
	Network        string                   `json:"network"`
	IP             string                   `json:"ip"`
	Ports          []containerModels.Port   `json:"ports"`
	Mounts         []containerModels.Mount  `json:"mounts"`
	Restart        string                   `json:"restart"`
	RestartRetries uint                     `json:"restartRetries"`
	StopTimeout    uint                     `json:"stopTimeout"`
	Annotations    map[string]string        `json:"annotations"`
	ExtraHosts     []string                 `json:"extraHosts"`
	ShmSize        string                   `json:"shmSize"`
	PullImage      bool                     `json:"pullImage"`
}

func SpecOf(c containerModels.Container) Spec {
	network := HostNetwork
	if c.Network != nil {
		network = c.Network.Name
	}
	return Spec{Image: c.Image, OS: c.OS, Command: c.Command, Entrypoint: c.Entrypoint, WorkDir: c.WorkDir,
		User: c.User, Hostname: c.Hostname, Env: c.Env, Network: network, IP: c.IP, Ports: c.Ports, Mounts: c.Mounts,
		Restart: c.Restart, RestartRetries: c.RestartRetries, StopTimeout: c.StopTimeout,
		Annotations: c.Annotations, ExtraHosts: c.ExtraHosts, ShmSize: c.ShmSize}
}

type CreateRequest struct {
	OCID        uint   `json:"ocId" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Pool        string `json:"pool" binding:"required"`
	Spec
	Cores            uint   `json:"cores"`
	Memory           uint64 `json:"memory"`
	StartAtBoot      bool   `json:"startAtBoot"`
	StartOrder       int    `json:"startOrder"`
	StartAfterCreate bool   `json:"startAfterCreate"`
	WebPort          uint16 `json:"webPort"`
	WebHTTPS         bool   `json:"webHttps"`
	CatalogApp       string `json:"catalogApp"`
	Stack            string `json:"stack"`
}

type CatalogPlan struct {
	Stack      string          `json:"stack"`
	Containers []CreateRequest `json:"containers"`
}

type StackRequest struct {
	Stack       string          `json:"stack" binding:"required"`
	Pool        string          `json:"pool" binding:"required"`
	OCID        uint            `json:"ocId" binding:"required"`
	StartAtBoot bool            `json:"startAtBoot"`
	Containers  []CreateRequest `json:"containers" binding:"required"`
}

type DeleteRequest struct {
	DeleteVolumes bool `json:"deleteVolumes"`
}

type OptionsRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Cores       *uint   `json:"cores"`
	Memory      *uint64 `json:"memory"`
	StartAtBoot *bool   `json:"startAtBoot"`
	StartOrder  *int    `json:"startOrder"`
}

type NetworkRequest struct {
	Name       string `json:"name" binding:"required"`
	Switch     string `json:"switch"`
	Subnet     string `json:"subnet"`
	Gateway    string `json:"gateway"`
	RangeStart string `json:"rangeStart"`
	RangeEnd   string `json:"rangeEnd"`
}

type RegistryCredentialRequest struct {
	Registry string `json:"registry" binding:"required"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type State struct {
	State      string    `json:"state"`
	ExitCode   int       `json:"exitCode"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	IPs        []string  `json:"ips"`
	MACs       []string  `json:"macs"`
}

type ImageInfo struct {
	ID           string   `json:"id"`
	Digest       string   `json:"digest"`
	Size         int64    `json:"size"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	URL          string   `json:"url"`
	ExposedPorts []string `json:"exposedPorts"`
	Volumes      []string `json:"volumes"`
}

type ListItem struct {
	containerModels.Container
	State string `json:"state"`
}

type SimpleList struct {
	OCID  uint   `json:"ocId"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type Detail struct {
	containerModels.Container
	Runtime     *State  `json:"runtime"`
	CPUUsage    float64 `json:"cpuUsage"`
	MemoryUsage float64 `json:"memoryUsage"`
}

type Image struct {
	ID      string    `json:"id"`
	Names   []string  `json:"names"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

type CatalogApp struct {
	Source      string           `json:"source"`
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Category    string           `json:"category"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	OS          string           `json:"os"`
	Variants    []CatalogVariant `json:"variants"`
	Stack       bool             `json:"stack"`
}

type CatalogVariant struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Image   string `json:"image"`
	Default bool   `json:"default"`
}

type CatalogVariable struct {
	Name     string            `json:"name"`
	Label    string            `json:"label"`
	Hint     string            `json:"hint,omitempty"`
	Type     string            `json:"type"`
	Default  string            `json:"default"`
	Optional bool              `json:"optional"`
	Advanced bool              `json:"advanced"`
	Volume   string            `json:"volume"`
	Owner    string            `json:"owner,omitempty"`
	Protocol string            `json:"protocol,omitempty"`
	Target   string            `json:"target,omitempty"`
	Values   map[string]string `json:"values,omitempty"`
}

type UsedPort struct {
	Protocol string `json:"protocol"`
	Port     uint16 `json:"port"`
	Address  string `json:"address"`
	By       string `json:"by"`
}

type CatalogChoice struct {
	ID      string                `json:"id"`
	Label   string                `json:"label"`
	Doc     string                `json:"doc"`
	Default string                `json:"default"`
	Options []CatalogChoiceOption `json:"options"`
}

type CatalogChoiceOption struct {
	ID    string            `json:"id"`
	Label string            `json:"label"`
	Doc   string            `json:"doc"`
	Adds  []string          `json:"adds"`
	Drops []string          `json:"drops"`
	Ask   []CatalogVariable `json:"ask"`
}

type CatalogTemplate struct {
	App        CatalogApp        `json:"app"`
	Variables  []CatalogVariable `json:"variables"`
	Choices    []CatalogChoice   `json:"choices"`
	Containers []string          `json:"containers"`
}

type CatalogRenderRequest struct {
	Variant string            `json:"variant"`
	Values  map[string]string `json:"values"`
	Choices map[string]string `json:"choices"`
}

type ContainerServiceInterface interface {
	List(ctx context.Context) ([]ListItem, error)
	Get(ctx context.Context, ocid uint) (Detail, error)
	ImageInfo(ctx context.Context, ocid uint) (ImageInfo, error)
	Logs(ctx context.Context, ocid uint, tail uint) (string, error)
	Usage(ctx context.Context, ocid uint, step db.GFSStep) ([]containerModels.Stats, error)
	UsageBootstrap(ctx context.Context, ocid uint) (db.StatsBootstrap[containerModels.Stats], error)
	Command(ctx context.Context, ocid uint, args ...string) (*exec.Cmd, error)
	ConsoleCommand(ctx context.Context, ocid uint, session, shell string) (*exec.Cmd, error)
	EndConsole(ctx context.Context, ocid uint, session string)
	UpdateOptions(ctx context.Context, ocid uint, req OptionsRequest) error

	UsedPorts(ctx context.Context, exceptOCID uint) ([]UsedPort, error)

	ListNetworks(ctx context.Context) ([]containerModels.Network, error)
	CreateNetwork(ctx context.Context, req NetworkRequest) (containerModels.Network, error)
	DeleteNetwork(ctx context.Context, id uint) error

	ListImages(ctx context.Context, pool string) ([]Image, error)
	PullImage(ctx context.Context, pool, image, os string) error
	RemoveImage(ctx context.Context, pool, id string) error
	PruneImages(ctx context.Context, pool string) error

	Catalog(ctx context.Context) ([]CatalogApp, error)
	CatalogTemplate(ctx context.Context, source, id string) (CatalogTemplate, error)
	CatalogIcon(ctx context.Context, source, id string) (string, error)
	RenderCatalogApp(ctx context.Context, source, id string, req CatalogRenderRequest) (CatalogPlan, error)

	ListRegistryCredentials(ctx context.Context) ([]containerModels.RegistryCredential, error)
	SaveRegistryCredential(ctx context.Context, req RegistryCredentialRequest) (containerModels.RegistryCredential, error)
	DeleteRegistryCredential(ctx context.Context, id uint) error
}
