// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package jail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	jailModels "github.com/alchemillahq/sylve/internal/db/models/jail"
	jailServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/jail"
)

const (
	jailGuestInfoTimeout  = 4 * time.Second
	jailGuestOutputLimit  = 256 << 10
	jailGuestVersionLimit = 64 << 10
)

var jailGuestVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+-[A-Za-z0-9_.-]+$`)

type jailGuestRuntime struct {
	JID  json.Number `json:"jid"`
	Name string      `json:"name"`
	Path string      `json:"path"`
	VNET string      `json:"vnet"`
	IPv4 string      `json:"ip4"`
	IPv6 string      `json:"ip6"`
}

func (s *Service) GetJailGuestInfo(ctx context.Context, ctID uint) (jailServiceInterfaces.GuestInfo, error) {
	unavailable := jailServiceInterfaces.GuestInfo{Interfaces: []jailServiceInterfaces.GuestNetworkInterface{}}
	if ctID == 0 {
		return unavailable, fmt.Errorf("invalid_ct_id")
	}
	if s == nil || s.DB == nil {
		return unavailable, fmt.Errorf("db_not_initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, jailGuestInfoTimeout)
	defer cancel()

	var jail jailModels.Jail
	if err := s.DB.WithContext(ctx).Preload("Storages").First(&jail, "ct_id = ?", ctID).Error; err != nil {
		return unavailable, fmt.Errorf("failed_to_fetch_jail_by_ctid: %w", err)
	}
	if jail.Type != jailModels.JailTypeFreeBSD {
		return unavailable, nil
	}

	info, err := s.collectJailGuestInfo(ctx, &jail)
	if err != nil {
		// Guest inspection is optional: stopped jails and missing tools do not
		// turn the summary page into an error state.
		return unavailable, nil
	}
	return info, nil
}

func (s *Service) collectJailGuestInfo(ctx context.Context, jail *jailModels.Jail) (jailServiceInterfaces.GuestInfo, error) {
	var info jailServiceInterfaces.GuestInfo
	name := s.GetCTIDHash(jail.CTID)
	runtime, err := s.readJailGuestRuntime(ctx, name)
	if err != nil {
		return info, err
	}
	root, err := s.resolveJailRoot(ctx, jail)
	if err != nil {
		return info, err
	}
	if filepath.Clean(runtime.Path) != filepath.Clean(root) {
		return info, fmt.Errorf("jail_guest_root_mismatch")
	}
	version, err := readJailGuestVersion(root)
	if err != nil {
		return info, err
	}
	kernel, err := s.runJailGuestCommand(ctx, "/usr/bin/uname", "-rm")
	if err != nil {
		return info, err
	}
	fields := strings.Fields(string(kernel))
	if len(fields) != 2 {
		return info, fmt.Errorf("invalid_host_kernel_info")
	}
	info.OSInfo = &jailServiceInterfaces.GuestOSInfo{
		Name: "FreeBSD", Version: version, KernelRelease: fields[0], Architecture: fields[1],
	}
	info.Interfaces = []jailServiceInterfaces.GuestNetworkInterface{}
	info.NetworkMode = "shared"
	if runtime.VNET == "new" {
		info.NetworkMode = "vnet"
	} else if runtime.IPv4 == "disable" && runtime.IPv6 == "disable" {
		info.NetworkMode = "disabled"
	}
	if info.NetworkMode != "disabled" {
		output, err := s.runJailGuestCommand(ctx, "/usr/bin/netstat", "-j", runtime.JID.String(), "-ibdn", "--libxo", "json")
		if err != nil {
			return info, err
		}
		info.Interfaces, err = parseJailGuestInterfaces(output, runtime)
		if err != nil {
			return info, err
		}
	}

	// Do not publish a snapshot collected across a stop/restart or migration.
	current, err := s.readJailGuestRuntime(ctx, name)
	if err != nil || current != runtime {
		return info, fmt.Errorf("jail_guest_runtime_changed")
	}
	info.Available = true
	return info, nil
}

func (s *Service) readJailGuestRuntime(ctx context.Context, name string) (jailGuestRuntime, error) {
	var runtime jailGuestRuntime
	output, err := s.runJailGuestCommand(ctx, "/usr/sbin/jls", "--libxo", "json", "-j", name, "jid", "name", "path", "vnet", "ip4", "ip6")
	if err != nil {
		return runtime, err
	}
	var listing struct {
		Information struct {
			Jails []jailGuestRuntime `json:"jail"`
		} `json:"jail-information"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return runtime, err
	}
	if len(listing.Information.Jails) != 1 {
		return runtime, fmt.Errorf("jail_guest_runtime_unavailable")
	}
	runtime = listing.Information.Jails[0]
	jid, err := runtime.JID.Int64()
	if err != nil || jid <= 0 || runtime.Name != name || !filepath.IsAbs(runtime.Path) ||
		(runtime.VNET != "new" && runtime.VNET != "inherit") {
		return jailGuestRuntime{}, fmt.Errorf("invalid_jail_guest_runtime")
	}
	for _, mode := range []string{runtime.IPv4, runtime.IPv6} {
		if mode != "new" && mode != "inherit" && mode != "disable" {
			return jailGuestRuntime{}, fmt.Errorf("invalid_jail_guest_network_mode")
		}
	}
	return runtime, nil
}

func readJailGuestVersion(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// Read the literal embedded by FreeBSD's build rather than executing any
	// guest-controlled script. Nonblocking open also rejects FIFOs safely.
	file, err := root.OpenFile("bin/freebsd-version", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > jailGuestVersionLimit {
		return "", fmt.Errorf("invalid_jail_guest_version_file")
	}
	data, err := io.ReadAll(io.LimitReader(file, jailGuestVersionLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > jailGuestVersionLimit {
		return "", fmt.Errorf("jail_guest_version_file_too_large")
	}
	var version string
	for _, line := range strings.Split(string(data), "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), "USERLAND_VERSION=")
		if !found {
			continue
		}
		if version != "" || len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
			return "", fmt.Errorf("invalid_jail_guest_version")
		}
		version = value[1 : len(value)-1]
		if !jailGuestVersionPattern.MatchString(version) {
			return "", fmt.Errorf("invalid_jail_guest_version")
		}
	}
	if version == "" {
		return "", fmt.Errorf("jail_guest_version_unavailable")
	}
	return version, nil
}

func parseJailGuestInterfaces(output []byte, runtime jailGuestRuntime) ([]jailServiceInterfaces.GuestNetworkInterface, error) {
	var listing struct {
		Statistics *struct {
			Interfaces []struct {
				Name    string  `json:"name"`
				Network string  `json:"network"`
				Address string  `json:"address"`
				RXBytes *uint64 `json:"received-bytes"`
				TXBytes *uint64 `json:"sent-bytes"`
			} `json:"interface"`
		} `json:"statistics"`
	}
	if err := json.Unmarshal(output, &listing); err != nil {
		return nil, err
	}
	if listing.Statistics == nil || listing.Statistics.Interfaces == nil {
		return nil, fmt.Errorf("jail_guest_interfaces_unavailable")
	}
	interfaces := make([]jailServiceInterfaces.GuestNetworkInterface, 0)
	indices := make(map[string]int)
	for _, row := range listing.Statistics.Interfaces {
		if row.Name == "" {
			return nil, fmt.Errorf("invalid_jail_guest_interface")
		}
		index, found := indices[row.Name]
		if !found {
			index = len(interfaces)
			indices[row.Name] = index
			interfaces = append(interfaces, jailServiceInterfaces.GuestNetworkInterface{
				Name: row.Name, IPAddresses: []jailServiceInterfaces.GuestIPAddress{},
			})
		}
		iface := &interfaces[index]
		if strings.HasPrefix(row.Network, "<Link#") {
			if mac, err := net.ParseMAC(row.Address); err == nil {
				iface.HardwareAddress = mac.String()
			}
			// Only link rows contain interface totals; address rows must not
			// be summed. Shared host interfaces have no jail-specific counters.
			if runtime.VNET == "new" && row.RXBytes != nil && row.TXBytes != nil {
				iface.Statistics = &jailServiceInterfaces.GuestNetworkStatistics{RXBytes: *row.RXBytes, TXBytes: *row.TXBytes}
			}
			continue
		}
		address, _, _ := strings.Cut(row.Address, "%")
		ip := net.ParseIP(address)
		if ip == nil {
			continue
		}
		ipType, bits, mode := "ipv6", 128, runtime.IPv6
		if ip.To4() != nil {
			ipType, bits, mode = "ipv4", 32, runtime.IPv4
		}
		if mode == "disable" {
			continue
		}
		slash := strings.LastIndexByte(row.Network, '/')
		if slash < 0 {
			return nil, fmt.Errorf("jail_guest_address_prefix_unavailable")
		}
		prefix, err := strconv.Atoi(row.Network[slash+1:])
		if err != nil || prefix < 0 || prefix > bits {
			return nil, fmt.Errorf("invalid_jail_guest_address_prefix")
		}
		entry := jailServiceInterfaces.GuestIPAddress{Type: ipType, Address: row.Address, Prefix: prefix}
		if !slices.Contains(iface.IPAddresses, entry) {
			iface.IPAddresses = append(iface.IPAddresses, entry)
		}
	}
	return interfaces, nil
}

type jailGuestOutput struct {
	buffer bytes.Buffer
}

func (b *jailGuestOutput) Write(data []byte) (int, error) {
	if len(data) > jailGuestOutputLimit-b.buffer.Len() {
		return 0, fmt.Errorf("jail_guest_command_output_too_large")
	}
	return b.buffer.Write(data)
}

func (s *Service) runJailGuestCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	if s.guestInfoRunFn != nil {
		return s.guestInfoRunFn(ctx, command, args...)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = []string{"PATH=/sbin:/bin:/usr/sbin:/usr/bin", "LC_ALL=C"}
	var output jailGuestOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.buffer.Bytes(), nil
}
