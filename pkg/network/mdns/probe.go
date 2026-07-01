package dnssd

import (
	"context"
	"math/rand"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

func ProbeService(ctx context.Context, srv Service) (Service, error) {
	conn, err := newMDNSConn(srv.Ifaces...)

	if err != nil {
		return srv, err
	}

	defer conn.close()

	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	delay := time.Duration(r.Intn(250)) * time.Millisecond
	time.Sleep(delay)

	return probeService(probeCtx, conn, srv, 250*time.Millisecond, false)
}

func ReprobeService(ctx context.Context, srv Service) (Service, error) {
	conn, err := newMDNSConn(srv.Ifaces...)

	if err != nil {
		return srv, err
	}

	defer conn.close()
	return probeService(ctx, conn, srv, 250*time.Millisecond, true)
}

func probeService(ctx context.Context, conn MDNSConn, srv Service, delay time.Duration, probeOnce bool) (s Service, e error) {
	candidate := srv.Copy()
	prevConflict := probeConflict{}

	numHostConflicts := 0
	numNameConflicts := 0

	for i := 1; i <= 100; i++ {
		conflict, err := probe(ctx, conn, *candidate)
		if err != nil {
			e = err
			return
		}

		if conflict.hasNone() {
			s = *candidate
			return
		}

		candidate = candidate.Copy()

		if conflict.hostname && (prevConflict.hostname || probeOnce) {
			numHostConflicts++
			candidate.Host = incrementHostname(candidate.Host, numHostConflicts+1)
			conflict.hostname = false
		}

		if conflict.serviceName && (prevConflict.serviceName || probeOnce) {
			numNameConflicts++
			candidate.Name = incrementServiceName(candidate.Name, numNameConflicts+1)
			conflict.serviceName = false
		}

		prevConflict = conflict

		if conflict.hasAny() {
			delay = 1 * time.Second
		}

		time.Sleep(delay)
	}

	return
}

func probe(ctx context.Context, conn MDNSConn, service Service) (conflict probeConflict, err error) {
	var queries []*Query
	for _, iface := range service.Interfaces() {
		queries = append(queries, probeQuery(service, iface))
	}

	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()

	conn.Drain(readCtx)
	ch := conn.Read(readCtx)

	queryTime := time.After(1 * time.Millisecond)
	queriesCount := 1

	for {
		select {
		case rsp := <-ch:

			if rsp.iface == nil {
				continue
			}

			reqAs, reqAAAAs, reqSRVs := splitRecords(filterRecords(rsp, &service))

			as := A(service, rsp.iface)
			aaaas := AAAA(service, rsp.iface)

			if len(reqAs) > 0 && len(as) > 0 && areDenyingAs(reqAs, as) {
				conflict.hostname = true
			}

			if len(reqAAAAs) > 0 && len(aaaas) > 0 && areDenyingAAAAs(reqAAAAs, aaaas) {
				conflict.hostname = true
			}

			conflict.serviceName = len(reqSRVs) > 0

		case <-ctx.Done():
			err = ctx.Err()
			return

		case <-queryTime:
			if conflict.hasAny() {
				return conflict, err
			}

			if queriesCount > 3 {
				return
			}

			queriesCount++
			for _, q := range queries {
				conn.SendQuery(q)
			}

			queryTime = time.After(250 * time.Millisecond)
		}
	}
}

func probeQuery(service Service, iface *net.Interface) *Query {
	msg := new(dns.Msg)

	instanceQ := dns.Question{
		Name:   service.EscapedServiceInstanceName(),
		Qtype:  dns.TypeANY,
		Qclass: dns.ClassINET,
	}

	hostQ := dns.Question{
		Name:   service.Hostname(),
		Qtype:  dns.TypeANY,
		Qclass: dns.ClassINET,
	}

	setQuestionUnicast(&instanceQ)
	setQuestionUnicast(&hostQ)

	msg.Question = []dns.Question{instanceQ, hostQ}

	srv := SRV(service)
	as := A(service, iface)
	aaaas := AAAA(service, iface)

	var authority = []dns.RR{srv}
	for _, a := range as {
		authority = append(authority, a)
	}
	for _, aaaa := range aaaas {
		authority = append(authority, aaaa)
	}
	msg.Ns = authority

	return &Query{msg: msg, iface: iface}
}

type probeConflict struct {
	hostname    bool
	serviceName bool
}

func (pr probeConflict) hasNone() bool {
	return !pr.hostname && !pr.serviceName
}

func (pr probeConflict) hasAny() bool {
	return pr.hostname || pr.serviceName
}

func isDenyingA(this *dns.A, that *dns.A) bool {
	if strings.EqualFold(this.Hdr.Name, that.Hdr.Name) {
		if !isValidRR(this) {
			return true
		}

		switch compareIP(this.A.To4(), that.A.To4()) {
		case -1:
		case 1:
			return true
		default:
		}
	}

	return false
}

func isDenyingAAAA(this *dns.AAAA, that *dns.AAAA) bool {
	if strings.EqualFold(this.Hdr.Name, that.Hdr.Name) {
		if !isValidRR(this) {
			return true
		}

		switch compareIP(this.AAAA.To16(), that.AAAA.To16()) {
		case -1:
		case 1:
			return true
		default:
		}
	}

	return false
}

func areDenyingAs(this []*dns.A, that []*dns.A) bool {
	if len(this) != len(that) {
		return true
	}

	sort.Sort(byAIP(this))
	sort.Sort(byAIP(that))

	for i, ti := range this {
		ta := that[i]
		if isDenyingA(ti, ta) {
			return true
		}
	}

	return false
}

func areDenyingAAAAs(this []*dns.AAAA, that []*dns.AAAA) bool {
	if len(this) != len(that) {
		return true
	}

	sort.Sort(byAAAAIP(this))
	sort.Sort(byAAAAIP(that))

	for i, ti := range this {
		ta := that[i]
		if isDenyingAAAA(ti, ta) {
			return true
		}
	}

	return false
}

type byAIP []*dns.A

func (a byAIP) Len() int      { return len(a) }
func (a byAIP) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byAIP) Less(i, j int) bool {
	return strings.Compare(a[i].A.To4().String(), a[j].A.To4().String()) == -1
}

type byAAAAIP []*dns.AAAA

func (a byAAAAIP) Len() int      { return len(a) }
func (a byAAAAIP) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byAAAAIP) Less(i, j int) bool {
	return strings.Compare(a[i].AAAA.To16().String(), a[j].AAAA.To16().String()) == -1
}

func isDenyingSRV(this *dns.SRV, that *dns.SRV) bool {
	if strings.EqualFold(this.Hdr.Name, that.Hdr.Name) {
		if !isValidRR(this) {
			return true
		}

		switch compareSRV(this, that) {
		case -1:
		case 1:
			return true
		default:
		}
	}

	return false
}

func isValidRR(rr dns.RR) bool {
	switch r := rr.(type) {
	case *dns.A:
		return !net.IPv4zero.Equal(r.A)
	case *dns.AAAA:
		return !net.IPv6zero.Equal(r.AAAA)
	case *dns.SRV:
		return len(r.Target) > 0 && r.Port != 0
	default:
	}

	return true
}

func compareIP(this net.IP, that net.IP) int {
	count := len(this)
	if count > len(that) {
		count = len(that)
	}

	for i := 0; i < count; i++ {
		if this[i] < that[i] {
			return -1
		} else if this[i] > that[i] {
			return 1
		}
	}

	if len(this) < len(that) {
		return -1
	} else if len(this) > len(that) {
		return 1
	}
	return 0
}

func compareSRV(this *dns.SRV, that *dns.SRV) int {
	if this.Priority < that.Priority {
		return -1
	} else if this.Priority > that.Priority {
		return 1
	}

	if this.Weight < that.Weight {
		return -1
	} else if this.Weight > that.Weight {
		return 1
	}

	if this.Port < that.Port {
		return -1
	} else if this.Port > that.Port {
		return 1
	}

	return strings.Compare(this.Target, that.Target)
}
