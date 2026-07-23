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
	sysctl "github.com/alchemillahq/sylve/pkg/utils/sysctl"
)

var ParsedConfig *internal.SylveConfig
var ConfigPath string

func ParseConfig(path string) *internal.SylveConfig {
	cfg, err := ReadConfig(path)
	if err != nil {
		log.Fatal(err)
	}

	dataPath, err := resolveDataPath(cfg.DataPath, filepath.Dir(path), false)
	if err != nil {
		log.Fatal(err)
	}
	cfg.DataPath = dataPath

	ConfigPath = path
	ParsedConfig = cfg

	if err := SetupDataPath(); err != nil {
		log.Fatal(err)
	}

	if reflect.DeepEqual(ParsedConfig.Admin, internal.BaseConfigAdmin{}) {
		log.Fatal("Admin configuration is missing or incomplete in the config file, please see config.example.json for reference")
	}

	return ParsedConfig
}

// ReadConfig decodes a Sylve configuration without mutating global state or
// creating directories, which lets CLI clients discover a running daemon.
func ReadConfig(path string) (*internal.SylveConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	cfg := &internal.SylveConfig{
		Auth: internal.AuthConfig{
			EnablePAM: true,
		},
		ZFS: internal.ZFSConfig{
			Tune: true,
		},
	}
	if err := json.NewDecoder(file).Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

// DataPathFromConfig resolves the configured data path without creating it.
func DataPathFromConfig(path string) (string, error) {
	cfg, err := ReadConfig(path)
	if err != nil {
		return "", err
	}
	return resolveDataPath(cfg.DataPath, filepath.Dir(path), false)
}

func IsPAMEnabled() bool {
	if ParsedConfig == nil {
		return true
	}

	return ParsedConfig.Auth.EnablePAM
}

func IsRunningInJail() bool {
	val, err := sysctl.GetInt64("security.jail.jailed")
	if err != nil {
		return false
	}

	return val == 1
}

func IsDevFSDisabled() bool {
	if ParsedConfig != nil && ParsedConfig.Jails.DisableDevFS {
		return true
	}

	if IsRunningInJail() {
		return true
	}

	return false
}

func GetDataPath() (string, error) {
	configuredPath := ""
	if ParsedConfig != nil {
		configuredPath = ParsedConfig.DataPath
	}

	dataPath, err := resolveDataPath(configuredPath, "", true)
	if err != nil {
		return "", err
	}
	if ParsedConfig != nil {
		ParsedConfig.DataPath = dataPath
	}
	return dataPath, nil
}

func resolveDataPath(configuredPath, configDir string, create bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	dataPath := ""
	// Explicit override for testing/packaging.
	if v, ok := os.LookupEnv("SYLVE_DATA_PATH"); ok {
		v = strings.TrimSpace(v)
		if v != "" {
			if !filepath.IsAbs(v) {
				v = filepath.Join(cwd, v)
			}
			dataPath = v
		}
	}

	if dataPath == "" && strings.TrimSpace(configuredPath) != "" {
		dataPath = strings.TrimSpace(configuredPath)
		if !filepath.IsAbs(dataPath) {
			if configDir == "" {
				configDir = cwd
			}
			dataPath = filepath.Join(configDir, dataPath)
		}
	}

	if dataPath == "" {
		// The port must set this as the default, we will fall back to it if the config file doesn't specify a path.
		dataPath = filepath.Join(cwd, "data")
		if runtime.GOOS == "freebsd" && os.Geteuid() == 0 {
			dataPath = "/var/db/sylve"
		}
	}

	if create {
		if err := os.MkdirAll(dataPath, 0755); err != nil {
			return "", fmt.Errorf("failed to create data directory: %w", err)
		}
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

func ResetForcePasswordReset() error {
	if ParsedConfig.Admin.ForcePasswordReset {
		ParsedConfig.Admin.ForcePasswordReset = false
	}

	return writeConfig()
}

func writeConfig() error {
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

func ResetRaftReset() error {
	if ParsedConfig.Raft.Reset {
		ParsedConfig.Raft.Reset = false
	}

	return writeConfig()
}
