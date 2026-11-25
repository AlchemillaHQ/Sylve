// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const (
	DownloadTicketTTL  = time.Minute
	maxDownloadTickets = 4096
)

var (
	ErrDownloadTicketInvalid  = errors.New("invalid_download_ticket")
	ErrDownloadTicketCapacity = errors.New("download_ticket_capacity_exhausted")
)

type downloadTicket struct {
	token   string
	scope   [sha256.Size]byte
	expires time.Time
}

func (s *Service) IssueDownloadTicket(token, scope string) (string, time.Time, error) {
	if strings.TrimSpace(token) == "" || scope == "" {
		return "", time.Time{}, ErrDownloadTicketInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	expires := now.Add(DownloadTicketTTL)

	s.downloadTicketsMu.Lock()
	defer s.downloadTicketsMu.Unlock()
	if s.downloadTickets == nil {
		s.downloadTickets = map[string]downloadTicket{}
	}
	for key, entry := range s.downloadTickets {
		if !now.Before(entry.expires) {
			delete(s.downloadTickets, key)
		}
	}
	if len(s.downloadTickets) >= maxDownloadTickets {
		return "", time.Time{}, ErrDownloadTicketCapacity
	}
	s.downloadTickets[ticket] = downloadTicket{token: token, scope: sha256.Sum256([]byte(scope)), expires: expires}
	return ticket, expires, nil
}

func (s *Service) RedeemDownloadTicket(ticket, scope string) (string, error) {
	s.downloadTicketsMu.Lock()
	defer s.downloadTicketsMu.Unlock()
	entry, ok := s.downloadTickets[ticket]
	if !ok {
		return "", ErrDownloadTicketInvalid
	}
	delete(s.downloadTickets, ticket)
	requested := sha256.Sum256([]byte(scope))
	if !time.Now().Before(entry.expires) || subtle.ConstantTimeCompare(entry.scope[:], requested[:]) != 1 {
		return "", ErrDownloadTicketInvalid
	}
	return entry.token, nil
}
