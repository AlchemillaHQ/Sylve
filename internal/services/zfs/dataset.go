// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"fmt"
	"os"
	zfsServiceInterfaces "sylve/internal/interfaces/services/zfs"
	"sylve/pkg/utils"
	"sylve/pkg/zfs"
)

func (s *Service) GetDatasets() ([]zfsServiceInterfaces.Dataset, error) {
	var results []zfsServiceInterfaces.Dataset

	datasets, err := zfs.Datasets("")
	if err != nil {
		return nil, err
	}

	for _, dataset := range datasets {
		props, err := dataset.GetAllProperties()
		if err != nil {
			return nil, err
		}

		propMap := make(map[string]string, len(props))
		for k, v := range props {
			propMap[k] = v
		}

		results = append(results, zfsServiceInterfaces.Dataset{
			Dataset:    *dataset,
			Properties: propMap,
		})
	}

	return results, nil
}

func (s *Service) DeleteSnapshot(guid string) error {
	datasets, err := zfs.Snapshots("")

	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				err := dataset.Destroy(zfs.DestroyDefault)

				if err != nil {
					return err
				}

				return nil
			}
		}
	}

	return fmt.Errorf("snapshot with guid %s not found", guid)
}

func (s *Service) CreateSnapshot(guid string, name string, recursive bool) error {
	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == dataset.Name+"@"+name {
			return fmt.Errorf("snapshot with name %s already exists", name)
		}

		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for k, v := range properties {
			if k == "guid" {
				if v == guid {
					shot, err := dataset.Snapshot(name, recursive)
					if err != nil {
						return err
					}

					if shot.Name == dataset.Name+"@"+name {
						return nil
					}
				}
			}
		}
	}

	return fmt.Errorf("dataset with guid %s not found", guid)
}

func (s *Service) CreateFilesystem(name string, props map[string]string) error {
	parent := ""

	for k, v := range props {
		if k == "parent" {
			parent = v
			continue
		}
	}

	if parent == "" {
		return fmt.Errorf("parent_not_found")
	}

	name = fmt.Sprintf("%s/%s", parent, name)
	delete(props, "parent")

	_, err := zfs.CreateFilesystem(name, props)

	if err != nil {
		return err
	}

	datasets, err := zfs.Datasets(name)
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == name {
			return nil
		}
	}

	return fmt.Errorf("failed to create filesystem %s", name)
}

func (s *Service) DeleteFilesystem(guid string) error {
	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		var keylocation string
		found := false

		for k, v := range properties {
			if v == guid {
				found = true
			}
			if k == "keylocation" {
				keylocation = v
			}
		}

		if found {
			if err := dataset.Destroy(zfs.DestroyDefault); err != nil {
				return err
			}

			if keylocation != "" {
				keylocation = keylocation[7:]
				if _, err := os.Stat(keylocation); err == nil {
					if err := os.Remove(keylocation); err != nil {
						return err
					}
				} else {
					fmt.Println("Keylocation file not found", keylocation)
				}
			}

			return nil
		}
	}

	return fmt.Errorf("filesystem with guid %s not found", guid)
}

func (s *Service) RollbackSnapshot(guid string, destroyMoreRecent bool) error {
	datasets, err := zfs.Snapshots("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				err := dataset.Rollback(destroyMoreRecent)
				if err != nil {
					return err
				}
				return nil
			}
		}
	}

	return fmt.Errorf("snapshot with guid %s not found", guid)
}

func (s *Service) CreateVolume(name string, parent string, props map[string]string) error {
	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		if dataset.Name == fmt.Sprintf("%s/%s", parent, name) && dataset.Type == "volume" {
			return fmt.Errorf("volume with name %s already exists", name)
		}
	}

	name = fmt.Sprintf("%s/%s", parent, name)

	if _, ok := props["size"]; !ok {
		return fmt.Errorf("size property not found")
	}

	pSize := utils.HumanFormatToSize(props["size"])

	_, err = zfs.CreateVolume(name, pSize, props)

	return err
}

func (s *Service) DeleteVolume(guid string) error {
	datasets, err := zfs.Datasets("")
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		properties, err := dataset.GetAllProperties()
		if err != nil {
			return err
		}

		for _, v := range properties {
			if v == guid {
				err := dataset.Destroy(zfs.DestroyDefault)
				if err != nil {
					return err
				}
				return nil
			}
		}
	}

	return fmt.Errorf("volume with guid %s not found", guid)
}
