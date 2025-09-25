// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"encoding/json"
	"fmt"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	"github.com/alchemillahq/sylve/pkg/s3"
)

func (s *Service) ListStorages() (clusterServiceInterfaces.Storages, error) {
	var s3 []clusterModels.ClusterS3Config
	var directories []clusterModels.ClusterDirectoryConfig

	err := s.DB.Order("id ASC").Find(&s3).Error

	if err != nil {
		return clusterServiceInterfaces.Storages{}, err
	}

	err = s.DB.Order("id ASC").Find(&directories).Error
	if err != nil {
		return clusterServiceInterfaces.Storages{}, err
	}

	return clusterServiceInterfaces.Storages{S3: s3, Directories: directories}, nil
}

func (s *Service) ProposeS3Config(name,
	endpoint,
	region,
	bucket,
	accessKey,
	secretKey string,
	bypassRaft bool) error {
	err := s3.ValidateConfig(endpoint, region, bucket, accessKey, secretKey)
	if err != nil {
		return fmt.Errorf("s3_config_invalid: %w", err)
	}

	if bypassRaft {
		s3 := clusterModels.ClusterS3Config{
			Name:      name,
			Endpoint:  endpoint,
			Region:    region,
			Bucket:    bucket,
			AccessKey: accessKey,
			SecretKey: secretKey,
		}

		err := s.DB.Create(&s3).Error
		if err != nil {
			return err
		}

		return nil
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		Name      string `json:"name"`
		Endpoint  string `json:"endpoint"`
		Region    string `json:"region"`
		Bucket    string `json:"bucket"`
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
	}{
		Name:      name,
		Endpoint:  endpoint,
		Region:    region,
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
	}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_note_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "s3Configs",
		Action: "create",
		Data:   data,
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_command: %w", err)
	}

	applyFuture := s.Raft.Apply(payload, 5*time.Second)
	if err := applyFuture.Error(); err != nil {
		return fmt.Errorf("raft_apply_failed: %w", err)
	}

	if resp, ok := applyFuture.Response().(error); ok && resp != nil {
		return fmt.Errorf("fsm_apply_failed: %w", resp)
	}

	return nil
}

func (s *Service) ProposeS3ConfigDelete(id uint, bypassRaft bool) error {
	if bypassRaft {
		return s.DB.Delete(&clusterModels.ClusterS3Config{}, id).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		ID uint `json:"id"`
	}{ID: id}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_delete_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "s3Configs",
		Action: "delete",
		Data:   data,
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_command: %w", err)
	}

	applyFuture := s.Raft.Apply(payload, 5*time.Second)
	if err := applyFuture.Error(); err != nil {
		return fmt.Errorf("raft_apply_failed: %w", err)
	}

	if resp, ok := applyFuture.Response().(error); ok && resp != nil {
		return fmt.Errorf("fsm_apply_failed: %w", resp)
	}

	return nil
}
