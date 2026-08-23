// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package certificates

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/alchemillahq/sylve/internal/services/dynamicdns"
)

func (s *Service) CheckDomain(ctx context.Context, rawDomain string) (*DomainCheckResult, error) {
	domain, err := normalizeDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(domain) != nil {
		return nil, invalidCertificate("domain check requires a DNS hostname")
	}
	if strings.HasPrefix(domain, "*.") {
		return nil, invalidCertificate("domain check requires a non-wildcard hostname")
	}

	resolver := s.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	resolvedAddresses, err := resolver.LookupNetIP(ctx, "ip", domain)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %s: %v", ErrDomainCheckFailed, domain, err)
	}
	resolved := makeAddressSet(resolvedAddresses)
	if len(resolved) == 0 {
		return nil, fmt.Errorf("%w: %s does not resolve to an IP address", ErrDomainCheckFailed, domain)
	}

	public := make(map[netip.Addr]struct{})
	stunResolver := s.stunResolver
	if stunResolver == nil {
		stunResolver = dynamicdns.NewSTUNResolver()
	}
	addresses, _ := stunResolver.Resolve(ctx, map[string]string{
		dynamicdns.SourceSettingSTUNServer: dynamicdns.DefaultSTUNServer,
	})
	if addresses.IPv4.IsValid() {
		public[addresses.IPv4.Unmap()] = struct{}{}
	}
	if addresses.IPv6.IsValid() {
		public[addresses.IPv6] = struct{}{}
	}

	interfaceAddrs := s.interfaceAddrs
	if interfaceAddrs == nil {
		interfaceAddrs = net.InterfaceAddrs
	}
	if localAddresses, localErr := interfaceAddrs(); localErr == nil {
		for _, localAddress := range localAddresses {
			prefix, parseErr := netip.ParsePrefix(localAddress.String())
			if parseErr != nil {
				continue
			}
			address := prefix.Addr().Unmap()
			if address.IsGlobalUnicast() && !address.IsPrivate() {
				public[address] = struct{}{}
			}
		}
	}

	result := &DomainCheckResult{
		Domain:          domain,
		Resolved:        sortedAddressStrings(resolved),
		PublicAddresses: sortedAddressStrings(public),
	}
	for address := range resolved {
		if _, matches := public[address.Unmap()]; matches {
			result.Matches = true
			break
		}
	}
	if len(public) == 0 {
		result.Warning = "Sylve could not discover a public address for this node."
	} else if !result.Matches {
		result.Warning = "The hostname does not resolve to a public address detected on this node."
	} else {
		result.Warning = "Address matching cannot verify firewall rules or TCP port 443 forwarding."
	}
	return result, nil
}

func makeAddressSet(addresses []netip.Addr) map[netip.Addr]struct{} {
	set := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if address.IsValid() {
			set[address.Unmap()] = struct{}{}
		}
	}
	return set
}

func sortedAddressStrings(addresses map[netip.Addr]struct{}) []string {
	values := make([]string, 0, len(addresses))
	for address := range addresses {
		values = append(values, address.String())
	}
	sort.Strings(values)
	return values
}
