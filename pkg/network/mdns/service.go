package dnssd

import (
	"bytes"

	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

type Config struct {
	Name      string
	Type      string
	Domain    string
	Host      string
	Text      map[string]string
	IPs       []net.IP
	Port      int
	Ifaces    []string
	BlockedIPNets []string
	AdvertiseIPType IPType
}

func (c Config) Copy() Config {
	return Config{
		Name:            c.Name,
		Type:            c.Type,
		Domain:          c.Domain,
		Host:            c.Host,
		Text:            c.Text,
		IPs:             c.IPs,
		Port:            c.Port,
		Ifaces:          c.Ifaces,
		BlockedIPNets:   c.BlockedIPNets,
		AdvertiseIPType: c.AdvertiseIPType,
	}
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isWhitespace(r rune) bool {
	return r == ' '
}

func validHostname(host string) string {
	result := ""
	z := len(host) - 1
	for i, r := range host {
		if isWhitespace(r) {
			r = '-'
		}
		if i == 0 && (!isDigit(r) && !isAlpha(r)) {
			continue
		}

		if i == z && (r == '-' || r == '.') {
			continue
		}

		if !isDigit(r) && !isAlpha(r) && r != '-' && r != '.' {
			continue
		}

		result += string(r)
	}

	return result
}

type IPType int

const (
	Both = IPType(0)
	IPv4 = IPType(4)
	IPv6 = IPType(6)
)

type Service struct {
	Name            string
	Type            string
	Domain          string
	Host            string
	Text            map[string]string
	TTL             time.Duration
	Port            int
	IPs             []net.IP
	Ifaces          []string
	Blocked         []*net.IPNet
	AdvertiseIPType IPType

	ifaceIPs   map[string][]net.IP
	expiration time.Time
}

func NewService(cfg Config) (s Service, err error) {
	name := cfg.Name
	typ := cfg.Type
	port := cfg.Port

	if len(name) == 0 {
		err = fmt.Errorf("invalid name \"%s\"", name)
		return
	}

	if len(typ) == 0 {
		err = fmt.Errorf("invalid type \"%s\"", typ)
		return
	}

	if port == 0 {
		err = fmt.Errorf("invalid port \"%d\"", port)
		return
	}

	domain := cfg.Domain
	if len(domain) == 0 {
		domain = "local"
	}

	host := cfg.Host
	if len(host) == 0 {
		host = hostname()
	}

	text := cfg.Text
	if text == nil {
		text = map[string]string{}
	}

	ips := []net.IP{}
	var ifaces []string

	if cfg.IPs != nil && len(cfg.IPs) > 0 {
		ips = cfg.IPs
	}

	if cfg.Ifaces != nil && len(cfg.Ifaces) > 0 {
		ifaces = cfg.Ifaces
	}

	blocked := make([]*net.IPNet, len(cfg.BlockedIPNets))
	for i, str := range cfg.BlockedIPNets {
		if _, ipNet, netErr := net.ParseCIDR(str); err != nil {
			err = netErr
			return
		} else {
			blocked[i] = ipNet
		}
	}

	return Service{
		Name:            trimServiceNameSuffixRight(name),
		Type:            typ,
		Domain:          domain,
		Host:            validHostname(host),
		Text:            text,
		Port:            port,
		IPs:             ips,
		Blocked:         blocked,
		AdvertiseIPType: cfg.AdvertiseIPType,
		Ifaces:          ifaces,
		ifaceIPs:        map[string][]net.IP{},
	}, nil
}

func (s *Service) Interfaces() []*net.Interface {
	if len(s.Ifaces) > 0 {
		ifis := []*net.Interface{}
		for _, name := range s.Ifaces {
			if ifi, err := net.InterfaceByName(name); err == nil {
				ifis = append(ifis, ifi)
			}
		}

		return ifis
	}

	return MulticastInterfaces()
}

func (s *Service) IsVisibleAtInterface(n string) bool {
	if len(s.Ifaces) == 0 {
		return true
	}

	for _, name := range s.Ifaces {
		if name == n {
			return true
		}
	}

	return false
}

func (s *Service) IPsAtInterface(iface *net.Interface) []net.IP {
	if iface == nil {
		return []net.IP{}
	}

	if ips, ok := s.ifaceIPs[iface.Name]; ok {
		return s.filterIPs(ips)
	}

	if len(s.IPs) > 0 {
		return s.filterIPs(s.IPs)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return []net.IP{}
	}

	ips := []net.IP{}
	for _, addr := range addrs {
		if ip, _, err := net.ParseCIDR(addr.String()); err == nil && !s.blocks(ip) {
			ips = append(ips, ip)
		}
	}

	return ips
}

func (s *Service) HasIPOnAnyInterface(ip net.IP) bool {
	for _, iface := range s.Interfaces() {
		ips := s.IPsAtInterface(iface)
		for _, ifaceIP := range ips {
			if ifaceIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}

func (s Service) Copy() *Service {
	return &Service{
		Name:            s.Name,
		Type:            s.Type,
		Domain:          s.Domain,
		Host:            s.Host,
		Text:            s.Text,
		TTL:             s.TTL,
		IPs:             s.IPs,
		Port:            s.Port,
		AdvertiseIPType: s.AdvertiseIPType,
		Ifaces:          s.Ifaces,
		Blocked:         s.Blocked,
		ifaceIPs:        s.ifaceIPs,
		expiration:      s.expiration,
	}
}

func (s Service) EscapedName() string {
	return escape.Replace(s.Name)
}

func (s *Service) filterIPs(ips []net.IP) []net.IP {
	var tmp []net.IP
	for _, ip := range ips {
		if !s.blocks(ip) {
			tmp = append(tmp, ip)
		}
	}

	return tmp
}

func (s *Service) blocks(ip net.IP) bool {
	for _, ipNet := range s.Blocked {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

func incrementHostname(name string, count int) string {
	return fmt.Sprintf("%s-%d", trimHostNameSuffixRight(name), count)
}

func trimHostNameSuffixRight(name string) string {
	minus := strings.LastIndex(name, "-")
	if minus == -1 ||
		minus == len(name)-1 {
		return name
	}

	after := name[minus+1:]
	for _, r := range after {
		if !isDigit(r) {
			return name
		}
	}

	trimmed := name[:minus]
	if len(trimmed) == 0 {
		return name
	}
	return trimmed
}

func trimServiceNameSuffixRight(name string) string {
	open := strings.LastIndex(name, "(")
	close := strings.LastIndex(name, ")")
	if open == -1 || close == -1 ||
		open >= close ||
		open == 0 ||
		close != len(name)-1 {
		return name
	}

	between := name[open+1 : close-1]
	for _, r := range between {
		if !isDigit(r) {
			return name
		}
	}

	if name[open-1] != ' ' {
		return name
	}

	trimmed := name[:open]
	trimmed = strings.TrimRight(trimmed, " ")
	if len(trimmed) == 0 {
		return name
	}
	return trimmed
}

func incrementServiceName(name string, count int) string {
	return fmt.Sprintf("%s (%d)", trimServiceNameSuffixRight(name), count)
}

func (s Service) EscapedServiceInstanceName() string {
	return fmt.Sprintf("%s.%s.%s.", s.EscapedName(), s.Type, s.Domain)
}

func (s Service) ServiceInstanceName() string {
	return fmt.Sprintf("%s.%s.%s.", s.Name, s.Type, s.Domain)
}

func (s Service) ServiceName() string {
	return fmt.Sprintf("%s.%s.", s.Type, s.Domain)
}

func (s Service) Hostname() string {
	return fmt.Sprintf("%s.%s.", s.Host, s.Domain)
}

func (s *Service) SetHostname(hostname string) {
	name, domain := parseHostname(hostname)

	if domain == s.Domain {
		s.Host = name
	}
}

func (s Service) ServicesMetaQueryName() string {
	return fmt.Sprintf("_services._dns-sd._udp.%s.", s.Domain)
}

func (s *Service) addIP(ip net.IP, iface *net.Interface) {
	s.IPs = append(s.IPs, ip)
	if iface != nil {
		ifaceIPs := []net.IP{ip}
		if ips, ok := s.ifaceIPs[iface.Name]; ok {
			ifaceIPs = append(ips, ip)
		}
		s.ifaceIPs[iface.Name] = ifaceIPs
	}
}

func newService(instance string) *Service {
	name, typ, domain := parseServiceInstanceName(instance)
	return &Service{
		Name:     name,
		Type:     typ,
		Domain:   domain,
		Text:     map[string]string{},
		IPs:      []net.IP{},
		Ifaces:   []string{},
		ifaceIPs: map[string][]net.IP{},
	}
}

var unescape = strings.NewReplacer("\\", "")
var escape *strings.Replacer

func init() {
	specialChars := []byte{'.', ' ', '\'', '@', ';', '(', ')', '"', '\\'}
	replaces := make([]string, 2*len(specialChars))
	for i, char := range specialChars {
		replaces[2*i] = string(char)
		replaces[2*i+1] = "\\" + string(char)
	}
	escape = strings.NewReplacer(replaces...)
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < len(r)/2; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func parseServiceInstanceName(str string) (name string, service string, domain string) {
	r := bytes.NewBufferString(reverse(strings.Trim(str, ".")))
	l, err := r.ReadString('.')
	if err != nil {
		return
	}
	domain = strings.Trim(l, ".")
	domain = reverse(domain)

	proto, err := r.ReadString('.')
	if err != nil {
		return
	}
	typee, err := r.ReadString('.')
	if err != nil {
		return
	}
	service = fmt.Sprintf("%s.%s", strings.Trim(reverse(typee), "."), strings.Trim(reverse(proto), "."))
	name = reverse(r.String())
	name = unescape.Replace(name)

	return
}

func hostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	name, _ := parseHostname(hostname)
	return name
}

func parseHostname(str string) (name string, domain string) {
	trimmed := strings.Trim(str, ".")
	last := strings.LastIndex(trimmed, ".")
	if last == -1 {
		name = trimmed
		return
	}

	name = strings.Trim(str[:last], ".")
	domain = strings.Trim(str[last+1:], ".")
	return
}

func MulticastInterfaces(filters ...string) []*net.Interface {
	var tmp []*net.Interface
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	for _, iface := range ifaces {
		iface := iface
		if (iface.Flags & net.FlagUp) == 0 {
			continue
		}

		if (iface.Flags & net.FlagMulticast) == 0 {
			continue
		}

		if !containsIfaces(iface.Name, filters) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if _, _, err := net.ParseCIDR(addr.String()); err == nil {
				tmp = append(tmp, &iface)
				break
			}
		}
	}

	return tmp
}

func containsIfaces(iface string, filters []string) bool {
	if filters == nil || len(filters) <= 0 {
		return true
	}

	for _, ifn := range filters {
		if ifn == iface {
			return true
		}
	}

	return false
}
