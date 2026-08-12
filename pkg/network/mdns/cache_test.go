// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dnssd

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/miekg/dns"
)

func TestCacheExpiresServiceAtTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := NewCache()
		adds, removes := cache.UpdateFrom(cachePTRRequest(2))
		if len(adds) != 1 || len(removes) != 0 || len(cache.Services()) != 1 {
			t.Fatalf("initial update: adds=%d removes=%d services=%d", len(adds), len(removes), len(cache.Services()))
		}

		time.Sleep(2 * time.Second)
		adds, removes = cache.UpdateFrom(&Request{msg: new(dns.Msg)})
		if len(adds) != 0 || len(removes) != 1 || len(cache.Services()) != 0 {
			t.Fatalf("expired update: adds=%d removes=%d services=%d", len(adds), len(removes), len(cache.Services()))
		}
	})
}

func TestCacheGoodbyeRemovesExistingServiceImmediately(t *testing.T) {
	cache := NewCache()
	cache.UpdateFrom(cachePTRRequest(120))

	adds, removes := cache.UpdateFrom(cachePTRRequest(0))
	if len(adds) != 0 || len(removes) != 1 || len(cache.Services()) != 0 {
		t.Fatalf("goodbye update: adds=%d removes=%d services=%d", len(adds), len(removes), len(cache.Services()))
	}

	empty := NewCache()
	adds, removes = empty.UpdateFrom(cachePTRRequest(0))
	if len(adds) != 0 || len(removes) != 0 || len(empty.Services()) != 0 {
		t.Fatalf("unknown goodbye: adds=%d removes=%d services=%d", len(adds), len(removes), len(empty.Services()))
	}
}

func cachePTRRequest(ttl uint32) *Request {
	msg := new(dns.Msg)
	msg.Answer = []dns.RR{&dns.PTR{
		Hdr: dns.RR_Header{
			Name:   "_test._tcp.local.",
			Rrtype: dns.TypePTR,
			Class:  dns.ClassINET,
			Ttl:    ttl,
		},
		Ptr: "Cache._test._tcp.local.",
	}}
	return &Request{msg: msg}
}
