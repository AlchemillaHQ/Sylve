package dnssd

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

func newLoopbackMDNSConn(t *testing.T) *mdnsConn {
	t.Helper()

	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("failed to create UDP listener: %v", err)
	}

	conn := &mdnsConn{
		ipv4:     ipv4.NewPacketConn(udpConn),
		udpConn4: udpConn,
		ch:       make(chan *Request),
		closed:   make(chan struct{}),
	}
	t.Cleanup(conn.Close)

	return conn
}

func waitForMDNSReaders(t *testing.T, conn *mdnsConn) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		conn.readWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mDNS readers did not stop after the socket closed")
	}
}

func TestMDNSConnReadStopsAfterSocketClose(t *testing.T) {
	conn := newLoopbackMDNSConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn.Read(ctx)
	conn.Read(ctx) // Read must not start a second reader pair.
	conn.Close()

	waitForMDNSReaders(t, conn)
}

func TestMDNSConnReadStopsAfterContextCancellation(t *testing.T) {
	conn := newLoopbackMDNSConn(t)
	ctx, cancel := context.WithCancel(context.Background())

	conn.Read(ctx)
	cancel()

	waitForMDNSReaders(t, conn)
}

func TestMDNSConnReadDeliversPacket(t *testing.T) {
	conn := newLoopbackMDNSConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	requests := conn.Read(ctx)

	msg := new(dns.Msg)
	msg.SetQuestion("_smb._tcp.local.", dns.TypePTR)
	payload, err := msg.Pack()
	if err != nil {
		t.Fatalf("failed to pack DNS query: %v", err)
	}

	writer, err := net.DialUDP("udp4", nil, conn.udpConn4.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("failed to create UDP writer: %v", err)
	}
	defer writer.Close()
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("failed to write DNS query: %v", err)
	}

	select {
	case request := <-requests:
		if request == nil || len(request.msg.Question) != 1 {
			t.Fatalf("unexpected mDNS request: %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("mDNS reader did not deliver the UDP packet")
	}

	conn.Close()
	waitForMDNSReaders(t, conn)
}
