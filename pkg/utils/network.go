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
	"fmt"
	"net"
	"strconv"
	"strings"

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

func IsPortInUse(port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	addr := fmt.Sprintf(":%d", port)

	tcpLn, tcpErr := net.Listen("tcp", addr)
	if tcpErr != nil {
		return false
	} else {
		tcpLn.Close()
	}

	udpAddr, udpResErr := net.ResolveUDPAddr("udp", addr)
	if udpResErr != nil {
		return false
	}

	udpConn, udpErr := net.ListenUDP("udp", udpAddr)
	if udpErr != nil {
		return false
	} else {
		udpConn.Close()
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

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, fmt.Sprintf(":%d", port)) {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				pid := fields[2]

				pidInt, err := strconv.Atoi(pid)
				if err != nil {
					return 0, fmt.Errorf("failed to convert PID to integer: %w", err)
				}

				return pidInt, nil
			}
		}
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
