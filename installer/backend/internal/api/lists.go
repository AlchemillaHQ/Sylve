// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	timezonesOnce sync.Once
	timezonesList []string
	keymapsOnce   sync.Once
	keymapsList   []string
)

func listTimezones() []string {
	timezonesOnce.Do(func() {
		const root = "/usr/share/zoneinfo"
		skipDirs := map[string]bool{"posix": true, "right": true, "SystemV": true}
		skipFiles := map[string]bool{
			"zone.tab": true, "zone1970.tab": true, "iso3166.tab": true,
			"leapseconds": true, "leap-seconds.list": true, "posixrules": true,
			"tzdata.zi": true, "+VERSION": true, "SECURITY": true, "localtime": true,
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[rel] {
					return fs.SkipDir
				}
				return nil
			}
			base := filepath.Base(path)
			if skipFiles[base] || strings.HasPrefix(base, ".") {
				return nil
			}
			timezonesList = append(timezonesList, rel)
			return nil
		})
		sort.Strings(timezonesList)
	})
	return timezonesList
}

func listKeymaps() []string {
	keymapsOnce.Do(func() {
		entries, err := os.ReadDir("/usr/share/vt/keymaps")
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".kbd") {
				continue
			}
			keymapsList = append(keymapsList, strings.TrimSuffix(name, ".kbd"))
		}
		sort.Strings(keymapsList)
	})
	return keymapsList
}
