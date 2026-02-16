// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type BackupConfig struct {
	LogLevel   int8   `json:"logLevel"`
	DataPath   string `json:"dataPath"`
	ListenPort int    `json:"listenPort"`
	ClusterKey string `json:"clusterKey"`
	TLS        struct {
		CertFile string `json:"certFile"`
		KeyFile  string `json:"keyFile"`
	} `json:"tlsConfig"`
}

func ParseBackupConfig(path string) (*BackupConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &BackupConfig{}
	if err := json.NewDecoder(file).Decode(cfg); err != nil {
		return nil, err
	}

	if cfg.ListenPort <= 0 || cfg.ListenPort > 65535 {
		return nil, fmt.Errorf("invalid_listen_port")
	}

	if cfg.DataPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cfg.DataPath = filepath.Join(cwd, "data-backup")
	}

	if err := os.MkdirAll(cfg.DataPath, 0755); err != nil {
		return nil, err
	}

	return cfg, nil
}
