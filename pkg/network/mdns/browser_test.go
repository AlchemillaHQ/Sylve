// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dnssd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/miekg/dns"
)

func TestBrowse(t *testing.T) {
	iface, err := loopbackInterface()
	if err != nil {
		t.Fatal(err)
	}

	localhost, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	localhost = strings.TrimSuffix(strings.Replace(localhost, " ", "-", -1), ".local") // replace spaces with dashes and remove .local suffix
	tests := []struct {
		name        string
		serviceName string
		serviceType string
		host        string
	}{
		{
			name:        "regular host",
			serviceName: "My Regular Service",
			serviceType: "_test-regular._tcp",
			host:        "My-Computer",
		},
		{
			name:        "empty host",
			serviceName: "My Empty Host Service",
			serviceType: "_test-empty._tcp",
			host:        "",
		},
		{
			name:        "ip address",
			serviceName: "My IP Service",
			serviceType: "_test-ip._tcp",
			host:        "192.168.0.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := Config{
					Name:   test.serviceName,
					Type:   test.serviceType,
					Host:   test.host,
					Port:   12334,
					Ifaces: []string{iface.Name},
				}
				srv, err := NewService(cfg)
				if err != nil {
					t.Fatal(err)
				}
				srv.ifaceIPs = map[string][]net.IP{
					iface.Name: {net.IPv4(192, 0, 2, 10)},
				}

				responderConn := newTestConn()
				browserConn := newTestConn()
				responderConn.iface = iface
				browserConn.iface = iface
				connectTestConns(responderConn, browserConn)

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				responder := newResponder(responderConn)
				responder.addManaged(srv)
				ready := make(chan error, 1)
				respondDone := make(chan error, 1)
				go func() {
					respondDone <- responder.RespondReady(ctx, ready)
				}()
				if err := <-ready; err != nil {
					t.Fatal(err)
				}

				resultChan := make(chan BrowseEntry, 1)
				lookupDone := make(chan error, 1)
				go func() {
					lookupDone <- lookupType(
						ctx,
						fmt.Sprintf("%s.local.", cfg.Type),
						browserConn,
						func(entry BrowseEntry) {
							select {
							case resultChan <- entry:
							default:
							}
						},
						func(BrowseEntry) {},
						iface.Name,
					)
				}()

				entry := <-resultChan
				if entry.Name != cfg.Name {
					t.Fatalf("is=%v want=%v", entry.Name, cfg.Name)
				}
				if test.name == "empty host" {
					if entry.Host != localhost {
						t.Fatalf("is=%v want=%v", entry.Host, localhost)
					}
				} else {
					if entry.Host != cfg.Host {
						t.Fatalf("is=%v want=%v", entry.Host, cfg.Host)
					}
				}
				if entry.Port != cfg.Port {
					t.Fatalf("is=%v want=%v", entry.Port, cfg.Port)
				}

				cancel()
				if err := <-lookupDone; !errors.Is(err, context.Canceled) {
					t.Fatalf("stop browser: %v", err)
				}
				if err := <-respondDone; !errors.Is(err, context.Canceled) {
					t.Fatalf("stop responder: %v", err)
				}
			})
		})
	}
}

func TestBrowseRemovesGoodbye(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		iface, err := loopbackInterface()
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(Config{
			Name:   "Goodbye",
			Type:   "_goodbye._tcp",
			Host:   "Goodbye-Host",
			Port:   12334,
			Ifaces: []string{iface.Name},
		})
		if err != nil {
			t.Fatal(err)
		}
		service.ifaceIPs = map[string][]net.IP{
			iface.Name: {net.IPv4(192, 0, 2, 20)},
		}

		conn := newTestConn()
		conn.iface = iface
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		added := make(chan BrowseEntry, 1)
		removed := make(chan BrowseEntry, 1)
		done := make(chan error, 1)
		go func() {
			done <- lookupType(ctx, service.ServiceName(), conn, func(entry BrowseEntry) {
				added <- entry
			}, func(entry BrowseEntry) {
				removed <- entry
			}, iface.Name)
		}()

		announcement := new(dns.Msg)
		announcement.Response = true
		announcement.Answer = []dns.RR{PTR(service), SRV(service), TXT(service)}
		for _, record := range A(service, iface) {
			announcement.Extra = append(announcement.Extra, record)
		}
		conn.in <- announcement
		if entry := <-added; entry.Name != service.Name {
			t.Fatalf("added entry = %+v", entry)
		}

		goodbye := PTR(service)
		goodbye.Hdr.Ttl = 0
		msg := new(dns.Msg)
		msg.Response = true
		msg.Answer = []dns.RR{goodbye}
		conn.in <- msg
		if entry := <-removed; entry.Name != service.Name {
			t.Fatalf("removed entry = %+v", entry)
		}

		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("stop browser: %v", err)
		}
	})
}

func TestIntegrationMDNSBrowseMulticast(t *testing.T) {
	if testing.Short() {
		t.Skip("requires real multicast sockets")
	}

	iface, err := loopbackInterface()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Name:   "Sylve mDNS integration",
		Type:   "_sylve-mdns._tcp",
		Host:   "Sylve-mDNS-Test",
		Port:   12334,
		Ifaces: []string{iface.Name},
	}
	service, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := NewResponder()
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if _, err := rs.Add(service); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	respondDone := make(chan error, 1)
	lookupDone := make(chan error, 1)
	lookupStarted := false
	defer func() {
		cancel()
		if lookupStarted {
			waitForMDNSIntegrationDone(t, "browser", lookupDone)
		}
		waitForMDNSIntegrationDone(t, "responder", respondDone)
	}()

	ready := make(chan error, 1)
	go func() {
		respondDone <- rs.(*responder).RespondReady(ctx, ready)
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("start responder: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("start responder: %v", ctx.Err())
	}

	results := make(chan BrowseEntry, 1)
	lookupStarted = true
	go func() {
		lookupDone <- LookupTypeAtInterfaces(
			ctx,
			fmt.Sprintf("%s.local.", cfg.Type),
			func(entry BrowseEntry) {
				select {
				case results <- entry:
				default:
				}
			},
			func(BrowseEntry) {},
			iface.Name,
		)
	}()

	select {
	case entry := <-results:
		if entry.Name != cfg.Name || entry.Host != cfg.Host || entry.Port != cfg.Port {
			t.Fatalf("browse entry = %+v, want name=%q host=%q port=%d", entry, cfg.Name, cfg.Host, cfg.Port)
		}
	case <-ctx.Done():
		t.Fatalf("browse multicast service: %v", ctx.Err())
	}
}

func waitForMDNSIntegrationDone(t *testing.T, name string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("stop %s: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("stop %s: timeout", name)
	}
}
