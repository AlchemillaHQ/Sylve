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
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/alchemillahq/sylve/internal"
)

var ParsedConfig *internal.SylveConfig
var ConfigPath string

func ParseConfig(path string) *internal.SylveConfig {
	ConfigPath = path
	file, err := os.Open(path)

	if err != nil {
		log.Fatal(err)
	}

	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(file)

	decoder := json.NewDecoder(file)
	ParsedConfig = &internal.SylveConfig{}
	err = decoder.Decode(ParsedConfig)

	if err != nil {
		log.Fatal(err)
	}

	err = SetupDataPath()

	if err != nil {
		log.Fatal(err)
	}

	if reflect.DeepEqual(ParsedConfig.Admin, internal.BaseConfigAdmin{}) {
		log.Fatal("Admin configuration is missing or incomplete in the config file, please see config.example.json for reference")
	}

	return ParsedConfig
}

func GetDataPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	// 1) Explicit override for testing/packaging.
	if v, ok := os.LookupEnv("SYLVE_DATA_PATH"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			if !filepath.IsAbs(v) {
				v = filepath.Join(cwd, v)
			}
			if ParsedConfig != nil {
				ParsedConfig.DataPath = v
			}
			if err := os.MkdirAll(v, 0755); err != nil {
				return "", fmt.Errorf("failed to create data directory: %w", err)
			}
			return v, nil
		}
	}

	// 2) Configured path takes precedence.
	if ParsedConfig != nil && ParsedConfig.DataPath != "" {
		return ParsedConfig.DataPath, nil
	}

	// 3) Reasonable defaults.
	//
	// On FreeBSD, Sylve is typically run as an rc service via daemon(8) with -c,
	// which chdirs to "/". Using cwd-relative defaults would otherwise end up in
	// "/data". Prefer "/var/db/sylve" when running as root on FreeBSD.
	dataPath := filepath.Join(cwd, "data")
	if runtime.GOOS == "freebsd" && os.Geteuid() == 0 {
		dataPath = "/var/db/sylve"
	}

	if ParsedConfig != nil {
		ParsedConfig.DataPath = dataPath
	}

	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	return dataPath, nil
}

func SetupDataPath() error {
	dataPath, err := GetDataPath()
	if err != nil {
		return fmt.Errorf("failed to get data path: %w", err)
	}

	dirs := []string{
		dataPath,
		filepath.Join(dataPath, "vms"),
		filepath.Join(dataPath, "jails"),
		filepath.Join(dataPath, "raft"),
		filepath.Join(dataPath, "downloads"),
		filepath.Join(dataPath, "downloads", "torrents"),
		filepath.Join(dataPath, "downloads", "http"),
		filepath.Join(dataPath, "downloads", "path"),
		filepath.Join(dataPath, "downloads", "extracted"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

func GetDownloadsPath(dType string) string {
	dataPath, err := GetDataPath()
	if err != nil {
		log.Fatal(err)
	}

	switch dType {
	case "torrents":
		return filepath.Join(dataPath, "downloads", "torrents")
	case "torrent.db":
		return filepath.Join(dataPath, "downloads", "torrents", "torrent.db")
	case "http":
		return filepath.Join(dataPath, "downloads", "http")
	case "path":
		return filepath.Join(dataPath, "downloads", "path")
	case "extracted":
		return filepath.Join(dataPath, "downloads", "extracted")
	}

	return filepath.Join(dataPath, "downloads")
}

func GetVMsPath() (string, error) {
	dataPath, err := GetDataPath()
	if err != nil {
		return "", fmt.Errorf("failed to get data path: %w", err)
	}

	vmsPath := filepath.Join(dataPath, "vms")

	return vmsPath, nil
}

func GetJailsPath() (string, error) {
	dataPath, err := GetDataPath()
	if err != nil {
		return "", fmt.Errorf("failed to get data path: %w", err)
	}

	jailsPath := filepath.Join(dataPath, "jails")

	return jailsPath, nil
}

func GetRaftPath() (string, error) {
	dataPath, err := GetDataPath()
	if err != nil {
		return "", fmt.Errorf("failed to get data path: %w", err)
	}

	raftPath := filepath.Join(dataPath, "raft")

	return raftPath, nil
}

func ResetRaftReset() error {
	if ParsedConfig.Raft.Reset {
		ParsedConfig.Raft.Reset = false
	}

	file, err := os.OpenFile(ConfigPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open config file for writing: %w", err)
	}

	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(ParsedConfig); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
