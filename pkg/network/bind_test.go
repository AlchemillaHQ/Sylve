// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"net"
	"testing"
)

func TestTryBindToPort(t *testing.T) {
	t.Run("success tcp bind", func(t *testing.T) {
		err := TryBindToPort("127.0.0.1", 0, "tcp")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("success tcp4 bind", func(t *testing.T) {
		err := TryBindToPort("127.0.0.1", 0, "tcp4")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("success udp bind", func(t *testing.T) {
		err := TryBindToPort("127.0.0.1", 0, "udp")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("success udp4 bind", func(t *testing.T) {
		err := TryBindToPort("127.0.0.1", 0, "udp4")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("invalid protocol", func(t *testing.T) {
		err := TryBindToPort("127.0.0.1", 0, "invalid-proto")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("tcp port already in use", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to reserve tcp port: %v", err)
		}
		defer ln.Close()

		port := ln.Addr().(*net.TCPAddr).Port

		err = TryBindToPort("127.0.0.1", port, "tcp")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("udp port already in use", func(t *testing.T) {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to reserve udp port: %v", err)
		}
		defer pc.Close()

		port := pc.LocalAddr().(*net.UDPAddr).Port

		err = TryBindToPort("127.0.0.1", port, "udp")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
