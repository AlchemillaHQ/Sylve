// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
)

func (s *Service) UsedPorts(ctx context.Context, exceptOCID uint) ([]containerServiceInterfaces.UsedPort, error) {
	var records []containerModels.Container
	if err := s.DB.WithContext(ctx).Find(&records).Error; err != nil {
		return nil, err
	}
	used := []containerServiceInterfaces.UsedPort{}
	seen := map[string]bool{}
	add := func(port containerServiceInterfaces.UsedPort) {
		if key := port.Protocol + "/" + strconv.Itoa(int(port.Port)); !seen[key] {
			seen[key] = true
			used = append(used, port)
		}
	}
	for _, record := range records {
		for _, port := range record.Ports {
			if record.OCID == exceptOCID {
				seen[port.Protocol+"/"+strconv.Itoa(int(port.HostPort))] = true
				continue
			}
			add(containerServiceInterfaces.UsedPort{Protocol: port.Protocol, Port: port.HostPort, Address: port.HostIP,
				By: fmt.Sprintf("container %s", record.Name)})
		}
	}

	output, err := exec.CommandContext(ctx, "/usr/bin/sockstat", "-46lqw").Output()
	if err != nil {
		return nil, fmt.Errorf("failed_to_list_listening_sockets: %w", err)
	}
	self, _ := os.Executable()
	for _, port := range parseSockstat(string(output), os.Getpid(), filepath.Base(self)) {
		add(port)
	}
	return used, nil
}

func parseSockstat(output string, pid int, program string) []containerServiceInterfaces.UsedPort {
	var ports []containerServiceInterfaces.UsedPort
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasPrefix(fields[4], "tcp") && !strings.HasPrefix(fields[4], "udp") {
			continue
		}
		at := strings.LastIndex(fields[5], ":")
		port, err := strconv.ParseUint(fields[5][at+1:], 10, 16)
		if at < 0 || err != nil {
			continue
		}
		address := strings.Trim(fields[5][:at], "[]")
		if address == "*" {
			address = ""
		}
		by := fields[1]
		if owner, _ := strconv.Atoi(fields[2]); owner == pid || by == program {
			by = "Sylve"
		}
		ports = append(ports, containerServiceInterfaces.UsedPort{Protocol: fields[4][:3], Port: uint16(port), Address: address, By: by})
	}
	return ports
}
