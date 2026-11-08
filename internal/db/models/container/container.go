// SPDX-License-Identifier: BSD-2-Clause

package containerModels

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const ImagesDataset = "sylve/containers/images"

func VolumesDataset(pool string, ocid uint) string {
	return fmt.Sprintf("%s/sylve/containers/%d", pool, ocid)
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Port struct {
	Protocol      string `json:"protocol"`
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
}

type MountType string

const (
	MountVolume  MountType = "volume"
	MountDataset MountType = "dataset"
	MountPath    MountType = "path"
)

type Mount struct {
	Type     MountType `json:"type"`
	Source   string    `json:"source"`
	Target   string    `json:"target"`
	ReadOnly bool      `json:"readOnly"`
	GUID     string    `json:"guid,omitempty"`
	Owner    string    `json:"owner,omitempty"`
	Mode     string    `json:"mode,omitempty"`
}

type Container struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	OCID        uint   `json:"ocId" gorm:"uniqueIndex;not null"`
	Name        string `json:"name" gorm:"uniqueIndex;not null"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Pool        string `json:"pool" gorm:"not null"`

	Image      string   `json:"image" gorm:"not null"`
	OS         string   `json:"os" gorm:"not null"`
	Command    []string `json:"command" gorm:"serializer:json"`
	Entrypoint []string `json:"entrypoint" gorm:"serializer:json"`
	WorkDir    string   `json:"workDir"`
	User       string   `json:"user"`
	Hostname   string   `json:"hostname"`
	Env        []EnvVar `json:"env" gorm:"serializer:json"`

	NetworkID *uint    `json:"networkId"`
	Network   *Network `json:"network,omitempty" gorm:"foreignKey:NetworkID"`
	IP        string   `json:"ip"`
	Ports     []Port   `json:"ports" gorm:"serializer:json"`
	Mounts    []Mount  `json:"mounts" gorm:"serializer:json"`

	Cores  uint   `json:"cores"`
	Memory uint64 `json:"memory"`

	Restart        string            `json:"restart" gorm:"not null;default:no"`
	RestartRetries uint              `json:"restartRetries"`
	StopTimeout    uint              `json:"stopTimeout" gorm:"not null;default:10"`
	Annotations    map[string]string `json:"annotations" gorm:"serializer:json"`
	ExtraHosts     []string          `json:"extraHosts" gorm:"serializer:json"`
	ShmSize        string            `json:"shmSize"`
	StartAtBoot    bool              `json:"startAtBoot"`
	StartOrder     int               `json:"startOrder"`

	WebPort    uint16 `json:"webPort"`
	WebHTTPS   bool   `json:"webHttps"`
	CatalogApp string `json:"catalogApp"`
	Stack      string `json:"stack" gorm:"index"`
	StackTitle string `json:"stackTitle"`
	Service    string `json:"service"`
	// UpdateDigest is the newer digest the image's tag points at in its
	// registry, found by the update check; empty when it's current or unknown.
	UpdateDigest string `json:"updateDigest"`

	RuntimeID       string    `json:"runtimeId"`
	CreationPending bool      `json:"creationPending" gorm:"not null;default:false"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func (Container) TableName() string { return "containers" }

func (c Container) RuntimeName() string { return fmt.Sprintf("sylve-%d", c.OCID) }

func (c Container) DatasetRoots() []string {
	roots := []string{c.Pool + "/" + ImagesDataset, VolumesDataset(c.Pool, c.OCID)}
	for _, mount := range c.Mounts {
		if mount.Type == MountDataset {
			roots = append(roots, mount.Source)
		}
	}
	return roots
}

type Network struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	Name       string    `json:"name" gorm:"uniqueIndex;not null"`
	SwitchID   *uint     `json:"switchId"`
	SwitchType string    `json:"switchType"`
	Bridge     string    `json:"bridge" gorm:"not null"`
	Subnet     string    `json:"subnet" gorm:"not null"`
	Gateway    string    `json:"gateway" gorm:"not null"`
	RangeStart string    `json:"rangeStart"`
	RangeEnd   string    `json:"rangeEnd"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (Network) TableName() string { return "container_networks" }

func (n Network) PodmanName() string { return "sylve-" + n.Name }

type RegistryCredential struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Registry  string    `json:"registry" gorm:"uniqueIndex;not null"`
	Username  string    `json:"username" gorm:"not null"`
	Password  string    `json:"-" gorm:"not null"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (RegistryCredential) TableName() string { return "container_registry_credentials" }

func DatasetsInUse(db *gorm.DB, guids []string) (bool, error) {
	if len(guids) == 0 || !db.Migrator().HasTable(&Container{}) {
		return false, nil
	}
	var count int64
	err := db.Raw(`SELECT COUNT(*) FROM containers, json_each(containers.mounts) m
		WHERE json_extract(m.value, '$.guid') IN ?`, guids).Scan(&count).Error
	return count > 0, err
}

type Stats struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	ContainerID uint      `json:"containerId" gorm:"index"`
	CPUUsage    float64   `json:"cpuUsage"`
	MemoryUsage float64   `json:"memoryUsage"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

func (Stats) TableName() string { return "container_stats" }

func (s Stats) GetID() uint { return s.ID }

func (s Stats) GetCreatedAt() time.Time { return s.CreatedAt }

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&Network{}, &Container{}, &RegistryCredential{}, &Stats{})
}
