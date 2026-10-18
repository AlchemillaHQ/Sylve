// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type liveNetwork struct {
	run          func(time.Duration, string, ...string) (string, error)
	interfaces   func() ([]liveInterface, error)
	resolverPath string
	supported    bool
}

type liveInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
	Up        bool     `json:"up"`
}

type liveNetworkState struct {
	Interfaces       []liveInterface `json:"interfaces"`
	Gateway          string          `json:"gateway"`
	DefaultInterface string          `json:"defaultInterface"`
	DNS              []string        `json:"dns"`
}

type liveNetworkRequest struct {
	Interface string   `json:"interface"`
	Address   string   `json:"address"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
}

func newLiveNetwork() *liveNetwork {
	return &liveNetwork{
		run: runCmd, interfaces: liveInterfaces,
		resolverPath: "/etc/resolv.conf", supported: runtime.GOOS == "freebsd",
	}
}

func liveInterfaces() ([]liveInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := []liveInterface{}
	for _, iface := range ifaces {
		if iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 || len(iface.HardwareAddr) == 0 {
			continue
		}
		entry := liveInterface{Name: iface.Name, MAC: iface.HardwareAddr.String(),
			Up: iface.Flags&net.FlagUp != 0, Addresses: []string{}}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil && prefix.Addr().Is4() {
				entry.Addresses = append(entry.Addresses, prefix.String())
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func routeField(output, key string) string {
	for _, line := range strings.Split(output, "\n") {
		if before, after, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && before == key {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

func (n *liveNetwork) state() (liveNetworkState, error) {
	ifaces, err := n.interfaces()
	if err != nil {
		return liveNetworkState{}, err
	}
	state := liveNetworkState{Interfaces: ifaces, DNS: []string{}}
	if n.supported {
		out, err := n.run(3*time.Second, "/sbin/route", "-n", "get", "-inet", "default")
		if err == nil {
			state.Gateway = routeField(out, "gateway")
			state.DefaultInterface = routeField(out, "interface")
		}
	}
	resolver, err := os.ReadFile(n.resolverPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return liveNetworkState{}, err
	}
	for _, line := range strings.Split(string(resolver), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			state.DNS = append(state.DNS, fields[1])
		}
	}
	return state, nil
}

func validateLiveNetwork(req liveNetworkRequest, ifaces []liveInterface) error {
	found := false
	for _, iface := range ifaces {
		if iface.Name == req.Interface {
			found = true
		}
	}
	if !found || strings.HasPrefix(req.Interface, "-") || strings.ContainsAny(req.Interface, " \t\r\n/;") {
		return fmt.Errorf("select an available Ethernet interface")
	}
	prefix, err := netip.ParsePrefix(req.Address)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 || !usableLiveIP(prefix.Addr()) {
		return fmt.Errorf("address must be an IPv4 CIDR with a prefix from /1 to /30")
	}
	if subnetEndpoint(prefix, prefix.Addr()) {
		return fmt.Errorf("address must not be the subnet's network or broadcast address")
	}
	gateway, err := netip.ParseAddr(req.Gateway)
	if err != nil || !gateway.Is4() || !usableLiveIP(gateway) || !prefix.Contains(gateway) ||
		gateway == prefix.Addr() || subnetEndpoint(prefix, gateway) {
		return fmt.Errorf("gateway must be a different usable IPv4 address in the same subnet")
	}
	if len(req.DNS) < 1 || len(req.DNS) > 3 {
		return fmt.Errorf("provide one to three IPv4 DNS servers")
	}
	for _, server := range req.DNS {
		addr, err := netip.ParseAddr(server)
		if err != nil || !addr.Is4() || !usableLiveIP(addr) {
			return fmt.Errorf("DNS servers must be usable IPv4 addresses")
		}
	}
	return nil
}

func usableLiveIP(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()
}

func subnetEndpoint(prefix netip.Prefix, addr netip.Addr) bool {
	network := prefix.Masked().Addr().As4()
	ip := addr.As4()
	hostBits := uint(32 - prefix.Bits())
	toUint := func(b [4]byte) uint32 {
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	n := toUint(network)
	return toUint(ip) == n || toUint(ip) == n|uint32((uint64(1)<<hostBits)-1)
}

func resolverSeed(old []byte, dns []string) []byte {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "nameserver" {
			lines = append(lines, line)
		}
	}
	for _, server := range dns {
		lines = append(lines, "nameserver "+server)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func writeResolver(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sylve-resolver-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (n *liveNetwork) apply(req liveNetworkRequest, state liveNetworkState) (retErr error) {
	oldDNS, err := os.ReadFile(n.resolverPath)
	dnsExisted := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var old liveInterface
	for _, iface := range state.Interfaces {
		if iface.Name == req.Interface {
			old = iface
		}
	}
	command := func(name string, args ...string) error {
		out, err := n.run(5*time.Second, name, args...)
		if err != nil {
			return fmt.Errorf("%s: %w (%s)", filepath.Base(name), err, firstLine(out))
		}
		return nil
	}
	setGateway := func(gateway string) error {
		verb := "add"
		if out, err := n.run(3*time.Second, "/sbin/route", "-n", "get", "-inet", "default"); err == nil && routeField(out, "gateway") != "" {
			verb = "change"
		}
		return command("/sbin/route", "-n", verb, "-inet", "default", gateway)
	}
	_, dhcpErr := n.run(3*time.Second, "/usr/sbin/service", "dhclient", "onestatus", req.Interface)
	stopped, addressSet, routeSet, dnsSet := false, false, false, false
	defer func() {
		if retErr == nil {
			return
		}
		var rollback []error
		if addressSet || stopped {
			newIP := strings.SplitN(req.Address, "/", 2)[0]
			wasPresent := false
			for _, cidr := range old.Addresses {
				wasPresent = wasPresent || strings.SplitN(cidr, "/", 2)[0] == newIP
				rollback = append(rollback, command("/sbin/ifconfig", old.Name, "inet", cidr, "alias"))
			}
			if addressSet && !wasPresent {
				rollback = append(rollback, command("/sbin/ifconfig", old.Name, "inet", newIP, "-alias"))
			}
			if !old.Up {
				rollback = append(rollback, command("/sbin/ifconfig", old.Name, "down"))
			}
		}
		if routeSet {
			if state.Gateway == "" {
				rollback = append(rollback, command("/sbin/route", "-n", "delete", "-inet", "default", req.Gateway))
			} else {
				rollback = append(rollback, setGateway(state.Gateway))
			}
		}
		if dnsSet {
			if dnsExisted {
				rollback = append(rollback, writeResolver(n.resolverPath, oldDNS))
			} else {
				rollback = append(rollback, os.Remove(n.resolverPath))
			}
		}
		if stopped {
			rollback = append(rollback, command("/usr/sbin/service", "dhclient", "onestart", req.Interface))
		}
		if err := errors.Join(rollback...); err != nil {
			retErr = fmt.Errorf("%w; rollback incomplete: %v", retErr, err)
		}
	}()
	if dhcpErr == nil {
		if err := command("/usr/sbin/service", "dhclient", "onestop", req.Interface); err != nil {
			return err
		}
		stopped = true
	}
	if err := command("/sbin/ifconfig", req.Interface, "inet", req.Address, "alias", "up"); err != nil {
		return err
	}
	addressSet = true
	if err := setGateway(req.Gateway); err != nil {
		return err
	}
	routeSet = true
	if err := writeResolver(n.resolverPath, resolverSeed(oldDNS, req.DNS)); err != nil {
		return err
	}
	dnsSet = true
	newIP := strings.SplitN(req.Address, "/", 2)[0]
	for _, cidr := range old.Addresses {
		ip := strings.SplitN(cidr, "/", 2)[0]
		if ip != newIP {
			if err := command("/sbin/ifconfig", req.Interface, "inet", ip, "-alias"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (n *liveNetwork) get(w http.ResponseWriter, _ *http.Request) {
	state, err := n.state()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (i *installer) liveNetworkPost(n *liveNetwork) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin network changes are not allowed"})
			return
		}
		var req liveNetworkRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid network settings"})
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one JSON document"})
			return
		}
		if !n.supported {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "live network changes require FreeBSD"})
			return
		}
		i.mu.Lock()
		defer i.mu.Unlock()
		if i.en != nil && i.en.Snapshot().Running {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "an install is running"})
			return
		}
		state, err := n.state()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if err := validateLiveNetwork(req, state.Interfaces); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := n.apply(req, state); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "url": "http://" + strings.SplitN(req.Address, "/", 2)[0] + ":8080"})
	}
}
