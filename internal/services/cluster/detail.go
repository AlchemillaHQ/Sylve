package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/alchemillahq/sylve/internal"
	"github.com/alchemillahq/sylve/internal/config"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	clusterServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/cluster"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
	libvirtServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/libvirt"
	"github.com/alchemillahq/sylve/pkg/utils"
)

func (s *Service) Detail() *clusterServiceInterfaces.Detail {
	nodeId, err := utils.GetSystemUUID()
	if err != nil {
		return nil
	}

	hostname, err := utils.GetSystemHostname()
	if err != nil {
		return nil
	}

	apiPort := config.ParsedConfig.Port

	return &clusterServiceInterfaces.Detail{
		NodeID:   nodeId,
		Hostname: hostname,
		APIPort:  apiPort,
	}
}

func (s *Service) Nodes() ([]clusterModels.ClusterNode, error) {
	var nodes []clusterModels.ClusterNode
	if err := s.DB.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Service) Resources() ([]clusterServiceInterfaces.NodeResources, error) {
	nodes, err := s.Nodes()
	if err != nil {
		return nil, err
	}

	selfHostname, err := utils.GetSystemHostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get system hostname: %w", err)
	}

	clusterToken, err := s.AuthService.CreateClusterJWT(0, selfHostname, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster jwt: %w", err)
	}

	var results []clusterServiceInterfaces.NodeResources

	for _, n := range nodes {
		base := "https://" + n.API

		jailsURL := fmt.Sprintf("%s/api/jail/simple", base)
		vmsURL := fmt.Sprintf("%s/api/vm/simple", base)

		headers := map[string]string{
			"Accept":          "application/json",
			"X-Cluster-Token": fmt.Sprintf("Bearer %s", clusterToken),
		}

		var jails []jailServiceInterfaces.SimpleList
		if body, _, err := utils.HTTPGetJSONRead(jailsURL, headers); err == nil {
			var resp internal.APIResponse[[]jailServiceInterfaces.SimpleList]
			if err := json.Unmarshal(body, &resp); err == nil && resp.Status == "success" {
				jails = resp.Data
			}
		}

		var vms []libvirtServiceInterfaces.SimpleList
		if body, _, err := utils.HTTPGetJSONRead(vmsURL, headers); err == nil {
			var resp internal.APIResponse[[]libvirtServiceInterfaces.SimpleList]
			if err := json.Unmarshal(body, &resp); err == nil && resp.Status == "success" {
				vms = resp.Data
			}
		}

		results = append(results, clusterServiceInterfaces.NodeResources{
			NodeUUID: n.NodeUUID,
			Hostname: n.Hostname,
			Jails:    jails,
			VMs:      vms,
		})
	}

	return results, nil
}
