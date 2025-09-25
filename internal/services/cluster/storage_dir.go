package cluster

import (
	"encoding/json"
	"fmt"
	"time"

	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) ProposeDirectoryConfig(name string, path string, bypassRaft bool) error {
	exists, err := utils.IsDir(path)

	if err != nil {
		return fmt.Errorf("failed to check if directory exists: %w", err)
	}

	if !exists {
		return fmt.Errorf("directory does not exist: %s", path)
	}

	if bypassRaft {
		dir := clusterModels.ClusterDirectoryConfig{
			Name: name,
			Path: path,
		}

		err := s.DB.Create(&dir).Error
		if err != nil {
			return err
		}

		return nil
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{
		Name: name,
		Path: path,
	}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_note_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "directoryConfigs",
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

func (s *Service) ProposeDirectoryConfigDelete(id uint, bypassRaft bool) error {
	if bypassRaft {
		return s.DB.Delete(&clusterModels.ClusterDirectoryConfig{}, id).Error
	}

	if s.Raft == nil {
		return fmt.Errorf("raft_not_initialized")
	}

	payloadStruct := struct {
		ID uint `json:"id"`
	}{ID: id}

	data, err := json.Marshal(payloadStruct)
	if err != nil {
		return fmt.Errorf("failed_to_marshal_payload: %w", err)
	}

	cmd := clusterModels.Command{
		Type:   "directoryConfigs",
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
