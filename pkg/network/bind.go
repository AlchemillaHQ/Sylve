// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/alchemillahq/sylve/pkg/utils"
)

const TentativeAddressWait = 15 * time.Second

const bindableIPPoll = 250 * time.Millisecond

func WaitForBindableIP(ctx context.Context, ip string, timeout time.Duration) error {
	return waitForBindableIP(ctx, ip, timeout, bindAnyPort, utils.IsLocalIP)
}

func bindAnyPort(ip string) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return err
	}
	return listener.Close()
}

func waitForBindableIP(
	ctx context.Context,
	ip string,
	timeout time.Duration,
	bind func(string) error,
	assigned func(string) bool,
) error {
	deadline := time.Now().Add(timeout)
	for {
		err := bind(ip)
		if err == nil || !errors.Is(err, syscall.EADDRNOTAVAIL) || !assigned(ip) || !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bindableIPPoll):
		}
	}
}

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
