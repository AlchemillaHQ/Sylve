// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package nfs

import (
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alchemillahq/gzfs"
	nfsModels "github.com/alchemillahq/sylve/internal/db/models/nfs"
	"github.com/alchemillahq/sylve/internal/sharerules"
)

const (
	MaxRequestBodyBytes int64 = 64 * 1024
	maxShares                 = 256
	maxClients                = 64
)

var (
	ErrInvalidRequest = errors.New("invalid_nfs_configuration")
	ErrConflict       = errors.New("nfs_configuration_conflict")
	ErrNotFound       = errors.New("nfs_share_not_found")
)

type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Block   string `json:"block,omitempty"`
	Message string `json:"message"`
	kind    error
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return e.kind }

func invalid(field, code, message string) error {
	return &ValidationError{Field: field, Code: code, Message: message, kind: ErrInvalidRequest}
}

func conflict(field, code, message string) error {
	return &ValidationError{Field: field, Code: code, Message: message, kind: ErrConflict}
}

var notShareableMessages = map[string]string{
	sharerules.BlockRoot:              "The root file system cannot be exported.",
	sharerules.BlockSystemPath:        "Datasets mounted on system paths cannot be exported.",
	sharerules.BlockBootEnvironment:   "Boot environments cannot be exported.",
	sharerules.BlockSylveData:         "Sylve-managed datasets cannot be exported.",
	sharerules.BlockContainsProtected: "Datasets containing Sylve data or a boot environment cannot be exported.",
}

func notShareable(block string) error {
	return &ValidationError{Field: "dataset", Code: "dataset_not_shareable", Block: block, Message: notShareableMessages[block], kind: ErrConflict}
}

func validGUID(guid string) bool {
	value, err := strconv.ParseUint(guid, 10, 64)
	return err == nil && value != 0 && strconv.FormatUint(value, 10) == guid
}

func normalizeConfig(config nfsModels.NFSConfig) (nfsModels.NFSConfig, error) {
	switch config.Protocol {
	case "v3", "v4", "v3v4":
	default:
		return config, invalid("protocol", "invalid_protocol", "Select NFSv3, NFSv4, or both.")
	}
	if config.V4MaxVersion == "" {
		config.V4MaxVersion = "4.2"
	}
	if !slices.Contains([]string{"4.0", "4.1", "4.2"}, config.V4MaxVersion) {
		return config, invalid("v4MaxVersion", "invalid_v4_max_version", "Select NFSv4.0, NFSv4.1 or NFSv4.2.")
	}
	if config.UDP && config.Protocol == "v4" {
		return config, invalid("udp", "udp_requires_nfsv3", "UDP is only available with NFSv3.")
	}
	if (config.MinThreads != 0 || config.MaxThreads != 0) && (config.MinThreads == 0 || config.MaxThreads < config.MinThreads || config.MaxThreads > 256) {
		return config, invalid("minThreads", "invalid_thread_range", "Set both thread bounds between 1 and 256, or use 0 for automatic.")
	}
	if len(config.BindAddresses) > 32 {
		return config, invalid("bindAddresses", "too_many_bind_addresses", "Select at most 32 local addresses.")
	}
	addresses := make([]string, 0, len(config.BindAddresses))
	for _, value := range config.BindAddresses {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil || address.Zone() != "" || address.Is4In6() || (!address.IsGlobalUnicast() && !address.IsLoopback()) || address.IsLinkLocalUnicast() {
			return config, invalid("bindAddresses", "invalid_bind_address", "Use unicast IPv4/IPv6 addresses without scope zones.")
		}
		addresses = append(addresses, address.String())
	}
	slices.Sort(addresses)
	config.BindAddresses = slices.Compact(addresses)
	config.KerberosPrincipal = strings.TrimSpace(config.KerberosPrincipal)
	if config.KerberosPrincipal != "" && !nfsPrincipal.MatchString(config.KerberosPrincipal) {
		return config, invalid("kerberosPrincipal", "invalid_kerberos_principal", "Use an NFS service principal: nfs/hostname@REALM.")
	}
	return config, nil
}

func clientPrefix(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Is4In6() || prefix.Addr().IsMulticast() {
			return netip.Prefix{}, invalid("clients", "invalid_client", "Use IPv4/IPv6 addresses or CIDRs. Hostnames and scoped addresses are not supported yet.")
		}
		if prefix.Bits() == 0 {
			return netip.Prefix{}, invalid("clients", "client_matches_everyone", "Use IPv4/IPv6 addresses or CIDRs; use Everyone explicitly instead of a /0 network.")
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil || addr.Zone() != "" || addr.Is4In6() || addr.IsUnspecified() || addr.IsMulticast() {
		return netip.Prefix{}, invalid("clients", "invalid_client", "Use IPv4/IPv6 addresses or CIDRs. Hostnames and scoped addresses are not supported yet.")
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func normalizeShare(input nfsModels.NFSShareConfig) (nfsModels.NFSShareConfig, error) {
	if !validGUID(input.Dataset) {
		return input, invalid("dataset", "invalid_dataset", "Select a ZFS filesystem by its GUID.")
	}
	input.Description = strings.TrimSpace(strings.ReplaceAll(input.Description, "\r\n", "\n"))
	if !utf8.ValidString(input.Description) || len(input.Description) > 256 || strings.IndexFunc(input.Description, func(value rune) bool { return value != '\n' && unicode.IsControl(value) }) >= 0 {
		return input, invalid("description", "invalid_description", "Description must be at most 256 bytes and cannot contain control characters other than line breaks.")
	}
	if len(input.Clients) > maxClients {
		return input, invalid("clients", "too_many_clients", "A share supports at most 64 client addresses or networks.")
	}
	if input.Everyone && len(input.Clients) != 0 {
		return input, invalid("clients", "everyone_with_clients", "Everyone cannot be combined with restricted clients.")
	}
	if !input.Everyone && len(input.Clients) == 0 {
		return input, invalid("clients", "clients_required", "Specify at least one client, or explicitly select Everyone.")
	}
	clients := make([]string, 0, len(input.Clients))
	prefixes := make([]netip.Prefix, 0, len(input.Clients))
	for _, value := range input.Clients {
		if len(value) > 64 {
			return input, invalid("clients", "client_too_long", "Client addresses and CIDRs must be at most 64 bytes.")
		}
		value = strings.TrimSpace(value)
		prefix, err := clientPrefix(value)
		if err != nil {
			return input, err
		}
		prefixes = append(prefixes, prefix)
		if strings.Contains(value, "/") {
			clients = append(clients, prefix.String())
		} else {
			clients = append(clients, prefix.Addr().String())
		}
	}
	if prefixesOverlap(prefixes) {
		return input, conflict("clients", "clients_overlap", "Client addresses or networks overlap within this share.")
	}
	slices.Sort(clients)
	input.Clients = clients
	if input.Security == nil {
		input.Security = []string{"sys"}
	}
	if len(input.Security) == 0 {
		return input, invalid("security", "security_required", "Select at least one security flavor.")
	}
	if len(input.Security) > 4 {
		return input, invalid("security", "invalid_security", "Select SYS, KRB5, KRB5I or KRB5P.")
	}
	flavors := make([]string, 0, len(input.Security))
	for _, flavor := range input.Security {
		flavor = strings.ToLower(strings.TrimSpace(flavor))
		if !slices.Contains(securityFlavors, flavor) {
			return input, invalid("security", "invalid_security", "Select SYS, KRB5, KRB5I or KRB5P.")
		}
		flavors = append(flavors, flavor)
	}
	slices.SortFunc(flavors, func(a, b string) int { return slices.Index(securityFlavors, a) - slices.Index(securityFlavors, b) })
	input.Security = slices.Compact(flavors)
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"maprootUser", &input.MaprootUser}, {"maprootGroup", &input.MaprootGroup},
		{"mapallUser", &input.MapallUser}, {"mapallGroup", &input.MapallGroup},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if *field.value != "" && (!utf8.ValidString(*field.value) || len(*field.value) > 512 || strings.HasPrefix(*field.value, "-") || strings.ContainsAny(*field.value, ":\"[]%/") || strings.IndexFunc(*field.value, unicode.IsControl) >= 0) {
			return input, invalid(field.name, "invalid_account_name", "Use a valid local or qualified directory account name.")
		}
	}
	if input.MaprootGroup != "" && input.MaprootUser == "" {
		return input, invalid("maprootGroup", "mapping_group_without_user", "A Maproot user is required with a group.")
	}
	if input.MapallGroup != "" && input.MapallUser == "" {
		return input, invalid("mapallGroup", "mapping_group_without_user", "A Mapall user is required with a group.")
	}
	if input.MaprootUser != "" && input.MapallUser != "" {
		return input, invalid("mapallUser", "maproot_with_mapall", "Maproot and Mapall cannot be combined.")
	}
	return input, nil
}

func validateClientConflicts(shares []nfsModels.NFSShare) error {
	prefixes := make(map[string][]netip.Prefix)
	everyone := make(map[string]bool)
	for _, share := range shares {
		if !share.Enabled {
			continue
		}
		if everyone[share.Dataset] || share.Everyone && len(prefixes[share.Dataset]) > 0 {
			return conflict("clients", "everyone_rule_overlap", "An Everyone rule cannot share a filesystem with another enabled rule.")
		}
		if share.Everyone {
			everyone[share.Dataset] = true
			continue
		}
		for _, client := range share.Clients {
			prefix, err := clientPrefix(client)
			if err != nil {
				return err
			}
			prefixes[share.Dataset] = append(prefixes[share.Dataset], prefix)
		}
	}
	for _, clients := range prefixes {
		if prefixesOverlap(clients) {
			return conflict("clients", "client_rules_overlap", "Enabled shares on the same filesystem must have disjoint client restrictions.")
		}
	}
	return nil
}

func prefixesOverlap(prefixes []netip.Prefix) bool {
	// Masked CIDRs are intervals: sorting by start (widest first on ties)
	// makes any overlap visible between adjacent intervals, in O(n log n).
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if order := a.Addr().Compare(b.Addr()); order != 0 {
			return order
		}
		return a.Bits() - b.Bits()
	})
	for i := 1; i < len(prefixes); i++ {
		if prefixes[i-1].Overlaps(prefixes[i]) {
			return true
		}
	}
	return false
}

func validateExportPath(path string) error {
	if len(path) > 1023 || !utf8.ValidString(path) || !filepath.IsAbs(path) || path == "/" || filepath.Clean(path) != path || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return conflict("dataset", "invalid_mountpoint", "The dataset must have a canonical, non-root absolute mountpoint without control characters.")
	}
	return nil
}

func datasetEligible(dataset *gzfs.Dataset, pools []string) error {
	if dataset == nil {
		return conflict("dataset", "dataset_missing", "The selected dataset is unavailable.")
	}
	if dataset.Type != gzfs.DatasetTypeFilesystem || !slices.Contains(pools, dataset.Pool) {
		return conflict("dataset", "dataset_not_in_usable_pool", "Select a filesystem in one of Sylve's usable pools.")
	}
	if block := sharerules.DatasetBlock(dataset, nil); block == sharerules.BlockBootEnvironment || block == sharerules.BlockSylveData {
		return notShareable(block)
	}
	if err := validateExportPath(dataset.Mountpoint); err != nil {
		return err
	}
	if !dataset.IsMounted() || dataset.Properties["keystatus"].Value == "unavailable" {
		return conflict("dataset", "dataset_not_mounted", "The selected filesystem must be unlocked and mounted.")
	}
	if dataset.Properties["sharenfs"].Value != "off" {
		return conflict("dataset", "dataset_sharenfs_set", "The filesystem's effective sharenfs property must be off; NFS exports are managed by Sylve.")
	}
	return nil
}
