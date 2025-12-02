package zfs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alchemillahq/sylve/pkg/exe"
	"github.com/alchemillahq/sylve/pkg/utils"
)

type InodeType int
type ChangeType int
type DestroyFlag int

const (
	DatasetFilesystem = "filesystem"
	DatasetSnapshot   = "snapshot"
	DatasetVolume     = "volume"
)

const (
	DestroyDefault         DestroyFlag = 1 << iota
	DestroyRecursive                   = 1 << iota
	DestroyRecursiveClones             = 1 << iota
	DestroyDeferDeletion               = 1 << iota
	DestroyForceUmount                 = 1 << iota
)

type zfs struct {
	exec exe.Executor
	sudo bool
}

type InodeChange struct {
	Change               ChangeType
	Type                 InodeType
	Path                 string
	NewPath              string
	ReferenceCountChange int
}

type ZFS interface {
	Datasets(filter string) ([]*Dataset, error)
	GetDataset(name string) (*Dataset, error)
	CreateFilesystem(name string, properties map[string]string) (*Dataset, error)
	EditFilesystem(name string, props map[string]string) error
	Filesystems(filter string) ([]*Dataset, error)
	CreateVolume(name string, size uint64, properties map[string]string) (*Dataset, error)
	EditVolume(name string, props map[string]string) error
	Volumes(filter string) ([]*Dataset, error)
	Snapshots(filter string) ([]*Dataset, error)
	ReceiveSnapshot(input io.Reader, name string, force ...bool) (*Dataset, error)

	ListZpools() ([]*Zpool, error)
	GetZpool(name string) (*Zpool, error)
	GetZpoolByGUID(guid string) (*Zpool, error)
	ScrubPool(name string) error
	CreateZpool(name string, properties map[string]string, args ...string) (*Zpool, error)
	GetPoolIODelay(poolName string) (float64, error)
	GetTotalIODelay() float64
	SetZpoolProperty(pool string, property string, value string) error
}

func (z *zfs) do(arg ...string) error {
	_, err := z.doOutput(arg...)
	return err
}

func (z *zfs) doOutput(arg ...string) ([][]string, error) {
	return z.run(nil, nil, "zfs", arg...)
}

func (z *zfs) doOutputJSON(arg ...string) ([]byte, error) {
	return z.runJSON("zfs", arg...)
}

func (z *zfs) listByType(t, filter string) ([]*Dataset, error) {
	var datasets []*Dataset

	args := []string{"list", "-rp", "-t", t, "-o", strings.Join(dsPropList, ","), "-j"}

	if filter != "" {
		args = append(args, filter)
	}

	outBytes, err := z.doOutputJSON(args...)
	if err != nil {
		return nil, err
	}

	var output JSONDatasets
	if err := json.Unmarshal(outBytes, &output); err != nil {
		return nil, fmt.Errorf("failed to unmarshal zfs list output: %w", err)
	}

	for name, jds := range output.Datasets {
		ds := &Dataset{z: z, Name: name, Props: make(map[string]string)}
		if err := ds.parsePropsJSON(jds); err != nil {
			return nil, err
		}

		datasets = append(datasets, ds)
	}

	return datasets, nil
}

func (z *zfs) Datasets(filter string) ([]*Dataset, error) {
	return z.listByType("all", filter)
}

func (z *zfs) Snapshots(filter string) ([]*Dataset, error) {
	return z.listByType(DatasetSnapshot, filter)
}

func (z *zfs) Filesystems(filter string) ([]*Dataset, error) {
	return z.listByType(DatasetFilesystem, filter)
}

func (z *zfs) Volumes(filter string) ([]*Dataset, error) {
	return z.listByType(DatasetVolume, filter)
}

func (z *zfs) GetDataset(name string) (*Dataset, error) {
	datasets, err := z.listByType("all", name)
	if err != nil {
		return nil, err
	}

	if len(datasets) == 0 {
		return nil, fmt.Errorf("dataset_not_found: %s", name)
	}

	return datasets[0], nil
}

func (z *zfs) ReceiveSnapshot(input io.Reader, name string, force ...bool) (*Dataset, error) {
	args := []string{"receive"}
	if len(force) > 0 && force[0] {
		args = append(args, "-F")
	}
	args = append(args, name)
	if _, err := z.run(input, nil, "zfs", args...); err != nil {
		return nil, err
	}
	return z.GetDataset(name)
}

func (z *zfs) CreateVolume(name string, size uint64, properties map[string]string) (*Dataset, error) {
	args := make([]string, 4, 5)
	args[0] = "create"
	args[1] = "-p"
	args[2] = "-V"
	args[3] = strconv.FormatUint(size, 10)

	if _, ok := properties["encryptionKey"]; ok {
		if properties["encryptionKey"] != "" && properties["encryption"] != "off" {
			if len([]byte(properties["encryptionKey"])) < 32 || len([]byte(properties["encryptionKey"])) > 512 {
				return nil, fmt.Errorf("invalid_encryption_key_length")
			}

			seed := fmt.Sprintf("%s-%s", name, properties["encryptionKey"])
			randomFile := fmt.Sprintf("/etc/zfs/keys/%s", utils.GenerateDeterministicUUID(seed))

			if _, err := os.Stat(randomFile); err == nil {
				return nil, fmt.Errorf("dont_reuse_encryption_keys")
			}

			if err := os.WriteFile(randomFile, []byte(properties["encryptionKey"]), 0600); err != nil {
				return nil, fmt.Errorf("failed_to_write_encryption_key")
			}

			properties["keylocation"] = fmt.Sprintf("file://%s", randomFile)
			properties["keyformat"] = "passphrase"
		}
	}

	delete(properties, "encryptionKey")
	delete(properties, "parent")
	delete(properties, "size")

	if properties != nil {
		args = append(args, propsSlice(properties)...)
	}

	args = append(args, name)
	if err := z.do(args...); err != nil {
		return nil, err
	}

	return z.GetDataset(name)
}

func (z *zfs) EditVolume(name string, props map[string]string) error {
	dataset, err := z.GetDataset(name)
	if err != nil {
		return fmt.Errorf("error_getting_dataset: %w", err)
	}

	if props == nil || len(props) == 0 {
		return fmt.Errorf("no_properties_to_edit")
	}

	if _, ok := props["encryptionKey"]; ok {
		delete(props, "encryptionKey")
	}

	if _, ok := props["quota"]; ok {
		if props["quota"] == "" {
			delete(props, "quota")
		}
	}

	var keyValPairs []string
	for k, v := range props {
		keyValPairs = append(keyValPairs, k, v)
	}
	if err := dataset.SetProperties(keyValPairs...); err != nil {
		return err
	}

	return nil
}

// https://openzfs.github.io/openzfs-docs/man/7/zfsprops.7.html.
func (z *zfs) CreateFilesystem(name string, properties map[string]string) (*Dataset, error) {
	args := make([]string, 1, 4)
	args[0] = "create"

	if _, ok := properties["encryptionKey"]; ok {
		if properties["encryptionKey"] != "" && properties["encryption"] != "off" {
			if len([]byte(properties["encryptionKey"])) < 32 || len([]byte(properties["encryptionKey"])) > 512 {
				return nil, fmt.Errorf("invalid_encryption_key_length")
			}

			seed := fmt.Sprintf("%s-%s", name, properties["encryptionKey"])
			randomFile := fmt.Sprintf("/etc/zfs/keys/%s", utils.GenerateDeterministicUUID(seed))

			if _, err := os.Stat(randomFile); err == nil {
				return nil, fmt.Errorf("dont_reuse_encryption_keys")
			}

			if err := os.WriteFile(randomFile, []byte(properties["encryptionKey"]), 0600); err != nil {
				return nil, fmt.Errorf("failed_to_write_encryption_key")
			}

			properties["keylocation"] = fmt.Sprintf("file://%s", randomFile)
			properties["keyformat"] = "passphrase"
		}
	}

	delete(properties, "encryptionKey")

	if _, ok := properties["quota"]; ok {
		if properties["quota"] == "" {
			delete(properties, "quota")
		}
	}

	if properties != nil {
		args = append(args, propsSlice(properties)...)
	}

	args = append(args, name)
	if err := z.do(args...); err != nil {
		return nil, err
	}
	return z.GetDataset(name)
}

func (z *zfs) EditFilesystem(name string, props map[string]string) error {
	dataset, err := z.GetDataset(name)
	if err != nil {
		return fmt.Errorf("error_getting_dataset: %w", err)
	}

	if props == nil || len(props) == 0 {
		return fmt.Errorf("no_properties_to_edit")
	}

	if _, ok := props["encryptionKey"]; ok {
		delete(props, "encryptionKey")
	}

	if _, ok := props["quota"]; ok {
		if props["quota"] == "" || props["quota"] == "0B" {
			delete(props, "quota")
		}
	}

	var keyValPairs []string
	for k, v := range props {
		keyValPairs = append(keyValPairs, k, v)
	}
	if err := dataset.SetProperties(keyValPairs...); err != nil {
		return err
	}

	return nil
}
