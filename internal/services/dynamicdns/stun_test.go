// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestParseSTUNResponseIPv4(t *testing.T) {
	transactionID := [12]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	target := netip.MustParseAddr("203.0.113.9").As4()
	message := make([]byte, 32)
	binary.BigEndian.PutUint16(message[0:2], stunBindingSuccess)
	binary.BigEndian.PutUint16(message[2:4], 12)
	binary.BigEndian.PutUint32(message[4:8], stunMagicCookie)
	copy(message[8:20], transactionID[:])
	binary.BigEndian.PutUint16(message[20:22], stunXORMappedAddr)
	binary.BigEndian.PutUint16(message[22:24], 8)
	message[25] = 0x01
	cookie := [4]byte{}
	binary.BigEndian.PutUint32(cookie[:], stunMagicCookie)
	for index := range target {
		message[28+index] = target[index] ^ cookie[index]
	}

	address, err := parseSTUNResponse(message, transactionID)
	if err != nil {
		t.Fatalf("parsing STUN response failed: %v", err)
	}
	if address.String() != "203.0.113.9" {
		t.Fatalf("unexpected parsed STUN address %s", address)
	}
}
