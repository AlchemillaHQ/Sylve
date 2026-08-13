// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dnssd

import (
	"context"

	"github.com/miekg/dns"
)

func LookupInstance(ctx context.Context, instance string) (Service, error) {
	var srv Service

	conn, err := NewMDNSConn()
	if err != nil {
		return srv, err
	}
	defer conn.Close()

	return lookupInstance(ctx, instance, conn)
}

func lookupInstance(ctx context.Context, instance string, conn MDNSConn) (srv Service, err error) {
	var cache = NewCache()

	m := new(dns.Msg)

	srvQ := dns.Question{
		Name:   instance,
		Qtype:  dns.TypeSRV,
		Qclass: dns.ClassINET,
	}
	txtQ := dns.Question{
		Name:   instance,
		Qtype:  dns.TypeTXT,
		Qclass: dns.ClassINET,
	}
	setQuestionUnicast(&srvQ)
	setQuestionUnicast(&txtQ)

	m.Question = []dns.Question{srvQ, txtQ}

	readCtx, readCancel := context.WithCancel(ctx)
	defer readCancel()

	ch := conn.Read(readCtx)

	for _, iface := range MulticastInterfaces() {
		if err = conn.SendQuery(&Query{msg: m, iface: iface}); err != nil {
			return
		}
	}

	for {
		select {
		case req := <-ch:
			cache.UpdateFrom(req)
			if s, ok := cache.services[instance]; ok {
				srv = *s
				return
			}
		case <-ctx.Done():
			err = ctx.Err()
			return
		}
	}
}
