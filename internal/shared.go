// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package internal

type BaseConfigAdmin struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type TLSConfig struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

type Raft struct {
	Reset bool `json:"reset"`
}

type DHTConfig struct {
	Port    int  `json:"port"`
	Enabled bool `json:"enabled"`
}

type BTTRPC struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type BTT struct {
	RPC BTTRPC    `json:"rpc"`
	DHT DHTConfig `json:"dht"`
}

type SylveConfig struct {
	Environment   string          `json:"environment"`
	ProxyToVite   bool            `json:"proxyToVite"`
	IP            string          `json:"ip"`
	Port          int             `json:"port"`
	HTTPPort      int             `json:"httpPort"`
	LogLevel      int8            `json:"logLevel"`
	WANInterfaces []string        `json:"wanInterfaces"`
	Admin         BaseConfigAdmin `json:"admin"`
	DataPath      string          `json:"dataPath"`
	TLS           TLSConfig       `json:"tlsConfig"`
	Raft          Raft            `json:"raft"`
	BTT           BTT             `json:"btt"`
}

type APIResponse[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    T      `json:"data"`
	Error   string `json:"error"`
}

const MinimumVMStorageSize = 1024 * 1024 * 128
