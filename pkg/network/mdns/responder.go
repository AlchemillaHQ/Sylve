package dnssd

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type ReadFunc func(*Request)

type Responder interface {
	Add(srv Service) (ServiceHandle, error)
	Remove(srv ServiceHandle)
	Respond(ctx context.Context) error
	Debug(ctx context.Context, fn ReadFunc)
}

type responder struct {
	isRunning bool

	conn      MDNSConn
	unmanaged []*serviceHandle
	managed   []*serviceHandle

	mutex     *sync.Mutex
	truncated *Request
	random    *rand.Rand
	upIfaces  []string
	watcher   LinkWatcher
}

func NewResponder() (Responder, error) {
	conn, err := newMDNSConn()
	if err != nil {
		return nil, err
	}

	r := newResponder(conn)
	r.watcher = newPlatformLinkWatcher()
	return r, nil
}

func newResponder(conn MDNSConn) *responder {
	return &responder{
		isRunning: false,
		conn:      conn,
		unmanaged: []*serviceHandle{},
		managed:   []*serviceHandle{},
		mutex:     &sync.Mutex{},
		random:    rand.New(rand.NewSource(time.Now().UnixNano())),
		upIfaces:  []string{},
	}
}

func (r *responder) Remove(h ServiceHandle) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for i, s := range r.managed {
		if h == s {
			handle := h.(*serviceHandle)
			r.unannounce([]*Service{handle.service})
			r.managed = append(r.managed[:i], r.managed[i+1:]...)
			return
		}
	}
}

func (r *responder) Add(srv Service) (ServiceHandle, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.isRunning {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()

		srv, err := r.register(ctx, srv)
		if err != nil {
			return nil, err
		}

		return r.addManaged(srv), nil
	}

	return r.addUnmanaged(srv), nil
}

func (r *responder) Respond(ctx context.Context) error {
	r.mutex.Lock()
	err := func() error {
		r.isRunning = true
		for _, h := range r.unmanaged {
			srv, err := r.register(ctx, *h.service)
			if err != nil {
				return err
			}

			h.service = &srv
			r.managed = append(r.managed, h)
		}
		r.unmanaged = []*serviceHandle{}
		return nil
	}()
	r.mutex.Unlock()

	if err != nil {
		return err
	}

	if r.watcher != nil {
		go r.handleLinkUpdates(ctx)
	}

	return r.respond(ctx)
}

func (r *responder) handleLinkUpdates(ctx context.Context) {
	ch, err := r.watcher.Subscribe(ctx)
	if err != nil || ch == nil {
		return
	}

	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			r.mutex.Lock()
			r.announce(services(r.managed))
			r.mutex.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (r *responder) announce(services []*Service) {
	for _, service := range services {
		for _, iface := range service.Interfaces() {
			service, iface := service, iface
			go r.announceAtInterface(service, iface)
		}
	}
}

func (r *responder) announceAtInterface(service *Service, iface *net.Interface) {
	ips := service.IPsAtInterface(iface)
	if len(ips) == 0 {
		return
	}

	var answer []dns.RR
	answer = append(answer, SRV(*service))
	answer = append(answer, PTR(*service))
	answer = append(answer, TXT(*service))
	answer = append(answer, aOrAaaaFilter(service, iface)...)

	msg := new(dns.Msg)
	msg.Answer = answer
	msg.Response = true
	msg.Authoritative = true

	setAnswerCacheFlushBit(msg)

	resp := &Response{msg: msg, iface: iface}

	r.conn.SendResponse(resp)
	time.Sleep(1 * time.Second)
	r.conn.SendResponse(resp)
}

func (r *responder) register(ctx context.Context, srv Service) (Service, error) {
	if !r.isRunning {
		return srv, fmt.Errorf("cannot register service when responder is not responding")
	}

	probed, err := ProbeService(ctx, srv)
	if err != nil {
		return srv, err
	}

	srvs := []*Service{&probed}
	for _, h := range r.managed {
		srvs = append(srvs, h.service)
	}
	go r.announce(srvs)

	return probed, nil
}

func (r *responder) addManaged(srv Service) ServiceHandle {
	h := &serviceHandle{&srv}
	r.managed = append(r.managed, h)
	return h
}

func (r *responder) addUnmanaged(srv Service) ServiceHandle {
	h := &serviceHandle{&srv}
	r.unmanaged = append(r.unmanaged, h)
	return h
}

func (r *responder) respond(ctx context.Context) error {
	if !r.isRunning {
		return fmt.Errorf("isRunning should be true before calling respond()")
	}

	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()
	ch := r.conn.Read(readCtx)

	for {
		select {
		case req := <-ch:
			r.mutex.Lock()
			r.handleRequest(req)
			r.mutex.Unlock()

		case <-ctx.Done():
			r.unannounce(services(r.managed))
			r.conn.Close()
			r.isRunning = false
			return ctx.Err()
		}
	}
}

func (r *responder) handleRequest(req *Request) {
	if len(r.managed) == 0 {
		return
	}

	if req.msg.Truncated {
		r.truncated = req
		return
	}

	if r.truncated != nil && r.truncated.from.IP.Equal(req.from.IP) {
		msgs := []*dns.Msg{r.truncated.msg, req.msg}
		r.truncated = nil
		req.msg = mergeMsgs(msgs)
	}

	if len(req.msg.Question) > 0 {
		r.handleQuery(req, services(r.managed))
	} else {
		conflicts := findConflicts(req, r.managed)
		for _, h := range conflicts {
			go r.reprobe(h)

			for i, m := range r.managed {
				if h == m {
					r.managed = append(r.managed[:i], r.managed[i+1:]...)
					break
				}
			}
		}
	}
}

func (r *responder) unannounce(services []*Service) {
	if len(services) == 0 {
		return
	}

	rrsByIfaceName := map[string][]dns.RR{}
	for _, srv := range services {
		rr := PTR(*srv)
		rr.Header().Ttl = 0
		for _, iface := range srv.Interfaces() {
			ips := srv.IPsAtInterface(iface)
			if len(ips) == 0 {
				continue
			}
			if rrs, ok := rrsByIfaceName[iface.Name]; ok {
				rrsByIfaceName[iface.Name] = append(rrs, rr)
			} else {
				rrsByIfaceName[iface.Name] = []dns.RR{rr}
			}
		}
	}

	for name, rrs := range rrsByIfaceName {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		msg := new(dns.Msg)
		msg.Answer = rrs
		msg.Response = true
		msg.Authoritative = true
		resp := &Response{msg: msg, iface: iface}
		r.conn.SendResponse(resp)
		time.Sleep(250 * time.Millisecond)
		r.conn.SendResponse(resp)
	}
}

func (r *responder) handleQuery(req *Request, services []*Service) {
	for _, q := range req.msg.Question {
		msgs := []*dns.Msg{}
		for _, srv := range services {
			if msg := r.handleQuestion(q, req, *srv); msg != nil {
				msgs = append(msgs, msg)
			}
		}

		msg := mergeMsgs(msgs)
		msg.SetReply(req.msg)
		msg.Response = true
		msg.Authoritative = true

		if isLegacyUnicastSource(req.from) {
			msg.Question = []dns.Question{q}
		} else {
			msg.Question = nil
		}

		if len(msg.Answer) == 0 {
			continue
		}

		if isUnicastQuestion(q) || isLegacyUnicastSource(req.from) {
			resp := &Response{msg: msg, addr: req.from, iface: req.iface}
			r.conn.SendResponse(resp)
		} else {
			resp := &Response{msg: msg, iface: req.iface}
			r.conn.SendResponse(resp)
		}
	}
}

func (r *responder) reprobe(h *serviceHandle) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	probed, err := ReprobeService(ctx, *h.service)
	if err != nil {
		return
	}
	h.service = &probed

	r.mutex.Lock()
	managed := append(r.managed, h)
	r.managed = managed
	r.mutex.Unlock()

	go r.announce(services(managed))
}

func (r *responder) handleQuestion(q dns.Question, req *Request, srv Service) *dns.Msg {
	resp := new(dns.Msg)
	switch strings.ToLower(q.Name) {
	case strings.ToLower(srv.ServiceName()):
		ptr := PTR(srv)
		resp.Answer = []dns.RR{ptr}

		extra := []dns.RR{SRV(srv), TXT(srv)}

		extra = append(extra, aOrAaaaFilter(&srv, req.iface)...)

		if nsec := NSEC(ptr, srv, req.iface); nsec != nil {
			extra = append(extra, nsec)
		}

		resp.Extra = extra

		delay := time.Duration(r.random.Intn(105)+20) * time.Millisecond
		time.Sleep(delay)

	case strings.ToLower(srv.EscapedServiceInstanceName()):
		resp.Answer = []dns.RR{SRV(srv), TXT(srv), PTR(srv)}

		var extra []dns.RR

		extra = append(extra, aOrAaaaFilter(&srv, req.iface)...)

		if nsec := NSEC(SRV(srv), srv, req.iface); nsec != nil {
			extra = append(extra, nsec)
		}

		resp.Extra = extra

		if !isLegacyUnicastSource(req.from) {
			setAnswerCacheFlushBit(resp)
		}

	case strings.ToLower(srv.Hostname()):
		var answer []dns.RR

		answer = append(answer, aOrAaaaFilter(&srv, req.iface)...)

		resp.Answer = answer

		if nsec := NSEC(SRV(srv), srv, req.iface); nsec != nil {
			resp.Extra = []dns.RR{nsec}
		}

		if !isLegacyUnicastSource(req.from) {
			setAnswerCacheFlushBit(resp)
		}

	case strings.ToLower(srv.ServicesMetaQueryName()):
		resp.Answer = []dns.RR{DNSSDServicesPTR(srv)}

	default:
		return nil
	}

	resp.Answer = remove(req.msg.Answer, resp.Answer)

	resp.SetReply(req.msg)
	if !isLegacyUnicastSource(req.from) {
		resp.Question = nil
	}
	resp.Response = true
	resp.Authoritative = true

	return resp
}

func findConflicts(req *Request, hs []*serviceHandle) []*serviceHandle {
	var conflicts []*serviceHandle
	for _, h := range hs {
		if containsConflictingAnswers(req, h) {
			conflicts = append(conflicts, h)
		}
	}

	return conflicts
}

func services(hs []*serviceHandle) []*Service {
	var result []*Service
	for _, h := range hs {
		result = append(result, h.service)
	}

	return result
}

func containsConflictingAnswers(req *Request, handle *serviceHandle) bool {
	as := A(*handle.service, req.iface)
	aaaas := AAAA(*handle.service, req.iface)
	reqAs, reqAAAAs, _ := splitRecords(filterRecords(req, handle.service))

	if len(reqAs) > 0 && areDenyingAs(reqAs, as) {
		return true
	}

	if len(reqAAAAs) > 0 && areDenyingAAAAs(reqAAAAs, aaaas) {
		return true
	}

	return false
}

func aOrAaaaFilter(service *Service, iface *net.Interface) []dns.RR {
	var result []dns.RR
	switch service.AdvertiseIPType {
	case IPv4:
		for _, a := range A(*service, iface) {
			result = append(result, a)
		}
	case IPv6:
		for _, aaaa := range AAAA(*service, iface) {
			result = append(result, aaaa)
		}
	default:
		for _, a := range A(*service, iface) {
			result = append(result, a)
		}
		for _, aaaa := range AAAA(*service, iface) {
			result = append(result, aaaa)
		}
	}
	return result
}
