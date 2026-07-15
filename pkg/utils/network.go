// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-playground/validator/v10"
)

func IsValidMetric(metric int) bool {
	return metric >= 0 && metric <= 255
}

func IsValidMTU(mtu int) bool {
	return mtu >= 68 && mtu <= 65535
}

func IsValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

func IsValidIPv4(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	return parsedIP.To4() != nil
}

func IsValidIPv6(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}
	return parsedIP.To4() == nil && parsedIP.To16() != nil
}

func IsValidVLAN(vlan int) bool {
	return vlan >= 0 && vlan <= 4095
}

func IsValidPort(port int) bool {
	return port >= 1 && port <= 65535
}

func IsValidIPv4CIDR(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)

	if err != nil {
		return false
	}

	return ip.To4() != nil
}

func IsValidIPv6CIDR(cidr string) bool {
	ip, _, err := net.ParseCIDR(cidr)

	if err != nil {
		return false
	}

	return ip.To4() == nil && ip.To16() != nil
}

func IsAssignableIPv4CIDR(cidr string) bool {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	ipv4 := ip.To4()
	if ipv4 == nil {
		return false
	}

	networkIPv4 := network.IP.To4()
	if networkIPv4 == nil {
		return false
	}

	ones, bits := network.Mask.Size()
	if bits != net.IPv4len*8 {
		return false
	}

	// Reject subnet base address except /32, where the only address is assignable.
	if ones < net.IPv4len*8 && ipv4.Equal(networkIPv4) {
		return false
	}

	// Reject directed broadcast where applicable (/30 and larger networks).
	if ones <= 30 {
		broadcast := make(net.IP, net.IPv4len)
		for i := 0; i < net.IPv4len; i++ {
			broadcast[i] = networkIPv4[i] | ^network.Mask[i]
		}
		if ipv4.Equal(broadcast) {
			return false
		}
	}

	return true
}

func IsAssignableIPv6CIDR(cidr string) bool {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	ipv6 := ip.To16()
	if ipv6 == nil || ip.To4() != nil {
		return false
	}

	networkIPv6 := network.IP.To16()
	if networkIPv6 == nil {
		return false
	}

	ones, bits := network.Mask.Size()
	if bits != net.IPv6len*8 {
		return false
	}

	// Reject subnet base address except /128, where the only address is assignable.
	if ones < net.IPv6len*8 && ipv6.Equal(networkIPv6) {
		return false
	}

	return true
}

func IsAssignableCIDR(cidr string) bool {
	return IsAssignableIPv4CIDR(cidr) || IsAssignableIPv6CIDR(cidr)
}

func IsValidMAC(mac string) bool {
	_, err := net.ParseMAC(mac)
	return err == nil
}

func IsValidFQDN(fqdn string) bool {
	validator := validator.New()
	err := validator.Var(fqdn, "fqdn")
	return err == nil
}

func IsValidDUID(duid string) bool {
	duid = strings.ReplaceAll(duid, ":", "")

	if len(duid) < 4 || len(duid)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(duid)
	return err == nil
}

func BridgeIfName(name string) string {
	return ShortHash("syl" + name)
}

func IsTCPPortInUse(port int) bool {
	return isPortInUse("tcp", port)
}

func IsUDPPortInUse(port int) bool {
	return isPortInUse("udp", port)
}

func isPortInUse(protocol string, port int) bool {
	if port < 1 || port > 65535 {
		return false
	}

	tryBind := func(network, address string) error {
		if protocol == "tcp" {
			listener, err := net.Listen(network, address)
			if err == nil {
				_ = listener.Close()
			}
			return err
		}

		connection, err := net.ListenPacket(network, address)
		if err == nil {
			_ = connection.Close()
		}
		return err
	}

	portText := strconv.Itoa(port)
	ipv4Hosts := []string{"127.0.0.1"}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip != nil && ip.To4() != nil && !ip.IsUnspecified() {
				ipv4Hosts = append(ipv4Hosts, ip.String())
			}
		}
	}
	ipv4Hosts = append(ipv4Hosts, "0.0.0.0")

	for _, host := range ipv4Hosts {
		if err := tryBind(protocol+"4", net.JoinHostPort(host, portText)); err != nil {
			return true
		}
	}

	for _, host := range []string{"::1", "::"} {
		err := tryBind(protocol+"6", net.JoinHostPort(host, portText))
		if err != nil &&
			!errors.Is(err, syscall.EAFNOSUPPORT) &&
			!errors.Is(err, syscall.EPROTONOSUPPORT) &&
			!errors.Is(err, syscall.EADDRNOTAVAIL) {
			return true
		}
	}
	return false
}

func GetPortUserPID(proto string, port int) (int, error) {
	if proto != "tcp" && proto != "udp" {
		return 0, fmt.Errorf("invalid protocol: %s", proto)
	}

	if !IsValidPort(port) {
		return 0, fmt.Errorf("invalid port: %d", port)
	}

	output, err := RunCommand("sockstat", "-P", proto, "-p", strconv.Itoa(port))

	if err != nil {
		return 0, fmt.Errorf("failed to run sockstat: %w", err)
	}

	ownPID := os.Getpid()
	portStr := fmt.Sprintf(":%d", port)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if !strings.Contains(line, portStr) {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pidStr := fields[2]
		if pidStr == "??" {
			continue
		}

		pidInt, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		if pidInt == ownPID {
			continue
		}

		if len(fields) >= 6 {
			localAddr := fields[5]
			if idx := strings.LastIndex(localAddr, ":"); idx >= 0 {
				if localAddr[idx+1:] != strconv.Itoa(port) {
					continue
				}
			}
		}

		return pidInt, nil
	}

	return 0, fmt.Errorf("no process found using %s port %d", proto, port)
}

func IsValidIPPort(ipPort string) bool {
	parts := strings.Split(ipPort, ":")
	if len(parts) != 2 {
		return false
	}

	ip := parts[0]
	portStr := parts[1]
	portInt, err := strconv.Atoi(portStr)
	if err != nil {
		return false
	}

	return IsValidIP(ip) && IsValidPort(portInt)
}
