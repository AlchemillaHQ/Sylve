package dnssd

import (
	"github.com/miekg/dns"

	"context"
	"fmt"
	"net"
)

type BrowseEntry struct {
	IPs       []net.IP
	Host      string
	Port      int
	IfaceName string
	Name      string
	Type      string
	Domain    string
	Text      map[string]string
}

type AddFunc func(BrowseEntry)

type RmvFunc func(BrowseEntry)

func LookupType(ctx context.Context, service string, add AddFunc, rmv RmvFunc) (err error) {
	conn, err := newMDNSConn()
	if err != nil {
		return err
	}
	defer conn.close()

	return lookupType(ctx, service, conn, add, rmv)
}

func LookupTypeAtInterfaces(ctx context.Context, service string, add AddFunc, rmv RmvFunc, ifaces ...string) (err error) {
	conn, err := newMDNSConn(ifaces...)
	if err != nil {
		return err
	}
	defer conn.close()

	return lookupType(ctx, service, conn, add, rmv, ifaces...)
}

func (e BrowseEntry) EscapedServiceInstanceName() string {
	return fmt.Sprintf("%s.%s.%s.", escape.Replace(e.Name), e.Type, e.Domain)
}

func (e BrowseEntry) ServiceInstanceName() string {
	return fmt.Sprintf("%s.%s.%s.", e.Name, e.Type, e.Domain)
}

func lookupType(ctx context.Context, service string, conn MDNSConn, add AddFunc, rmv RmvFunc, ifaces ...string) (err error) {
	var cache = NewCache()

	m := new(dns.Msg)
	m.Question = []dns.Question{
		dns.Question{
			Name:   service,
			Qtype:  dns.TypePTR,
			Qclass: dns.ClassINET,
		},
	}

	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()

	ch := conn.Read(readCtx)

	qs := make(chan *Query)
	go func() {
		for _, iface := range MulticastInterfaces(ifaces...) {
			iface := iface
			q := &Query{msg: m, iface: iface}
			qs <- q
		}
	}()

	es := []*BrowseEntry{}
	for {
		select {
		case q := <-qs:
			conn.SendQuery(q)

		case req := <-ch:
			cache.UpdateFrom(req)
			for _, srv := range cache.Services() {
				if srv.ServiceName() != service {
					continue
				}

				for ifaceName, ips := range srv.ifaceIPs {
					var found = false
					for _, e := range es {
						if e.Name == srv.Name && e.IfaceName == ifaceName {
							found = true
							break
						}
					}
					if !found {
						e := BrowseEntry{
							IPs:       ips,
							Host:      srv.Host,
							Port:      srv.Port,
							IfaceName: ifaceName,
							Name:      srv.Name,
							Type:      srv.Type,
							Domain:    srv.Domain,
							Text:      srv.Text,
						}
						es = append(es, &e)
						add(e)
					}
				}
			}

			tmp := []*BrowseEntry{}
			for _, e := range es {
				var found = false
				for _, srv := range cache.Services() {
					if srv.ServiceInstanceName() == e.ServiceInstanceName() {
						found = true
						break
					}
				}

				if found {
					tmp = append(tmp, e)
				} else {
					rmv(*e)
				}
			}
			es = tmp
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
