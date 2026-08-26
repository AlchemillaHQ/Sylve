// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utilities

import (
	"errors"
	"path/filepath"
	"strings"
)

var (
	ErrDownloaderPostProcessOptions = errors.New(
		"tar archives cannot be converted to RAW; disable RAW conversion or use a compressed disk image",
	)
	ErrDownloaderExtractionFormat = errors.New(
		"automatic extraction requires a tar archive or a supported compressed file",
	)
)

var downloaderTarArchiveSuffixes = []string{
	".tar",
	".tar.gz",
	".tgz",
	".tar.bz2",
	".tbz",
	".tbz2",
	".tar.xz",
	".txz",
	".tar.zst",
	".tar.zstd",
	".tzst",
	".tar.z",
}

func IsTarArchiveName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	for _, suffix := range downloaderTarArchiveSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func ValidateDownloaderPostProcessOptions(
	name string,
	automaticExtraction bool,
	automaticRawConversion bool,
) error {
	if automaticExtraction && automaticRawConversion && IsTarArchiveName(name) {
		return ErrDownloaderPostProcessOptions
	}
	return nil
}
