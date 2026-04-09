// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package networkModels

import "time"

type WireGuardServer struct {
	ID uint `json:"id" gorm:"primaryKey"`

	Enabled bool `json:"enabled" gorm:"default:true"`

	Port      uint     `json:"port"`
	Addresses []string `json:"addresses" gorm:"serializer:json;type:json"`

	AllowWireGuardPort      bool   `json:"allowWireGuardPort" gorm:"not null;default:false"`
	MasqueradeIPv4Interface string `json:"masqueradeIPv4Interface"`
	MasqueradeIPv6Interface string `json:"masqueradeIPv6Interface"`

	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`

	Peers []WireGuardServerPeer `json:"peers" gorm:"foreignKey:WireGuardServerID"`

	LastKernelRX uint64 `json:"-"`
	LastKernelTX uint64 `json:"-"`
	MTU          uint   `json:"mtu"`
	Metric       uint   `json:"metric"`

	RX uint64 `json:"rx"`
	TX uint64 `json:"tx"`

	Uptime        uint64    `json:"uptime"`
	LastHandshake time.Time `json:"lastHandshake"`
	RestartedAt   time.Time `json:"restartedAt"`
	CreatedAt     time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt     time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type WireGuardServerPeer struct {
	ID uint `json:"id" gorm:"primaryKey"`

	Name              string `json:"name"`
	Enabled           bool   `json:"enabled" gorm:"default:true"`
	WireGuardServerID uint   `json:"wireguardServerId"`

	PrivateKey   string `json:"privateKey"`
	PublicKey    string `json:"publicKey"`
	PreSharedKey string `json:"preSharedKey"`

	ClientIPs           []string `json:"clientIPs" gorm:"serializer:json;type:json"`
	RoutableIPs         []string `json:"routableIPs" gorm:"serializer:json;type:json"`
	RouteIPs            bool     `json:"routeIPs"`
	PersistentKeepalive bool     `json:"persistentKeepalive"`

	LastHandshake time.Time `json:"lastHandshake"`

	RX           uint64 `json:"rx"`
	TX           uint64 `json:"tx"`
	LastKernelRX uint64 `json:"-"`
	LastKernelTX uint64 `json:"-"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type WireGuardClient struct {
	ID uint `json:"id" gorm:"primaryKey"`

	Enabled bool `json:"enabled" gorm:"default:true"`

	Name         string `json:"name" gorm:"uniqueIndex"`
	EndpointHost string `json:"endpointHost"`
	EndpointPort uint   `json:"endpointPort"`
	ListenPort   uint   `json:"listenPort"`

	PrivateKey    string `json:"privateKey"`
	PublicKey     string `json:"publicKey"`
	PeerPublicKey string `json:"peerPublicKey"`
	PreSharedKey  string `json:"preSharedKey"`

	AllowedIPs      []string `json:"allowedIPs" gorm:"serializer:json;type:json"`
	Addresses       []string `json:"addresses" gorm:"serializer:json;type:json"`
	RouteAllowedIPs bool     `json:"routeAllowedIPs"`

	MTU                 uint `json:"mtu"`
	Metric              uint `json:"metric"`
	FIB                 uint `json:"fib"`
	PersistentKeepalive bool `json:"persistentKeepalive"`

	RX           uint64 `json:"rx"`
	TX           uint64 `json:"tx"`
	KernelLastRX uint64 `json:"-"`
	KernelLastTX uint64 `json:"-"`

	Uptime        uint64    `json:"uptime"`
	LastHandshake time.Time `json:"lastHandshake"`
	RestartedAt   time.Time `json:"restartedAt"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}
