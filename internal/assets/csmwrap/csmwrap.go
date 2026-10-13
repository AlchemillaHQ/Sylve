// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

// Package csmwrap materializes the source-built boot helper embedded in Sylve.
package csmwrap

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const maximumISOSize = 64 << 20

type Manifest struct {
	Architecture string `json:"architecture"`
	Size         int64  `json:"size"`
}

func Info() (Manifest, error) {
	if runtime.GOARCH != "amd64" {
		return Manifest{}, fmt.Errorf("csmwrap_only_available_on_amd64")
	}
	var manifest Manifest
	if err := json.Unmarshal(buildManifest, &manifest); err != nil {
		return manifest, fmt.Errorf("csmwrap_invalid_manifest: %w", err)
	}
	if manifest.Architecture != "x86_64" || manifest.Size <= 0 || manifest.Size > maximumISOSize {
		return manifest, fmt.Errorf("csmwrap_invalid_manifest")
	}
	return manifest, nil
}

func Ensure(dataPath string) (string, error) {
	manifest, err := Info()
	if err != nil {
		return "", err
	}
	return materialize(dataPath, manifest, compressedISO)
}

func materialize(dataPath string, manifest Manifest, compressed []byte) (string, error) {
	base, err := filepath.Abs(dataPath)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", fmt.Errorf("csmwrap_open_data_path: %w", err)
	}
	defer root.Close()
	directory := filepath.Join("firmware", "csmwrap")
	if err := root.MkdirAll(directory, 0755); err != nil {
		return "", fmt.Errorf("csmwrap_create_directory: %w", err)
	}
	name := filepath.Join(directory, "CSMWrap.iso")
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("csmwrap_decompress_image: %w", err)
	}
	defer reader.Close()
	temporary := filepath.Join(directory, ".iso-"+rand.Text()+".tmp")
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(temporary)
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(reader, manifest.Size+1))
	if err != nil {
		return "", fmt.Errorf("csmwrap_decompress_image: %w", err)
	}
	if size != manifest.Size {
		return "", fmt.Errorf("csmwrap_image_size_mismatch")
	}
	if err := file.Chmod(0444); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := root.Rename(temporary, name); err != nil {
		return "", err
	}
	return filepath.Join(base, name), nil
}
