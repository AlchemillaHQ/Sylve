// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package cluster

import (
	"errors"
	"fmt"
	"sync"

	"github.com/hashicorp/raft"
)

var errRaftAddressOverrideNotFound = errors.New("raft_address_override_not_found")

type raftAddressProvider struct {
	mu        sync.RWMutex
	overrides map[raft.ServerID]raft.ServerAddress
}

func newRaftAddressProvider() *raftAddressProvider {
	return &raftAddressProvider{overrides: make(map[raft.ServerID]raft.ServerAddress)}
}

func (p *raftAddressProvider) ServerAddr(id raft.ServerID) (raft.ServerAddress, error) {
	p.mu.RLock()
	address, ok := p.overrides[id]
	p.mu.RUnlock()
	if !ok {
		return "", errRaftAddressOverrideNotFound
	}
	return address, nil
}

func (p *raftAddressProvider) set(id raft.ServerID, address raft.ServerAddress) {
	p.mu.Lock()
	p.overrides[id] = address
	p.mu.Unlock()
}

func (p *raftAddressProvider) clear(id raft.ServerID) {
	p.mu.Lock()
	delete(p.overrides, id)
	p.mu.Unlock()
}

func (s *Service) installRaftAddressOverride(
	id raft.ServerID,
	address raft.ServerAddress,
	allowDisruption bool,
) error {
	if !allowDisruption {
		return fmt.Errorf("cluster_readdress_disruption_acknowledgement_required")
	}
	if s.addressProvider == nil {
		s.addressProvider = newRaftAddressProvider()
	}
	s.addressProvider.set(id, address)
	if s.Transport != nil {
		s.Transport.CloseStreams()
	}
	return nil
}

func (s *Service) clearRaftAddressOverride(id raft.ServerID) {
	if s.addressProvider != nil {
		s.addressProvider.clear(id)
	}
	if s.Transport != nil {
		s.Transport.CloseStreams()
	}
}
