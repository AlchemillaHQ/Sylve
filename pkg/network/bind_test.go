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
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWaitForBindableIP(t *testing.T) {
	tentative := &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRNOTAVAIL)}
	assigned := func(string) bool { return true }

	t.Run("bindable now", func(t *testing.T) {
		if err := WaitForBindableIP(t.Context(), "127.0.0.1", time.Minute); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("not this host's", func(t *testing.T) {
		start := time.Now()
		err := WaitForBindableIP(t.Context(), "192.0.2.1", time.Minute)
		if !errors.Is(err, syscall.EADDRNOTAVAIL) || time.Since(start) > 5*time.Second {
			t.Fatalf("error = %v after %s; want EADDRNOTAVAIL at once", err, time.Since(start))
		}
	})

	t.Run("tentative until ready", func(t *testing.T) {
		tries := 0
		bind := func(string) error {
			if tries++; tries < 3 {
				return tentative
			}
			return nil
		}
		if err := waitForBindableIP(t.Context(), "2001:db8::10", time.Minute, bind, assigned); err != nil || tries != 3 {
			t.Fatalf("error = %v after %d tries", err, tries)
		}
	})

	t.Run("tentative past the timeout", func(t *testing.T) {
		tries := 0
		bind := func(string) error { tries++; return tentative }
		if err := waitForBindableIP(t.Context(), "2001:db8::10", 0, bind, assigned); !errors.Is(err, syscall.EADDRNOTAVAIL) || tries != 1 {
			t.Fatalf("error = %v after %d tries", err, tries)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		bind := func(string) error { return tentative }
		if err := waitForBindableIP(ctx, "2001:db8::10", time.Minute, bind, assigned); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("other errors", func(t *testing.T) {
		tries := 0
		bind := func(string) error { tries++; return syscall.EACCES }
		if err := waitForBindableIP(t.Context(), "2001:db8::10", time.Minute, bind, assigned); !errors.Is(err, syscall.EACCES) || tries != 1 {
			t.Fatalf("error = %v after %d tries", err, tries)
		}
	})
}

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

func TestTryBindToPortIPv6(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer ln.Close()

	for _, proto := range []string{"tcp", "tcp6"} {
		t.Run(proto, func(t *testing.T) {
			if err := TryBindToPort("::1", 0, proto); err != nil {
				t.Fatalf("expected IPv6 loopback bind to succeed, got %v", err)
			}
		})
	}
}
