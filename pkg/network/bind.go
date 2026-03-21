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
	"net"
	"strconv"
)

func TryBindToPort(ip string, port int, proto string) error {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))

	switch proto {
	case "tcp", "tcp4", "tcp6":
		ln, err := net.Listen(proto, addr)
		if err != nil {
			return err
		}
		return ln.Close()

	case "udp", "udp4", "udp6":
		pc, err := net.ListenPacket(proto, addr)
		if err != nil {
			return err
		}
		return pc.Close()
	}

	return fmt.Errorf("unsupported protocol: %s", proto)
}
