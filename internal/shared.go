// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package internal

type BaseConfigAdmin struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type TLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

type SylveConfig struct {
	Environment string            `json:"environment"`
	ProxyToVite bool              `json:"proxyToVite"`
	IP          string            `json:"ip"`
	Port        int               `json:"port"`
	LogLevel    int8              `json:"logLevel"`
	Admins      []BaseConfigAdmin `json:"admins"`
	DataPath    string            `json:"dataPath"`
	TLS         TLSConfig         `json:"tlsConfig"`
}

type APIResponse[T any] struct {
	Status string `json:"status" extensions:"x-order=1"`
	Data   T      `json:"data,omitempty" extensions:"x-order=2"`
}

type ErrorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

type SuccessResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
