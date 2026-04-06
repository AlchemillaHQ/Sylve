// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

//go:build freebsd && cgo

package network

/*
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/ioctl.h>
#include <sys/sockio.h>
#include <net/if.h>
#include <netinet/in.h>
#include <netinet/in_var.h>
#include <netinet6/in6_var.h>
#include <netinet6/nd6.h>
#include <arpa/inet.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static int
wg_open_socket(void)
{
	return socket(AF_INET, SOCK_DGRAM, 0);
}

static void
wg_set_ifname(char *dst, const char *src)
{
	strncpy(dst, src, IFNAMSIZ);
	dst[IFNAMSIZ - 1] = '\0';
}

static int
wg_if_exists(const char *name)
{
	unsigned int idx = if_nametoindex(name);
	return idx > 0 ? 1 : 0;
}

static int
wg_create(char *out_name, size_t out_len)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	strlcpy(ifr.ifr_name, "wg", sizeof(ifr.ifr_name));

	if (ioctl(fd, SIOCIFCREATE2, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	if (out_name != NULL && out_len > 0) {
		strlcpy(out_name, ifr.ifr_name, out_len);
	}

	close(fd);
	return 0;
}

static int
wg_rename(const char *old_name, const char *new_name)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, old_name);

	char new_ifname[IFNAMSIZ];
	memset(new_ifname, 0, sizeof(new_ifname));
	wg_set_ifname(new_ifname, new_name);
	ifr.ifr_data = (caddr_t)new_ifname;

	if (ioctl(fd, SIOCSIFNAME, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_destroy(const char *name)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, name);

	if (ioctl(fd, SIOCIFDESTROY, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_set_up(const char *name)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, name);

	if (ioctl(fd, SIOCGIFFLAGS, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	ifr.ifr_flags |= IFF_UP;
	if (ioctl(fd, SIOCSIFFLAGS, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_set_mtu(const char *name, unsigned int mtu)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, name);
	ifr.ifr_mtu = (int)mtu;

	if (ioctl(fd, SIOCSIFMTU, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_set_metric(const char *name, unsigned int metric)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, name);
	ifr.ifr_metric = (int)metric;

	if (ioctl(fd, SIOCSIFMETRIC, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_set_fib(const char *name, unsigned int fib)
{
	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct ifreq ifr;
	memset(&ifr, 0, sizeof(ifr));
	wg_set_ifname(ifr.ifr_name, name);
	ifr.ifr_fib = (int)fib;

	if (ioctl(fd, SIOCSIFFIB, &ifr) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static int
wg_add_inet_addr(const char *name, const char *ip, unsigned int prefix)
{
	if (prefix > 32) {
		return -EINVAL;
	}

	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct in_aliasreq req;
	memset(&req, 0, sizeof(req));
	wg_set_ifname(req.ifra_name, name);

	struct sockaddr_in *addr = (struct sockaddr_in *)&req.ifra_addr;
	addr->sin_len = sizeof(*addr);
	addr->sin_family = AF_INET;
	if (inet_pton(AF_INET, ip, &addr->sin_addr) != 1) {
		close(fd);
		return -EINVAL;
	}

	struct sockaddr_in *mask = (struct sockaddr_in *)&req.ifra_mask;
	mask->sin_len = sizeof(*mask);
	mask->sin_family = AF_INET;
	if (prefix == 0) {
		mask->sin_addr.s_addr = 0;
	} else {
		uint32_t value = 0xffffffffu << (32u - prefix);
		mask->sin_addr.s_addr = htonl(value);
	}

	if (ioctl(fd, SIOCAIFADDR, &req) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}

static void
wg_fill_in6_prefixmask(struct in6_addr *mask, unsigned int prefix)
{
	memset(mask, 0, sizeof(*mask));

	for (unsigned int i = 0; i < 16 && prefix > 0; i++) {
		if (prefix >= 8) {
			mask->s6_addr[i] = 0xff;
			prefix -= 8;
			continue;
		}

		mask->s6_addr[i] = (uint8_t)(0xff << (8 - prefix));
		prefix = 0;
	}
}

static int
wg_add_inet6_addr(const char *name, const char *ip, unsigned int prefix)
{
	if (prefix > 128) {
		return -EINVAL;
	}

	int fd = wg_open_socket();
	if (fd < 0) {
		return -errno;
	}

	struct in6_aliasreq req;
	memset(&req, 0, sizeof(req));
	wg_set_ifname(req.ifra_name, name);

	struct sockaddr_in6 *addr = (struct sockaddr_in6 *)&req.ifra_addr;
	addr->sin6_len = sizeof(*addr);
	addr->sin6_family = AF_INET6;
	if (inet_pton(AF_INET6, ip, &addr->sin6_addr) != 1) {
		close(fd);
		return -EINVAL;
	}

	struct sockaddr_in6 *mask = (struct sockaddr_in6 *)&req.ifra_prefixmask;
	mask->sin6_len = sizeof(*mask);
	mask->sin6_family = AF_INET6;
	wg_fill_in6_prefixmask(&mask->sin6_addr, prefix);

	req.ifra_lifetime.ia6t_vltime = ND6_INFINITE_LIFETIME;
	req.ifra_lifetime.ia6t_pltime = ND6_INFINITE_LIFETIME;

	if (ioctl(fd, SIOCAIFADDR_IN6, &req) < 0) {
		int err = errno;
		close(fd);
		return -err;
	}

	close(fd);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"unsafe"
)

type wireGuardInterfaceOpsFreeBSD struct{}
type wireGuardInterfaceOpsFreeBSDShellFallback struct{}

func newWireGuardInterfaceOps() wireGuardInterfaceOps {
	// Always use shell-backed interface operations on FreeBSD. This keeps the
	// rest of the application CGO-enabled while avoiding CGO ioctl paths for
	// WireGuard interface lifecycle/address operations.
	return wireGuardInterfaceOpsFreeBSDShellFallback{}
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) Exists(string) (bool, error) {
	return false, errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) Create(string) (string, error) {
	return "", errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) Rename(string, string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) Destroy(string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) AddAddress(string, string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) SetMTU(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) SetMetric(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) SetFIB(string, uint) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSDShellFallback) Up(string) error {
	return errWireGuardInterfaceOpsUnsupported
}

func (wireGuardInterfaceOpsFreeBSD) Exists(name string) (bool, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return C.wg_if_exists(cName) == 1, nil
}

func (wireGuardInterfaceOpsFreeBSD) Create(cloneType string) (string, error) {
	if strings.TrimSpace(cloneType) != "wg" {
		return "", errWireGuardInterfaceOpsUnsupported
	}

	var created [C.IFNAMSIZ]C.char
	if err := wireGuardNativeInterfaceError("failed_to_create_wireguard_interface", C.wg_create(&created[0], C.size_t(len(created)))); err != nil {
		return "", err
	}

	name := strings.TrimSpace(C.GoString(&created[0]))
	if name == "" {
		return "", fmt.Errorf("failed_to_resolve_created_wireguard_interface")
	}

	return name, nil
}

func (wireGuardInterfaceOpsFreeBSD) Rename(currentName string, newName string) error {
	cCurrentName := C.CString(currentName)
	defer C.free(unsafe.Pointer(cCurrentName))

	cNewName := C.CString(newName)
	defer C.free(unsafe.Pointer(cNewName))

	return wireGuardNativeInterfaceError("failed_to_rename_wireguard_interface", C.wg_rename(cCurrentName, cNewName))
}

func (wireGuardInterfaceOpsFreeBSD) Destroy(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return wireGuardNativeInterfaceError("failed_to_destroy_wireguard_interface", C.wg_destroy(cName))
}

func (wireGuardInterfaceOpsFreeBSD) AddAddress(name string, hostCIDR string) error {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(hostCIDR))
	if err != nil {
		return fmt.Errorf("invalid_wireguard_cidr: %s", hostCIDR)
	}

	prefixLength, _ := network.Mask.Size()

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	if ip4 := ip.To4(); ip4 != nil {
		cIP := C.CString(ip4.String())
		defer C.free(unsafe.Pointer(cIP))

		return wireGuardNativeInterfaceError(
			"add_inet_addr",
			C.wg_add_inet_addr(cName, cIP, C.uint(prefixLength)),
		)
	}

	ip6 := ip.To16()
	if ip6 == nil {
		return fmt.Errorf("invalid_wireguard_cidr: %s", hostCIDR)
	}

	cIP := C.CString(ip6.String())
	defer C.free(unsafe.Pointer(cIP))

	return wireGuardNativeInterfaceError(
		"add_inet6_addr",
		C.wg_add_inet6_addr(cName, cIP, C.uint(prefixLength)),
	)
}

func (wireGuardInterfaceOpsFreeBSD) SetMTU(name string, mtu uint) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return wireGuardNativeInterfaceError("failed_to_set_wireguard_mtu", C.wg_set_mtu(cName, C.uint(mtu)))
}

func (wireGuardInterfaceOpsFreeBSD) SetMetric(name string, metric uint) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return wireGuardNativeInterfaceError("failed_to_set_wireguard_metric", C.wg_set_metric(cName, C.uint(metric)))
}

func (wireGuardInterfaceOpsFreeBSD) SetFIB(name string, fib uint) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return wireGuardNativeInterfaceError("failed_to_set_wireguard_fib", C.wg_set_fib(cName, C.uint(fib)))
}

func (wireGuardInterfaceOpsFreeBSD) Up(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return wireGuardNativeInterfaceError("failed_to_set_wireguard_interface_up", C.wg_set_up(cName))
}

func wireGuardNativeInterfaceError(op string, result C.int) error {
	if result >= 0 {
		return nil
	}

	errno := syscall.Errno(-int(result))
	return fmt.Errorf("%s: %w", op, errno)
}
