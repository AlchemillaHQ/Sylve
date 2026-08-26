// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package dynamicdns

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"
)

const (
	stunBindingRequest = 0x0001
	stunBindingSuccess = 0x0101
	stunMagicCookie    = 0x2112A442
	stunXORMappedAddr  = 0x0020
)

func discoverSTUNAddress(ctx context.Context, network, server string) (netip.Addr, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, network, server)
	if err != nil {
		return netip.Addr{}, err
	}
	defer connection.Close()

	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return netip.Addr{}, err
	}

	var transactionID [12]byte
	if _, err := rand.Read(transactionID[:]); err != nil {
		return netip.Addr{}, fmt.Errorf("failed to generate STUN transaction ID: %w", err)
	}

	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:2], stunBindingRequest)
	binary.BigEndian.PutUint32(request[4:8], stunMagicCookie)
	copy(request[8:20], transactionID[:])
	if _, err := connection.Write(request); err != nil {
		return netip.Addr{}, err
	}

	response := make([]byte, 1500)
	count, err := connection.Read(response)
	if err != nil {
		return netip.Addr{}, err
	}

	return parseSTUNResponse(response[:count], transactionID)
}

func parseSTUNResponse(message []byte, transactionID [12]byte) (netip.Addr, error) {
	if len(message) < 20 {
		return netip.Addr{}, fmt.Errorf("STUN response is too short")
	}
	if binary.BigEndian.Uint16(message[0:2]) != stunBindingSuccess {
		return netip.Addr{}, fmt.Errorf("STUN response is not a binding success")
	}
	if binary.BigEndian.Uint32(message[4:8]) != stunMagicCookie {
		return netip.Addr{}, fmt.Errorf("STUN response has an invalid magic cookie")
	}
	if string(message[8:20]) != string(transactionID[:]) {
		return netip.Addr{}, fmt.Errorf("STUN response transaction ID does not match")
	}

	messageLength := int(binary.BigEndian.Uint16(message[2:4]))
	if messageLength > len(message)-20 {
		return netip.Addr{}, fmt.Errorf("STUN response has an invalid length")
	}

	for offset, end := 20, 20+messageLength; offset+4 <= end; {
		attributeType := binary.BigEndian.Uint16(message[offset : offset+2])
		attributeLength := int(binary.BigEndian.Uint16(message[offset+2 : offset+4]))
		valueStart := offset + 4
		valueEnd := valueStart + attributeLength
		if valueEnd > end {
			return netip.Addr{}, fmt.Errorf("STUN response has a truncated attribute")
		}

		if attributeType == stunXORMappedAddr {
			return parseXORMappedAddress(message[valueStart:valueEnd], transactionID)
		}

		offset = valueEnd + ((4 - attributeLength%4) % 4)
	}

	return netip.Addr{}, fmt.Errorf("STUN response has no XOR-MAPPED-ADDRESS")
}

func parseXORMappedAddress(value []byte, transactionID [12]byte) (netip.Addr, error) {
	if len(value) < 4 {
		return netip.Addr{}, fmt.Errorf("STUN XOR-MAPPED-ADDRESS is too short")
	}

	family := value[1]
	addressBytes := value[4:]
	switch family {
	case 0x01:
		if len(addressBytes) != 4 {
			return netip.Addr{}, fmt.Errorf("STUN IPv4 XOR-MAPPED-ADDRESS is invalid")
		}
		var address [4]byte
		cookie := [4]byte{}
		binary.BigEndian.PutUint32(cookie[:], stunMagicCookie)
		for index := range address {
			address[index] = addressBytes[index] ^ cookie[index]
		}
		return netip.AddrFrom4(address), nil
	case 0x02:
		if len(addressBytes) != 16 {
			return netip.Addr{}, fmt.Errorf("STUN IPv6 XOR-MAPPED-ADDRESS is invalid")
		}
		var mask [16]byte
		binary.BigEndian.PutUint32(mask[0:4], stunMagicCookie)
		copy(mask[4:], transactionID[:])
		var address [16]byte
		for index := range address {
			address[index] = addressBytes[index] ^ mask[index]
		}
		return netip.AddrFrom16(address), nil
	default:
		return netip.Addr{}, fmt.Errorf("STUN XOR-MAPPED-ADDRESS has an unsupported family")
	}
}
