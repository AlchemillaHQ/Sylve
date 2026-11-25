// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.

package auth

import (
	"errors"
	"testing"
	"time"
)

func TestDownloadTicketsAreSingleUseScopedAndShortLived(t *testing.T) {
	service := &Service{}
	ticket, expires, err := service.IssueDownloadTicket("session-token", "file:/etc/motd")
	if err != nil || ticket == "" || time.Until(expires) > DownloadTicketTTL {
		t.Fatalf("issue = %q %v %v", ticket, expires, err)
	}
	if token, err := service.RedeemDownloadTicket(ticket, "file:/etc/motd"); err != nil || token != "session-token" {
		t.Fatalf("redeem = %q %v", token, err)
	}
	if _, err := service.RedeemDownloadTicket(ticket, "file:/etc/motd"); !errors.Is(err, ErrDownloadTicketInvalid) {
		t.Fatalf("second redeem returned %v; want %v", err, ErrDownloadTicketInvalid)
	}

	wrongScope, _, _ := service.IssueDownloadTicket("session-token", "file:/etc/motd")
	if _, err := service.RedeemDownloadTicket(wrongScope, "file:/etc/master.passwd"); !errors.Is(err, ErrDownloadTicketInvalid) {
		t.Fatalf("wrong scope returned %v", err)
	}
	if _, err := service.RedeemDownloadTicket(wrongScope, "file:/etc/motd"); !errors.Is(err, ErrDownloadTicketInvalid) {
		t.Fatal("a ticket tried against the wrong scope stayed usable")
	}

	expired, _, _ := service.IssueDownloadTicket("session-token", "archive:/tmp")
	service.downloadTicketsMu.Lock()
	entry := service.downloadTickets[expired]
	entry.expires = time.Now().Add(-time.Second)
	service.downloadTickets[expired] = entry
	service.downloadTicketsMu.Unlock()
	if _, err := service.RedeemDownloadTicket(expired, "archive:/tmp"); !errors.Is(err, ErrDownloadTicketInvalid) {
		t.Fatalf("expired ticket returned %v", err)
	}

	if _, _, err := service.IssueDownloadTicket("", "file:/x"); !errors.Is(err, ErrDownloadTicketInvalid) {
		t.Fatalf("ticket without a session returned %v", err)
	}
	for range maxDownloadTickets {
		if _, _, err := service.IssueDownloadTicket("session-token", "file:/x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.IssueDownloadTicket("session-token", "file:/x"); !errors.Is(err, ErrDownloadTicketCapacity) {
		t.Fatalf("over capacity returned %v; want %v", err, ErrDownloadTicketCapacity)
	}
}
