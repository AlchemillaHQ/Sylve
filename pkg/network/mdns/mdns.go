package dnssd

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	IPv4LinkLocalMulticast    = net.ParseIP("224.0.0.251")
	IPv6LinkLocalMulticast    = net.ParseIP("ff02::fb")

	AddrIPv4LinkLocalMulticast = &net.UDPAddr{
		IP:   IPv4LinkLocalMulticast,
		Port: 5353,
	}

	AddrIPv6LinkLocalMulticast = &net.UDPAddr{
		IP:   IPv6LinkLocalMulticast,
		Port: 5353,
	}

	TTLDefault  uint32 = 75 * 6
	TTLHostname uint32 = 120
)

type Query struct {
	msg   *dns.Msg
	iface *net.Interface
}

func (q Query) IfaceName() string {
	if q.iface != nil {
		return q.iface.Name
	}

	return "?"
}

type Response struct {
	msg   *dns.Msg
	addr  *net.UDPAddr
	iface *net.Interface
}

type Request struct {
	msg   *dns.Msg
	from  *net.UDPAddr
	iface *net.Interface
}

func (r Request) String() string {
	return fmt.Sprintf("%s@%s\n%v", r.from.IP, r.IfaceName(), r.msg)
}

func (r Request) Raw() *dns.Msg {
	return r.msg
}

func (r Request) From() *net.UDPAddr {
	return r.from
}

func (r Request) IfaceName() string {
	if r.iface != nil {
		return r.iface.Name
	}

	return "?"
}

func isLegacyUnicastSource(addr *net.UDPAddr) bool {
	return addr != nil && addr.Port != 5353
}

type MDNSConn interface {
	SendQuery(q *Query) error
	SendResponse(resp *Response) error
	Read(ctx context.Context) <-chan *Request
	Drain(ctx context.Context)
	Close()
}

type mdnsConn struct {
	ipv4     *ipv4.PacketConn
	ipv6     *ipv6.PacketConn
	udpConn4 *net.UDPConn
	udpConn6 *net.UDPConn
	ch       chan *Request
}

func NewMDNSConn() (MDNSConn, error) {
	return newMDNSConn()
}

func (c *mdnsConn) SendQuery(q *Query) error {
	return c.sendQuery(q.msg, q.iface)
}

func (c *mdnsConn) SendResponse(resp *Response) error {
	if resp.addr != nil {
		return c.sendResponseTo(resp.msg, resp.iface, resp.addr)
	}

	return c.sendResponse(resp.msg, resp.iface)
}

func (c *mdnsConn) Read(ctx context.Context) <-chan *Request {
	return c.read(ctx)
}

func (c *mdnsConn) Drain(ctx context.Context) {
	for {
		select {
		case <-c.Read(ctx):
		default:
			return
		}
	}
}

func (c *mdnsConn) Close() {
	c.close()
}

func newMDNSConn(ifs ...string) (*mdnsConn, error) {
	var errs []error
	var connIPv4 *ipv4.PacketConn
	var connIPv6 *ipv6.PacketConn

	conn4, err := net.ListenUDP("udp4", AddrIPv4LinkLocalMulticast)
	if err != nil {
		errs = append(errs, err)
	}

	connIPv4 = ipv4.NewPacketConn(conn4)
	if err := connIPv4.SetControlMessage(ipv4.FlagInterface, true); err != nil {
	}
	if err := connIPv4.SetMulticastLoopback(true); err != nil {
	}
	if err := connIPv4.SetTTL(255); err != nil {
	}
	if err := connIPv4.SetMulticastTTL(255); err != nil {
	}

	for _, iface := range MulticastInterfaces(ifs...) {
		connIPv4.JoinGroup(iface, &net.UDPAddr{IP: IPv4LinkLocalMulticast})
	}

	conn6, err := net.ListenUDP("udp6", AddrIPv6LinkLocalMulticast)
	if err != nil {
		errs = append(errs, err)
	}
	connIPv6 = ipv6.NewPacketConn(conn6)
	if err := connIPv6.SetControlMessage(ipv6.FlagInterface, true); err != nil {
	}
	if err := connIPv6.SetMulticastLoopback(true); err != nil {
	}
	if err := connIPv6.SetHopLimit(255); err != nil {
	}
	if err := connIPv6.SetMulticastHopLimit(255); err != nil {
	}
	for _, iface := range MulticastInterfaces(ifs...) {
		connIPv6.JoinGroup(iface, &net.UDPAddr{IP: IPv6LinkLocalMulticast})
	}

	if err := first(errs...); connIPv4 == nil && connIPv6 == nil {
		return nil, fmt.Errorf("Failed setting up UDP server: %v", err)
	}

	return &mdnsConn{
		ipv4:     connIPv4,
		ipv6:     connIPv6,
		udpConn4: conn4,
		udpConn6: conn6,
		ch:       make(chan *Request),
	}, nil
}

func (c *mdnsConn) close() {
	if c.ipv4 != nil {
		c.ipv4.Close()
	}

	if c.ipv6 != nil {
		c.ipv6.Close()
	}

	if c.udpConn4 != nil {
		c.udpConn4.Close()
	}

	if c.udpConn6 != nil {
		c.udpConn6.Close()
	}
}

func (c *mdnsConn) read(ctx context.Context) <-chan *Request {
	c.readInto(ctx, c.ch)
	return c.ch
}

func (c *mdnsConn) readInto(ctx context.Context, ch chan *Request) {

	isDone := func(ctx context.Context) bool {
		return ctx.Err() != nil
	}

	if c.ipv4 != nil {
		go func() {
			buf := make([]byte, 65536)
			for {
				if isDone(ctx) {
					return
				}

				n, cm, from, err := c.ipv4.ReadFrom(buf)
				if err != nil {
					continue
				}

				udpAddr, ok := from.(*net.UDPAddr)
				if !ok {
					continue
				}

				var iface *net.Interface
				if cm != nil {
					iface, err = net.InterfaceByIndex(cm.IfIndex)
					if err != nil {
						continue
					}
				} else {
					iface, err = getInterfaceByIp(udpAddr.IP)
					if err != nil {
						continue
					}
				}

				if n > 0 {
					m := new(dns.Msg)
					if err := m.Unpack(buf); err == nil && !shouldIgnore(m) {
						ch <- &Request{m, udpAddr, iface}
					}
				}
			}
		}()
	}

	if c.ipv6 != nil {
		go func() {
			buf := make([]byte, 65536)
			for {
				if isDone(ctx) {
					return
				}

				n, cm, from, err := c.ipv6.ReadFrom(buf)
				if err != nil {
					continue
				}

				udpAddr, ok := from.(*net.UDPAddr)
				if !ok {
					continue
				}

				var iface *net.Interface
				if cm != nil {
					iface, err = net.InterfaceByIndex(cm.IfIndex)
					if err != nil {
						continue
					}
				} else {
					iface, err = net.InterfaceByName(udpAddr.Zone)
					if err != nil {
						continue
					}
				}

				if n > 0 {
					m := new(dns.Msg)
					if err := m.Unpack(buf); err == nil && !shouldIgnore(m) {
						ch <- &Request{m, udpAddr, iface}
					}
				}
			}
		}()
	}
}

func (c *mdnsConn) sendQuery(m *dns.Msg, iface *net.Interface) error {
	sanitizeQuery(m)

	return c.writeMsg(m, iface)
}

func (c *mdnsConn) sendResponse(m *dns.Msg, iface *net.Interface) error {
	sanitizeResponse(m)

	return c.writeMsg(m, iface)
}

func (c *mdnsConn) sendResponseTo(m *dns.Msg, iface *net.Interface, addr *net.UDPAddr) error {
	if !isLegacyUnicastSource(addr) {
		sanitizeResponse(m)
	}

	return c.writeMsgTo(m, iface, addr)
}

func (c *mdnsConn) writeMsg(m *dns.Msg, iface *net.Interface) error {
	var err error
	if c.ipv4 != nil {
		err = c.writeMsgTo(m, iface, AddrIPv4LinkLocalMulticast)
	}

	if c.ipv6 != nil {
		err = c.writeMsgTo(m, iface, AddrIPv6LinkLocalMulticast)
	}

	return err
}

func (c *mdnsConn) writeMsgTo(m *dns.Msg, iface *net.Interface, addr *net.UDPAddr) error {
	if !isLegacyUnicastSource(addr) {
		sanitizeMsg(m)
	}

	if c.ipv4 != nil && addr.IP.To4() != nil {
		if out, err := m.Pack(); err == nil {
			var ctrl *ipv4.ControlMessage
			if iface != nil {
				ctrl = &ipv4.ControlMessage{
					IfIndex: iface.Index,
				}
			}
			c.ipv4.PacketConn.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err = c.ipv4.WriteTo(out, ctrl, addr); err != nil {
				return err
			}
		}
	}

	if c.ipv6 != nil && addr.IP.To4() == nil {
		if out, err := m.Pack(); err == nil {
			var ctrl *ipv6.ControlMessage
			if iface != nil {
				ctrl = &ipv6.ControlMessage{
					IfIndex: iface.Index,
				}
			}
			c.ipv6.PacketConn.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err = c.ipv6.WriteTo(out, ctrl, addr); err != nil {
				return err
			}
		}
	}

	return nil
}

func shouldIgnore(m *dns.Msg) bool {
	if m.Opcode != 0 {
		return true
	}

	if m.Rcode != 0 {
		return true
	}

	return false
}

func sanitizeResponse(m *dns.Msg) {
	if m.Question != nil && len(m.Question) > 0 {
		m.Question = nil
	}

	if !m.Response {
		m.Response = true
	}

	if !m.Authoritative {
		m.Authoritative = true
	}

	if m.Truncated {
		m.Truncated = false
	}
}

func sanitizeQuery(m *dns.Msg) {
	if m.Response {
		m.Response = false
	}

	if m.Authoritative {
		m.Authoritative = false
	}
}

func sanitizeMsg(m *dns.Msg) {
	if m.Opcode != 0 {
		m.Opcode = 0
	}

	if m.RecursionDesired {
		m.RecursionDesired = false
	}

	if m.Zero {
		m.Zero = false
	}

	if m.AuthenticatedData {
		m.AuthenticatedData = false
	}

	if m.CheckingDisabled {
		m.CheckingDisabled = false
	}

	if m.Rcode != 0 {
		m.Rcode = 0
	}
}

func first(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}

func setAnswerCacheFlushBit(msg *dns.Msg) {
	for _, a := range msg.Answer {
		switch a.(type) {
		case *dns.PTR:
			continue
		default:
			a.Header().Class |= (1 << 15)
		}
	}
}

func setQuestionUnicast(q *dns.Question) {
	q.Qclass |= (1 << 15)
}

func isUnicastQuestion(q dns.Question) bool {
	return q.Qclass&(1<<15) != 0
}

func getInterfaceByIp(ip net.IP) (*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagRunning != 0 {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.Contains(ip) {
					return &iface, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("could not find interface by %v", ip)
}
