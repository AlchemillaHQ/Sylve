package dnssd

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/miekg/dns"
)

func TestRespondClosesConnectionAfterRegistrationFailure(t *testing.T) {
	conn := newTestConn()
	responder := newResponder(conn)
	responder.probe = func(context.Context, Service) (Service, error) {
		return Service{}, errors.New("probe failed")
	}

	service, err := NewService(Config{Name: "Test", Type: "_test._tcp", Port: 1234})
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	if _, err := responder.Add(service); err != nil {
		t.Fatalf("failed to add service: %v", err)
	}

	if err := responder.Respond(context.Background()); err == nil {
		t.Fatal("expected registration failure")
	}

	select {
	case <-conn.closed:
	default:
		t.Fatal("responder connection was not closed")
	}
	if responder.isRunning {
		t.Fatal("responder remained marked as running")
	}
}

func TestRespondReadyReportsRegistrationResult(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			iface, err := loopbackInterface()
			if err != nil {
				t.Fatal(err)
			}

			conn := newTestConn()
			conn.iface = iface
			responder := newResponder(conn)
			service, err := NewService(Config{
				Name:   "Test",
				Type:   "_test._tcp",
				Port:   1234,
				Ifaces: []string{iface.Name},
			})
			if err != nil {
				t.Fatalf("failed to create service: %v", err)
			}
			service.ifaceIPs = map[string][]net.IP{
				iface.Name: {net.IPv4(192, 0, 2, 1)},
			}
			responder.addManaged(service)

			ctx, cancel := context.WithCancel(t.Context())
			ready := make(chan error, 1)
			done := make(chan error, 1)
			go func() {
				done <- responder.RespondReady(ctx, ready)
			}()

			if err := <-ready; err != nil {
				t.Fatalf("unexpected readiness error: %v", err)
			}
			started := time.Now()
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("expected canceled responder, got %v", err)
			}
			if elapsed := time.Since(started); elapsed != 250*time.Millisecond {
				t.Fatalf("logical goodbye duration = %s, want 250ms", elapsed)
			}
			if got := len(conn.sent); got != 2 {
				t.Fatalf("goodbye packets = %d, want 2", got)
			}
			for range 2 {
				msg := <-conn.sent
				if len(msg.Answer) != 1 || msg.Answer[0].Header().Ttl != 0 {
					t.Fatalf("unexpected goodbye response: %v", msg)
				}
			}
		})
	})

	t.Run("failure", func(t *testing.T) {
		conn := newTestConn()
		responder := newResponder(conn)
		responder.probe = func(context.Context, Service) (Service, error) {
			return Service{}, errors.New("probe failed")
		}
		service, err := NewService(Config{Name: "Test", Type: "_test._tcp", Port: 1234})
		if err != nil {
			t.Fatalf("failed to create service: %v", err)
		}
		if _, err := responder.Add(service); err != nil {
			t.Fatalf("failed to add service: %v", err)
		}

		ready := make(chan error, 1)
		done := make(chan error, 1)
		go func() {
			done <- responder.RespondReady(context.Background(), ready)
		}()

		if err := <-ready; err == nil || err.Error() != "probe failed" {
			t.Fatalf("expected readiness failure, got %v", err)
		}
		if err := <-done; err == nil || err.Error() != "probe failed" {
			t.Fatalf("expected responder failure, got %v", err)
		}
	})
}

func TestRemove(t *testing.T) {
	cfg := Config{
		Name: "Test",
		Type: "_asdf._tcp",
		Port: 1234,
	}
	si, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}

	msg := new(dns.Msg)
	msg.Answer = []dns.RR{SRV(si), TXT(si)}

	answers := []dns.RR{SRV(si), TXT(si), PTR(si)}
	unknown := remove(msg.Answer, answers)

	if x := len(unknown); x != 1 {
		t.Fatal(x)
	}

	rr := unknown[0]
	if _, ok := rr.(*dns.PTR); !ok {
		t.Fatal("Invalid type", rr)
	}
}

func TestRegisterServiceWithExplicitIP(t *testing.T) {
	cfg := Config{
		Host:   "Computer",
		Name:   "Test",
		Type:   "_asdf._tcp",
		Domain: "local",
		Port:   12345,
		Ifaces: []string{"lo0"},
	}
	sv, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sv.ifaceIPs = map[string][]net.IP{
		"lo0": {net.IP{192, 168, 0, 123}},
	}

	synctest.Test(t, func(t *testing.T) {
		srv := resolveTestService(t, sv)

		if is, want := srv.Name, "Test"; is != want {
			t.Fatalf("%v != %v", is, want)
		}

		if is, want := srv.Type, "_asdf._tcp"; is != want {
			t.Fatalf("%v != %v", is, want)
		}

		if is, want := srv.Host, "Computer"; is != want {
			t.Fatalf("%v != %v", is, want)
		}

		ips := srv.IPsAtInterface(&net.Interface{Name: "lo0"})
		if is, want := len(ips), 1; is != want {
			t.Fatalf("%v != %v", is, want)
		}

		if is, want := ips[0].String(), "192.168.0.123"; is != want {
			t.Fatalf("%v != %v", is, want)
		}
	})
}

type expectedIP struct {
	advType  IPType
	expected []net.IP
}

func TestRegisterServiceWithSpecifiedAdvertisedIP(t *testing.T) {
	v4 := net.IP{192, 168, 0, 123}
	v6 := net.ParseIP("fe80::1")

	var expectedIPs = map[string]expectedIP{
		"v4 only":            {IPv4, []net.IP{v4}},
		"v6 only":            {IPv6, []net.IP{v6}},
		"both / unspecified": {IPType(0), []net.IP{v4, v6}},
	}

	for name, expected := range expectedIPs {
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				Host:            "Computer",
				Name:            "Test",
				Type:            "_asdf._tcp",
				Domain:          "local",
				Port:            12345,
				Ifaces:          []string{"lo0"},
				AdvertiseIPType: expected.advType,
			}
			sv, err := NewService(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sv.ifaceIPs = map[string][]net.IP{
				"lo0": {v4, v6},
			}

			synctest.Test(t, func(t *testing.T) {
				srv := resolveTestService(t, sv)

				if is, want := srv.Name, "Test"; is != want {
					t.Fatalf("%v != %v", is, want)
				}

				if is, want := srv.Type, "_asdf._tcp"; is != want {
					t.Fatalf("%v != %v", is, want)
				}

				if is, want := srv.Host, "Computer"; is != want {
					t.Fatalf("%v != %v", is, want)
				}

				ips := srv.IPsAtInterface(&net.Interface{Name: "lo0"})
				if is, want := len(ips), len(expected.expected); is != want {
					t.Fatalf("%v != %v", is, want)
				}

				for i, ip := range ips { // this should always be the same order as a records are processed before aaaa records
					if is, want := ip, expected.expected[i]; !is.Equal(want) {
						t.Fatalf("%v != %v", is, want)
					}
				}
			})
		})
	}
}

func resolveTestService(t *testing.T, service Service) Service {
	t.Helper()

	iface, err := loopbackInterface()
	if err != nil {
		t.Fatal(err)
	}

	conn := newTestConn()
	otherConn := newTestConn()
	conn.iface = iface
	otherConn.iface = iface
	connectTestConns(conn, otherConn)
	defer otherConn.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	responder := newResponder(conn)
	responder.addManaged(service)
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- responder.RespondReady(ctx, ready)
	}()
	if err := <-ready; err != nil {
		t.Fatalf("start responder: %v", err)
	}

	resolved, err := lookupInstance(ctx, service.EscapedServiceInstanceName(), otherConn)
	if err != nil {
		t.Fatalf("resolve service: %v", err)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop responder: %v", err)
	}
	return resolved
}
