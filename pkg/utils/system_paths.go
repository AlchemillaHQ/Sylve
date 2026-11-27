// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package utils

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	baseSystemTrees        = []string{"/bin", "/boot", "/lib", "/libexec", "/rescue", "/sbin", "/usr"}
	baseSystemExceptions   = []string{"/usr/local", "/usr/home"}
	sylveDatasetName       = regexp.MustCompile(`^[^/@]+/sylve(/|$)`)
	bootEnvironmentDataset = regexp.MustCompile(`^[^/@]+/ROOT(/|$)`)
)

func IsSylveDataset(name string) bool {
	return sylveDatasetName.MatchString(name)
}

func IsBootEnvironmentDataset(name string) bool {
	return bootEnvironmentDataset.MatchString(name)
}

func CleanPathWithin(path, root string) bool {
	if root == string(filepath.Separator) {
		return strings.HasPrefix(path, root)
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func IsBaseSystemPath(path string) bool {
	for _, exception := range baseSystemExceptions {
		if path != exception && CleanPathWithin(path, exception) {
			return false
		}
	}
	for _, tree := range baseSystemTrees {
		if CleanPathWithin(path, tree) {
			return true
		}
	}
	return false
}
