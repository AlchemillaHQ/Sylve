// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	networkModels "github.com/alchemillahq/sylve/internal/db/models/network"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/logger"
	"github.com/alchemillahq/sylve/internal/network/attachment"
	"gorm.io/gorm"
)

func (s *Service) ListNetworks(ctx context.Context) ([]containerModels.Network, error) {
	var networks []containerModels.Network
	err := s.DB.WithContext(ctx).Order("name").Find(&networks).Error
	return networks, err
}

func (s *Service) networkByName(ctx context.Context, name string) (*containerModels.Network, error) {
	var network containerModels.Network
	err := s.DB.WithContext(ctx).Where("name = ?", name).First(&network).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if name != containerServiceInterfaces.DefaultNetwork {
			return nil, fmt.Errorf("container_network_not_found: %s", name)
		}
		network, err = s.CreateNetwork(ctx, containerServiceInterfaces.NetworkRequest{Name: name})
	}
	if err != nil {
		return nil, err
	}
	return &network, nil
}

func (s *Service) CreateNetwork(ctx context.Context, req containerServiceInterfaces.NetworkRequest) (containerModels.Network, error) {
	network := containerModels.Network{Name: req.Name, Subnet: req.Subnet, Gateway: req.Gateway, RangeStart: req.RangeStart, RangeEnd: req.RangeEnd}
	if !nameRegex.MatchString(req.Name) || req.Name == containerServiceInterfaces.HostNetwork {
		return network, fmt.Errorf("invalid_network_name")
	}

	if req.Switch != "" {
		if err := s.resolveSwitch(ctx, &network, req.Switch); err != nil {
			return network, err
		}
		if req.Subnet == "" || req.Gateway == "" {
			return network, fmt.Errorf("switch_network_requires_subnet_and_gateway")
		}
	} else if req.Subnet == "" {
		subnet, err := s.freeSubnet(ctx)
		if err != nil {
			return network, err
		}
		network.Subnet = subnet
	}

	_, subnet, err := net.ParseCIDR(network.Subnet)
	if err != nil || subnet.IP.To4() == nil || subnet.String() != network.Subnet {
		return network, fmt.Errorf("invalid_subnet: use IPv4 CIDR notation such as 10.89.0.0/24")
	}
	if network.Gateway == "" {
		gateway := append(net.IP(nil), subnet.IP.To4()...)
		gateway[3]++
		network.Gateway = gateway.String()
	}
	for _, address := range []string{network.Gateway, network.RangeStart, network.RangeEnd} {
		if ip := net.ParseIP(address); address != "" && (ip == nil || !subnet.Contains(ip)) {
			return network, fmt.Errorf("invalid_address_outside_subnet: %s", address)
		}
	}
	if network.SwitchID == nil {
		if err := s.requireSubnetFree(ctx, subnet); err != nil {
			return network, err
		}
	}

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&network).Error; err != nil {
			return err
		}
		if network.SwitchID == nil {
			network.Bridge = fmt.Sprintf("sylc%d", network.ID)
			if err := tx.Model(&network).Update("bridge", network.Bridge).Error; err != nil {
				return err
			}
		}
		return s.writeConflist(network)
	})
	if err == nil && network.SwitchID == nil {
		s.applyFirewall()
	}
	return network, err
}

func (s *Service) applyFirewall() {
	if s.ApplyFirewall == nil {
		return
	}
	if err := s.ApplyFirewall(); err != nil {
		logger.L.Warn().Err(err).Msg("container_network_firewall_apply_failed")
	}
}

func (s *Service) resolveSwitch(ctx context.Context, network *containerModels.Network, name string) error {
	var standard networkModels.StandardSwitch
	if err := s.DB.WithContext(ctx).Where("name = ?", name).Limit(1).Find(&standard).Error; err != nil {
		return err
	}
	if standard.ID != 0 {
		network.SwitchID, network.SwitchType, network.Bridge = &standard.ID, "standard", standard.BridgeName
		return nil
	}
	var manual networkModels.ManualSwitch
	if err := s.DB.WithContext(ctx).Where("name = ?", name).Limit(1).Find(&manual).Error; err != nil {
		return err
	}
	if manual.ID == 0 {
		return fmt.Errorf("switch_not_found: %s", name)
	}
	network.SwitchID, network.SwitchType, network.Bridge = &manual.ID, "manual", manual.Bridge
	return nil
}

func (s *Service) requireSubnetFree(ctx context.Context, subnet *net.IPNet) error {
	var networks []containerModels.Network
	if err := s.DB.WithContext(ctx).Find(&networks).Error; err != nil {
		return err
	}
	var used []*net.IPNet
	for _, network := range networks {
		if _, other, err := net.ParseCIDR(network.Subnet); err == nil {
			used = append(used, other)
		}
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if prefix, ok := address.(*net.IPNet); ok {
			used = append(used, prefix)
		}
	}
	for _, other := range used {
		if other.Contains(subnet.IP) || subnet.Contains(other.IP) {
			return fmt.Errorf("subnet_in_use: %s overlaps %s", subnet, other)
		}
	}
	return nil
}

func (s *Service) freeSubnet(ctx context.Context) (string, error) {
	for i := 0; i < 256; i++ {
		candidate := fmt.Sprintf("10.89.%d.0/24", i)
		_, subnet, _ := net.ParseCIDR(candidate)
		if s.requireSubnetFree(ctx, subnet) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no_free_subnet: specify one")
}

func (s *Service) writeConflist(network containerModels.Network) error {
	ipRange := map[string]string{"subnet": network.Subnet, "gateway": network.Gateway}
	if network.RangeStart != "" {
		ipRange["rangeStart"] = network.RangeStart
	}
	if network.RangeEnd != "" {
		ipRange["rangeEnd"] = network.RangeEnd
	}
	nat := network.SwitchID == nil
	bridge := map[string]any{
		"type": "bridge", "bridge": network.Bridge, "isGateway": nat, "ipMasq": nat, "hairpinMode": true,
		"capabilities": map[string]bool{"ips": true},
		"ipam": map[string]any{"type": "host-local", "ranges": [][]map[string]string{{ipRange}},
			"routes": []map[string]string{{"dst": "0.0.0.0/0", "gw": network.Gateway}}},
	}
	plugins := []any{bridge}
	if nat {
		plugins = append(plugins,
			map[string]any{"type": "portmap", "capabilities": map[string]bool{"portMappings": true}},
			map[string]any{"type": "firewall"},
			map[string]any{"type": "tuning"},
			map[string]any{"type": "dnsname", "domainName": "dns.podman", "capabilities": map[string]bool{"aliases": true}})
	} else {
		plugins = append(plugins, map[string]any{"type": "tuning"})
	}
	data, err := json.MarshalIndent(map[string]any{"cniVersion": "0.4.0", "name": network.PodmanName(), "plugins": plugins}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.conflistPath(network), string(data)+"\n", 0o600)
}

func (s *Service) conflistPath(network containerModels.Network) string {
	return s.path("cni", "net.d", network.PodmanName()+".conflist")
}

func (s *Service) DeleteNetwork(ctx context.Context, id uint) error {
	var network containerModels.Network
	if err := s.DB.WithContext(ctx).First(&network, id).Error; err != nil {
		return err
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&containerModels.Container{}).Where("network_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("container_network_in_use")
	}
	if err := s.DB.WithContext(ctx).Delete(&network).Error; err != nil {
		return err
	}
	if err := os.Remove(s.conflistPath(network)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if network.SwitchID == nil {
		_ = exec.CommandContext(ctx, "/sbin/ifconfig", network.Bridge, "destroy").Run()
		dns := filepath.Join(dnsnameDir, network.PodmanName())
		_ = exec.CommandContext(ctx, "/bin/pkill", "-f", "--", "--conf-file="+filepath.Join(dns, "dnsmasq.conf")).Run()
		_ = os.RemoveAll(dns)
		s.applyFirewall()
	}
	return nil
}

// NetworkSwitchName is "" for a network on its own bridge or one that doesn't exist yet.
func (s *Service) NetworkSwitchName(ctx context.Context, name string) (string, error) {
	var network containerModels.Network
	if err := s.DB.WithContext(ctx).Where("name = ?", name).Limit(1).Find(&network).Error; err != nil {
		return "", err
	}
	if network.SwitchID == nil {
		return "", nil
	}
	identity, err := attachment.NewResolver(s.DB).ResolveIdentityByID(network.SwitchType, *network.SwitchID)
	if err != nil {
		return "", err
	}
	return identity.Name, nil
}
