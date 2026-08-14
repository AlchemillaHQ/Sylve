// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package network

import (
	"errors"
	"fmt"
)

const (
	MaxWireGuardServerPeerNameBytes = 128
	MaxWireGuardServerPeerCIDRs     = 256
	MaxWireGuardServerPeerCIDRBytes = 128
	MaxWireGuardClientNameBytes     = 128
	MaxWireGuardClientEndpointBytes = 253
	MaxWireGuardClientCIDRs         = 256
	MaxWireGuardClientCIDRBytes     = 128
)

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
	ErrInvalidWireGuardServer         = errors.New("invalid wireguard server")
	ErrWireGuardServerConflict        = errors.New("wireguard server conflict")
	ErrInvalidWireGuardClient         = errors.New("invalid wireguard client")
	ErrWireGuardClientConflict        = errors.New("wireguard client conflict")
)

type wireGuardServerError struct {
	kind  error
	code  string
	cause error
}

func (e *wireGuardServerError) Error() string {
	if e.cause == nil {
		return e.code
	}
	return fmt.Sprintf("%s: %v", e.code, e.cause)
}

func (e *wireGuardServerError) Unwrap() error {
	return e.kind
}

func invalidWireGuardServer(code string, cause error) error {
	return &wireGuardServerError{kind: ErrInvalidWireGuardServer, code: code, cause: cause}
}

func wireGuardServerConflict(code string, cause error) error {
	return &wireGuardServerError{kind: ErrWireGuardServerConflict, code: code, cause: cause}
}

func invalidWireGuardClient(code string, cause error) error {
	return &wireGuardServerError{kind: ErrInvalidWireGuardClient, code: code, cause: cause}
}

func wireGuardClientConflict(code string, cause error) error {
	return &wireGuardServerError{kind: ErrWireGuardClientConflict, code: code, cause: cause}
}

// WireGuardErrorCode returns a stable code suitable for an API response.
func WireGuardErrorCode(err error) string {
	var serverErr *wireGuardServerError
	if errors.As(err, &serverErr) {
		return serverErr.code
	}

	switch {
	case errors.Is(err, ErrWireGuardServiceDisabled):
		return ErrWireGuardServiceDisabled.Error()
	case errors.Is(err, ErrWireGuardServerAlreadyInited):
		return ErrWireGuardServerAlreadyInited.Error()
	case errors.Is(err, ErrWireGuardServerNotInited):
		return ErrWireGuardServerNotInited.Error()
	case errors.Is(err, ErrWireGuardServerPeerNotFound):
		return ErrWireGuardServerPeerNotFound.Error()
	case errors.Is(err, ErrWireGuardClientNotFound):
		return ErrWireGuardClientNotFound.Error()
	case errors.Is(err, ErrWireGuardClientPrivateKeyReq):
		return ErrWireGuardClientPrivateKeyReq.Error()
	case errors.Is(err, ErrWireGuardEndpointHostRequired):
		return ErrWireGuardEndpointHostRequired.Error()
	case errors.Is(err, ErrWireGuardEndpointPortInvalid):
		return ErrWireGuardEndpointPortInvalid.Error()
	case errors.Is(err, ErrWireGuardAllowedIPsRequired):
		return ErrWireGuardAllowedIPsRequired.Error()
	case errors.Is(err, ErrWireGuardAddressesRequired):
		return ErrWireGuardAddressesRequired.Error()
	case errors.Is(err, ErrWireGuardClientIPsRequired):
		return ErrWireGuardClientIPsRequired.Error()
	case errors.Is(err, ErrWireGuardPeerPublicKeyRequired):
		return ErrWireGuardPeerPublicKeyRequired.Error()
	default:
		return "wireguard_operation_failed"
	}
}

type InitWireGuardServerRequest struct {
	Port                    uint     `json:"port" binding:"required,min=1,max=65535"`
	Addresses               []string `json:"addresses" binding:"omitempty,dive,cidr"`
	MTU                     *uint    `json:"mtu" binding:"omitempty,min=576,max=9000"`
	PrivateKey              *string  `json:"privateKey"`
	AllowWireGuardPort      bool     `json:"allowWireGuardPort"`
	MasqueradeIPv4Interface string   `json:"masqueradeIPv4Interface"`
	MasqueradeIPv6Interface string   `json:"masqueradeIPv6Interface"`
}

type WireGuardServerEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type WireGuardServerPeerRequest struct {
	ID *uint `json:"-"`

	Name                string `json:"name" binding:"required,max=128"`
	Enabled             *bool  `json:"enabled"`
	PersistentKeepalive *bool  `json:"persistentKeepalive"`

	PrivateKey   *string `json:"privateKey"`
	PreSharedKey *string `json:"preSharedKey"`

	ClientIPs   []string `json:"clientIPs" binding:"required,min=1,max=256,dive,cidr"`
	RoutableIPs []string `json:"routableIPs" binding:"omitempty,max=256,dive,cidr"`
	RouteIPs    *bool    `json:"routeIPs"`
}

type WireGuardServerPeerEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type WireGuardClientRequest struct {
	ID *uint `json:"-"`

	Name string `json:"name" binding:"required,max=128"`

	Enabled *bool `json:"enabled"`

	EndpointHost string `json:"endpointHost" binding:"required,max=253"`
	EndpointPort uint   `json:"endpointPort" binding:"required,min=1,max=65535"`

	ListenPort *uint `json:"listenPort" binding:"omitempty,max=65535"`

	PrivateKey string `json:"privateKey" binding:"required"`

	PeerPublicKey string  `json:"peerPublicKey" binding:"required"`
	PreSharedKey  *string `json:"preSharedKey"`

	AllowedIPs      []string `json:"allowedIPs" binding:"required,min=1,max=256,dive,cidr"`
	RouteAllowedIPs *bool    `json:"routeAllowedIPs"`
	Addresses       []string `json:"addresses" binding:"required,min=1,max=256,dive,cidr"`

	MTU                 *uint `json:"mtu" binding:"omitempty,min=576,max=9000"`
	Metric              *uint `json:"metric"`
	FIB                 *uint `json:"fib"`
	PersistentKeepalive *bool `json:"persistentKeepalive"`
}

type WireGuardClientEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}
