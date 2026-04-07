// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import "errors"

var (
	ErrWireGuardServiceDisabled       = errors.New("wireguard_service_disabled")
	ErrWireGuardServerAlreadyInited   = errors.New("wireguard_server_already_initialized")
	ErrWireGuardServerNotInited       = errors.New("wireguard_server_not_initialized")
	ErrWireGuardServerPeerNotFound    = errors.New("wireguard_server_peer_not_found")
	ErrWireGuardClientNotFound        = errors.New("wireguard_client_not_found")
	ErrWireGuardClientPrivateKeyReq   = errors.New("wireguard_client_private_key_required")
	ErrWireGuardEndpointHostRequired  = errors.New("wireguard_endpoint_host_required")
	ErrWireGuardEndpointPortInvalid   = errors.New("wireguard_endpoint_port_invalid")
	ErrWireGuardAllowedIPsRequired    = errors.New("wireguard_allowed_ips_required")
	ErrWireGuardAddressesRequired     = errors.New("wireguard_addresses_required")
	ErrWireGuardClientIPsRequired     = errors.New("wireguard_client_ips_required")
	ErrWireGuardPeerPublicKeyRequired = errors.New("wireguard_peer_public_key_required")
)

type InitWireGuardServerRequest struct {
	Port                    uint     `json:"port" binding:"required,min=1,max=65535"`
	Addresses               []string `json:"addresses" binding:"omitempty,dive,cidr"`
	MTU                     *uint    `json:"mtu" binding:"omitempty,min=576,max=9000"`
	PrivateKey              *string  `json:"privateKey"`
	AllowWireGuardPort      bool     `json:"allowWireGuardPort"`
	MasqueradeIPv4Interface string   `json:"masqueradeIPv4Interface"`
	MasqueradeIPv6Interface string   `json:"masqueradeIPv6Interface"`
}

type WireGuardServerPeerRequest struct {
	ID *uint `json:"-"`

	Name                string `json:"name" binding:"required"`
	Enabled             *bool  `json:"enabled"`
	PersistentKeepalive *bool  `json:"persistentKeepalive"`

	PrivateKey   *string `json:"privateKey"`
	PreSharedKey *string `json:"preSharedKey"`

	ClientIPs   []string `json:"clientIPs" binding:"omitempty,dive,cidr"`
	RoutableIPs []string `json:"routableIPs" binding:"omitempty,dive,cidr"`
	RouteIPs    *bool    `json:"routeIPs"`
}

type RemoveWireGuardServerPeersRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1,dive,gt=0"`
}

type WireGuardClientRequest struct {
	ID *uint `json:"-"`

	Name string `json:"name" binding:"required"`

	Enabled *bool `json:"enabled"`

	EndpointHost string `json:"endpointHost" binding:"required"`
	EndpointPort uint   `json:"endpointPort" binding:"required,min=1,max=65535"`

	ListenPort *uint `json:"listenPort" binding:"omitempty,min=1,max=65535"`

	PrivateKey string `json:"privateKey" binding:"required"`

	PeerPublicKey string  `json:"peerPublicKey" binding:"required"`
	PreSharedKey  *string `json:"preSharedKey"`

	AllowedIPs      []string `json:"allowedIPs" binding:"required,min=1,dive,cidr"`
	RouteAllowedIPs *bool    `json:"routeAllowedIPs"`
	Addresses       []string `json:"addresses" binding:"required,min=1,dive,cidr"`

	MTU                 *uint `json:"mtu" binding:"omitempty,min=576,max=9000"`
	Metric              *uint `json:"metric"`
	FIB                 *uint `json:"fib"`
	PersistentKeepalive *bool `json:"persistentKeepalive"`
}
