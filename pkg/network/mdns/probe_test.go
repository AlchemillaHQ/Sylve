package dnssd

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/miekg/dns"
)

var testAddr = net.UDPAddr{
	IP:   net.IP{},
	Port: 1234,
	Zone: "",
}

var testIface = &net.Interface{
	Index:        0,
	MTU:          0,
	Name:         "lo0",
	HardwareAddr: []byte{},
	Flags:        net.FlagUp,
}

type testConn struct {
	read   chan *Request
	in     chan *dns.Msg
	out    chan *dns.Msg
	sent   chan *dns.Msg
	closed chan struct{}
	iface  *net.Interface

	readOnce  sync.Once
	closeOnce sync.Once
}

func newTestConn() *testConn {
	return &testConn{
		read:   make(chan *Request),
		in:     make(chan *dns.Msg, 64),
		out:    make(chan *dns.Msg, 64),
		sent:   make(chan *dns.Msg, 64),
		closed: make(chan struct{}),
		iface:  testIface,
	}
}

func connectTestConns(left, right *testConn) {
	left.out = right.in
	right.out = left.in
}

func (c *testConn) SendQuery(q *Query) error {
	return c.send(q.msg)
}

func (c *testConn) SendResponse(resp *Response) error {
	return c.send(resp.msg)
}

func (c *testConn) send(msg *dns.Msg) error {
	select {
	case c.sent <- msg.Copy():
	case <-c.closed:
		return net.ErrClosed
	}

	select {
	case c.out <- msg.Copy():
		return nil
	case <-c.closed:
		return net.ErrClosed
	}
}

func (c *testConn) Read(ctx context.Context) <-chan *Request {
	c.readOnce.Do(func() {
		go c.start(ctx)
	})
	return c.read
}

func (c *testConn) Drain(ctx context.Context) {
	ch := c.Read(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		default:
			return
		}
	}
}

func (c *testConn) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
}

func (c *testConn) start(ctx context.Context) {
	for {
		select {
		case msg := <-c.in:
			if msg == nil {
				continue
			}
			req := &Request{msg: msg, from: &testAddr, iface: c.iface}
			select {
			case c.read <- req:
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			}
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		}
	}
}

// TestProbing tests probing by using 2 services with the same
// service instance name and host name.Once the first services
// is announced, the probing for the second service should give
func TestProbing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		iface, err := loopbackInterface()
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		conn := newTestConn()
		otherConn := newTestConn()
		conn.iface = iface
		otherConn.iface = iface
		connectTestConns(conn, otherConn)

		cfg := Config{
			Name:   "My Service",
			Type:   "_hap._tcp",
			Host:   "My Computer",
			Port:   12334,
			Ifaces: []string{iface.Name},
		}
		srv, err := NewService(cfg)
		if err != nil {
			t.Fatal(err)
		}
		srv.ifaceIPs = map[string][]net.IP{
			iface.Name: {net.IP{192, 168, 0, 122}},
		}

		rcfg := cfg.Copy()
		rsrv, err := NewService(rcfg)
		if err != nil {
			t.Fatal(err)
		}
		rsrv.ifaceIPs = map[string][]net.IP{
			iface.Name: {net.IP{192, 168, 0, 123}},
		}

		r := newResponder(otherConn)
		r.addManaged(rsrv)
		responderCtx, stopResponder := context.WithCancel(ctx)
		responderDone := make(chan error, 1)
		go func() {
			responderDone <- r.Respond(responderCtx)
		}()
		responderStopped := false
		t.Cleanup(func() {
			if responderStopped {
				return
			}
			stopResponder()
			<-responderDone
		})

		started := time.Now()
		resolved, err := probeService(ctx, conn, srv, 500*time.Millisecond, true)
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed <= 0 || elapsed >= 10*time.Second {
			t.Fatalf("logical probe duration = %s, want a bounded positive duration", elapsed)
		}
		if got := len(conn.sent); got < 4 {
			t.Fatalf("probe queries = %d, want at least 4 across conflict and retry", got)
		}

		if is, want := resolved.Host, "My-Computer-2"; is != want {
			t.Fatalf("is=%v want=%v", is, want)
		}
		if is, want := resolved.Name, "My Service (2)"; is != want {
			t.Fatalf("is=%v want=%v", is, want)
		}

		stopResponder()
		if err := <-responderDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("responder error = %v, want context cancellation", err)
		}
		responderStopped = true
	})
}

func loopbackInterface() (*net.Interface, error) {
	iface, err := net.InterfaceByName("lo0")
	if err == nil {
		return iface, nil
	}
	return net.InterfaceByName("lo")
}

func TestIsLexicographicLater(t *testing.T) {
	this := &dns.A{
		Hdr: dns.RR_Header{
			Name:   "MyPrinter.local.",
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    TTLHostname,
		},
		A: net.ParseIP("169.254.99.200"),
	}

	that := &dns.A{
		Hdr: dns.RR_Header{
			Name:   "MyPrinter.local.",
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    TTLHostname,
		},
		A: net.ParseIP("169.254.200.50"),
	}

	if is, want := compareIP(this.A.To4(), that.A.To4()), -1; is != want {
		t.Fatalf("is=%v want=%v", is, want)
	}

	if is, want := compareIP(that.A.To4(), this.A.To4()), 1; is != want {
		t.Fatalf("is=%v want=%v", is, want)
	}
}

func TestDenyingAs(t *testing.T) {
	tests := []struct {
		This   []*dns.A
		That   []*dns.A
		Result bool
	}{
		{
			This: []*dns.A{
				&dns.A{
					Hdr: dns.RR_Header{
						Name:   "MyPrinter.local.",
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    TTLHostname,
					},
					A: net.ParseIP("169.254.99.200"),
				},
			},
			That: []*dns.A{
				&dns.A{
					Hdr: dns.RR_Header{
						Name:   "MyPrinter.local.",
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    TTLHostname,
					},
					A: net.ParseIP("169.254.99.200"),
				},
			},
			Result: false,
		},
		{
			This: []*dns.A{
				&dns.A{
					Hdr: dns.RR_Header{
						Name:   "MyPrinter.local.",
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    TTLHostname,
					},
					A: net.ParseIP("169.254.99.200"),
				},
			},
			That:   []*dns.A{},
			Result: true,
		},
		{
			This: []*dns.A{},
			That: []*dns.A{
				&dns.A{
					Hdr: dns.RR_Header{
						Name:   "MyPrinter.local.",
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    TTLHostname,
					},
					A: net.ParseIP("169.254.99.200"),
				},
			},
			Result: true,
		},
		{
			This:   []*dns.A{},
			That:   []*dns.A{},
			Result: false,
		},
	}

	for _, test := range tests {
		if is, want := areDenyingAs(test.This, test.That), test.Result; is != want {
			t.Fatalf("%v != %v is=%v want=%v", test.This, test.That, is, want)
		}
	}
}
