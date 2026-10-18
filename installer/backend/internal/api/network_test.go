// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validLiveRequest() liveNetworkRequest {
	return liveNetworkRequest{Interface: "vtnet0", Address: "10.0.2.15/24", Gateway: "10.0.2.2", DNS: []string{"10.0.2.3"}}
}

func TestValidateLiveNetwork(t *testing.T) {
	ifaces := []liveInterface{{Name: "vtnet0"}}
	cases := []struct {
		name string
		edit func(*liveNetworkRequest)
	}{
		{"unknown interface", func(r *liveNetworkRequest) { r.Interface = "lo0" }},
		{"option injection", func(r *liveNetworkRequest) { r.Interface = "-a" }},
		{"shell injection", func(r *liveNetworkRequest) { r.Interface = "vtnet0;reboot" }},
		{"plain address", func(r *liveNetworkRequest) { r.Address = "10.0.2.15" }},
		{"ipv6 address", func(r *liveNetworkRequest) { r.Address = "fd00::1/64" }},
		{"loopback", func(r *liveNetworkRequest) { r.Address = "127.0.0.1/8" }},
		{"link local", func(r *liveNetworkRequest) { r.Address = "169.254.1.1/16" }},
		{"network", func(r *liveNetworkRequest) { r.Address = "10.0.2.0/24" }},
		{"broadcast", func(r *liveNetworkRequest) { r.Address = "10.0.2.255/24" }},
		{"zero prefix", func(r *liveNetworkRequest) { r.Address = "10.0.2.15/0" }},
		{"host prefix", func(r *liveNetworkRequest) { r.Address = "10.0.2.15/32" }},
		{"gateway outside subnet", func(r *liveNetworkRequest) { r.Gateway = "10.1.2.2" }},
		{"gateway is host", func(r *liveNetworkRequest) { r.Gateway = "10.0.2.15" }},
		{"gateway network", func(r *liveNetworkRequest) { r.Gateway = "10.0.2.0" }},
		{"gateway broadcast", func(r *liveNetworkRequest) { r.Gateway = "10.0.2.255" }},
		{"no dns", func(r *liveNetworkRequest) { r.DNS = nil }},
		{"dns injection", func(r *liveNetworkRequest) { r.DNS = []string{"1.1.1.1\nsearch evil"} }},
		{"ipv6 dns", func(r *liveNetworkRequest) { r.DNS = []string{"fd00::53"} }},
	}
	if err := validateLiveNetwork(validLiveRequest(), ifaces); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validLiveRequest()
			tc.edit(&req)
			if err := validateLiveNetwork(req, ifaces); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}

func TestResolverSeed(t *testing.T) {
	old := "# live DNS\nsearch example.test\nnameserver 9.9.9.9\noptions timeout:1\nnameserver 8.8.8.8\n"
	want := "# live DNS\nsearch example.test\noptions timeout:1\nnameserver 10.0.2.3\n"
	if got := string(resolverSeed([]byte(old), []string{"10.0.2.3"})); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func fakeLiveNetwork(t *testing.T) (*liveNetwork, *[]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(path, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	n := &liveNetwork{
		resolverPath: path, supported: true,
		interfaces: func() ([]liveInterface, error) {
			return []liveInterface{{Name: "vtnet0", Addresses: []string{"10.0.2.14/24"}, Up: true}}, nil
		},
		run: func(_ time.Duration, name string, args ...string) (string, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if name == "/sbin/route" && args[1] == "get" {
				return "gateway: 10.0.2.2\ninterface: vtnet0", nil
			}
			return "", nil
		},
	}
	return n, &calls
}

func TestLiveNetworkApply(t *testing.T) {
	n, calls := fakeLiveNetwork(t)
	state, err := n.state()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.apply(validLiveRequest(), state); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(*calls, "\n")
	for _, want := range []string{
		"service dhclient onestop vtnet0", "ifconfig vtnet0 inet 10.0.2.15/24 alias up",
		"route -n change -inet default 10.0.2.2", "ifconfig vtnet0 inet 10.0.2.14 -alias",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("missing %q in %s", want, commands)
		}
	}
	data, err := os.ReadFile(n.resolverPath)
	if err != nil || string(data) != "nameserver 10.0.2.3\n" {
		t.Fatalf("resolver = %q, error = %v", data, err)
	}
}

func TestLiveNetworkRollback(t *testing.T) {
	n, calls := fakeLiveNetwork(t)
	state, err := n.state()
	if err != nil {
		t.Fatal(err)
	}
	run := n.run
	n.run = func(timeout time.Duration, name string, args ...string) (string, error) {
		out, err := run(timeout, name, args...)
		if name == "/sbin/ifconfig" && strings.Join(args, " ") == "vtnet0 inet 10.0.2.14 -alias" {
			return "injected failure", errors.New("failed")
		}
		return out, err
	}
	if err := n.apply(validLiveRequest(), state); err == nil {
		t.Fatal("expected failure")
	}
	commands := strings.Join(*calls, "\n")
	for _, want := range []string{
		"ifconfig vtnet0 inet 10.0.2.14/24 alias", "ifconfig vtnet0 inet 10.0.2.15 -alias",
		"service dhclient onestart vtnet0",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("missing rollback %q in %s", want, commands)
		}
	}
	data, err := os.ReadFile(n.resolverPath)
	if err != nil || string(data) != "nameserver 9.9.9.9\n" {
		t.Fatalf("resolver not restored: %q, %v", data, err)
	}
}

func TestLiveNetworkFromUnaddressedNIC(t *testing.T) {
	n, calls := fakeLiveNetwork(t)
	n.interfaces = func() ([]liveInterface, error) {
		return []liveInterface{{Name: "vtnet0", Addresses: []string{}, Up: false}}, nil
	}
	run := n.run
	hasRoute := false
	n.run = func(timeout time.Duration, name string, args ...string) (string, error) {
		out, err := run(timeout, name, args...)
		if name == "/usr/sbin/service" {
			return "not running", errors.New("not running")
		}
		if name == "/sbin/route" {
			if args[1] == "get" && !hasRoute {
				return "not in table", errors.New("no route")
			}
			if args[1] == "add" {
				hasRoute = true
			}
		}
		return out, err
	}
	state, err := n.state()
	if err != nil || len(state.Interfaces) != 1 || state.Interfaces[0].Up || state.Gateway != "" {
		t.Fatalf("unaddressed NIC lost: %+v, %v", state, err)
	}
	if err := validateLiveNetwork(validLiveRequest(), state.Interfaces); err != nil {
		t.Fatal(err)
	}
	if err := n.apply(validLiveRequest(), state); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(*calls, "\n")
	if !strings.Contains(commands, "route -n add -inet default 10.0.2.2") || strings.Contains(commands, "onestop") {
		t.Fatalf("wrong recovery commands: %s", commands)
	}
}

func TestLiveNetworkUnsupported(t *testing.T) {
	n, calls := fakeLiveNetwork(t)
	n.supported = false
	r := httptest.NewRequest(http.MethodPost, "http://installer:8080/api/network/live", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	(&installer{}).liveNetworkPost(n)(w, r)
	if w.Code != http.StatusNotImplemented || len(*calls) != 0 {
		t.Fatalf("unsupported host ran commands: %d, %v", w.Code, *calls)
	}
}

func TestLiveNetworkPostGuards(t *testing.T) {
	body := `{"interface":"vtnet0","address":"10.0.2.15/24","gateway":"10.0.2.2","dns":["10.0.2.3"]}`
	for _, tc := range []struct {
		name, body, origin string
		want               int
	}{
		{"valid", body, "http://installer:8080", http.StatusOK},
		{"cross origin", body, "http://evil.test", http.StatusForbidden},
		{"opaque origin", body, "null", http.StatusForbidden},
		{"unknown field", strings.TrimSuffix(body, "}") + `,"command":"reboot"}`, "", http.StatusBadRequest},
		{"trailing document", body + `{}`, "", http.StatusBadRequest},
		{"invalid", `{}`, "", http.StatusBadRequest},
		{"oversized", strings.Repeat(" ", 4097) + body, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, calls := fakeLiveNetwork(t)
			r := httptest.NewRequest(http.MethodPost, "http://installer:8080/api/network/live", strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			(&installer{}).liveNetworkPost(n)(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if tc.want != http.StatusOK {
				for _, call := range *calls {
					if !strings.Contains(call, "route -n get") {
						t.Fatalf("invalid request mutated network: %s", call)
					}
				}
			}
		})
	}
}
