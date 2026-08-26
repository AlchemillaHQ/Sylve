// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"fmt"
	"slices"
	"strings"

	networkServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/network"
	utils "github.com/alchemillahq/sylve/pkg/utils"

	"github.com/alchemillahq/sylve/pkg/network/iface"
)

const sylveEpairGroup = "sylve"

var (
	epairInterfaceList = iface.List
	epairRunCommand    = utils.RunCommand
)

func (s *Service) CreateEpair(name string) error {
	s.epairMutex.Lock()
	defer s.epairMutex.Unlock()
	return s.createEpair(name)
}

func (s *Service) createEpair(name string) error {
	output, err := epairRunCommand("/sbin/ifconfig", "epair", "create")
	if err != nil {
		return fmt.Errorf("failed to create epair: %w", err)
	}

	epairA := strings.TrimSpace(string(output))
	if epairA == "" {
		return fmt.Errorf("failed to get epair name")
	}

	epairB := strings.TrimSuffix(epairA, "a") + "b"

	_, err = epairRunCommand("/sbin/ifconfig", epairA, "name", name+"a")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairA, name+"a", err)
	}

	_, err = epairRunCommand("/sbin/ifconfig", epairB, "name", name+"b")
	if err != nil {
		return fmt.Errorf("failed to rename epair %s to %s: %w", epairB, name+"b", err)
	}
	for _, epair := range []string{name + "a", name + "b"} {
		if _, err = epairRunCommand("/sbin/ifconfig", epair, "group", sylveEpairGroup); err != nil {
			return fmt.Errorf("failed to mark epair %s as Sylve-managed: %w", epair, err)
		}
	}

	return nil
}

func (s *Service) DeleteEpair(name string) error {
	s.epairMutex.Lock()
	defer s.epairMutex.Unlock()
	return s.deleteEpair(name)
}

func (s *Service) deleteEpair(name string) error {
	ifaces, err := epairInterfaceList()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	var epairA string
	epairBExists := false
	for _, ifc := range ifaces {
		if ifc.Name == name+"b" {
			epairBExists = true
		}
		if ifc.Name != name+"a" {
			continue
		}
		if !slices.Contains(ifc.Groups, sylveEpairGroup) {
			return fmt.Errorf("%w: refusing to delete unmanaged epair %s", networkServiceInterfaces.ErrEpairOwnershipConflict, name)
		}
		// The VNET transfer drops custom groups from the jail-side b interface.
		// The host-visible a side is therefore the ownership sentinel.
		epairA = ifc.Name
	}

	if epairA == "" {
		if epairBExists {
			return fmt.Errorf("%w: refusing to delete %s without its ownership sentinel", networkServiceInterfaces.ErrEpairStateConflict, name+"b")
		}
		return fmt.Errorf("epair %s not found", name)
	}

	_, err = epairRunCommand("/sbin/ifconfig", epairA, "destroy")

	if err != nil {
		return fmt.Errorf("failed to delete epair %s: %w", epairA, err)
	}

	return nil
}

// EnsureEpair is deliberately scoped to one configured pair. It creates a
// missing pair, reuses a complete Sylve-managed pair, and refuses to guess when
// only one endpoint is host-visible. The latter may be a healthy running VNET
// jail, so callers must never repair that state destructively.
func (s *Service) EnsureEpair(name string) error {
	s.epairMutex.Lock()
	defer s.epairMutex.Unlock()

	ifaces, err := epairInterfaceList()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	var epairA, epairB *iface.Interface
	for _, ifc := range ifaces {
		switch ifc.Name {
		case name + "a":
			epairA = ifc
		case name + "b":
			epairB = ifc
		}
	}

	if epairA == nil && epairB == nil {
		return s.createEpair(name)
	}
	if epairA == nil {
		return fmt.Errorf("%w: %s exists without its host-side peer", networkServiceInterfaces.ErrEpairStateConflict, name+"b")
	}
	if !slices.Contains(epairA.Groups, sylveEpairGroup) {
		return fmt.Errorf("%w: refusing to adopt unmanaged epair %s", networkServiceInterfaces.ErrEpairOwnershipConflict, epairA.Name)
	}
	if epairB == nil {
		return fmt.Errorf("%w: %s is not host-visible", networkServiceInterfaces.ErrEpairStateConflict, name+"b")
	}

	return nil
}
